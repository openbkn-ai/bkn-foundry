// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

func TestRuleChecksOfAClassComeFromItsActionType(t *testing.T) {
	for _, test := range []struct {
		name     string
		running  []string
		sentBy   string
		accepted bool
	}{
		{"a class without one takes any rule", nil, "", true},
		{"a class without one takes no action type", nil, "at1", false},
		{"a class with one takes it", []string{"at1"}, "at1", true},
		{"a class with one takes no other", []string{"at1"}, "at2", false},
		{"a class with one takes no rule", []string{"at1"}, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			asked := ""
			s := &service{recognitions: func(_ context.Context, _ *sql.Tx, knID, classID string) ([]string, error) {
				asked = knID + "/" + classID
				return test.running, nil
			}}
			err := s.checkRecognition(context.Background(), nil, "kn1", "late_order", test.sentBy)
			if test.accepted {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var httpErr *httperrors.HTTPError
				if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusConflict ||
					httpErr.ErrorCode != berrors.BknBackend_KnEvent_RecognitionMismatch {
					t.Fatalf("%v, want 409 %s", err, berrors.BknBackend_KnEvent_RecognitionMismatch)
				}
			}
			if asked != "kn1/late_order" {
				t.Fatalf("asked for %q, want the action types of the class", asked)
			}
		})
	}
}

func TestHitDetailIsAnObjectOfBoundedSize(t *testing.T) {
	check := func(detail string) error {
		input := interfaces.KnEventRuleCheckInput{EventType: "t", Status: "ok",
			Hits: []interfaces.KnEventRuleHit{{Key: "a", Detail: json.RawMessage(detail)}}}
		return validateRuleCheckInput(&input)
	}
	if err := check(`{"metric":{"value":0.8}}`); err != nil {
		t.Fatal(err)
	}
	if err := check(`[1]`); err == nil {
		t.Fatal("a detail that is not an object is refused")
	}
	if err := check(`{"x":"` + strings.Repeat("x", interfaces.KnEventHitDetailMaxBytes) + `"}`); err == nil {
		t.Fatal("an oversized detail is refused")
	}
}

// A check opens an event with what it found, and every change it makes names its execution.
func TestRuleCheckRecordsItsDetailAndExecution(t *testing.T) {
	o := testOp(100)
	o.executionID = "exec-1"
	open := ruleEvent("B", interfaces.KnEventVerified, "v1", 0)
	open.Detail = json.RawMessage(`{"first":true}`)
	changes, _ := applyRuleCheck(ruleType, &interfaces.KnEventRuleCheck{Status: "ok", Time: 50, LastOKRuleVersion: "v1"},
		[]*interfaces.KnEvent{open}, interfaces.KnEventRuleCheckInput{
			EventType: "low_stock", RuleVersion: "v1", Status: "ok",
			Hits: []interfaces.KnEventRuleHit{
				{Key: "A", Detail: json.RawMessage(`{"metric":{"value":3}}`)},
				{Key: "B", Detail: json.RawMessage(`{"metric":{"value":4}}`)},
			},
		}, o)
	if len(changes) != 2 {
		t.Fatalf("%d changes, want 2", len(changes))
	}
	for _, entry := range changes {
		if entry.transition.ExecutionID != "exec-1" {
			t.Fatalf("change %s of %s names execution %q", entry.transition.Action, entry.event.ObjectKey, entry.transition.ExecutionID)
		}
		switch entry.event.ObjectKey {
		case "A":
			if string(entry.event.Detail) != `{"metric":{"value":3}}` {
				t.Fatalf("new event detail %s", entry.event.Detail)
			}
		case "B":
			if string(entry.event.Detail) != `{"first":true}` {
				t.Fatalf("a later check changed the detail to %s", entry.event.Detail)
			}
		}
	}
}
