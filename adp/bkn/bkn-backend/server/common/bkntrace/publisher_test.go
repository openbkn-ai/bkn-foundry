package bkntrace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

type captureEvidenceSender struct{ records []evidencepublisher.Record }

func (s *captureEvidenceSender) Send(_ context.Context, record evidencepublisher.Record) error {
	s.records = append(s.records, record)
	return nil
}

func TestPublishEvidenceEventUsesCanonicalPublisherRecord(t *testing.T) {
	sender := &captureEvidenceSender{}
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: "bkn-backend", BaseStreamID: "backend", WorkloadIdentity: "bkn-backend",
		ProcessBootID: "boot-1", CapturePolicyRevision: "1",
	}, sender)
	if err != nil {
		t.Fatalf("evidencepublisher.New() error = %v", err)
	}
	setEvidencePublisher(publisher)
	t.Cleanup(func() { setEvidencePublisher(nil) })

	result := publishEvidenceEvent(context.Background(), Event{"event_id": "evt-1", "event_type": "query.completed", "payload": map[string]any{"count": 1}})
	if result.Disposition != evidencepublisher.Accepted {
		t.Fatalf("disposition = %q, want accepted", result.Disposition)
	}
	if len(publisher.SnapshotQueue()) != 1 {
		t.Fatalf("queue length = %d, want 1", len(publisher.SnapshotQueue()))
	}
}

func TestSubmitEventsMatchesCanonicalEvidenceFixture(t *testing.T) {
	sender := &captureEvidenceSender{}
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: "bkn-backend", BaseStreamID: "backend", WorkloadIdentity: "bkn-backend",
		ProcessBootID: "boot-1", CapturePolicyRevision: "41",
	}, sender)
	if err != nil {
		t.Fatal(err)
	}
	setEvidencePublisher(publisher)
	t.Cleanup(func() { setEvidencePublisher(nil); _ = publisher.Close(context.Background()) })

	reqCtx := testRequestContext()
	reqCtx.ConversationID = "conv_schema_read_001"
	reqCtx.SessionScopePresent = true
	events := BuildSchemaReadEvents(testTraceContext(), reqCtx, ReadSubject{
		EntityKind: EntityKindObjectType, Operation: "bkn.schema.object_type.list", KNID: "kn_demo",
	}, nil)
	SubmitEvents(testTraceContext(), reqCtx, events)
	queued := publisher.SnapshotQueue()
	if len(queued) != 1 {
		t.Fatalf("queued records = %d, want 1", len(queued))
	}
	var payload map[string]any
	if err := json.Unmarshal(queued[0].Value, &payload); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"event_id":           events[0]["event_id"],
		"event_type":         "knowledge.read.observed",
		"conversation_id":    "conv_schema_read_001",
		"producer_id":        "bkn-backend",
		"producer_stream_id": "backend:boot-1",
	} {
		if payload[key] != want {
			t.Fatalf("payload[%q] = %#v, want %#v; payload=%s", key, payload[key], want, queued[0].Value)
		}
	}
	wantHeaders := []evidencepublisher.Header{{Key: "content-type", Value: "application/json"}, {Key: "bkn-trace-schema-version", Value: "3.0.0"}, {Key: "capture_policy_revision", Value: "41"}, {Key: "producer_instance_id", Value: "bkn-backend#boot-1"}, {Key: "bkn-evidence-record-class", Value: "live"}}
	if len(queued[0].Headers) != len(wantHeaders) {
		t.Fatalf("headers = %+v", queued[0].Headers)
	}
	for i := range wantHeaders {
		if queued[0].Headers[i] != wantHeaders[i] {
			t.Fatalf("header[%d] = %+v, want %+v", i, queued[0].Headers[i], wantHeaders[i])
		}
	}
}

func TestSubmitEventsSkipsEventsWithoutTrustedSessionScope(t *testing.T) {
	for _, field := range []string{"conversation_id", "interaction_id", "source_flag"} {
		t.Run(field, func(t *testing.T) {
			publisher, err := evidencepublisher.New(evidencepublisher.Config{
				ProducerID: "bkn-backend", BaseStreamID: "backend", WorkloadIdentity: "bkn-backend",
				ProcessBootID: "boot-1", CapturePolicyRevision: "41",
			}, &captureEvidenceSender{})
			if err != nil {
				t.Fatal(err)
			}
			setEvidencePublisher(publisher)
			t.Cleanup(func() { setEvidencePublisher(nil); _ = publisher.Close(context.Background()) })
			reqCtx := testRequestContext()
			reqCtx.ConversationID = "conv_schema_read_001"
			reqCtx.SessionScopePresent = true
			switch field {
			case "conversation_id":
				reqCtx.ConversationID = ""
			case "interaction_id":
				reqCtx.InteractionID = ""
			case "source_flag":
				reqCtx.SessionScopePresent = false
			}
			events := BuildSchemaReadEvents(testTraceContext(), reqCtx, ReadSubject{EntityKind: EntityKindObjectType, KNID: "kn_demo"}, nil)
			SubmitEvents(testTraceContext(), reqCtx, events)
			if got := len(publisher.SnapshotQueue()); got != 0 {
				t.Fatalf("queued records = %d, want 0 without %s", got, field)
			}
		})
	}
}
