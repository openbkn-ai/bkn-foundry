package bkntrace

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

type captureEvidenceSender struct{ records []evidencepublisher.Record }

func (s *captureEvidenceSender) Send(_ context.Context, record evidencepublisher.Record) error {
	s.records = append(s.records, record)
	return nil
}

func TestSubmitEventsPublishesCanonicalKafkaRecord(t *testing.T) {
	sender := &captureEvidenceSender{}
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: "agent-retrieval", BaseStreamID: "agent-retrieval", WorkloadIdentity: "agent-retrieval",
		ProcessBootID: "boot-1", CapturePolicyRevision: "41",
	}, sender)
	if err != nil {
		t.Fatal(err)
	}
	SetEvidencePublisher(publisher)
	t.Cleanup(func() { SetEvidencePublisher(nil); _ = publisher.Close(context.Background()) })
	t.Setenv("BKN_TRACE_EVIDENCE_INGEST_URL", "http://legacy.invalid/evidence/events")
	previousClient := artifactHTTPClient
	artifactHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("Evidence event used the retired HTTP ingest path")
		return nil, nil
	})}
	t.Cleanup(func() { artifactHTTPClient = previousClient })

	event := Event{"event_id": "evt-1", "event_type": "retrieval.completed", "conversation_id": "conv-1", "interaction_id": "int-1", "operation_id": "op-1", "attempt": 1, "bkn.trace.schema.version": ContractVersion, "observed_at": "2026-09-23T00:00:00Z"}
	if err := SubmitEvents(testTraceContext(), nil, nil, []Event{event}); err != nil {
		t.Fatal(err)
	}
	queued := publisher.SnapshotQueue()
	if len(queued) != 1 {
		t.Fatalf("queued records = %d, want 1", len(queued))
	}
	var payload map[string]any
	if err := json.Unmarshal(queued[0].Value, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["producer_id"] != "agent-retrieval" || payload["conversation_id"] != "conv-1" ||
		payload["producer_stream_id"] != "agent-retrieval:boot-1" || payload["producer_sequence"] != float64(1) {
		t.Fatalf("canonical payload = %#v", payload)
	}
	if queued[0].Key != "agent-retrieval:boot-1" || queued[0].Header("capture_policy_revision") != "41" ||
		queued[0].Header("producer_instance_id") != "agent-retrieval#boot-1" ||
		queued[0].Header("bkn-evidence-record-class") != "live" {
		t.Fatalf("Kafka key/headers = key:%q headers:%#v", queued[0].Key, queued[0].Headers)
	}
	if got := publisher.Flush(context.Background()); got.Published != 1 || got.Dropped != 0 {
		t.Fatalf("flush result = %#v, want one acknowledged record", got)
	}
	if len(sender.records) != 1 {
		t.Fatalf("sender records=%d, want one", len(sender.records))
	}
}

func TestSubmitEventsQueueFullFailsOpen(t *testing.T) {
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: "agent-retrieval", BaseStreamID: "agent-retrieval", WorkloadIdentity: "agent-retrieval",
		ProcessBootID: "boot-1", CapturePolicyRevision: "41", MaxRecordBytes: 1,
	}, &captureEvidenceSender{})
	if err != nil {
		t.Fatal(err)
	}
	SetEvidencePublisher(publisher)
	t.Cleanup(func() { SetEvidencePublisher(nil); _ = publisher.Close(context.Background()) })
	var logOutput bytes.Buffer
	previousLogWriter := log.Writer()
	log.SetOutput(&logOutput)
	t.Cleanup(func() { log.SetOutput(previousLogWriter) })
	ctx := withEvidenceOutcome(testTraceContext())
	if err := SubmitEvents(ctx, nil, nil, []Event{{"event_id": "evt-drop", "event_type": "retrieval.completed"}}); err != nil {
		t.Fatalf("Kafka queue drop changed the business result: %v", err)
	}
	if queued := publisher.SnapshotQueue(); len(queued) != 0 {
		t.Fatalf("oversized event unexpectedly queued: %d", len(queued))
	}
	if outcome := evidenceOutcomeFromContext(ctx); outcome == nil || !outcome.attempted || outcome.accepted {
		t.Fatalf("dropped event outcome=%#v, want attempted but not accepted", outcome)
	}
	if !strings.Contains(logOutput.String(), evidencepublisher.ReasonMessageTooLarge) {
		t.Fatalf("queue drop was not observable in logs: %q", logOutput.String())
	}
}
