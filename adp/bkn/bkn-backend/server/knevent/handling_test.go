// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

var checker = interfaces.AccountInfo{ID: "u1", Type: "user"}

func handledClass(mode string) eventClass {
	return eventClass{id: "late_order", name: "订单超期", source: "rule", subject: "order",
		handling: &interfaces.ObjectTypeEventHandling{ActionTypeID: "notify_buyer", Mode: mode}}
}

func detected(id, key string, backlog bool) change {
	return change{event: &interfaces.KnEvent{ID: id, ObjectKey: key, Backlog: backlog},
		transition: interfaces.KnEventTransition{Action: actionDetected}}
}

// A check hands the events it newly finds to the class's handling action, run by itself, on the
// objects the check named; not the events of a backlog, nor the ones it already had.
func TestHandlingsForTheEventsACheckNewlyFinds(t *testing.T) {
	changes := []change{
		detected("e1", "PO-1", false),
		detected("e2", "PO-2", true),
		{event: &interfaces.KnEvent{ID: "e3", ObjectKey: "PO-3"}, transition: interfaces.KnEventTransition{Action: interfaces.KnEventChecked}},
		detected("e4", "PO-4", false),
	}
	hits := []interfaces.KnEventRuleHit{
		{Key: "PO-1", Identity: json.RawMessage(`{"id":"PO-1"}`)},
		{Key: "PO-2", Identity: json.RawMessage(`{"id":"PO-2"}`)},
		{Key: "PO-4"},
	}
	o := op{now: 1000, actor: checker}

	rows := handlingsFor(handledClass(interfaces.KnEventHandlingAuto), changes, hits, o)

	if len(rows) != 2 {
		t.Fatalf("handlings %+v, want e1 and e4", rows)
	}
	first := rows[0]
	if first.eventID != "e1" || string(first.target) != `{"id":"PO-1"}` || first.actionTypeID != "notify_buyer" ||
		first.subject != checker || first.status != interfaces.KnEventHandlingQueued || first.nextAt != 1000 {
		t.Fatalf("first handling %+v", first)
	}
	// Without the object's identity there is nothing to execute the action on.
	if rows[1].eventID != "e4" || rows[1].status != interfaces.KnEventHandlingFailed || rows[1].message == "" {
		t.Fatalf("second handling %+v", rows[1])
	}

	if string(first.inputs) != "{}" {
		t.Fatalf("a class that binds no inputs gave %s", first.inputs)
	}

	for _, class := range []eventClass{handledClass(interfaces.KnEventHandlingSuggest), {id: "late_order", source: "rule"}} {
		if rows := handlingsFor(class, changes, hits, o); len(rows) != 0 {
			t.Fatalf("a class that does not run its handling action by itself queued %+v", rows)
		}
	}
}

// The values a class gives the inputs of its handling action go with each event it queues, as
// they were when the event was found.
func TestHandlingsCarryTheClassesInputs(t *testing.T) {
	class := handledClass(interfaces.KnEventHandlingAuto)
	class.handling.Parameters = map[string]interfaces.ObjectTypeEventHandlingParameter{
		"reason": {ValueFrom: "const", Value: "late"},
		"buyer":  {ValueFrom: "property", Value: "buyer_id"},
	}
	hits := []interfaces.KnEventRuleHit{{Key: "PO-1", Identity: json.RawMessage(`{"id":"PO-1"}`)}}

	rows := handlingsFor(class, []change{detected("e1", "PO-1", false)}, hits, op{now: 1000, actor: checker})

	want := `{"buyer":{"value_from":"property","value":"buyer_id"},"reason":{"value_from":"const","value":"late"}}`
	if len(rows) != 1 || string(rows[0].inputs) != want {
		t.Fatalf("handlings %+v, want inputs %s", rows, want)
	}
}

type fakeHandlingStore struct {
	due     []handlingRow
	settled []handlingRow
}

func (f *fakeHandlingStore) alone(_ context.Context, _ string, fn func() error) (bool, error) {
	return true, fn()
}

