package audit

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublishTelemetryExposesOnlyBoundedOutcomeCounters(t *testing.T) {
	telemetry := NewPublishTelemetry()
	telemetry.Observe("accepted")
	telemetry.Observe("delivered")
	telemetry.Observe("dropped_queue_full")
	recorder := httptest.NewRecorder()
	telemetry.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	for _, want := range []string{
		`audit_event_publish_total{source_id="bkn-safe-admin",result="accepted",reason="none"} 1`,
		`audit_event_publish_total{source_id="bkn-safe-admin",result="delivered",reason="none"} 1`,
		`audit_event_publish_total{source_id="bkn-safe-admin",result="dropped",reason="queue_full"} 1`,
	} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("missing counter %q in %q", want, recorder.Body.String())
		}
	}
	if strings.Contains(recorder.Body.String(), "event_id") {
		t.Fatal("metrics must not retain Audit payload fields")
	}
}

func TestPublishTelemetrySeparatesAccessSourceWithoutPayloadFields(t *testing.T) {
	telemetry := NewPublishTelemetry()
	telemetry.ObserveForSource("bkn-safe-access", "accepted")
	recorder := httptest.NewRecorder()
	telemetry.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(recorder.Body.String(), `audit_event_publish_total{source_id="bkn-safe-access",result="accepted",reason="none"} 1`) {
		t.Fatalf("missing access counter in %q", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "request_id") || strings.Contains(recorder.Body.String(), "actor_id") {
		t.Fatal("metrics must not retain payload fields")
	}
}
