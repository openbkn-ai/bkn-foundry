package bkntrace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

func TestKafkaEmitterAdmitsCanonicalActionEvidence(t *testing.T) {
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: "agent-operator-integration", BaseStreamID: "agent-operator-integration",
		WorkloadIdentity: "agent-operator-integration", ProcessBootID: "boot-test",
		CapturePolicyRevision: "1",
	}, testEvidenceSender{})
	if err != nil {
		t.Fatal(err)
	}
	action, ok := ParseAction(testHeaders(), "box", "tool", "user")
	if !ok {
		t.Fatal("expected action")
	}
	events, _ := action.AfterPermission(nil)
	if err := NewKafkaEmitter(publisher).Emit(context.Background(), action, events); err != nil {
		t.Fatal(err)
	}
	queue := publisher.SnapshotQueue()
	if len(queue) != 1 || queue[0].Header("bkn-trace-schema-version") != "3.0.0" {
		t.Fatalf("unexpected admitted record: %#v", queue)
	}
	var record struct {
		EventID   string          `json:"event_id"`
		EventType string          `json:"event_type"`
		Envelope  json.RawMessage `json:"envelope"`
	}
	if err := json.Unmarshal(queue[0].Value, &record); err != nil {
		t.Fatal(err)
	}
	if record.EventID != events[0].EventID || record.EventType != "action.approved" {
		t.Fatalf("canonical event identity lost: %#v", record)
	}
	var envelope struct {
		Trace map[string]any `json:"trace"`
		Event Event          `json:"event"`
	}
	if err := json.Unmarshal(record.Envelope, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Trace["traceparent"] != testHeaders()["traceparent"] || envelope.Event.EventID != events[0].EventID || envelope.Event.CausationEventID != "evt_action_approval_requested_001" {
		t.Fatalf("action evidence context lost: %#v", envelope)
	}
}

type testEvidenceSender struct{}

func (testEvidenceSender) Send(context.Context, evidencepublisher.Record) error { return nil }
