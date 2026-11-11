// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	typeTable   = "t_kn_event_type"
	eventTable  = "t_kn_event"
	changeTable = "t_kn_event_change"
	checkTable  = "t_kn_event_rule_check"
	headTable   = "t_kn_event_head"

	// Rows per multi-row statement, well under MariaDB's 65535 placeholders.
	eventBatch  = 200
	changeBatch = 500

	// classLockWait is how many seconds a change of a network's event classes
	// waits for one in progress elsewhere.
	classLockWait = 30
	// lockRelease bounds giving a named lock back once its work is done.
	lockRelease = 2 * time.Second
	// lockName is how a named lock is called in MariaDB. Its names are
	// server-wide and short: hashed with the database's, two databases on one
	// server do not wait on each other, and a long name fits.
	lockName = "SHA2(CONCAT(DATABASE(), ':', ?), 224)"
)

// errClassesBusy says a change of a network's event classes waited too long
// for one in progress.
var errClassesBusy = errors.New("another change of the network's event types is in progress")

var typeColumns = []string{
	"f_kn_id", "f_code", "f_name", "f_source", "f_subject", "f_severity", "f_merge_hours",
	"f_rule", "f_rule_version", "f_creator", "f_creator_type", "f_create_time",
	"f_updater", "f_updater_type", "f_update_time", "f_object_type_id", "f_dataset_id",
}

var eventColumns = []string{
	"f_id", "f_kn_id", "f_event_type", "f_title", "f_status", "f_severity", "f_source",
	"f_rule_version", "f_object_type", "f_object_key", "f_object_name", "f_object_attributes",
	"f_occurred_at", "f_recorded_at", "f_ended_at", "f_first_detected_at", "f_last_checked_at",
	"f_check_status", "f_detail", "f_backlog", "f_merged_into", "f_reported_by", "f_reported_by_name",
	"f_verified_by", "f_verified_by_type", "f_verified_by_name", "f_verified_at", "f_revision",
	"f_create_time", "f_update_time",
}

var changeColumns = []string{
	"f_kn_id", "f_seq", "f_event_id", "f_action", "f_from_status", "f_to_status",
	"f_actor", "f_actor_type", "f_actor_name", "f_note", "f_time",
}

var checkColumns = []string{
	"f_event_type", "f_status", "f_rule_version", "f_hits", "f_message", "f_last_ok", "f_checked_by", "f_time",
}

// eventFrom joins each event to its type for the type's current name, which
// is never stored on the event.
const eventFrom = eventTable + " e LEFT JOIN " + typeTable + " t ON t.f_kn_id = e.f_kn_id AND t.f_code = e.f_event_type"

