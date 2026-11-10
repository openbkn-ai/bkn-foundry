// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_version

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	stateTable   = "t_kn_version_state"
	versionTable = "t_kn_version"
	eventTable   = "t_kn_version_event"
)

var (
	versionAccessOnce sync.Once
	versionAccessInst interfaces.KNVersionAccess
)

type versionAccess struct {
	db *sql.DB
}

func NewKNVersionAccess(appSetting *common.AppSetting) interfaces.KNVersionAccess {
	versionAccessOnce.Do(func() {
		versionAccessInst = &versionAccess{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return versionAccessInst
}

// NewKNVersionAccessWithDB is for tests.
func NewKNVersionAccessWithDB(db *sql.DB) interfaces.KNVersionAccess {
	return &versionAccess{db: db}
}

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (va *versionAccess) q(tx *sql.Tx) querier {
	if tx != nil {
		return tx
	}
	return va.db
}

func lockClause(forUpdate bool) string {
	if forUpdate {
		return " FOR UPDATE"
	}
	return ""
}

func (va *versionAccess) LockNetwork(ctx context.Context, tx *sql.Tx, knID string, exclusive bool) (bool, error) {
	lock := " LOCK IN SHARE MODE"
	if exclusive {
		lock = " FOR UPDATE"
	}
	var id string
	err := tx.QueryRowContext(ctx, "SELECT f_id FROM "+knTable+" WHERE f_id = ? AND f_branch = ?"+lock,
		knID, interfaces.MAIN_BRANCH).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock network %s: %w", knID, err)
	}
	return true, nil
}

const stateColumns = "f_kn_id, f_mode, f_running_version, f_previous_version, f_next_version, " +
	"f_draft_base_version, f_draft_based_on_running, f_draft_modified, f_draft_revision, " +
	"f_draft_index_build, f_draft_index_revision, f_enabled_by, f_enabled_by_type, f_enable_time, f_update_time"

func stateValues(s *interfaces.KNVersionState) []any {
	return []any{s.KNID, s.Mode, s.RunningVersion, s.PreviousVersion, s.NextVersion,
		s.DraftBaseVersion, s.DraftBasedOnRunning, s.DraftModified, s.DraftRevision,
		s.DraftIndexBuild, s.DraftIndexRevision, s.EnabledBy, s.EnabledByType, s.EnableTime, s.UpdateTime}
}

func (va *versionAccess) GetState(ctx context.Context, tx *sql.Tx, knID string, forUpdate bool) (*interfaces.KNVersionState, error) {
	s := &interfaces.KNVersionState{}
	err := va.q(tx).QueryRowContext(ctx, "SELECT "+stateColumns+" FROM "+stateTable+" WHERE f_kn_id = ?"+lockClause(forUpdate),
		knID).Scan(&s.KNID, &s.Mode, &s.RunningVersion, &s.PreviousVersion, &s.NextVersion,
		&s.DraftBaseVersion, &s.DraftBasedOnRunning, &s.DraftModified, &s.DraftRevision,
		&s.DraftIndexBuild, &s.DraftIndexRevision, &s.EnabledBy, &s.EnabledByType, &s.EnableTime, &s.UpdateTime)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get the version state of %s: %w", knID, err)
	}
	return s, nil
}

func (va *versionAccess) CreateState(ctx context.Context, tx *sql.Tx, state *interfaces.KNVersionState) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO "+stateTable+" ("+stateColumns+") VALUES (?"+strings.Repeat(", ?", 14)+")",
		stateValues(state)...)
	if err != nil {
		return fmt.Errorf("create the version state of %s: %w", state.KNID, err)
	}
	return nil
}

func (va *versionAccess) UpdateState(ctx context.Context, tx *sql.Tx, s *interfaces.KNVersionState) error {
	_, err := tx.ExecContext(ctx, "UPDATE "+stateTable+" SET f_mode = ?, f_running_version = ?, f_previous_version = ?, "+
		"f_next_version = ?, f_draft_base_version = ?, f_draft_based_on_running = ?, f_draft_modified = ?, "+
		"f_draft_revision = ?, f_draft_index_build = ?, f_draft_index_revision = ?, f_update_time = ? WHERE f_kn_id = ?",
		s.Mode, s.RunningVersion, s.PreviousVersion, s.NextVersion, s.DraftBaseVersion, s.DraftBasedOnRunning,
		s.DraftModified, s.DraftRevision, s.DraftIndexBuild, s.DraftIndexRevision, s.UpdateTime, s.KNID)
	if err != nil {
		return fmt.Errorf("update the version state of %s: %w", s.KNID, err)
	}
	return nil
}

