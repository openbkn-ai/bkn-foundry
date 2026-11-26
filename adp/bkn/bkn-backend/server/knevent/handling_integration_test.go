// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

//go:build integration

package knevent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A class that runs its handling action by itself has each event its check newly finds handled,
// once, as the class's execution subject; the event shows how that went.
func TestIntegrationNewEventsAreHandledOnce(t *testing.T) {
	s, db, c := integrationService(t)
	ctx := userContext()
	defineObjectType(t, db, "kn1", interfaces.MAIN_BRANCH, "low_stock", "低库存", &interfaces.ObjectTypeEvent{
		Source: "rule", Subject: "sku",
		HandlingAction: &interfaces.ObjectTypeEventHandling{ActionTypeID: "reorder", Mode: interfaces.KnEventHandlingAuto,
			Parameters: map[string]interfaces.ObjectTypeEventHandlingParameter{"quantity": {ValueFrom: "property", Value: "reorder_qty"}}},
	})
	hit := func(key string) interfaces.KnEventRuleHit {
		return interfaces.KnEventRuleHit{Key: key, Identity: json.RawMessage(`{"sku_id":"` + key + `"}`)}
	}
	check := func(hits ...interfaces.KnEventRuleHit) {
		t.Helper()
		c.tick(1000)
		must(s.RecordRuleCheck(ctx, "kn1", interfaces.KnEventRuleCheckInput{EventType: "low_stock", Status: "ok", Hits: hits}))
	}
	// The first check finds a backlog, which a person decides on.
	check(hit("A"))
	// The next finds B anew; A is still there and is not handled again.
	check(hit("A"), hit("B"))
	check(hit("A"), hit("B"))

	due := must(s.store.dueHandlings(context.Background(), c.now.Load(), 10))
	if len(due) != 1 || string(due[0].target) != `{"sku_id":"B"}` || due[0].actionTypeID != "reorder" ||
		due[0].subject.ID == "" || string(due[0].inputs) != `{"quantity":{"value_from":"property","value":"reorder_qty"}}` {
		t.Fatalf("due %+v, want B alone", due)
	}

	access := &fakeHandlingAccess{outcome: interfaces.EventHandlingOutcome{Unavailable: errors.New("down")}}
	h := &handler{store: s.store, actions: access, now: func() time.Time { return time.UnixMilli(c.now.Load()) }}
	h.round(context.Background())
	if again := must(s.store.dueHandlings(context.Background(), c.now.Load(), 10)); len(again) != 0 {
		t.Fatalf("a handling that could not be tried is due again at once: %+v", again)
	}
	c.tick(time.Minute.Milliseconds())
	access.outcome = interfaces.EventHandlingOutcome{ExecutionID: "ex-1"}
	h.round(context.Background())
	h.round(context.Background())
	if len(access.asked) != 2 {
		t.Fatalf("asked %d times, want 2", len(access.asked))
	}

	list := must(s.ListEvents(ctx, "kn1", interfaces.KnEventFilter{}))
	handled := map[string]*interfaces.KnEventHandling{}
	for _, event := range list.Entries {
		handled[event.ObjectKey] = event.Handling
	}
	if handled["A"] != nil {
		t.Fatalf("the backlog event was handled: %+v", handled["A"])
	}
	b := handled["B"]
	if b == nil || b.Status != interfaces.KnEventHandlingStarted || b.ExecutionID != "ex-1" || b.Attempts != 2 || b.ActionTypeID != "reorder" {
		t.Fatalf("B handled %+v", b)
	}
	for _, event := range list.Entries {
		if event.ObjectKey == "B" {
			got := must(s.GetEvent(ctx, "kn1", event.ID))
			if got.Handling == nil || got.Handling.ExecutionID != "ex-1" {
				t.Fatalf("the event shows %+v", got.Handling)
			}
		}
	}

	// Deleting the network takes its handlings.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.DeleteKnEvents(context.Background(), tx, "kn1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + handlingTable).Scan(&left); err != nil || left != 0 {
		t.Fatalf("handlings left: %d, %v", left, err)
	}
}

// Whether an action type can handle the events of a class is read in the saving transaction.
func TestIntegrationTheHandlingActionTypeIsRead(t *testing.T) {
	db := integrationDB(t)
	if _, err := db.Exec("INSERT INTO t_action_type (f_id, f_name, f_comment, f_bkn_raw_content, f_kn_id, f_branch, f_object_type_id," +
		" f_action_source, f_operation_config, f_parameters) VALUES ('reorder', '补货', '', '', 'kn1', 'main', 'sku', '{}'," +
		" '{\"type\":\"object\"}'," +
		" '[{\"name\":\"qty\",\"value_from\":\"const\"},{\"name\":\"reason\",\"value_from\":\"input\"}]')"); err != nil {
		t.Fatal(err)
	}
	store := newStore(db)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	action, found, err := store.handlingActionType(context.Background(), tx, "kn1", "main", "reorder")
	if err != nil || !found || action.objectTypeID != "sku" || action.recordsEvents || len(action.inputs) != 1 || action.inputs[0] != "reason" {
		t.Fatalf("read %+v %v %v", action, found, err)
	}
	if _, found, err := store.handlingActionType(context.Background(), tx, "kn1", "main", "missing"); found || err != nil {
		t.Fatalf("a missing action type: %v %v", found, err)
	}
}