func eventSelectColumns() []string {
	columns := make([]string, 0, len(eventColumns)+1)
	for _, column := range eventColumns {
		columns = append(columns, "e."+column)
	}
	return append(columns, "COALESCE(t.f_name, '')")
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type scanner interface{ Scan(dest ...any) error }

// store keeps a network's events, their change feed and its rule checks.
type store struct {
	db *sql.DB
	// classWait is how many seconds a change of a network's event classes
	// waits for one in progress.
	classWait int
}

func newStore(db *sql.DB) *store {
	return &store{db: db, classWait: classLockWait}
}

func rawJSON(value string) json.RawMessage {
	if value == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(value)
}

func scanType(row scanner) (interfaces.KnEventType, error) {
	var eventType interfaces.KnEventType
	var rule string
	err := row.Scan(
		&eventType.KNID, &eventType.Code, &eventType.Name, &eventType.Source, &eventType.Subject,
		&eventType.Severity, &eventType.MergeHours, &rule, &eventType.RuleVersion,
		&eventType.Creator, &eventType.CreatorType, &eventType.CreateTime,
		&eventType.Updater, &eventType.UpdaterType, &eventType.UpdateTime,
		&eventType.ObjectTypeID, &eventType.DatasetID,
	)
	eventType.Rule = rawJSON(rule)
	return eventType, err
}

// scanEvent reads eventSelectColumns after any leading columns.
func scanEvent(row scanner, leading ...any) (*interfaces.KnEvent, error) {
	var event interfaces.KnEvent
	var attributes, detail string
	destinations := make([]any, 0, len(leading)+len(eventColumns)+1)
	destinations = append(destinations, leading...)
	destinations = append(destinations,
		&event.ID, &event.KNID, &event.EventType, &event.Title, &event.Status, &event.Severity, &event.Source,
		&event.RuleVersion, &event.ObjectType, &event.ObjectKey, &event.ObjectName, &attributes,
		&event.OccurredAt, &event.RecordedAt, &event.EndedAt, &event.FirstDetectedAt, &event.LastCheckedAt,
		&event.CheckStatus, &detail, &event.Backlog, &event.MergedInto, &event.ReportedBy, &event.ReportedByName,
		&event.VerifiedBy, &event.VerifiedByType, &event.VerifiedByName, &event.VerifiedAt, &event.Revision,
		&event.CreateTime, &event.UpdateTime, &event.EventTypeName,
	)
	if err := row.Scan(destinations...); err != nil {
		return nil, err
	}
	event.ObjectAttributes = map[string]interfaces.KnEventAttribute{}
	if attributes != "" {
		if err := json.Unmarshal([]byte(attributes), &event.ObjectAttributes); err != nil {
			return nil, fmt.Errorf("decode attributes of event %s: %w", event.ID, err)
		}
	}
	event.Detail = rawJSON(detail)
	event.Actions = actionsFor(&event)
	return &event, nil
}

func queryEvents(ctx context.Context, q queryer, builder sq.SelectBuilder) ([]*interfaces.KnEvent, error) {
	query, args, err := builder.ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := []*interfaces.KnEvent{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func getType(ctx context.Context, q queryer, knID, code string) (interfaces.KnEventType, bool, error) {
	query, args, err := sq.Select(typeColumns...).From(typeTable).
		Where(sq.Eq{"f_kn_id": knID, "f_code": code}).ToSql()
	if err != nil {
		return interfaces.KnEventType{}, false, err
	}
	eventType, err := scanType(q.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return interfaces.KnEventType{}, false, nil
	}
	return eventType, err == nil, err
}

func (s *store) listTypes(ctx context.Context, knID string) ([]interfaces.KnEventType, error) {
	query, args, err := sq.Select(typeColumns...).From(typeTable).
		Where(sq.Eq{"f_kn_id": knID}).OrderBy("f_code ASC").ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	types := []interfaces.KnEventType{}
	for rows.Next() {
		eventType, err := scanType(rows)
		if err != nil {
			return nil, err
		}
		types = append(types, eventType)
	}
	return types, rows.Err()
}

func (s *store) getType(ctx context.Context, knID, code string) (interfaces.KnEventType, bool, error) {
	return getType(ctx, s.db, knID, code)
}

// createType inserts an event type, reporting false when the code is taken.
func (w *writer) createType(eventType interfaces.KnEventType) (bool, error) {
	query, args, err := sq.Insert(typeTable).Columns(typeColumns...).Values(
		eventType.KNID, eventType.Code, eventType.Name, eventType.Source, eventType.Subject,
		eventType.Severity, eventType.MergeHours, string(eventType.Rule), eventType.RuleVersion,
		eventType.Creator, eventType.CreatorType, eventType.CreateTime,
		eventType.Updater, eventType.UpdaterType, eventType.UpdateTime,
		eventType.ObjectTypeID, eventType.DatasetID,
	).Suffix("ON DUPLICATE KEY UPDATE f_code = f_code").ToSql()
	if err != nil {
		return false, err
	}
	result, err := w.tx.ExecContext(w.ctx, query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// updateType stores what an update may change. A type that moves to another
// dataset has none of its events there yet; MariaDB assigns in order, so that
// is decided before the dataset is.
func (w *writer) updateType(eventType interfaces.KnEventType) error {
	query, args, err := sq.Update(typeTable).SetMap(map[string]any{
		"f_name": eventType.Name, "f_subject": eventType.Subject, "f_severity": eventType.Severity,
		"f_merge_hours": eventType.MergeHours, "f_rule": string(eventType.Rule), "f_rule_version": eventType.RuleVersion,
		"f_updater": eventType.Updater, "f_updater_type": eventType.UpdaterType, "f_update_time": eventType.UpdateTime,
		"f_object_type_id": eventType.ObjectTypeID,
	}).
		Set("f_dataset_synced", sq.Expr("IF(f_dataset_id = ?, f_dataset_synced, 0)", eventType.DatasetID)).
		Set("f_dataset_id", eventType.DatasetID).
		Where(sq.Eq{"f_kn_id": eventType.KNID, "f_code": eventType.Code}).ToSql()
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, query, args...)
	return err
}

func (s *store) listEvents(ctx context.Context, knID string, filter interfaces.KnEventFilter, after *eventCursor) ([]*interfaces.KnEvent, error) {
	where := sq.And{sq.Eq{"e.f_kn_id": knID}}
	if len(filter.EventTypes) > 0 {
		where = append(where, sq.Eq{"e.f_event_type": filter.EventTypes})
	}
	if len(filter.Statuses) > 0 {
		where = append(where, sq.Eq{"e.f_status": filter.Statuses})
	}
	if filter.ObjectKey != "" {
		where = append(where, sq.Eq{"e.f_object_key": filter.ObjectKey})
	}
	if after != nil {
		where = append(where, sq.Or{
			sq.Lt{"e.f_recorded_at": after.recordedAt},
			sq.And{sq.Eq{"e.f_recorded_at": after.recordedAt}, sq.Lt{"e.f_id": after.id}},
		})
	}
	return queryEvents(ctx, s.db, sq.Select(eventSelectColumns()...).From(eventFrom).Where(where).
		OrderBy("e.f_recorded_at DESC", "e.f_id DESC").Limit(uint64(filter.Limit)+1))
}

func (s *store) getEvent(ctx context.Context, knID, eventID string) (*interfaces.KnEvent, bool, error) {
	events, err := queryEvents(ctx, s.db, sq.Select(eventSelectColumns()...).From(eventFrom).
		Where(sq.Eq{"e.f_kn_id": knID, "e.f_id": eventID}))
	if err != nil || len(events) == 0 {
		return nil, false, err
	}
	return events[0], true, nil
}

// history is an event's changes other than pure detection refreshes.
func (s *store) history(ctx context.Context, knID, eventID string) ([]interfaces.KnEventTransition, error) {
	query, args, err := sq.Select(
		"f_action", "f_from_status", "f_to_status", "f_actor", "f_actor_type", "f_actor_name", "f_note", "f_time",
	).From(changeTable).
		Where(sq.Eq{"f_event_id": eventID, "f_kn_id": knID}).
		Where(sq.NotEq{"f_action": interfaces.KnEventChecked}).
		OrderBy("f_seq ASC").ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	history := []interfaces.KnEventTransition{}
	for rows.Next() {
		var entry interfaces.KnEventTransition
		if err := rows.Scan(&entry.Action, &entry.FromStatus, &entry.ToStatus, &entry.Actor,
			&entry.ActorType, &entry.ActorName, &entry.Note, &entry.Time); err != nil {
			return nil, err
		}
		history = append(history, entry)
	}
	return history, rows.Err()
}

func (s *store) listChecks(ctx context.Context, knID string) ([]interfaces.KnEventRuleCheck, error) {
	query, args, err := sq.Select(checkColumns...).From(checkTable).
		Where(sq.Eq{"f_kn_id": knID}).OrderBy("f_event_type ASC").ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	checks := []interfaces.KnEventRuleCheck{}
	for rows.Next() {
		check, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		checks = append(checks, check)
	}
	return checks, rows.Err()
}

func scanCheck(row scanner) (interfaces.KnEventRuleCheck, error) {
	var check interfaces.KnEventRuleCheck
	err := row.Scan(&check.EventType, &check.Status, &check.RuleVersion, &check.Hits,
		&check.Message, &check.LastOK, &check.CheckedBy, &check.Time)
	return check, err
}

// listChanges reads the feed after a sequence. Each change carries its
// event's current state, so changes of one event in a page agree.
func (s *store) listChanges(ctx context.Context, knID string, after int64, limit int) ([]interfaces.KnEventChange, error) {
	columns := append([]string{"c.f_seq", "c.f_event_id", "c.f_action", "c.f_time"}, eventSelectColumns()...)
	query, args, err := sq.Select(columns...).
		From(changeTable + " c JOIN " + eventTable + " e ON e.f_id = c.f_event_id" +
			" LEFT JOIN " + typeTable + " t ON t.f_kn_id = e.f_kn_id AND t.f_code = e.f_event_type").
		Where(sq.Eq{"c.f_kn_id": knID}).Where(sq.Gt{"c.f_seq": after}).
		OrderBy("c.f_seq ASC").Limit(uint64(limit)).ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	changes := []interfaces.KnEventChange{}
	for rows.Next() {
		var entry interfaces.KnEventChange
		event, err := scanEvent(rows, &entry.Seq, &entry.EventID, &entry.Action, &entry.Time)
		if err != nil {
			return nil, err
		}
		entry.Event = *event
		changes = append(changes, entry)
	}
	return changes, rows.Err()
}

// DeleteKnEvents removes a network's events in the transaction that deletes
// the network. The head row goes first: it waits for a write in progress
// (events and event types alike), and a write that waited for it finds the
// network gone.
func (s *store) DeleteKnEvents(ctx context.Context, tx *sql.Tx, knID string) error {
	for _, table := range []string{headTable, changeTable, eventTable, checkTable, typeTable} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE f_kn_id = ?", knID); err != nil {
			return fmt.Errorf("delete %s of knowledge network %s: %w", table, knID, err)
		}
	}
	return nil
}

// writer is one write transaction of a network's events. It holds the
// network's head row, so writes of a network happen one at a time and their
// changes are numbered in commit order.
type writer struct {
	ctx  context.Context
	tx   *sql.Tx
	knID string
	seq  int64
}

// write runs fn in a transaction holding the network's head row. Reads see
// what earlier writes committed, so fn decides on current state.
func (s *store) write(ctx context.Context, knID string, fn func(*writer) error) error {
	for attempt := 0; ; attempt++ {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return err
		}
		var last int64
		err = tx.QueryRowContext(ctx, "SELECT f_last_seq FROM "+headTable+" WHERE f_kn_id = ? FOR UPDATE", knID).Scan(&last)
		if errors.Is(err, sql.ErrNoRows) && attempt == 0 {
			_ = tx.Rollback()
			// Create the head outside any transaction: concurrent first writes
			// then wait for each other on the row lock instead of deadlocking
			// on the duplicate-key check.
			if _, err := s.db.ExecContext(ctx, "INSERT IGNORE INTO "+headTable+" (f_kn_id, f_last_seq) VALUES (?, 0)", knID); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		w := &writer{ctx: ctx, tx: tx, knID: knID, seq: last}
		if err := fn(w); err != nil {
			_ = tx.Rollback()
			return err
		}
		if w.seq != last {
			if _, err := tx.ExecContext(ctx, "UPDATE "+headTable+" SET f_last_seq = ? WHERE f_kn_id = ?", w.seq, knID); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		return tx.Commit()
	}
}

func (w *writer) eventType(code string) (interfaces.KnEventType, bool, error) {
	return getType(w.ctx, w.tx, w.knID, code)
}

// events reads the network's events matching where, oldest first.
func (w *writer) events(where sq.Sqlizer) ([]*interfaces.KnEvent, error) {
	return queryEvents(w.ctx, w.tx, sq.Select(eventSelectColumns()...).From(eventFrom).
		Where(sq.Eq{"e.f_kn_id": w.knID}).Where(where).OrderBy("e.f_recorded_at ASC", "e.f_id ASC"))
}

func (w *writer) event(eventID string) (*interfaces.KnEvent, bool, error) {
	events, err := w.events(sq.Eq{"e.f_id": eventID})
	if err != nil || len(events) == 0 {
		return nil, false, err
	}
	return events[0], true, nil
}

func (w *writer) ruleCheck(eventType string) (*interfaces.KnEventRuleCheck, error) {
	query, args, err := sq.Select(checkColumns...).From(checkTable).
		Where(sq.Eq{"f_kn_id": w.knID, "f_event_type": eventType}).ToSql()
	if err != nil {
		return nil, err
	}
	check, err := scanCheck(w.tx.QueryRowContext(w.ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &check, nil
}

func (w *writer) saveCheck(check interfaces.KnEventRuleCheck) error {
	query, args, err := sq.Insert(checkTable).Columns(append([]string{"f_kn_id"}, checkColumns...)...).Values(
		w.knID, check.EventType, check.Status, check.RuleVersion, check.Hits,
		check.Message, check.LastOK, check.CheckedBy, check.Time,
	).Suffix("ON DUPLICATE KEY UPDATE " + updateAll(checkColumns[1:])).ToSql()
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, query, args...)
	return err
}

// record stores the events the changes left behind and appends the changes
// to the feed, numbered after the network's last change.
func (w *writer) record(changes []change) error {
	events := make([]*interfaces.KnEvent, 0, len(changes))
	stored := make(map[*interfaces.KnEvent]bool, len(changes))
	for _, entry := range changes {
		if !stored[entry.event] {
			stored[entry.event] = true
			events = append(events, entry.event)
		}
	}
	for start := 0; start < len(events); start += eventBatch {
		if err := w.saveEvents(events[start:min(start+eventBatch, len(events))]); err != nil {
			return err
		}
	}
	for start := 0; start < len(changes); start += changeBatch {
		if err := w.appendChanges(changes[start:min(start+changeBatch, len(changes))]); err != nil {
			return err
		}
	}
	return nil
}

func (w *writer) saveEvents(events []*interfaces.KnEvent) error {
	insert := sq.Insert(eventTable).Columns(eventColumns...)
	for _, event := range events {
		event.KNID = w.knID
		if len(event.Detail) == 0 {
			event.Detail = rawJSON("")
		}
		attributes, err := json.Marshal(event.ObjectAttributes)
		if err != nil {
			return fmt.Errorf("encode attributes of event %s: %w", event.ID, err)
		}
		insert = insert.Values(
			event.ID, event.KNID, event.EventType, event.Title, event.Status, event.Severity, event.Source,
			event.RuleVersion, event.ObjectType, event.ObjectKey, event.ObjectName, string(attributes),
			event.OccurredAt, event.RecordedAt, event.EndedAt, event.FirstDetectedAt, event.LastCheckedAt,
			event.CheckStatus, string(event.Detail), event.Backlog, event.MergedInto,
			event.ReportedBy, event.ReportedByName, event.VerifiedBy, event.VerifiedByType, event.VerifiedByName,
			event.VerifiedAt, event.Revision, event.CreateTime, event.UpdateTime,
		)
		event.Actions = actionsFor(event)
	}
	// Events are never moved between networks or types; the rest follows the write.
	query, args, err := insert.Suffix("ON DUPLICATE KEY UPDATE " + updateAll(eventColumns[3:])).ToSql()
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, query, args...)
	return err
}

func (w *writer) appendChanges(changes []change) error {
	insert := sq.Insert(changeTable).Columns(changeColumns...)
	for _, entry := range changes {
		w.seq++
		t := entry.transition
		insert = insert.Values(
			w.knID, w.seq, entry.event.ID, t.Action, t.FromStatus, t.ToStatus,
			t.Actor, t.ActorType, t.ActorName, t.Note, t.Time,
		)
	}
	query, args, err := insert.ToSql()
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, query, args...)
	return err
}

// deleteType removes an event type that no event refers to.
// hasEvents reports whether events of a type exist.
func (w *writer) hasEvents(code string) (bool, error) {
	var one int
	err := w.tx.QueryRowContext(w.ctx, "SELECT 1 FROM "+eventTable+" WHERE f_kn_id = ? AND f_event_type = ? LIMIT 1",
		w.knID, code).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (w *writer) deleteType(code string) (found, inUse bool, err error) {
	if _, found, err = w.eventType(code); err != nil || !found {
		return found, false, err
	}
	if inUse, err = w.hasEvents(code); err != nil || inUse {
		return true, inUse, err
	}
	for _, table := range []struct{ name, column string }{{typeTable, "f_code"}, {checkTable, "f_event_type"}} {
		if _, err := w.tx.ExecContext(w.ctx, "DELETE FROM "+table.name+" WHERE f_kn_id = ? AND "+table.column+" = ?",
			w.knID, code); err != nil {
			return true, false, err
		}
	}
	return true, false, nil
}

func updateAll(columns []string) string {
	clause := ""
	for index, column := range columns {
		if index > 0 {
			clause += ", "
		}
		clause += column + " = VALUES(" + column + ")"
	}
	return clause
}

// The copy of the ledger in the event classes' datasets is kept by one
// replica at a time, which follows each network's feed from where the head
// row says the datasets have got to.

// setClass stores the class an event type was given outside an update of the
// type itself. Its events are not in the dataset yet.
func (w *writer) setClass(code string, class eventClass) error {
	_, err := w.tx.ExecContext(w.ctx, "UPDATE "+typeTable+
		" SET f_object_type_id = ?, f_dataset_synced = IF(f_dataset_id = ?, f_dataset_synced, 0), f_dataset_id = ?"+
		" WHERE f_kn_id = ? AND f_code = ?",
		class.objectTypeID, class.datasetID, class.datasetID, w.knID, code)
	return err
}

func (s *store) queryTypes(ctx context.Context, where sq.Sqlizer, limit uint64) ([]interfaces.KnEventType, error) {
	query, args, err := sq.Select(typeColumns...).From(typeTable).Where(where).
		OrderBy("f_kn_id ASC", "f_code ASC").Limit(limit).ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	types := []interfaces.KnEventType{}
	for rows.Next() {
		eventType, err := scanType(rows)
		if err != nil {
			return nil, err
		}
		types = append(types, eventType)
	}
	return types, rows.Err()
}

// classlessTypes lists event types that have no class yet: those created
// before events were object types.
func (s *store) classlessTypes(ctx context.Context, limit int) ([]interfaces.KnEventType, error) {
	return s.queryTypes(ctx, sq.Eq{"f_object_type_id": ""}, uint64(limit))
}

// unsyncedTypes lists event types whose dataset does not hold the events the
// ledger already had when the type was given it.
func (s *store) unsyncedTypes(ctx context.Context, limit int) ([]interfaces.KnEventType, error) {
	return s.queryTypes(ctx, sq.And{sq.NotEq{"f_dataset_id": ""}, sq.Eq{"f_dataset_synced": 0}}, uint64(limit))
}

// classesOf maps the event types of a network to their classes, "" for a type
// that has none.
func (s *store) classesOf(ctx context.Context, knID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT f_code, f_object_type_id FROM "+typeTable+" WHERE f_kn_id = ?", knID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	classes := map[string]string{}
	for rows.Next() {
		var code, class string
		if err := rows.Scan(&code, &class); err != nil {
			return nil, err
		}
		classes[code] = class
	}
	return classes, rows.Err()
}

// forgetClass leaves an event type without the class it had, which may be
// gone: the type is then given its class again, as one from before events were
// object types is. Its events are not written to the dataset meanwhile, and
// are all written once it has the dataset again.
func (s *store) forgetClass(ctx context.Context, knID, code, objectTypeID string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE "+typeTable+" SET f_object_type_id = '', f_dataset_id = '', f_dataset_synced = 0"+
		" WHERE f_kn_id = ? AND f_code = ? AND f_object_type_id = ?", knID, code, objectTypeID)
	return err
}

// datasetsOf maps the event types of a network to their datasets.
func (s *store) datasetsOf(ctx context.Context, knID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT f_code, f_dataset_id FROM "+typeTable+" WHERE f_kn_id = ? AND f_dataset_id <> ''", knID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	datasets := map[string]string{}
	for rows.Next() {
		var code, dataset string
		if err := rows.Scan(&code, &dataset); err != nil {
			return nil, err
		}
		datasets[code] = dataset
	}
	return datasets, rows.Err()
}

// setSynced records whether a type's dataset holds the ledger's events of the
// type. It leaves a type alone that has been moved to another dataset since.
func (s *store) setSynced(ctx context.Context, knID, code, datasetID string, synced bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE "+typeTable+" SET f_dataset_synced = ? WHERE f_kn_id = ? AND f_code = ? AND f_dataset_id = ?",
		synced, knID, code, datasetID)
	return err
}

// eventsOfType reads a page of a type's events from the ledger, in the order
// of their IDs, after one of them.
func (s *store) eventsOfType(ctx context.Context, knID, code, afterID string, limit int) ([]*interfaces.KnEvent, error) {
	return queryEvents(ctx, s.db, sq.Select(eventSelectColumns()...).From(eventFrom).
		Where(sq.Eq{"e.f_kn_id": knID, "e.f_event_type": code}).Where(sq.Gt{"e.f_id": afterID}).
		OrderBy("e.f_id ASC").Limit(uint64(limit)))
}

// feedPosition is how far a network's feed has got, and how far its copy in
// the datasets.
type feedPosition struct {
	knID      string
	last      int64
	projected int64
}

// behind lists the networks whose feed has changes their datasets lack.
func (s *store) behind(ctx context.Context, limit int) ([]feedPosition, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT f_kn_id, f_last_seq, f_projected_seq FROM "+headTable+
		" WHERE f_projected_seq < f_last_seq ORDER BY f_kn_id LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	positions := []feedPosition{}
	for rows.Next() {
		var position feedPosition
		if err := rows.Scan(&position.knID, &position.last, &position.projected); err != nil {
			return nil, err
		}
		positions = append(positions, position)
	}
	return positions, rows.Err()
}

// setProjected moves on how far a network's datasets have got; never back.
func (s *store) setProjected(ctx context.Context, knID string, seq int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE "+headTable+" SET f_projected_seq = ? WHERE f_kn_id = ? AND f_projected_seq < ?",
		seq, knID, seq)
	return err
}