func (va *versionAccess) MarkDraftWritten(ctx context.Context, tx *sql.Tx, knID string, expected *int64) (int64, bool, error) {
	query := "UPDATE " + stateTable + " SET f_draft_revision = f_draft_revision + 1, f_draft_modified = 1 WHERE f_kn_id = ?"
	args := []any{knID}
	if expected != nil {
		query += " AND f_draft_revision = ?"
		args = append(args, *expected)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, false, fmt.Errorf("mark the draft of %s written: %w", knID, err)
	}
	// f_draft_revision always changes, so a matched row is always an affected row.
	if rows, err := result.RowsAffected(); err != nil || rows == 0 {
		return 0, false, err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, "SELECT f_draft_revision FROM "+stateTable+" WHERE f_kn_id = ?", knID).Scan(&revision); err != nil {
		return 0, false, fmt.Errorf("read the draft revision of %s: %w", knID, err)
	}
	return revision, true, nil
}

func (va *versionAccess) MaxVersion(ctx context.Context, tx *sql.Tx, knID string) (int, error) {
	var max sql.NullInt64
	if err := va.q(tx).QueryRowContext(ctx, "SELECT MAX(f_version) FROM "+versionTable+" WHERE f_kn_id = ?", knID).Scan(&max); err != nil {
		return 0, fmt.Errorf("get the latest version of %s: %w", knID, err)
	}
	return int(max.Int64), nil
}

const versionColumns = "f_kn_id, f_version, f_label, f_status, f_origin, f_base_version, f_draft_revision, " +
	"f_index_build, f_notes, f_source, f_publisher, f_publisher_type, f_publish_time, " +
	"f_status_by, f_status_by_type, f_status_time"

func (va *versionAccess) CreateVersion(ctx context.Context, tx *sql.Tx, v *interfaces.KNVersion) error {
	source := ""
	if v.Source != nil {
		raw, err := json.Marshal(v.Source)
		if err != nil {
			return err
		}
		source = string(raw)
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO "+versionTable+" ("+versionColumns+") VALUES (?"+strings.Repeat(", ?", 15)+")",
		v.KNID, v.Version, v.Label, v.Status, v.Origin, v.BaseVersion, v.DraftRevision, v.IndexBuild, v.Notes, source,
		v.Publisher.ID, v.Publisher.Type, v.PublishTime, v.StatusBy.ID, v.StatusBy.Type, v.StatusTime)
	if err != nil {
		return fmt.Errorf("create version %d of %s: %w", v.Version, v.KNID, err)
	}
	return nil
}

func scanVersion(scan func(...any) error) (*interfaces.KNVersion, error) {
	v := &interfaces.KNVersion{}
	var source string
	if err := scan(&v.KNID, &v.Version, &v.Label, &v.Status, &v.Origin, &v.BaseVersion, &v.DraftRevision,
		&v.IndexBuild, &v.Notes, &source, &v.Publisher.ID, &v.Publisher.Type, &v.PublishTime,
		&v.StatusBy.ID, &v.StatusBy.Type, &v.StatusTime); err != nil {
		return nil, err
	}
	if source != "" {
		v.Source = &interfaces.KNVersionSource{}
		if err := json.Unmarshal([]byte(source), v.Source); err != nil {
			return nil, fmt.Errorf("decode the source of version %d: %w", v.Version, err)
		}
	}
	return v, nil
}

func (va *versionAccess) GetVersion(ctx context.Context, tx *sql.Tx, knID string, version int, forUpdate bool) (*interfaces.KNVersion, error) {
	row := va.q(tx).QueryRowContext(ctx, "SELECT "+versionColumns+" FROM "+versionTable+
		" WHERE f_kn_id = ? AND f_version = ?"+lockClause(forUpdate), knID, version)
	v, err := scanVersion(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get version %d of %s: %w", version, knID, err)
	}
	return v, nil
}

