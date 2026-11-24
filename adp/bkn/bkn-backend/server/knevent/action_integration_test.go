// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

//go:build integration

package knevent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// defineRecognition stores a record-event action type of a class as the action type service does,
// as far as events read it.
func defineRecognition(t *testing.T, db *sql.DB, kn, branch, id, class string) {
	t.Helper()
	config, err := json.Marshal(interfaces.ActionOperationConfig{Type: interfaces.ActionOperationEvent, EventClass: class})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO t_action_type (f_id, f_name, f_comment, f_bkn_raw_content, f_kn_id, f_branch,"+
		" f_action_type, f_object_type_id, f_operation_config, f_action_source, f_execution_subject, f_execution_subject_type, f_rule_version)"+
		" VALUES (?, ?, '', '', ?, ?, 'add', 'sku', ?, '{}', 'u1', 'user', 'v1')",
		id, id, kn, branch, string(config)); err != nil {
		t.Fatal(err)
	}
}

func refusedWith(t *testing.T, err error, status int, code string) {
	t.Helper()
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != status || httpErr.ErrorCode != code {
		t.Fatalf("%v, want %d %s", err, status, code)
	}
}

// A class that a record-event action type records takes checks from it alone, and its events tell
// which execution found or closed them.
func TestIntegrationRuleChecksOfARecordEventActionType(t *testing.T) {
	s, db, c := integrationService(t)
	defineClasses(t, db, "kn1")
	check := func(ctx context.Context, actionTypeID string, hits ...interfaces.KnEventRuleHit) (interfaces.KnEventRuleCheckResult, error) {
		c.tick(60 * 1000)
		return s.RecordRuleCheck(ctx, "kn1", interfaces.KnEventRuleCheckInput{
			EventType: "low_stock", ActionTypeID: actionTypeID, RuleVersion: "v1", Status: "ok", Hits: hits,
		})
	}
	// Without one, any rule may check the class, and no check names an action type.
	if _, err := check(userContext(), "low_stock_check"); err == nil {
		t.Fatal("a check that names an action type the class does not have is refused")
	} else {
		refusedWith(t, err, http.StatusConflict, berrors.BknBackend_KnEvent_RecognitionMismatch)
	}
	must(check(userContext(), ""))

	// A draft's action type is not the running version's.
	defineRecognition(t, db, "kn1", "draft", "low_stock_check", "low_stock")
	must(check(userContext(), ""))

	defineRecognition(t, db, "kn1", interfaces.MAIN_BRANCH, "low_stock_check", "low_stock")
	for _, other := range []string{"", "another_check"} {
		_, err := check(userContext(), other)
		refusedWith(t, err, http.StatusConflict, berrors.BknBackend_KnEvent_RecognitionMismatch)
	}

	first := interfaces.WithExecutionID(userContext(), "exec-1")
	opened := must(check(first, "low_stock_check", interfaces.KnEventRuleHit{Key: "A", Detail: json.RawMessage(`{"metric":{"value":3}}`)}))
	if len(opened.Events) != 1 || string(opened.Events[0].Detail) != `{"metric":{"value":3}}` {
		t.Fatalf("opened %+v", opened.Events)
	}
	second := interfaces.WithExecutionID(userContext(), "exec-2")
	must(check(second, "low_stock_check"))

	event := must(s.GetEvent(userContext(), "kn1", opened.Events[0].ID))
	if string(event.Detail) != `{"metric":{"value":3}}` || len(event.History) != 2 {
		t.Fatalf("event %+v", event)
	}
	if event.History[0].Action != actionDetected || event.History[0].ExecutionID != "exec-1" ||
		event.History[1].Action != actionRecovered || event.History[1].ExecutionID != "exec-2" {
		t.Fatalf("history %+v, want the executions that found and closed it", event.History)
	}
}

// A class its record-event action type records keeps its subject and is not deleted, in the
// transaction that would change it.
func TestIntegrationAClassItsActionTypeRecordsIsKept(t *testing.T) {
	_, db, _ := integrationService(t)
	defineClasses(t, db, "kn1")
	kind := &eventKind{store: newStore(db), available: func() bool { return true }}
	class := newClass("low_stock")
	class.Event.Subject = "sku"

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := kind.recognizedBy(context.Background(), tx, class, "delete"); err != nil {
		t.Fatalf("a class without an action type: %v", err)
	}
	if _, err := tx.Exec("INSERT INTO t_action_type (f_id, f_name, f_comment, f_bkn_raw_content, f_kn_id, f_branch,"+
		" f_operation_config, f_action_source) VALUES ('low_stock_check', 'low_stock_check', '', '', 'kn1', ?, ?, '{}')",
		interfaces.MAIN_BRANCH, `{"type":"event","event_class":"low_stock"}`); err != nil {
		t.Fatal(err)
	}
	refusedWith(t, kind.recognizedBy(context.Background(), tx, class, "delete"),
		http.StatusConflict, berrors.BknBackend_KnEvent_RecognizedByAction)

	ids := must(kind.store.recognitionActions(context.Background(), nil, "kn1", interfaces.MAIN_BRANCH, "low_stock"))
	if len(ids) != 0 {
		t.Fatalf("outside the transaction, %v", ids)
	}
}

// A class with events is refused for them even when a record-event action type records it:
// deleting the action type first would not let the class go.
func TestIntegrationDeletingAClassNamesItsEventsFirst(t *testing.T) {
	cs := newClassService(t)
	class := lateOrder("kn1", interfaces.MAIN_BRANCH)
	if err := cs.save(t, class, nil); err != nil {
		t.Fatal(err)
	}
	defineRecognition(t, cs.db, "kn1", interfaces.MAIN_BRANCH, "late_order_check", "late_order")
	refusedWith(t, cs.remove(t, class), http.StatusConflict, berrors.BknBackend_KnEvent_RecognizedByAction)

	must(cs.ReportEvent(userContext(), "kn1", interfaces.KnEventReportInput{EventType: "late_order", Title: "late"}))
	refusedWith(t, cs.remove(t, class), http.StatusConflict, berrors.BknBackend_KnEvent_EventTypeInUse)
}