// alone runs fn unless another replica is running it under the same name,
// and reports whether it ran.
func (s *store) alone(ctx context.Context, name string, fn func() error) (ran bool, err error) {
	return s.locked(ctx, name, 0, fn)
}

// classLock names the lock of a network's event classes. Whatever makes,
// changes or removes a class holds it, from reading what the network has to
// storing the type: a class is written outside the head lock, and two codes
// can stand for one class.
func classLock(knID string) string { return "bkn_kn_event_class:" + knID }

// exclusive runs fn holding the named lock, waiting for whoever holds it.
func (s *store) exclusive(ctx context.Context, name string, fn func() error) error {
	ran, err := s.locked(ctx, name, s.classWait, fn)
	if err == nil && !ran {
		return errClassesBusy
	}
	return err
}

// locked runs fn holding a named lock, waiting up to wait seconds for it, and
// reports whether it got the lock. The lock belongs to a connection of its
// own; fn's own statements use others.
func (s *store) locked(ctx context.Context, name string, wait int, fn func() error) (ran bool, err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK("+lockName+", ?)", name, wait).Scan(&acquired); err != nil {
		return false, err
	}
	if acquired.Int64 != 1 {
		return false, nil
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockRelease)
		defer cancel()
		if _, err := conn.ExecContext(releaseCtx, "SELECT RELEASE_LOCK("+lockName+")", name); err != nil {
			// A lock that was not given back goes with its connection, which
			// then must not return to the pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return true, fn()
}
