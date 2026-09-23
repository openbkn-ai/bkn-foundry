package bkntrace

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
	action, ok := parseTestAction()
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
		EventID        string          `json:"event_id"`
		EventType      string          `json:"event_type"`
		ConversationID string          `json:"conversation_id"`
		InteractionID  string          `json:"interaction_id"`
		StartedAt      string          `json:"started_at"`
		Envelope       json.RawMessage `json:"envelope"`
	}
	if err := json.Unmarshal(queue[0].Value, &record); err != nil {
		t.Fatal(err)
	}
	if record.EventID != events[0].EventID || record.EventType != "action.approved" || record.ConversationID != "conv_action_001" || record.InteractionID != events[0].InteractionID || record.StartedAt == "" {
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

func TestKafkaEmitterDoesNotAdmitEvidenceWithoutConversationContext(t *testing.T) {
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
		t.Fatal("missing correlation metadata must not disable ActionExecutions control flow")
	}
	events, _ := action.AfterPermission(nil)
	if err := NewKafkaEmitter(publisher).Emit(context.Background(), action, events); err == nil {
		t.Fatal("Evidence without conversation correlation metadata was accepted")
	}
	if got := len(publisher.SnapshotQueue()); got != 0 {
		t.Fatalf("queued non-canonical Evidence: %d", got)
	}
}

func TestPeriodicFlushReportsCoverageGapFields(t *testing.T) {
	want := evidencepublisher.DrainResult{
		ProducerInstanceID: "agent-operator-integration#boot-test", CapturePolicyRevision: "1",
		LastAcceptedSequence: 17, Dropped: 2,
	}
	stop, done := make(chan struct{}), make(chan struct{})
	reported := make(chan evidencepublisher.DrainResult, 1)
	flushes := 0
	report := func(got evidencepublisher.DrainResult) {
		reported <- got
	}
	go runEvidenceFlushLoop(stop, done, time.Millisecond, func(context.Context) evidencepublisher.DrainResult {
		flushes++
		if flushes == 1 {
			return want
		}
		return evidencepublisher.DrainResult{}
	}, report)
	select {
	case got := <-reported:
		if got.ProducerInstanceID != want.ProducerInstanceID || got.Dropped != want.Dropped ||
			got.LastAcceptedSequence != want.LastAcceptedSequence || got.CapturePolicyRevision != want.CapturePolicyRevision {
			t.Fatalf("coverage-gap fields=%#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("flush drop was not reported")
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("flush worker did not stop")
	}
}

func TestFlushWorkerWaitsForInflightFlushBeforeStopping(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	flushStarted := make(chan struct{}, 1)
	allowFlushToFinish := make(chan struct{})
	go runEvidenceFlushLoop(stop, done, time.Millisecond, func(context.Context) evidencepublisher.DrainResult {
		select {
		case flushStarted <- struct{}{}:
		default:
		}
		<-allowFlushToFinish
		return evidencepublisher.DrainResult{}
	}, nil)
	<-flushStarted
	close(stop)
	select {
	case <-done:
		t.Fatal("flush worker stopped while a flush was in flight")
	default:
	}
	close(allowFlushToFinish)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("flush worker did not stop after the in-flight flush completed")
	}
}

type testEvidenceSender struct{}

func (testEvidenceSender) Send(context.Context, evidencepublisher.Record) error { return nil }
