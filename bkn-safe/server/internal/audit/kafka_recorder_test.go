package audit

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type auditPublisherStub struct {
	values      [][]byte
	disposition auditpublisher.Disposition
}

func TestKafkaRecorderLogsStructuredCoverageGapWithoutTargetName(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	publisher := &auditPublisherStub{disposition: auditpublisher.DroppedQueueFull}
	entry := Entry{
		ActorID: "admin-1", ActorNameSnapshot: "Administrator", RequestID: "req-gap-1",
		Method: "POST", Resource: "clients", Action: "add_redirect_uri", Status: 200,
		TargetID: "oauth-client:studio:redirect-uri:digest", TargetName: "OpenBKN Studio redirect URI https://example.test/callback?token=secret",
	}
	if err := NewKafkaRecorder(publisher, "test").Record(context.Background(), entry); err == nil {
		t.Fatal("rejected publish must return an observability error")
	}
	logLine := output.String()
	for _, want := range []string{
		`"msg":"safe audit coverage gap"`, `"request_id":"req-gap-1"`, `"resource":"clients"`,
		`"action":"add_redirect_uri"`, `"stage":"publish"`, `"reason":"publish_rejected"`,
		`"target_id":"oauth-client:studio:redirect-uri:digest"`,
	} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("coverage-gap log missing %s: %s", want, logLine)
		}
	}
	if strings.Contains(logLine, "token=secret") {
		t.Fatalf("coverage-gap service log leaked target URI: %s", logLine)
	}
}

func (s *auditPublisherStub) TryPublish(value []byte) auditpublisher.Disposition {
	s.values = append(s.values, append([]byte(nil), value...))
	return s.disposition
}

func TestKafkaRecorderPublishesCommittedSafeFact(t *testing.T) {
	publisher := &auditPublisherStub{disposition: auditpublisher.Accepted}
	recorder := NewKafkaRecorder(publisher, "test")
	entry := Entry{ActorID: "verified-admin", ActorNameSnapshot: "Administrator", ActorType: "user", AuthMethod: "oauth", RequestID: "req-safe-1", SourceChannel: "api", Method: "POST", Resource: "users", Action: "create", TargetID: "user-1", TargetName: "User One", Status: 201}
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
	} else if !CoverageGapWasLogged(err) {
		t.Fatal("unavailable publisher error did not mark its coverage-gap log")
	}
	publisher := &auditPublisherStub{disposition: auditpublisher.DroppedQueueFull}
	if err := NewKafkaRecorder(publisher, "test").Record(context.Background(), entry); err == nil {
		t.Fatal("dropped event silently accepted")
	} else if !CoverageGapWasLogged(err) {
		t.Fatal("dropped publisher error did not mark its coverage-gap log")
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
