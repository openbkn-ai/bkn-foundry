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

const publicationTable = "t_bkn_project_kb_publication"
const publicationColumns = "f_network_id,f_project_id,f_generation_id,f_state,f_revision,f_data,f_create_time,f_update_time"
const publicationQuery = "SELECT " + publicationColumns + " FROM " + publicationTable + " WHERE f_network_id=?"

type publicationAccess struct{ db *sql.DB }

func NewPublications(db *sql.DB) interfaces.KBPublicationAccess { return &publicationAccess{db: db} }
func validPublication(p *interfaces.KBPublication) bool {
	if p == nil || len(p.NetworkID) > 128 || len(p.ProjectID) > 40 || !safeID.MatchString(p.NetworkID) || !safeID.MatchString(p.ProjectID) || !safeID.MatchString(p.GenerationID) || p.LineageID == "" || p.DefinitionCommit == "" || p.FullBuildID == "" || p.Publisher.ID == "" || p.Publisher.Type != "user" || p.Revision < 1 || p.DraftUpdateTime <= 0 || p.CreateTime <= 0 || p.UpdateTime < p.CreateTime {
		return false
	}
	if !hash.MatchString(p.ModelFingerprint) || !hash.MatchString(p.RuntimeDigest) || p.RuntimeRowID != p.RuntimeDigest || !hash.MatchString(p.DefinitionDigest) || p.Snapshot.ProjectID != p.ProjectID || p.Snapshot.Owner.Type != "app" || p.Snapshot.Owner.ID == "" || p.Snapshot.ResourceID == "" || p.Snapshot.CatalogID == "" || p.Snapshot.ObjectTypeID != "published_model" || len(p.Ranges) == 0 {
		return false
	}
	switch p.State {
	case "preparing", "sealed", "mounted", "active", "archived":
		return true
	}
	return false
}
func readPublication(row scanner) (*interfaces.KBPublication, error) {
	var network, project, generation, state, data string
	var revision, created, updated int64
	if err := row.Scan(&network, &project, &generation, &state, &revision, &data, &created, &updated); err != nil {
		return nil, err
	}
	var p interfaces.KBPublication
	if json.Unmarshal([]byte(data), &p) != nil || p.NetworkID != network || p.ProjectID != project || p.GenerationID != generation {
		return nil, errCorrupt
	}
	p.State = state
	p.Revision = revision
	p.CreateTime = created
	p.UpdateTime = updated
	if !validPublication(&p) {
		return nil, errCorrupt
	}
	return &p, nil
}
func (a *publicationAccess) Get(ctx context.Context, network string) (*interfaces.KBPublication, error) {
	p, err := readPublication(a.db.QueryRowContext(ctx, publicationQuery, network))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}
