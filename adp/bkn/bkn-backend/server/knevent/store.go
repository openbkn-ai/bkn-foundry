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
	"slices"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	eventTable  = "t_kn_event"
	changeTable = "t_kn_event_change"
	checkTable  = "t_kn_event_rule_check"
	headTable   = "t_kn_event_head"
	// handlingTable keeps how the platform handles the events of classes that run their
	// handling action by themselves.
	handlingTable = "t_kn_event_handling"

	objectTypeTable   = "t_object_type"
	versionStateTable = "t_kn_version_state"

	// runningBranch is the branch a network runs on, read from its version state vs: the running
	// version of a versioned network, and main for a network without versions or with none
	// published yet. Events are written and read against the event classes of that branch.
	runningBranch = "IF(COALESCE(vs.f_running_version, 0) > 0, CONCAT('" + interfaces.VERSION_BRANCH_PREFIX +
		"', vs.f_running_version), '" + interfaces.MAIN_BRANCH + "')"

	// Rows per multi-row statement, well under MariaDB's 65535 placeholders.
	eventBatch  = 200
	changeBatch = 500

	// lockRelease bounds giving a named lock back once its work is done.
	lockRelease = 2 * time.Second
	// lockName is how a named lock is called in MariaDB. Its names are
	// server-wide and short: hashed with the database's, two databases on one
	// server do not wait on each other, and a long name fits.
	lockName = "SHA2(CONCAT(DATABASE(), ':', ?), 224)"
)

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
	"f_actor", "f_actor_type", "f_actor_name", "f_note", "f_time", "f_execution_id",
}

var checkColumns = []string{
	"f_event_type", "f_status", "f_rule_version", "f_hits", "f_message", "f_last_ok", "f_last_ok_rule_version",
	"f_checked_by", "f_time",
}

// classJoin joins the event e to its class t on the branch the network runs on, for the class's
// current name, which is never stored on the event.
const classJoin = " LEFT JOIN " + versionStateTable + " vs ON vs.f_kn_id = e.f_kn_id" +
	" LEFT JOIN " + objectTypeTable + " t ON t.f_kn_id = e.f_kn_id AND t.f_branch = " + runningBranch +
	" AND t.f_id = e.f_event_type"

const eventFrom = eventTable + " e" + classJoin

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
}

func newStore(db *sql.DB) *store {
	return &store{db: db}
}

func rawJSON(value string) json.RawMessage {
	if value == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(value)
}

// eventClass is what the events of a class take from it: the class as the network runs it.
type eventClass struct {
	id         string
	name       string
	source     string
	subject    string
	severity   string
	mergeHours float64
	// handling is the class's handling action, nil when it has none.
	handling *interfaces.ObjectTypeEventHandling
}