func (va *versionAccess) ListVersions(ctx context.Context, knID string, query *interfaces.KNVersionsQuery) ([]*interfaces.KNVersion, int, error) {
	where := " WHERE f_kn_id = ?"
	args := []any{knID}
	if len(query.Statuses) > 0 {
		where += " AND f_status IN (?" + strings.Repeat(", ?", len(query.Statuses)-1) + ")"
		for _, status := range query.Statuses {
			args = append(args, status)
		}
	}
	var total int
	if err := va.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+versionTable+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count the versions of %s: %w", knID, err)
	}
	rows, err := va.db.QueryContext(ctx, "SELECT "+versionColumns+" FROM "+versionTable+where+
		" ORDER BY f_version DESC"+limitClause(query.Offset, query.Limit), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list the versions of %s: %w", knID, err)
	}
	defer func() { _ = rows.Close() }()
	versions := []*interfaces.KNVersion{}
	for rows.Next() {
		v, err := scanVersion(rows.Scan)
		if err != nil {
			return nil, 0, err
		}
		versions = append(versions, v)
	}
	return versions, total, rows.Err()
}

func limitClause(offset, limit int) string {
	if limit <= 0 {
		return ""
	}
	return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, max(offset, 0))
}

func (va *versionAccess) UpdateVersionStatus(ctx context.Context, tx *sql.Tx, v *interfaces.KNVersion) error {
	_, err := tx.ExecContext(ctx, "UPDATE "+versionTable+" SET f_status = ?, f_status_by = ?, f_status_by_type = ?, "+
		"f_status_time = ? WHERE f_kn_id = ? AND f_version = ?",
		v.Status, v.StatusBy.ID, v.StatusBy.Type, v.StatusTime, v.KNID, v.Version)
	if err != nil {
		return fmt.Errorf("update the status of version %d of %s: %w", v.Version, v.KNID, err)
	}
	return nil
}

const eventColumns = "f_id, f_kn_id, f_type, f_from_version, f_to_version, f_reason, f_notes, " +
	"f_operator, f_operator_type, f_create_time, f_idempotency_key"

func (va *versionAccess) CreateEvent(ctx context.Context, tx *sql.Tx, e *interfaces.KNVersionEvent) error {
	var key any
	if e.IdempotencyKey != "" {
		key = e.IdempotencyKey
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO "+eventTable+" ("+eventColumns+") VALUES (?"+strings.Repeat(", ?", 10)+")",
		e.ID, e.KNID, e.Type, e.FromVersion, e.ToVersion, e.Reason, e.Notes, e.Operator.ID, e.Operator.Type, e.CreateTime, key)
	if err != nil {
		return fmt.Errorf("create a %s event of %s: %w", e.Type, e.KNID, err)
	}
	return nil
}

func scanEvent(scan func(...any) error) (*interfaces.KNVersionEvent, error) {
	e := &interfaces.KNVersionEvent{}
	var key sql.NullString
	if err := scan(&e.ID, &e.KNID, &e.Type, &e.FromVersion, &e.ToVersion, &e.Reason, &e.Notes,
		&e.Operator.ID, &e.Operator.Type, &e.CreateTime, &key); err != nil {
		return nil, err
	}
	e.IdempotencyKey = key.String
	return e, nil
}

func (va *versionAccess) GetEventByIdempotencyKey(ctx context.Context, tx *sql.Tx, knID, key string) (*interfaces.KNVersionEvent, error) {
	row := va.q(tx).QueryRowContext(ctx, "SELECT "+eventColumns+" FROM "+eventTable+
		" WHERE f_kn_id = ? AND f_idempotency_key = ?", knID, key)
	e, err := scanEvent(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get the event of idempotency key on %s: %w", knID, err)
	}
	return e, nil
}

