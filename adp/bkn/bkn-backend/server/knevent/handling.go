// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// HandlingWorker names the worker that runs the handling actions of events.
const HandlingWorker = "kn-event-handling"

const (
	// handlePeriod is how often the worker looks for events to handle.
	handlePeriod = 5 * time.Second
	// handleRun bounds one round of the worker.
	handleRun = time.Minute
	// handleLock lets one replica handle events at a time.
	handleLock = "bkn_kn_event_handling"
	// handlePage is how many events a round handles at most.
	handlePage = 100
	// handleAttemptsMax is how many times ontology-query is asked before an event's handling
	// is given up; the waits between grow from handleRetryFirst up to handleRetryLast.
	handleAttemptsMax = 8
	handleRetryFirst  = time.Minute
	handleRetryLast   = time.Hour
	// handleMessageMax bounds the reason kept with a handling.
	handleMessageMax = 1000
)

// handlingRow is the platform's handling of one event.
type handlingRow struct {
	eventID      string
	knID         string
	eventType    string
	actionTypeID string
	target       json.RawMessage
	// inputs are the class's values for inputs of the action type when the event was found, as
	// ontology-query takes them: {name: {value_from, value}}.
	inputs      json.RawMessage
	subject     interfaces.AccountInfo
	status      string
	attempts    int
	nextAt      int64
	executionID string
	message     string
	time        int64
}

// handlingsFor are the handlings a check starts: one for each event it newly found, when the
// class runs its handling action by itself. An event of a backlog, found by the first check
// after the rule was set up or changed, is not handled by itself: it may have been wrong for
// long, and a person decides. The event's object is the identity the check gave for it.
func handlingsFor(class eventClass, changes []change, hits []interfaces.KnEventRuleHit, o op) []handlingRow {
	if class.handling == nil || class.handling.Mode != interfaces.KnEventHandlingAuto {
		return nil
	}
	identities := make(map[string]json.RawMessage, len(hits))
	for _, hit := range hits {
		if _, seen := identities[hit.Key]; !seen {
			identities[hit.Key] = hit.Identity
		}
	}
	inputs := json.RawMessage("{}")
	if len(class.handling.Parameters) > 0 {
		if encoded, err := json.Marshal(class.handling.Parameters); err == nil {
			inputs = encoded
		}
	}
	var rows []handlingRow
	for _, entry := range changes {
		event := entry.event
		if entry.transition.Action != actionDetected || event.Backlog {
			continue
		}
		row := handlingRow{
			eventID: event.ID, eventType: class.id, actionTypeID: class.handling.ActionTypeID,
			target: identities[event.ObjectKey], inputs: inputs, subject: o.actor,
			status: interfaces.KnEventHandlingQueued, nextAt: o.now, time: o.now,
		}
		if len(row.target) == 0 {
			row.target = json.RawMessage("{}")
			row.status = interfaces.KnEventHandlingFailed
			row.message = "the check gave no identity of the object"
		}
		rows = append(rows, row)
	}
	return rows
}

// queueHandlings keeps the handlings of a check in its transaction.
func (w *writer) queueHandlings(rows []handlingRow) error {
	for start := 0; start < len(rows); start += eventBatch {
		insert := sq.Insert(handlingTable).Columns("f_event_id", "f_kn_id", "f_event_type", "f_action_type_id",
			"f_target", "f_parameters", "f_subject_id", "f_subject_type", "f_status", "f_attempts", "f_next_at", "f_message",
			"f_create_time", "f_update_time")
		for _, row := range rows[start:min(start+eventBatch, len(rows))] {
			insert = insert.Values(row.eventID, w.knID, row.eventType, row.actionTypeID, string(row.target),
				string(row.inputs), row.subject.ID, row.subject.Type, row.status, 0, row.nextAt, row.message, row.time, row.time)
		}
		query, args, err := insert.Options("IGNORE").ToSql()
		if err != nil {
			return err
		}
		if _, err := w.tx.ExecContext(w.ctx, query, args...); err != nil {
			return fmt.Errorf("queue the handling of events: %w", err)
		}
	}
	return nil
}

// dueHandlings lists the handlings to try now, the longest waiting first.
func (s *store) dueHandlings(ctx context.Context, now int64, limit int) ([]handlingRow, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT f_event_id, f_kn_id, f_event_type, f_action_type_id, f_target,"+
		" f_parameters, f_subject_id, f_subject_type, f_attempts FROM "+handlingTable+
		" WHERE f_status = ? AND f_next_at <= ? ORDER BY f_next_at, f_event_id LIMIT ?",
		interfaces.KnEventHandlingQueued, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	due := []handlingRow{}
	for rows.Next() {
		var row handlingRow
		var target, inputs string
		if err := rows.Scan(&row.eventID, &row.knID, &row.eventType, &row.actionTypeID, &target, &inputs,
			&row.subject.ID, &row.subject.Type, &row.attempts); err != nil {
			return nil, err
		}
		row.target = json.RawMessage(target)
		row.inputs = json.RawMessage(inputs)
		due = append(due, row)
	}
	return due, rows.Err()
}