// readClass reads the event class id of a network on the branch it runs on, reporting false
// when the network runs no such class: no object type of the ID, or one that is not an event
// class.
func readClass(ctx context.Context, q queryer, knID, id string) (eventClass, bool, error) {
	var name, kinds string
	var config sql.NullString
	err := q.QueryRowContext(ctx, "SELECT t.f_name, t.f_kinds, t.f_event FROM "+objectTypeTable+" t"+
		" LEFT JOIN "+versionStateTable+" vs ON vs.f_kn_id = t.f_kn_id"+
		" WHERE t.f_kn_id = ? AND t.f_id = ? AND t.f_branch = "+runningBranch, knID, id).
		Scan(&name, &kinds, &config)
	if errors.Is(err, sql.ErrNoRows) {
		return eventClass{}, false, nil
	}
	if err != nil {
		return eventClass{}, false, err
	}
	var listed []string
	if kinds != "" {
		if err := json.Unmarshal([]byte(kinds), &listed); err != nil {
			return eventClass{}, false, fmt.Errorf("decode the kinds of object type %s: %w", id, err)
		}
	}
	if !slices.Contains(listed, interfaces.ObjectTypeKindEvent) || !config.Valid || config.String == "" {
		return eventClass{}, false, nil
	}
	var event interfaces.ObjectTypeEvent
	if err := json.Unmarshal([]byte(config.String), &event); err != nil {
		return eventClass{}, false, fmt.Errorf("decode the event configuration of object type %s: %w", id, err)
	}
	return eventClass{
		id: id, name: name, source: event.Source, subject: event.Subject,
		severity: event.Severity, mergeHours: event.MergeHours, handling: event.HandlingAction,
	}, true, nil
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

// occurredWithin bounds the time events occurred, from inclusive to exclusive; 0 leaves that end
// open.
func occurredWithin(where sq.And, column string, from, to int64) sq.And {
	if from > 0 {
		where = append(where, sq.GtOrEq{column: from})
	}
	if to > 0 {
		where = append(where, sq.Lt{column: to})
	}
	return where
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
	where = occurredWithin(where, "e.f_occurred_at", filter.From, filter.To)
	if after != nil {
		where = append(where, sq.Or{
			sq.Lt{"e.f_recorded_at": after.recordedAt},
			sq.And{sq.Eq{"e.f_recorded_at": after.recordedAt}, sq.Lt{"e.f_id": after.id}},
		})
	}
	return queryEvents(ctx, s.db, sq.Select(eventSelectColumns()...).From(eventFrom).Where(where).
		OrderBy("e.f_recorded_at DESC", "e.f_id DESC").Limit(uint64(filter.Limit)+1))
}

// countEvents counts the events of each class that occurred in [from, to). A rejected or
// duplicate report is not an occurrence, nor is what a rule found already there when it first
// decided: that happened before, at a time nobody knows.
func (s *store) countEvents(ctx context.Context, knID string, filter interfaces.KnEventStatsFilter) ([]interfaces.KnEventStatEntry, error) {
	where := sq.And{
		sq.Eq{"f_kn_id": knID, "f_backlog": false},
		sq.NotEq{"f_status": []string{interfaces.KnEventRejected, interfaces.KnEventDuplicate}},
	}
	if len(filter.EventTypes) > 0 {
		where = append(where, sq.Eq{"f_event_type": filter.EventTypes})
	}
	if filter.ObjectKey != "" {
		where = append(where, sq.Eq{"f_object_key": filter.ObjectKey})
	}
	where = occurredWithin(where, "f_occurred_at", filter.From, filter.To)
	query, args, err := sq.Select("f_event_type", "COUNT(*)").
		Column(sq.Expr("COALESCE(SUM(f_status IN (?, ?)), 0)", interfaces.KnEventUnverified, interfaces.KnEventVerified)).
		Column("MAX(f_occurred_at)").
		From(eventTable).Where(where).GroupBy("f_event_type").OrderBy("f_event_type ASC").ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	entries := []interfaces.KnEventStatEntry{}
	for rows.Next() {
		var entry interfaces.KnEventStatEntry
		if err := rows.Scan(&entry.EventType, &entry.Count, &entry.OpenCount, &entry.LastOccurredAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
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
		"f_execution_id",
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
			&entry.ActorType, &entry.ActorName, &entry.Note, &entry.Time, &entry.ExecutionID); err != nil {
			return nil, err
		}
		history = append(history, entry)
	}
	return history, rows.Err()
}

// listChecks lists the last checks of the network's rule classes, leaving out a class never
// checked and one the network no longer runs.
func (s *store) listChecks(ctx context.Context, knID string) ([]interfaces.KnEventRuleCheck, error) {
	columns := make([]string, 0, len(checkColumns))
	for _, column := range checkColumns {
		columns = append(columns, "c."+column)
	}
	query, args, err := sq.Select(columns...).
		From(checkTable + " c LEFT JOIN " + versionStateTable + " vs ON vs.f_kn_id = c.f_kn_id" +
			" JOIN " + objectTypeTable + " t ON t.f_kn_id = c.f_kn_id AND t.f_branch = " + runningBranch +
			" AND t.f_id = c.f_event_type").
		Where(sq.Eq{"c.f_kn_id": knID}).Where(sq.Gt{"c.f_time": 0}).OrderBy("c.f_event_type ASC").ToSql()
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
		&check.Message, &check.LastOK, &check.LastOKRuleVersion, &check.CheckedBy, &check.Time)
	return check, err
}

