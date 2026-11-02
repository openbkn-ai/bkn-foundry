// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbbuild

import (
	"context"
	"database/sql"
	"errors"
	"reflect"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const otherLinkingBuilds = "SELECT COUNT(*) FROM t_bkn_project_kb_build WHERE f_project_id=? AND f_id<>? AND f_state IN ('preparing','queued','running','attention','finishing','cancel_pending','linking','linking_attention')"

func (a *access) ClaimLinking(ctx context.Context, projectID, id string, caller interfaces.AccountInfo, operation, signature string, now, until int64) (*interfaces.KBBuildRecord, error) {
	if caller.Type != "user" || caller.ID == "" || !safeID.MatchString(operation) || !hash.MatchString(signature) || now <= 0 || until <= now || until-now > 900000 {
		return nil, nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	owner, archived, err := project(ctx, tx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if archived || owner != caller {
		return nil, nil
	}
	r, err := read(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM "+table+" WHERE f_project_id=? AND f_id=? FOR UPDATE", projectID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !valid(r) || r.Build.Owner != caller || !r.Identity.Cleaned || (r.Build.State != "succeeded" && r.Build.State != "linking" && r.Build.State != "linking_attention") || (r.LeaseUntil > now && r.LeaseID != operation) {
		return nil, nil
	}
	if r.Build.Linking != nil && r.Build.Linking.OperationID == operation && r.Build.Linking.Signature != signature {
		return nil, nil
	}
	for _, file := range r.Build.Files {
		if file.State != "succeeded" {
			return nil, nil
		}
	}
	active, err := current(ctx, tx, r.Build)
	if err != nil || !active {
		return nil, err
	}
	var others int64
	if err := tx.QueryRowContext(ctx, otherLinkingBuilds, projectID, id).Scan(&others); err != nil {
		return nil, err
	}
	if others != 0 {
		return nil, nil
	}
	// A fresh client intent for the same verified complete snapshot is also a
	// no-op. Preserve the original receipt instead of writing identical rows.
	if r.Build.State == "succeeded" && r.Build.Linking != nil && r.Build.Linking.Signature == signature && r.Build.Linking.State == "succeeded" {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return r, nil
	}
	r.Build.State = "linking"
	r.Build.Revision++
	r.Build.UpdateTime = max(now, r.Build.UpdateTime+1)
	r.Build.Linking = &interfaces.KBLinkingReceipt{OperationID: operation, Signature: signature, State: "running"}
	r.LeaseID, r.LeaseUntil = operation, until
	payload, identity, err := encode(r)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE "+table+" SET f_state=?,f_revision=?,f_data=?,f_identity=?,f_lease_id=?,f_lease_until=?,f_update_time=? WHERE f_project_id=? AND f_id=?", r.Build.State, r.Build.Revision, payload, identity, operation, until, r.Build.UpdateTime, projectID, id)
	if err != nil {
		return nil, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return nil, errCorrupt
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}

func (a *access) CheckLinking(ctx context.Context, projectID, id, operation string, revision, now int64) (bool, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	r, ok, err := linkingCurrent(ctx, tx, projectID, id, operation, revision, now)
	if err != nil || !ok || r == nil {
		return false, err
	}
	// Renew only a still-live, fenced operation. Large batches may outlast the
	// initial lease; expired or cancelled operations must never be resurrected.
	// A no-op UPDATE reports zero changed rows with the default MySQL DSN.
	// The row is already locked and fenced; keep an equal or later expiry.
	if until := now + 900000; until > r.LeaseUntil {
		result, err := tx.ExecContext(ctx, "UPDATE "+table+" SET f_lease_until=? WHERE f_project_id=? AND f_id=? AND f_revision=? AND f_lease_id=?", until, projectID, id, revision, operation)
		if err != nil {
			return false, err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return false, errCorrupt
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func linkingCurrent(ctx context.Context, tx *sql.Tx, projectID, id, operation string, revision, now int64) (*interfaces.KBBuildRecord, bool, error) {
	owner, archived, err := project(ctx, tx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if archived {
		return nil, false, nil
	}
	r, err := read(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM "+table+" WHERE f_project_id=? AND f_id=? FOR UPDATE", projectID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !valid(r) || owner != r.Build.Owner || r.Build.State != "linking" || r.Build.Revision != revision || r.LeaseID != operation || r.LeaseUntil <= now || r.Build.Linking == nil || r.Build.Linking.OperationID != operation || r.Build.Linking.State != "running" {
		return nil, false, nil
	}
	active, err := current(ctx, tx, r.Build)
	return r, active, err
}

func (a *access) SaveLinking(ctx context.Context, next *interfaces.KBBuildRecord, revision, now int64) (bool, error) {
	if !valid(next) || next.Build.Linking == nil || next.Build.Revision != revision+1 || next.Build.UpdateTime <= 0 || now <= 0 {
		return false, nil
	}
	receipt := next.Build.Linking
	if !safeID.MatchString(receipt.OperationID) || !hash.MatchString(receipt.Signature) || receipt.RowsWritten < 0 || receipt.Eligible < 0 || receipt.Linked < 0 || receipt.Pending < 0 || receipt.Linked+receipt.Pending != receipt.Eligible {
		return false, nil
	}
	complete := next.Build.State == "succeeded" && receipt.State == "succeeded"
	attention := next.Build.State == "linking_attention" && receipt.State == "attention"
	if !complete && !attention {
		return false, nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	old, ok, err := linkingCurrent(ctx, tx, next.Build.ProjectID, next.Build.ID, next.LeaseID, revision, now)
	if err != nil || !ok {
		return false, err
	}
	if old.Build.Linking.Signature != receipt.Signature || old.Build.Linking.OperationID != receipt.OperationID || old.Build.UpdateTime >= next.Build.UpdateTime || old.InputHash != next.InputHash || !reflect.DeepEqual(old.Identity, next.Identity) {
		return false, nil
	}
	left, right := *old.Build, *next.Build
	left.State, right.State = "", ""
	left.Linking, right.Linking = nil, nil
	left.Revision, right.Revision, left.UpdateTime, right.UpdateTime = 0, 0, 0, 0
	if !reflect.DeepEqual(left, right) {
		return false, nil
	}
	payload, identity, err := encode(next)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE "+table+" SET f_state=?,f_revision=?,f_data=?,f_identity=?,f_lease_id='',f_lease_until=0,f_update_time=? WHERE f_project_id=? AND f_id=? AND f_revision=?", next.Build.State, next.Build.Revision, payload, identity, next.Build.UpdateTime, next.Build.ProjectID, next.Build.ID, revision)
	if err != nil {
		return false, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return false, errCorrupt
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

var _ interfaces.KBLinkingAccess = (*access)(nil)
