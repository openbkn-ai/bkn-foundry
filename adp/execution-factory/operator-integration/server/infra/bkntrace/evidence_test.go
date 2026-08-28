package bkntrace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

type memoryExecutionStore struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *memoryExecutionStore) PutIfAbsent(_ context.Context, key, value string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.values[key]; exists {
		return false, nil
	}
	s.values[key] = value
	return true, nil
}

func (s *memoryExecutionStore) Get(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key], nil
}

func (s *memoryExecutionStore) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

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

func TestParseActionDoesNotRequireBusinessDomain(t *testing.T) {
	headers := testHeaders()
	if _, ok := ParseAction(headers, "box", "tool", "user"); !ok {
		t.Fatal("expected action without business domain")
	}
}

func TestParseActionRejectsInvalidW3CTraceparent(t *testing.T) {
	for _, traceparent := range []string{
		"00-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz-abcdef1234567890-01",
		"00-00000000000000000000000000000000-abcdef1234567890-01",
		"00-1234567890abcdef1234567890abcdef-0000000000000000-01",
		"01-1234567890abcdef1234567890abcdef-abcdef1234567890-01",
	} {
		headers := testHeaders()
		headers["traceparent"] = traceparent
		if _, ok := ParseAction(headers, "box", "tool", "user"); ok {
			t.Fatalf("invalid traceparent accepted: %s", traceparent)
		}
	}
}

func TestHTTPEmitterRetriesNon2xxAndIncludesOriginalTraceparent(t *testing.T) {
	attempts := 0
	var envelope map[string]any
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		status := http.StatusAccepted
		if attempts < 3 {
			status = http.StatusServiceUnavailable
		}
		return &http.Response{
			StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader("")),
		}, nil
	})}

	action, ok := ParseAction(testHeaders(), "box", "tool", "user")
	if !ok {
		t.Fatal("expected action")
	}
	events, _ := action.AfterPermission(nil)
	emitter := &HTTPEmitter{
		URL: "http://trace.invalid/events", Client: client, MaxAttempts: 3, RetryBackoff: time.Millisecond,
	}
	if err := emitter.Emit(context.Background(), action, events); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts=%d", attempts)
	}
	trace := envelope["trace"].(map[string]any)
	if trace["traceparent"] != testHeaders()["traceparent"] {
		t.Fatalf("traceparent lost: %#v", trace)
	}
	if _, exists := trace["business_domain"]; exists {
		t.Fatalf("business domain must not be emitted: %#v", trace)
	}
}

func TestHTTPEmitterSendsDedicatedIngestToken(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("X-BKN-Trace-Ingest-Token"); got != "producer-token" {
			t.Fatalf("ingest token header=%q", got)
		}
		return &http.Response{StatusCode: http.StatusAccepted, Status: "202 Accepted", Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	action, ok := ParseAction(testHeaders(), "box", "tool", "user")
	if !ok {
		t.Fatal("expected action")
	}
	events, _ := action.AfterPermission(nil)
	emitter := &HTTPEmitter{URL: "http://trace.invalid/events", Token: "producer-token", Client: client, MaxAttempts: 1}
	if err := emitter.Emit(context.Background(), action, events); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPEmitterRetriesTimeout(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return nil, context.DeadlineExceeded
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Status:     http.StatusText(http.StatusAccepted),
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})}
	action, ok := ParseAction(testHeaders(), "box", "tool", "user")
	if !ok {
		t.Fatal("expected action")
	}
	events, _ := action.AfterPermission(nil)
	emitter := &HTTPEmitter{
		URL: "http://trace.invalid/events", Client: client, MaxAttempts: 3,
	}
	if err := emitter.Emit(context.Background(), action, events); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("timeout attempts=%d", attempts)
	}
}