func (va *versionAccess) ListEvents(ctx context.Context, knID string, offset, limit int) ([]*interfaces.KNVersionEvent, int, error) {
	var total int
	if err := va.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+eventTable+" WHERE f_kn_id = ?", knID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count the version events of %s: %w", knID, err)
	}
	rows, err := va.db.QueryContext(ctx, "SELECT "+eventColumns+" FROM "+eventTable+
		" WHERE f_kn_id = ? ORDER BY f_create_time DESC, f_id DESC"+limitClause(offset, limit), knID)
	if err != nil {
		return nil, 0, fmt.Errorf("list the version events of %s: %w", knID, err)
	}
	defer func() { _ = rows.Close() }()
	events := []*interfaces.KNVersionEvent{}
	for rows.Next() {
		e, err := scanEvent(rows.Scan)
		if err != nil {
			return nil, 0, err
		}
		events = append(events, e)
	}
	return events, total, rows.Err()
}

func (va *versionAccess) DeleteState(ctx context.Context, tx *sql.Tx, knID string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+stateTable+" WHERE f_kn_id = ?", knID); err != nil {
		return fmt.Errorf("delete the version state of %s: %w", knID, err)
	}
	return nil
}

func (va *versionAccess) DeleteNetwork(ctx context.Context, tx *sql.Tx, knID string) error {
	for _, table := range []string{stateTable, versionTable, eventTable, "t_object_metric_version_seq"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE f_kn_id = ?", knID); err != nil {
			return fmt.Errorf("delete %s of %s: %w", table, knID, err)
		}
	}
	return nil
}

// revisionTables maps each revision kind to the table of its definitions.
var revisionTables = map[string]string{
	interfaces.RevisionKindObjectType:        "t_object_type",
	interfaces.RevisionKindRelationType:      "t_relation_type",
	interfaces.RevisionKindActionType:        "t_action_type",
	interfaces.RevisionKindConceptGroup:      "t_concept_group",
	interfaces.RevisionKindValueType:         "t_value_type",
	interfaces.RevisionKindRiskType:          "t_risk_type",
	interfaces.RevisionKindCapabilityBinding: "t_kn_capability_binding",
}

func revisionTable(kind string) (string, error) {
	table, ok := revisionTables[kind]
	if !ok {
		return "", fmt.Errorf("definitions of kind %q have no revision", kind)
	}
	return table, nil
}

func (va *versionAccess) DefinitionRevision(ctx context.Context, tx *sql.Tx, kind, knID, branch, id string) (string, bool, error) {
	table, err := revisionTable(kind)
	if err != nil {
		return "", false, err
	}
	var revision string
	err = va.q(tx).QueryRowContext(ctx, "SELECT f_revision FROM "+table+
		" WHERE f_kn_id = ? AND f_branch = ? AND f_id = ?"+lockClause(tx != nil), knID, branch, id).Scan(&revision)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get the revision of %s %s: %w", kind, id, err)
	}
	return revision, true, nil
}

func (va *versionAccess) TouchDefinitionRevision(ctx context.Context, tx *sql.Tx, kind, knID, branch, id string) error {
	table, err := revisionTable(kind)
	if err != nil {
		return err
	}
	if _, err := va.q(tx).ExecContext(ctx, "UPDATE "+table+" SET f_revision = ? WHERE f_kn_id = ? AND f_branch = ? AND f_id = ?",
		interfaces.NewDefinitionRevision(), knID, branch, id); err != nil {
		return fmt.Errorf("touch the revision of %s %s: %w", kind, id, err)
	}
	return nil
}

func (va *versionAccess) DefinitionRevisions(ctx context.Context, kind, knID, branch string, ids []string) (map[string]string, error) {
	revisions := map[string]string{}
	if len(ids) == 0 {
		return revisions, nil
	}
	table, err := revisionTable(kind)
	if err != nil {
		return nil, err
	}
	args := []any{knID, branch}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := va.db.QueryContext(ctx, "SELECT f_id, f_revision FROM "+table+" WHERE f_kn_id = ? AND f_branch = ? AND f_id IN (?"+
		strings.Repeat(", ?", len(ids)-1)+")", args...)
	if err != nil {
		return nil, fmt.Errorf("list the revisions of %s: %w", kind, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, revision string
		if err := rows.Scan(&id, &revision); err != nil {
			return nil, err
		}
		revisions[id] = revision
	}
	return revisions, rows.Err()
}
