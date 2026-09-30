package audit

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type auditPublisherStub struct {
	values      [][]byte
	disposition auditpublisher.Disposition
}

func (s *auditPublisherStub) TryPublish(value []byte) auditpublisher.Disposition {
	s.values = append(s.values, append([]byte(nil), value...))
	return s.disposition
}

func TestKafkaRecorderPublishesCommittedSafeFact(t *testing.T) {
	publisher := &auditPublisherStub{disposition: auditpublisher.Accepted}
	recorder := NewKafkaRecorder(publisher, "test")
	entry := Entry{ActorID: "verified-admin", ActorType: "user", AuthMethod: "oauth", RequestID: "req-safe-1", SourceChannel: "api", Method: "POST", Resource: "users", Action: "create", TargetID: "user-1", Status: 201}
	if err := recorder.Record(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if len(publisher.values) != 1 {
		t.Fatalf("published %d events, want one", len(publisher.values))
	}
	if err := recorder.RecordBatch(context.Background(), []Entry{entry, entry}); err != nil {
		t.Fatal(err)
	}
	if len(publisher.values) != 3 {
		t.Fatalf("published %d events, want three", len(publisher.values))
	}
}

func TestKafkaRecorderRejectsUnavailableOrDroppedPublisher(t *testing.T) {
	entry := Entry{RequestID: "req-safe-gap", Method: "POST", Resource: "users", Action: "create", Status: 400}
	if err := NewKafkaRecorder(nil, "test").Record(context.Background(), entry); err == nil {
		t.Fatal("unavailable publisher silently accepted")
	}
	publisher := &auditPublisherStub{disposition: auditpublisher.DroppedQueueFull}
	if err := NewKafkaRecorder(publisher, "test").Record(context.Background(), entry); err == nil {
		t.Fatal("dropped event silently accepted")
	}
}

func TestKafkaRecorderCountsAcceptedAndDroppedWithoutPayload(t *testing.T) {
	telemetry := NewPublishTelemetry()
	publisher := &auditPublisherStub{disposition: auditpublisher.Accepted}
	recorder := NewKafkaRecorder(publisher, "test", telemetry)
	entry := Entry{ActorID: "admin-1", ActorNameSnapshot: "Administrator", RequestID: "req-safe-metrics-secret", Method: "POST", Resource: "roles", Action: "create", Status: 201, TargetID: "role-metrics-secret", TargetName: "运营角色"}
	if err := recorder.Record(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	publisher.disposition = auditpublisher.DroppedQueueFull
	if err := recorder.Record(context.Background(), entry); err == nil {
		t.Fatal("expected dropped event")
	}
	w := httptest.NewRecorder()
	telemetry.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	for _, want := range []string{`result="accepted",reason="none"} 1`, `result="dropped",reason="queue_full"} 1`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing %q in %q", want, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "metrics-secret") {
		t.Fatal("metrics exposed request or target identity")
	}
}