// listChanges reads the feed after a sequence. Each change carries its
// event's current state, so changes of one event in a page agree.
func (s *store) listChanges(ctx context.Context, knID string, after int64, limit int) ([]interfaces.KnEventChange, error) {
	columns := append([]string{"c.f_seq", "c.f_event_id", "c.f_action", "c.f_time"}, eventSelectColumns()...)
	query, args, err := sq.Select(columns...).
		From(changeTable + " c JOIN " + eventTable + " e ON e.f_id = c.f_event_id" + classJoin).
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
// the network. The head row goes first: it waits for a write in progress, and
// a write that waited for it finds the network gone.
func (s *store) DeleteKnEvents(ctx context.Context, tx *sql.Tx, knID string) error {
	for _, table := range []string{headTable, changeTable, eventTable, checkTable, handlingTable} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE f_kn_id = ?", knID); err != nil {
			return fmt.Errorf("delete %s of knowledge network %s: %w", table, knID, err)
		}
	}
	return nil
}

// What an event class keeps beside its definition, written in the transaction that saves or
// deletes the class.

// classHasEvents reports whether the ledger has events of a class.
func (s *store) classHasEvents(ctx context.Context, tx *sql.Tx, knID, classID string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM "+eventTable+" WHERE f_kn_id = ? AND f_event_type = ? LIMIT 1",
		knID, classID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// holdHead takes a network's head row in tx, as an event write does, so that no event is written
// until tx ends.
func (s *store) holdHead(ctx context.Context, tx *sql.Tx, knID string) error {
	// Made outside the transaction, before anything in it reads the row: a locking read of a row
	// that is not there would keep its place from the insert.
	if _, err := s.db.ExecContext(ctx, "INSERT IGNORE INTO "+headTable+" (f_kn_id, f_last_seq) VALUES (?, 0)", knID); err != nil {
		return err
	}
	var last int64
	return tx.QueryRowContext(ctx, "SELECT f_last_seq FROM "+headTable+" WHERE f_kn_id = ? FOR UPDATE", knID).Scan(&last)
}

// keepCheckRow makes sure a class has its row of rule checks. reset starts the row over, for a
// class only now made. synced is what a new row says of the dataset: whether it holds the
// ledger's events of the class. unsync marks an existing row's dataset as lacking them, for a
// dataset only now made again.
func (s *store) keepCheckRow(ctx context.Context, tx *sql.Tx, knID, classID string, reset, synced, unsync bool) error {
	if reset {
		if err := s.deleteCheckRow(ctx, tx, knID, classID); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO "+checkTable+" (f_kn_id, f_event_type, f_dataset_synced) VALUES (?, ?, ?)"+
		" ON DUPLICATE KEY UPDATE f_dataset_synced = IF(?, 0, f_dataset_synced)", knID, classID, synced, unsync)
	return err
}

func (s *store) deleteCheckRow(ctx context.Context, tx *sql.Tx, knID, classID string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM "+checkTable+" WHERE f_kn_id = ? AND f_event_type = ?", knID, classID)
	return err
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

// eventClass reads a class of the network as it runs it. Under the head row, a class with events
// cannot be deleted meanwhile.
func (w *writer) eventClass(id string) (eventClass, bool, error) {
	return readClass(w.ctx, w.tx, w.knID, id)
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
	// A row the class was given before it was ever checked records no check.
	if check.Time == 0 {
		return nil, nil
	}
	return &check, nil
}

// saveCheck stores the last check of a class. Whether its dataset holds the ledger's events is
// left as it is; a row made here says it does not, and the dataset is written again.
func (w *writer) saveCheck(check interfaces.KnEventRuleCheck) error {
	query, args, err := sq.Insert(checkTable).Columns(append([]string{"f_kn_id"}, checkColumns...)...).Values(
		w.knID, check.EventType, check.Status, check.RuleVersion, check.Hits,
		check.Message, check.LastOK, check.LastOKRuleVersion, check.CheckedBy, check.Time,
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
	// Events are never moved between networks or classes; the rest follows the write.
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
			t.Actor, t.ActorType, t.ActorName, t.Note, t.Time, t.ExecutionID,
		)
	}
	query, args, err := insert.ToSql()
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, query, args...)
	return err
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

// classRef names an event class of a network.
type classRef struct {
	knID string
	id   string
}

// unsyncedClasses lists the classes whose dataset does not hold the events the ledger already
// had when the class was given it.
func (s *store) unsyncedClasses(ctx context.Context, limit int) ([]classRef, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT f_kn_id, f_event_type FROM "+checkTable+
		" WHERE f_dataset_synced = 0 ORDER BY f_kn_id, f_event_type LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	classes := []classRef{}
	for rows.Next() {
		var class classRef
		if err := rows.Scan(&class.knID, &class.id); err != nil {
			return nil, err
		}
		classes = append(classes, class)
	}
	return classes, rows.Err()
}

// classesOf lists the classes of a network that have a dataset: every class ever saved, until it
// is deleted.
func (s *store) classesOf(ctx context.Context, knID string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT f_event_type FROM "+checkTable+" WHERE f_kn_id = ?", knID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	classes := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		classes[id] = true
	}
	return classes, rows.Err()
}

