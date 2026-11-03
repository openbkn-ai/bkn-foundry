// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbbuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const refreshTable = "t_bkn_project_kb_refresh"
const refreshColumns = "f_lineage_id,f_network_id,f_state,f_revision,f_data,f_lease_id,f_lease_until,f_next_check,f_update_time"
const refreshQuery = "SELECT " + refreshColumns + " FROM " + refreshTable + " WHERE f_lineage_id=?"

type refreshAccess struct{ db *sql.DB }

func NewRefreshes(db *sql.DB) interfaces.KBRefreshAccess { return &refreshAccess{db: db} }
func validRefresh(r *interfaces.KBRefreshRecord) bool {
	if r == nil || !safeID.MatchString(r.LineageID) || len(r.LineageID) > 40 || r.Revision < 1 || r.UpdateTime <= 0 || r.NextCheck < 0 || r.LeaseUntil < 0 || (r.NetworkID != "" && !safeID.MatchString(r.NetworkID)) || len(r.Reason) > 64 || (r.Reason != "" && !safeID.MatchString(r.Reason)) {
		return false
	}
	if r.HoldNetworkID != "" && !safeID.MatchString(r.HoldNetworkID) {
		return false
	}
	for generation, baseline := range r.Generations {
		build := baseline.Build
		if !safeID.MatchString(generation) || build == nil || build.GenerationID != generation || build.Mode != "full" || build.State != "succeeded" || !hash.MatchString(build.ModelFingerprint) || len(build.Files) > 1000 || (baseline.HoldNetworkID != "" && !safeID.MatchString(baseline.HoldNetworkID)) {
			return false
		}
	}
	switch r.State {
	case "idle", "running", "ready", "applying", "finishing", "discarding", "attention":
	default:
		return false
	}
	if r.Pending != nil {
		b := r.Pending.Build
		if b == nil || !safeID.MatchString(b.ID) || !safeID.MatchString(b.ProjectID) || !hash.MatchString(b.ModelFingerprint) || !safeID.MatchString(b.GenerationID) || len(b.Files) > 1000 || len(r.Pending.Removed) > 1000 || len(r.Pending.Changed) > 1000 || r.Pending.ApplyIndex < 0 {
			return false
		}
		seen := map[int]bool{}
		for _, index := range r.Pending.Changed {
			if index < 0 || index >= len(b.Files) || seen[index] {
				return false
			}
			seen[index] = true
		}
	}
	return (r.State != "running" && r.State != "ready" && r.State != "applying" && r.State != "finishing" && r.State != "discarding") || (r.NetworkID != "" && r.Pending != nil)
}
func readRefresh(row scanner) (*interfaces.KBRefreshRecord, error) {
	var lineage, network, state, raw string
	var revision, next, updated, until int64
	var lease string
	if err := row.Scan(&lineage, &network, &state, &revision, &raw, &lease, &until, &next, &updated); err != nil {
		return nil, err
	}
	var r interfaces.KBRefreshRecord
	if json.Unmarshal([]byte(raw), &r) != nil || r.LineageID != lineage {
		return nil, errCorrupt
	}
	r.NetworkID, r.State, r.Revision, r.NextCheck, r.UpdateTime, r.LeaseID, r.LeaseUntil = network, state, revision, next, updated, lease, until
	if !validRefresh(&r) {
		return nil, errCorrupt
	}
	return &r, nil
}
func (a *refreshAccess) Ensure(ctx context.Context, lineage string, now int64) error {
	r := &interfaces.KBRefreshRecord{LineageID: lineage, State: "idle", Revision: 1, UpdateTime: now}
	if !validRefresh(r) {
		return errCorrupt
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	// An existing lineage journal is never reset by another discovery pass.
	_, err = a.db.ExecContext(ctx, "INSERT INTO "+refreshTable+" ("+refreshColumns+") VALUES (?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE f_lineage_id=VALUES(f_lineage_id)", lineage, "", "idle", int64(1), string(raw), "", int64(0), int64(0), now)
	return err
}
func (a *refreshAccess) Get(ctx context.Context, lineage string) (*interfaces.KBRefreshRecord, error) {
	r, err := readRefresh(a.db.QueryRowContext(ctx, refreshQuery, lineage))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}
func (a *refreshAccess) Claim(ctx context.Context, lineage, lease string, now, until int64) (*interfaces.KBRefreshRecord, error) {
	return a.claim(ctx, lineage, lease, 0, now, until, false)
}
func (a *refreshAccess) ClaimReview(ctx context.Context, lineage, lease string, revision, now, until int64) (*interfaces.KBRefreshRecord, error) {
	return a.claim(ctx, lineage, lease, revision, now, until, true)
}
func (a *refreshAccess) claim(ctx context.Context, lineage, lease string, revision, now, until int64, review bool) (*interfaces.KBRefreshRecord, error) {
	if !safeID.MatchString(lineage) || !safeID.MatchString(lease) || now <= 0 || until <= now {
		return nil, nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	r, err := readRefresh(tx.QueryRowContext(ctx, refreshQuery+" FOR UPDATE", lineage))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if review {
		// Only an explicit authenticated review caller uses this path. A paused
		// exact revision can transfer its idle lease to that request's worker.
		if r.State != "attention" || r.Revision != revision {
			return nil, nil
		}
	} else if r.State == "attention" || r.NextCheck > now || (r.LeaseID != "" && r.LeaseID != lease && r.LeaseUntil > now) {
		return nil, nil
	}
	result, err := tx.ExecContext(ctx, "UPDATE "+refreshTable+" SET f_lease_id=?,f_lease_until=?,f_update_time=? WHERE f_lineage_id=? AND f_revision=?", lease, until, now, lineage, r.Revision)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, errCorrupt
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	r.LeaseID, r.LeaseUntil, r.UpdateTime = lease, until, now
	return r, nil
}
func (a *refreshAccess) Save(ctx context.Context, r *interfaces.KBRefreshRecord, revision int64, lease string, now, until int64) (bool, error) {
	if !validRefresh(r) || r.Revision != revision+1 || lease == "" || r.LeaseID != lease || until <= now || now <= 0 || r.UpdateTime < now {
		return false, nil
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return false, err
	}
	result, err := a.db.ExecContext(ctx, "UPDATE "+refreshTable+" SET f_network_id=?,f_state=?,f_revision=?,f_data=?,f_lease_id=?,f_lease_until=?,f_next_check=?,f_update_time=? WHERE f_lineage_id=? AND f_revision=? AND f_lease_id=? AND f_lease_until>?", r.NetworkID, r.State, r.Revision, string(raw), lease, until, r.NextCheck, r.UpdateTime, r.LineageID, revision, lease, now)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
