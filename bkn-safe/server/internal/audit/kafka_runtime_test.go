package audit

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

func TestKafkaRuntimeWithoutConfigKeepsBusinessFailOpen(t *testing.T) {
	runtime := NewKafkaRuntimeFromEnv(func(string) string { return "" })
	if runtime == nil || runtime.Recorder == nil {
		t.Fatal("missing configuration must still provide a fail-open recorder")
	}
	if err := runtime.Recorder.Record(context.Background(), Entry{
		RequestID: "req-safe-unconfigured", Method: "POST", Resource: "users", Action: "create", Status: 400,
	}); err == nil {
		t.Fatal("unconfigured publisher was not reported as a coverage gap")
	}
	runtime.Close()
}

func TestDeliveryObserverAttributesSharedPublisherAccessDelivery(t *testing.T) {
	telemetry := NewPublishTelemetry()
	safeDeliveryObserver{telemetry: telemetry}.ObserveDelivery(auditpublisher.Delivery{
		Record:  auditpublisher.Record{Key: []byte("bkn-safe-access\x1fsession\x1faccess-1")},
		Outcome: auditpublisher.Delivered,
	})
	recorder := httptest.NewRecorder()
	telemetry.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(recorder.Body.String(), `audit_event_publish_total{source_id="bkn-safe-access",result="delivered",reason="none"} 1`) {
		t.Fatalf("shared delivery attribution = %q", recorder.Body.String())
	}
}