// settleHandling writes how a try of a queued handling went.
func (s *store) settleHandling(ctx context.Context, row handlingRow) error {
	_, err := s.db.ExecContext(ctx, "UPDATE "+handlingTable+" SET f_status = ?, f_attempts = ?, f_next_at = ?,"+
		" f_execution_id = ?, f_message = ?, f_update_time = ? WHERE f_event_id = ? AND f_status = ?",
		row.status, row.attempts, row.nextAt, row.executionID, row.message, row.time,
		row.eventID, interfaces.KnEventHandlingQueued)
	return err
}

// handlingsOf reads how the given events of a network were handled, by event.
func (s *store) handlingsOf(ctx context.Context, knID string, eventIDs []string) (map[string]*interfaces.KnEventHandling, error) {
	found := map[string]*interfaces.KnEventHandling{}
	if len(eventIDs) == 0 {
		return found, nil
	}
	query, args, err := sq.Select("f_event_id", "f_action_type_id", "f_status", "f_execution_id", "f_message",
		"f_attempts", "f_update_time").From(handlingTable).
		Where(sq.Eq{"f_kn_id": knID, "f_event_id": eventIDs}).ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var eventID string
		var handling interfaces.KnEventHandling
		if err := rows.Scan(&eventID, &handling.ActionTypeID, &handling.Status, &handling.ExecutionID,
			&handling.Message, &handling.Attempts, &handling.UpdateTime); err != nil {
			return nil, err
		}
		found[eventID] = &handling
	}
	return found, rows.Err()
}

// handler runs the handling actions of events: each queued handling executes its action type on
// the event's object, as the execution subject of the event's class.
type handler struct {
	store   handlingStore
	actions interfaces.EventHandlingAccess
	now     func() time.Time
}

// handlingStore is what the handler needs of the store.
type handlingStore interface {
	alone(ctx context.Context, name string, fn func() error) (bool, error)
	dueHandlings(ctx context.Context, now int64, limit int) ([]handlingRow, error)
	settleHandling(ctx context.Context, row handlingRow) error
}

func newHandler(store *store, actions interfaces.EventHandlingAccess) *handler {
	return &handler{store: store, actions: actions, now: time.Now}
}

// run works every period until ctx ends.
func (h *handler) run(ctx context.Context) error {
	ticker := time.NewTicker(handlePeriod)
	defer ticker.Stop()
	for {
		h.round(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// round handles what is due, unless another replica is at it.
func (h *handler) round(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, handleRun)
	defer cancel()
	_, err := h.store.alone(ctx, handleLock, func() error {
		due, err := h.store.dueHandlings(ctx, h.now().UnixMilli(), handlePage)
		if err != nil {
			return fmt.Errorf("list the events to handle: %w", err)
		}
		for _, row := range due {
			if ctx.Err() != nil {
				return nil
			}
			if err := h.store.settleHandling(ctx, h.try(ctx, row)); err != nil {
				logger.Errorf("knowledge-network events: keep the handling of event %s: %v", row.eventID, err)
			}
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		logger.Errorf("knowledge-network events: handle events: %v", err)
	}
}

// try executes the handling action of one event and says how it went.
func (h *handler) try(ctx context.Context, row handlingRow) handlingRow {
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, row.subject)
	outcome := h.actions.ExecuteForEvent(ctx, row.knID, row.actionTypeID, row.target, row.inputs)
	now := h.now()
	row.time = now.UnixMilli()
	row.attempts++
	switch {
	case outcome.ExecutionID != "":
		row.status, row.executionID, row.message = interfaces.KnEventHandlingStarted, outcome.ExecutionID, ""
	case outcome.Running:
		row.status, row.message = interfaces.KnEventHandlingStarted,
			"an execution of the action on the object was already running"
	case outcome.NeedsPerson != "":
		row.status, row.message = interfaces.KnEventHandlingNeedsPerson, clip(outcome.NeedsPerson, handleMessageMax)
	case outcome.Refused != "":
		row.status, row.message = interfaces.KnEventHandlingFailed, clip(outcome.Refused, handleMessageMax)
	default:
		reason := "ontology-query could not be asked"
		if outcome.Unavailable != nil {
			reason = outcome.Unavailable.Error()
		}
		row.message = clip(reason, handleMessageMax)
		if row.attempts >= handleAttemptsMax {
			row.status = interfaces.KnEventHandlingFailed
			break
		}
		row.status = interfaces.KnEventHandlingQueued
		row.nextAt = now.Add(retryWait(row.attempts)).UnixMilli()
	}
	return row
}

// retryWait is the wait after the given number of failed tries: doubling from handleRetryFirst,
// up to handleRetryLast.
func retryWait(attempts int) time.Duration {
	wait := handleRetryFirst
	for i := 1; i < attempts && wait < handleRetryLast; i++ {
		wait *= 2
	}
	return min(wait, handleRetryLast)
}
