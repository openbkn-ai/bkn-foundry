package bkntrace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"syscall"
	"testing"
)

func TestGuardFinishRetainsSafeFailurePhaseAndCorrelation(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			return nil, errors.New("private-business-payload-and-token")
		}
		return nil, context.DeadlineExceeded
	})
	_, apiErr, err := NewGuard(client).Finish(finishLifecycleContext(false), pendingGuardState(), "private-business-payload-and-token", false, false)
	if err != nil || apiErr == nil || apiErr.Code != "receipt_pending" {
		t.Fatalf("uncertain finish changed: api=%#v err=%v", apiErr, err)
	}
	for _, want := range []string{`operation_id="op-1"`, `request_id="req_finish_recovery_0001"`, `phase="finalize"`, `phase="receipt_read"`, `failure_kind="deadline_exceeded"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("missing diagnostic field %s", want)
		}
	}
	if strings.Contains(logs.String(), "private-business-payload-and-token") {
		t.Fatal("diagnostic leaked business content or credentials")
	}
}

func TestGuardBeginRetainsRegistrationTransportCause(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
		return nil, syscall.ECONNREFUSED
	})
	_, _, _, _, err := NewGuard(client).Begin(finishLifecycleContext(false), GuardIntent{
		Context:  BusinessContext{ConversationID: "conv-1", InteractionID: "int-1", OperationKey: "key-1"},
		ToolName: "query_object_instance", Protocol: "mcp", SourceModule: "context-loader", Input: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("expected registration failure")
	}
	for _, want := range []string{`phase="ensure_operation"`, `failure_kind="connection_refused"`, `request_id="req_finish_recovery_0001"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("registration failure lost %s", want)
		}
	}
}