func (f *fakeHandlingStore) dueHandlings(context.Context, int64, int) ([]handlingRow, error) {
	return f.due, nil
}

func (f *fakeHandlingStore) settleHandling(_ context.Context, row handlingRow) error {
	f.settled = append(f.settled, row)
	return nil
}

type fakeHandlingAccess struct {
	outcome interfaces.EventHandlingOutcome
	asked   []interfaces.AccountInfo
	inputs  []string
}

func (f *fakeHandlingAccess) ExecuteForEvent(ctx context.Context, knID, actionTypeID string, identity, inputs json.RawMessage) interfaces.EventHandlingOutcome {
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	f.asked = append(f.asked, account)
	f.inputs = append(f.inputs, string(inputs))
	return f.outcome
}

func TestTheHandlerSettlesEachTry(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	queued := handlingRow{eventID: "e1", knID: "kn1", actionTypeID: "notify_buyer",
		target: json.RawMessage(`{"id":"PO-1"}`), inputs: json.RawMessage(`{"reason":{"value_from":"const","value":"late"}}`),
		subject: checker, status: interfaces.KnEventHandlingQueued}
	for name, c := range map[string]struct {
		outcome   interfaces.EventHandlingOutcome
		attempts  int
		status    string
		execution string
		nextAt    int64
	}{
		"started":     {interfaces.EventHandlingOutcome{ExecutionID: "ex-1"}, 0, interfaces.KnEventHandlingStarted, "ex-1", 0},
		"running":     {interfaces.EventHandlingOutcome{Running: true}, 0, interfaces.KnEventHandlingStarted, "", 0},
		"governed":    {interfaces.EventHandlingOutcome{NeedsPerson: "governance required"}, 0, interfaces.KnEventHandlingNeedsPerson, "", 0},
		"refused":     {interfaces.EventHandlingOutcome{Refused: "action type not found"}, 0, interfaces.KnEventHandlingFailed, "", 0},
		"unavailable": {interfaces.EventHandlingOutcome{Unavailable: errors.New("connection refused")}, 2, interfaces.KnEventHandlingQueued, "", now.Add(4 * time.Minute).UnixMilli()},
		"given up":    {interfaces.EventHandlingOutcome{Unavailable: errors.New("connection refused")}, handleAttemptsMax - 1, interfaces.KnEventHandlingFailed, "", 0},
	} {
		t.Run(name, func(t *testing.T) {
			row := queued
			row.attempts = c.attempts
			store := &fakeHandlingStore{due: []handlingRow{row}}
			access := &fakeHandlingAccess{outcome: c.outcome}
			h := &handler{store: store, actions: access, now: func() time.Time { return now }}

			h.round(context.Background())

			if len(access.asked) != 1 || access.asked[0] != checker {
				t.Fatalf("executed as %+v, want the class's execution subject", access.asked)
			}
			if access.inputs[0] != string(queued.inputs) {
				t.Fatalf("executed with inputs %s, want %s", access.inputs[0], queued.inputs)
			}
			if len(store.settled) != 1 {
				t.Fatalf("settled %+v", store.settled)
			}
			got := store.settled[0]
			if got.status != c.status || got.executionID != c.execution || got.attempts != c.attempts+1 || got.nextAt != c.nextAt {
				t.Fatalf("settled %+v, want status %s, execution %q, attempts %d, next %d",
					got, c.status, c.execution, c.attempts+1, c.nextAt)
			}
			if c.status != interfaces.KnEventHandlingStarted && got.message == "" {
				t.Fatalf("settled %+v without a reason", got)
			}
		})
	}
}

func TestRetryWaitDoublesUpToAnHour(t *testing.T) {
	for attempts, want := range map[int]time.Duration{1: time.Minute, 2: 2 * time.Minute, 4: 8 * time.Minute, 7: time.Hour, 20: time.Hour} {
		if got := retryWait(attempts); got != want {
			t.Fatalf("retryWait(%d) = %v, want %v", attempts, got, want)
		}
	}
}