func TestActionStagesHaveDistinctReplayStableObservedAt(t *testing.T) {
	action, ok := ParseAction(testHeaders(), "box", "tool", "user")
	if !ok {
		t.Fatal("expected action")
	}
	first, _ := action.Complete(nil, []byte(`{"ok":true}`), nil)
	second, _ := action.Complete(nil, []byte(`{"ok":true}`), nil)
	seen := map[string]bool{}
	for i := range first {
		if seen[first[i].ObservedAt] {
			t.Fatalf("stage timestamp reused: %s", first[i].ObservedAt)
		}
		seen[first[i].ObservedAt] = true
		if !reflect.DeepEqual(first[i], second[i]) {
			t.Fatalf("replay changed event %d", i)
		}
	}
}

func TestExecutionGateAllowsOneConcurrentSideEffectAndReplaysResult(t *testing.T) {
	action, ok := ParseAction(testHeaders(), "box", "tool", "user")
	if !ok {
		t.Fatal("expected action")
	}
	gate := NewExecutionGate(&memoryExecutionStore{values: map[string]string{}})
	results := make(chan ExecutionState, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, _ := gate.Acquire(context.Background(), action)
			results <- state
		}()
	}
	wg.Wait()
	close(results)
	acquired := 0
	for state := range results {
		if state.Acquired {
			acquired++
		}
	}
	if acquired != 1 {
		t.Fatalf("side effect rights=%d", acquired)
	}
	if err := gate.Complete(context.Background(), action, []byte(`{"ok":true}`), false); err != nil {
		t.Fatal(err)
	}
	replay, err := gate.Acquire(context.Background(), action)
	if err != nil || !replay.Completed || string(replay.Result) != `{"ok":true}` {
		t.Fatalf("result not replayed: state=%#v err=%v", replay, err)
	}
}

func TestExecutionGateDeduplicatesSameActionAcrossAttempts(t *testing.T) {
	store := &memoryExecutionStore{values: map[string]string{}}
	gate := NewExecutionGate(store)
	action, ok := ParseAction(testHeaders(), "box", "tool", "user")
	if !ok {
		t.Fatal("expected action")
	}
	state, err := gate.Acquire(context.Background(), action)
	if err != nil || !state.Acquired {
		t.Fatalf("first attempt not acquired: state=%+v err=%v", state, err)
	}
	if err := gate.Complete(context.Background(), action, []byte(`{"task":"monitor-1"}`), false); err != nil {
		t.Fatal(err)
	}

	retry := action
	retry.attempt++
	state, err = gate.Acquire(context.Background(), retry)
	if err != nil || !state.Completed || state.Acquired || string(state.Result) != `{"task":"monitor-1"}` {
		t.Fatalf("retry must replay prior result: state=%+v err=%v", state, err)
	}
}

func TestExecutionGateIsolatesSameActionIDAcrossTenants(t *testing.T) {
	gate := NewExecutionGate(&memoryExecutionStore{values: map[string]string{}})
	first, ok := ParseAction(testHeaders(), "box", "tool", "user")
	if !ok {
		t.Fatal("expected first action")
	}
	second := first
	second.accountID = "another-account"

	firstState, firstErr := gate.Acquire(context.Background(), first)
	secondState, secondErr := gate.Acquire(context.Background(), second)
	if firstErr != nil || secondErr != nil || !firstState.Acquired || !secondState.Acquired {
		t.Fatalf("tenant-scoped actions must acquire independently: first=%+v/%v second=%+v/%v", firstState, firstErr, secondState, secondErr)
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
	allowed := map[string]map[string]bool{
		"action.approved": {
			"action_instance_id": true, "actor_ref": true, "policy_decision_ref": true, "status": true,
		},
		"action.executed": {
			"action_instance_id": true, "invocation_ref": true, "tool_ref": true, "status": true,
			"error_category": true, "error_hash": true,
		},
		"action.result_recorded": {
			"action_instance_id": true, "result_hash": true, "artifact_ref": true, "task_ref": true, "status": true,
		},
	}
	for _, event := range first {
		for field := range event.Payload {
			if !allowed[event.EventType][field] {
				t.Fatalf("%s contains non-2.1 payload field %s", event.EventType, field)
			}
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