// setSynced records whether a class's dataset holds the ledger's events of the class.
func (s *store) setSynced(ctx context.Context, knID, classID string, synced bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE "+checkTable+" SET f_dataset_synced = ? WHERE f_kn_id = ? AND f_event_type = ?",
		synced, knID, classID)
	return err
}

// eventsOfClass reads a page of a class's events from the ledger, in the order of their IDs,
// after one of them.
func (s *store) eventsOfClass(ctx context.Context, knID, classID, afterID string, limit int) ([]*interfaces.KnEvent, error) {
	return queryEvents(ctx, s.db, sq.Select(eventSelectColumns()...).From(eventFrom).
		Where(sq.Eq{"e.f_kn_id": knID, "e.f_event_type": classID}).Where(sq.Gt{"e.f_id": afterID}).
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

// handlingAction is what decides whether an action type can handle the events of a class.
type handlingAction struct {
	objectTypeID  string
	recordsEvents bool
	inputs        []string
}

// handlingActionType reads an action type of a branch in tx, reporting false when the branch
// has none of the ID.
func (s *store) handlingActionType(ctx context.Context, tx *sql.Tx, knID, branch, id string) (handlingAction, bool, error) {
	var objectTypeID string
	var operation, parameters sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT f_object_type_id, f_operation_config, f_parameters FROM t_action_type"+
		" WHERE f_kn_id = ? AND f_branch = ? AND f_id = ?", knID, branch, id).Scan(&objectTypeID, &operation, &parameters)
	if errors.Is(err, sql.ErrNoRows) {
		return handlingAction{}, false, nil
	}
	if err != nil {
		return handlingAction{}, false, err
	}
	action := handlingAction{objectTypeID: objectTypeID}
	if operation.Valid && operation.String != "" {
		var config struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(operation.String), &config); err != nil {
			return handlingAction{}, false, fmt.Errorf("decode the operation of action type %s: %w", id, err)
		}
		action.recordsEvents = config.Type == interfaces.ActionOperationEvent
	}
	if parameters.Valid && parameters.String != "" {
		var list []struct {
			Name      string `json:"name"`
			ValueFrom string `json:"value_from"`
		}
		if err := json.Unmarshal([]byte(parameters.String), &list); err != nil {
			return handlingAction{}, false, fmt.Errorf("decode the parameters of action type %s: %w", id, err)
		}
		for _, parameter := range list {
			if parameter.ValueFrom == "input" {
				action.inputs = append(action.inputs, parameter.Name)
			}
		}
	}
	return action, true, nil
}

// recognitionActions lists the record-event action types of a class in a branch, in tx when it is
// given, so a save sees what the transaction has deleted.
func (s *store) recognitionActions(ctx context.Context, tx *sql.Tx, knID, branch, classID string) ([]string, error) {
	var q queryer = s.db
	if tx != nil {
		q = tx
	}
	rows, err := q.QueryContext(ctx, "SELECT f_id FROM t_action_type WHERE f_kn_id = ? AND f_branch = ?"+
		" AND JSON_VALUE(f_operation_config, '$.type') = ? AND JSON_VALUE(f_operation_config, '$.event_class') = ?"+
		" ORDER BY f_id", knID, branch, interfaces.ActionOperationEvent, classID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
