// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_execution

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestEventExecutionRequest(t *testing.T) {
	identity := json.RawMessage(`{"id":"PO-1"}`)
	for inputs, want := range map[string]string{
		"":     `{"_instance_identities":[{"id":"PO-1"}],"trigger_type":"event"}`,
		"{}":   `{"_instance_identities":[{"id":"PO-1"}],"trigger_type":"event"}`,
		"null": `{"_instance_identities":[{"id":"PO-1"}],"trigger_type":"event"}`,
		`{"reason":{"value_from":"property","value":"buyer"}}`: `{"_instance_identities":[{"id":"PO-1"}],` +
			`"input_bindings":{"reason":{"value_from":"property","value":"buyer"}},"trigger_type":"event"}`,
	} {
		got, err := json.Marshal(eventExecutionRequest(identity, json.RawMessage(inputs)))
		if err != nil || string(got) != want {
			t.Fatalf("inputs %q: %s (%v), want %s", inputs, got, err, want)
		}
	}
}

func TestEventHandlingOutcome(t *testing.T) {
	refusal := func(code string) []byte {
		return []byte(`{"error_code":"` + code + `","description":"refused","error_details":"why"}`)
	}
	if got := eventHandlingOutcome(http.StatusAccepted, []byte(`{"execution_id":"ex-1"}`)); got.ExecutionID != "ex-1" {
		t.Fatalf("accepted: %+v", got)
	}
	if got := eventHandlingOutcome(http.StatusConflict, refusal(governanceRequired)); got.NeedsPerson == "" {
		t.Fatalf("governed: %+v", got)
	}
	if got := eventHandlingOutcome(http.StatusConflict, refusal(duplicateExecution)); !got.Running {
		t.Fatalf("duplicate: %+v", got)
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound} {
		if got := eventHandlingOutcome(status, refusal("OntologyQuery.ActionExecution.InvalidParameter")); got.Refused != "refused: why" {
			t.Fatalf("%d: %+v", status, got)
		}
	}
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusInternalServerError} {
		if got := eventHandlingOutcome(status, refusal("OntologyQuery.ActionExecution.GovernanceUnavailable")); got.Unavailable == nil {
			t.Fatalf("%d: %+v", status, got)
		}
	}
	if got := eventHandlingOutcome(http.StatusAccepted, []byte(`{}`)); got.Refused == "" {
		t.Fatalf("accepted without an execution: %+v", got)
	}
}
