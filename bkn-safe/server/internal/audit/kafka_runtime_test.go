package audit

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/accesslog"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/decisionlog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

func TestKafkaRuntimeWithoutConfigKeepsBusinessFailOpen(t *testing.T) {
	runtime := NewKafkaRuntimeFromEnv(func(string) string { return "" })
	if runtime == nil || runtime.Recorder == nil {
		t.Fatal("missing configuration must still provide a fail-open recorder")
	}
	if runtime.Publisher() != nil {
		t.Fatal("unavailable publisher must be a nil interface for access and decision recorders")
	}
	if err := runtime.Recorder.Record(context.Background(), Entry{
		RequestID: "req-safe-unconfigured", Method: "POST", Resource: "users", Action: "create", Status: 400,
	}); err == nil {
		t.Fatal("unconfigured publisher was not reported as a coverage gap")
	}
	runtime.Close()
}

func TestUnavailableKafkaKeepsAccessAndDecisionRecordersFailOpen(t *testing.T) {
	runtime := NewKafkaRuntimeFromEnv(func(key string) string {
		if key == "BKN_AUDIT_ENVIRONMENT" {
			return "production"
		}
		return ""
	})
	defer runtime.Close()
	access := accesslog.NewKafkaRecorder(runtime.Publisher(), "production")
	if err := access.Record(context.Background(), accesslog.Entry{Action: "login", Outcome: "success"}); err == nil {
		t.Fatal("unavailable Kafka must report the access audit gap")
	}
	decision := decisionlog.NewKafkaRecorder(runtime.Publisher(), "production", nil)
	decision.Record(decisionlog.Entry{ResourceType: "safe_admin", ResourceID: "console", Decision: decisionlog.DecisionDeny})
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
