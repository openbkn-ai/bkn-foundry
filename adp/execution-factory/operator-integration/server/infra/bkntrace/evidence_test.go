package bkntrace

import (
	"errors"
	"strings"
	"testing"
)

func testHeaders() map[string]any {
	return map[string]any{
		"traceparent":                            "00-1234567890abcdef1234567890abcdef-abcdef1234567890-01",
		"bkn-request-id":                         "req_action_001",
		"bkn-interaction-id":                     "int_action_001",
		"bkn-operation-id":                       "op_action_001",
		"bkn-causation-event-id":                 "evt_claim_001",
		"bkn-claim-id":                           "claim_001",
		"bkn-action-instance-id":                 "action_001",
		"bkn-action-type":                        "monitor",
		"bkn-action-reversible":                  "true",
		"bkn-action-policy-ref":                  "e2e-monitor-auto-approve",
		"bkn-action-observed-at":                 "2026-07-25T10:00:00.000000Z",
		"bkn-action-approval-requested-event-id": "evt_action_approval_requested_001",
		"bkn-attempt":                            "2",
		"x-account-id":                           "acct-test", "x-account-type": "user",
	}
}

func TestLifecycleBuildsStableAllowlistedMonitorEvents(t *testing.T) {
	action, ok := ParseAction(testHeaders(), "box-secret", "tool-secret", "user-secret")
	if !ok {
		t.Fatal("expected complete action context")
	}
	first, err := action.Complete(nil, []byte(`{"secret":"result"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := action.Complete(nil, []byte(`{"secret":"result"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"action.approved", "action.executed", "action.result_recorded"}
	if len(first) != len(want) {
		t.Fatalf("got %d events", len(first))
	}
	for i := range first {
		if first[i].EventType != want[i] {
			t.Fatalf("event %d: got %s want %s", i, first[i].EventType, want[i])
		}
		if first[i].EventID != second[i].EventID {
			t.Fatalf("event id changed on replay: %s != %s", first[i].EventID, second[i].EventID)
		}
		if first[i].Attempt != 2 {
			t.Fatalf("event %d lost attempt: %d", i, first[i].Attempt)
		}
	}
	serialized := string(MustJSON(first))
	for _, forbidden := range []string{"box-secret", "tool-secret", "user-secret", `\"secret\":\"result\"`} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("sensitive value leaked: %s", forbidden)
		}
	}
}

func TestLifecycleRejectsBeforeExecutionAndHashesFailure(t *testing.T) {
	action, ok := ParseAction(testHeaders(), "box", "tool", "user")
	if !ok {
		t.Fatal("expected complete action context")
	}
	permissionErr := errors.New("permission detail must not leak")
	events, err := action.Complete(permissionErr, nil, nil)
	if !errors.Is(err, ErrActionRejected) {
		t.Fatalf("got %v", err)
	}
	if len(events) != 1 || events[0].EventType != "action.rejected" {
		t.Fatalf("unexpected rejected lifecycle: %#v", events)
	}
	serialized := string(MustJSON(events))
	if strings.Contains(serialized, permissionErr.Error()) || !strings.Contains(serialized, "policy_decision_ref") {
		t.Fatalf("permission decision must use a controlled reference: %s", serialized)
	}
}

func TestOnlyReversibleMonitorUsesAutomaticTestPolicy(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(h map[string]any) { h["bkn-action-type"] = "purchase" },
		func(h map[string]any) { h["bkn-action-reversible"] = "false" },
		func(h map[string]any) { h["bkn-action-policy-ref"] = "unknown" },
	} {
		headers := testHeaders()
		mutate(headers)
		if _, ok := ParseAction(headers, "box", "tool", "user"); ok {
			t.Fatal("unsafe action unexpectedly accepted by automatic test policy")
		}
	}
}
