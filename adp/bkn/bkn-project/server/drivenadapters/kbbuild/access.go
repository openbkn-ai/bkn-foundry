// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kbbuild persists process intents and worker leases, never instances.
package kbbuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const table = "t_bkn_project_kb_build"
const projectLock = "SELECT f_archived,f_owner,f_owner_type FROM t_bkn_project_project WHERE f_id=? FOR UPDATE"
const selectionLock = "SELECT f_state,f_revision FROM t_bkn_project_kb_selection WHERE f_project_id=? AND f_id=? AND f_current=1 FOR UPDATE"
const draftLock = "SELECT f_status,f_update_time FROM t_bkn_project_draft WHERE f_project_id=? FOR UPDATE"
const activeBuilds = "SELECT COUNT(*) FROM t_bkn_project_kb_build WHERE f_project_id=? AND f_state IN ('preparing','queued','running','attention','finishing','cancel_pending','linking','linking_attention')"
const columns = "f_id,f_project_id,f_state,f_revision,f_input_hash,f_data,f_identity,f_lease_id,f_lease_until,f_create_time,f_update_time"

var safeID = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,128}$`)
var hash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var errCorrupt = errors.New("invalid document build process record")

type access struct{ db *sql.DB }

func New(db *sql.DB) interfaces.KBBuildQueue { return &access{db: db} }

type scanner interface{ Scan(...any) error }

func read(row scanner) (*interfaces.KBBuildRecord, error) {
	var id, project, state, data, identity string
	var revision, created, updated int64
	result := &interfaces.KBBuildRecord{}
	if err := row.Scan(&id, &project, &state, &revision, &result.InputHash, &data, &identity, &result.LeaseID, &result.LeaseUntil, &created, &updated); err != nil {
		return nil, err
	}
	var build interfaces.KBBuild
	if json.Unmarshal([]byte(data), &build) != nil || json.Unmarshal([]byte(identity), &result.Identity) != nil || build.ID != id || build.ProjectID != project {
		return nil, errCorrupt
	}
	build.State, build.Revision, build.CreateTime, build.UpdateTime = state, revision, created, updated
	result.Build = &build
	return result, nil
}
func (a *access) Get(ctx context.Context, project, id string) (*interfaces.KBBuildRecord, error) {
	record, err := read(a.db.QueryRowContext(ctx, "SELECT "+columns+" FROM "+table+" WHERE f_project_id=? AND f_id=?", project, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return record, err
}
func project(ctx context.Context, tx *sql.Tx, id string) (interfaces.AccountInfo, bool, error) {
	var owner interfaces.AccountInfo
	var archived bool
	err := tx.QueryRowContext(ctx, projectLock, id).Scan(&archived, &owner.ID, &owner.Type)
	return owner, archived, err
}
func current(ctx context.Context, tx *sql.Tx, build *interfaces.KBBuild) (bool, error) {
	var state string
	var revision int64
	err := tx.QueryRowContext(ctx, selectionLock, build.ProjectID, build.SelectionID).Scan(&state, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state != "active" || revision != build.SelectionRevision {
		return false, nil
	}
	var status string
	var at int64
	err = tx.QueryRowContext(ctx, draftLock, build.ProjectID).Scan(&status, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == interfaces.DRAFT_STATUS_READY && at == build.DraftUpdateTime, nil
}
func valid(record *interfaces.KBBuildRecord) bool {
	if record == nil || record.Build == nil {
		return false
	}
	b := record.Build
	return len(b.ID) <= 40 && len(b.ProjectID) <= 40 && len(b.SelectionID) <= 40 && safeID.MatchString(b.ID) && safeID.MatchString(b.ProjectID) && b.NetworkID != "" && b.Owner.ID != "" && b.Owner.Type == "user" && b.Revision > 0 && b.SelectionID != "" && b.SelectionRevision > 0 && b.DraftUpdateTime > 0 && b.DefinitionCommit != "" && b.GenerationID != "" && b.AgentVersion > 0 && hash.MatchString(b.ModelFingerprint) && hash.MatchString(record.InputHash) && len(b.Files) > 0
}
func encode(record *interfaces.KBBuildRecord) (string, string, error) {
	payload, err := json.Marshal(record.Build)
	if err != nil {
		return "", "", err
	}
	identity, err := json.Marshal(record.Identity)
	if err != nil {
		return "", "", err
	}
	return string(payload), string(identity), nil
}
func (a *access) Create(ctx context.Context, record *interfaces.KBBuildRecord) (bool, error) {
	if !valid(record) || record.Build.State != "preparing" || record.Build.Revision != 1 || record.LeaseID != "" || record.LeaseUntil != 0 || record.Build.CreateTime <= 0 || record.Build.UpdateTime != record.Build.CreateTime {
		return false, nil
	}
	payload, identity, err := encode(record)
	if err != nil {
		return false, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	owner, archived, err := project(ctx, tx, record.Build.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if archived || owner != record.Build.Owner {
		return false, nil
	}
	active, err := current(ctx, tx, record.Build)
	if err != nil || !active {
		return false, err
	}
	b := record.Build
	var activeCount int64
	if err = tx.QueryRowContext(ctx, activeBuilds, b.ProjectID).Scan(&activeCount); err != nil {
		return false, err
	}
	if activeCount != 0 {
		return false, nil
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO "+table+" ("+columns+") VALUES (?,?,?,?,?,?,?,?,?,?,?)", b.ID, b.ProjectID, b.State, b.Revision, record.InputHash, payload, identity, "", int64(0), b.CreateTime, b.UpdateTime)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
func terminal(state string) bool {
	return state == "cancelled" || state == "succeeded" || state == "needs_review"
}

// Claim also leases inactive intents for cancellation/cleanup. The returned
// cancel_pending state authorizes no source read, inference or Dataset write.
func (a *access) Claim(ctx context.Context, projectID, id, lease string, now, until int64) (*interfaces.KBBuildRecord, error) {
	if !safeID.MatchString(lease) || now <= 0 || until <= now || until-now > 300000 {
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
	record, err := read(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM "+table+" WHERE f_project_id=? AND f_id=? FOR UPDATE", projectID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !valid(record) || terminal(record.Build.State) || record.Identity.Cleaned || (record.LeaseUntil > now && record.LeaseID != lease) {
		return nil, nil
	}
	active := false
	if !archived && owner == record.Build.Owner {
		active, err = current(ctx, tx, record.Build)
		if err != nil {
			return nil, err
		}
	}
	if !active && record.Build.State != "cancel_pending" {
		record.Build.State = "cancel_pending"
		record.Build.Revision++
		record.Build.UpdateTime = max(now, record.Build.UpdateTime+1)
	}
	record.LeaseID, record.LeaseUntil = lease, until
	payload, identity, err := encode(record)
	if err != nil {
		return nil, err
	}
	mutation, err := tx.ExecContext(ctx, "UPDATE "+table+" SET f_state=?,f_revision=?,f_data=?,f_identity=?,f_lease_id=?,f_lease_until=?,f_update_time=? WHERE f_project_id=? AND f_id=?", record.Build.State, record.Build.Revision, payload, identity, lease, until, record.Build.UpdateTime, projectID, id)
	if err != nil {
		return nil, err
	}
	if count, err := mutation.RowsAffected(); err != nil || count != 1 {
		return nil, errCorrupt
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return record, nil
}
func sameIntent(a, b *interfaces.KBBuild) bool {
	if a.ReferenceID != b.ReferenceID || a.ReferenceRevision != b.ReferenceRevision || a.NetworkID != b.NetworkID || a.ID != b.ID || a.ProjectID != b.ProjectID || a.Owner != b.Owner || a.Mode != b.Mode || a.SelectionID != b.SelectionID || a.SelectionRevision != b.SelectionRevision || a.ModelFingerprint != b.ModelFingerprint || a.GenerationID != b.GenerationID || a.DefinitionCommit != b.DefinitionCommit || a.DraftUpdateTime != b.DraftUpdateTime || a.AgentVersion != b.AgentVersion || a.CreateTime != b.CreateTime || len(a.Files) != len(b.Files) {
		return false
	}
	for i := range a.Files {
		if !reflect.DeepEqual(a.Files[i].File, b.Files[i].File) {
			return false
		}
	}
	return true
}
func (a *access) Save(ctx context.Context, next *interfaces.KBBuildRecord, revision, now int64) (bool, error) {
	if !valid(next) || revision < 1 || next.Build.Revision != revision+1 || !safeID.MatchString(next.LeaseID) || now <= 0 || next.Build.UpdateTime <= 0 {
		return false, nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	owner, archived, err := project(ctx, tx, next.Build.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	old, err := read(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM "+table+" WHERE f_project_id=? AND f_id=? FOR UPDATE", next.Build.ProjectID, next.Build.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if next.Build.UpdateTime <= old.Build.UpdateTime || old.Build.Revision != revision || old.LeaseID != next.LeaseID || old.LeaseUntil <= now || !sameIntent(old.Build, next.Build) || old.InputHash != next.InputHash || terminal(old.Build.State) || next.Identity.GrantID != old.Identity.GrantID {
		return false, nil
	}
	if old.Identity.PrincipalID != "" && (old.Identity.PrincipalID != next.Identity.PrincipalID || old.Identity.Revision != next.Identity.Revision) {
		return false, nil
	}
	if old.Identity.Cleaned || (old.Identity.Outcome != "" && old.Identity.Outcome != next.Identity.Outcome) {
		return false, nil
	}
	if (old.Build.PromptVersion != nil && (next.Build.PromptVersion == nil || *old.Build.PromptVersion != *next.Build.PromptVersion)) || (old.Build.ModelID != "" && old.Build.ModelID != next.Build.ModelID) || (old.Build.Model != "" && old.Build.Model != next.Build.Model) {
		return false, nil
	}
	stopping := next.Build.State == "cancel_pending" || next.Build.State == "cancelled" || next.Build.State == "needs_review"
	if !stopping {
		if archived || owner != next.Build.Owner {
			return false, nil
		}
		active, err := current(ctx, tx, next.Build)
		if err != nil || !active {
			return false, err
		}
	}
	payload, identity, err := encode(next)
	if err != nil {
		return false, err
	}
	lease, until := old.LeaseID, old.LeaseUntil
	if terminal(next.Build.State) || next.Identity.Cleaned {
		lease = ""
		until = 0
	}
	mutation, err := tx.ExecContext(ctx, "UPDATE "+table+" SET f_state=?,f_revision=?,f_data=?,f_identity=?,f_lease_id=?,f_lease_until=?,f_update_time=? WHERE f_project_id=? AND f_id=? AND f_revision=?", next.Build.State, next.Build.Revision, payload, identity, lease, until, next.Build.UpdateTime, next.Build.ProjectID, next.Build.ID, revision)
	if err != nil {
		return false, err
	}
	if count, err := mutation.RowsAffected(); err != nil || count != 1 {
		return false, errCorrupt
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