func publicationBuild(ctx context.Context, tx *sql.Tx, p *interfaces.KBPublication) (bool, error) {
	r, err := read(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM "+table+" WHERE f_project_id=? AND f_id=? FOR UPDATE", p.ProjectID, p.FullBuildID))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !valid(r) || r.Build.NetworkID != p.LineageID || r.Build.Mode != "full" || r.Build.State != "succeeded" || !r.Identity.Cleaned || r.Build.Owner != p.Publisher || r.Build.ModelFingerprint != p.ModelFingerprint || r.Build.GenerationID != p.GenerationID || r.Build.DefinitionCommit != p.DefinitionCommit || r.Build.DraftUpdateTime != p.DraftUpdateTime {
		return false, nil
	}
	for _, file := range r.Build.Files {
		if file.State != "succeeded" {
			return false, nil
		}
	}
	if r.Build.Linking != nil && r.Build.Linking.State != "succeeded" {
		return false, nil
	}
	return currentModel(ctx, tx, r.Build)
}
func (a *publicationAccess) Prepare(ctx context.Context, p *interfaces.KBPublication) (bool, error) {
	if !validPublication(p) || p.State != "preparing" || p.Revision != 1 || p.UpdateTime != p.CreateTime {
		return false, nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	owner, archived, err := project(ctx, tx, p.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if archived || owner != p.Publisher {
		return false, nil
	}
	ok, err := publicationBuild(ctx, tx, p)
	if err != nil || !ok {
		return false, err
	}
	existing, err := readPublication(tx.QueryRowContext(ctx, publicationQuery+" FOR UPDATE", p.NetworkID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if existing != nil {
		if existing.ProjectID != p.ProjectID || existing.Publisher != p.Publisher || existing.FullBuildID != p.FullBuildID || existing.RuntimeDigest != p.RuntimeDigest || existing.DefinitionDigest != p.DefinitionDigest || existing.GenerationID != p.GenerationID {
			return false, nil
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO "+publicationTable+" ("+publicationColumns+") VALUES (?,?,?,?,?,?,?,?)", p.NetworkID, p.ProjectID, p.GenerationID, p.State, p.Revision, string(raw), p.CreateTime, p.UpdateTime)
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
func (a *publicationAccess) Seal(ctx context.Context, network, runtimeDigest string, now int64) (bool, error) {
	p, err := a.Get(ctx, network)
	if err != nil || p == nil {
		return false, err
	}
	if !hash.MatchString(runtimeDigest) || p.RuntimeDigest != runtimeDigest || now <= 0 {
		return false, nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	owner, archived, err := project(ctx, tx, p.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if archived || owner != p.Publisher {
		return false, nil
	}
	locked, err := readPublication(tx.QueryRowContext(ctx, publicationQuery+" FOR UPDATE", network))
	if err != nil {
		return false, err
	}
	if locked.Revision != p.Revision || locked.RuntimeDigest != runtimeDigest {
		return false, nil
	}
	if locked.State != "preparing" {
		if locked.State == "archived" {
			return false, nil
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	ok, err := publicationBuild(ctx, tx, locked)
	if err != nil || !ok {
		return false, err
	}
	var active int64
	if err := tx.QueryRowContext(ctx, activeBuilds, p.ProjectID).Scan(&active); err != nil {
		return false, err
	}
	if active != 0 {
		return false, nil
	}
	locked.State = "sealed"
	locked.Revision++
	locked.UpdateTime = max(now, locked.UpdateTime+1)
	locked.Snapshot.Ready = true
	raw, err := json.Marshal(locked)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE "+publicationTable+" SET f_state=?,f_revision=?,f_data=?,f_update_time=? WHERE f_network_id=? AND f_revision=?", locked.State, locked.Revision, string(raw), locked.UpdateTime, network, p.Revision)
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

const publicationRelease = "SELECT COUNT(*) FROM t_bkn_project_release WHERE f_network_id=? AND f_project_id=? AND f_lineage_id=? AND f_publisher=? AND f_publisher_type=?"

func (a *publicationAccess) MarkMounted(ctx context.Context, network, runtimeDigest string, now int64) (bool, error) {
	return a.advance(ctx, network, runtimeDigest, "sealed", "mounted", now, false)
}
func (a *publicationAccess) Activate(ctx context.Context, network, runtimeDigest string, now int64) (bool, error) {
	return a.advance(ctx, network, runtimeDigest, "mounted", "active", now, true)
}
func (a *publicationAccess) advance(ctx context.Context, network, runtimeDigest, from, to string, now int64, releaseRequired bool) (bool, error) {
	if !hash.MatchString(runtimeDigest) || now <= 0 {
		return false, nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := readPublication(tx.QueryRowContext(ctx, publicationQuery+" FOR UPDATE", network))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if p.RuntimeDigest != runtimeDigest {
		return false, nil
	}
	if p.State == to || (to == "mounted" && p.State == "active") {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	if p.State != from {
		return false, nil
	}
	if releaseRequired {
		var recorded int64
		if err := tx.QueryRowContext(ctx, publicationRelease, p.NetworkID, p.ProjectID, p.LineageID, p.Publisher.ID, p.Publisher.Type).Scan(&recorded); err != nil {
			return false, err
		}
		if recorded != 1 {
			return false, nil
		}
	}
	previous := p.Revision
	p.State = to
	p.Revision++
	p.UpdateTime = max(now, p.UpdateTime+1)
	raw, err := json.Marshal(p)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE "+publicationTable+" SET f_state=?,f_revision=?,f_data=?,f_update_time=? WHERE f_network_id=? AND f_revision=?", p.State, p.Revision, string(raw), p.UpdateTime, p.NetworkID, previous)
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

func (a *publicationAccess) ActiveNetworks(ctx context.Context, after string, limit int) ([]string, error) {
	if limit < 1 || limit > 128 {
		limit = 16
	}
	rows, err := a.db.QueryContext(ctx, "SELECT p.f_network_id FROM "+publicationTable+" p JOIN t_bkn_project_release r ON r.f_network_id=p.f_network_id WHERE p.f_state='active' AND p.f_network_id>? AND p.f_network_id=COALESCE((SELECT s.f_to_network_id FROM t_bkn_project_switch s WHERE s.f_lineage_id=r.f_lineage_id ORDER BY s.f_create_time DESC,s.f_id DESC LIMIT 1),r.f_lineage_id) ORDER BY p.f_network_id LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	networks := []string{}
	for rows.Next() {
		var network string
		if err := rows.Scan(&network); err != nil {
			return nil, err
		}
		networks = append(networks, network)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return networks, nil
}

var _ interfaces.KBPublicationDiscovery = (*publicationAccess)(nil)
