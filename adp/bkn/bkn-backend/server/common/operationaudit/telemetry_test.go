package operationaudit

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublishTelemetryCountsEveryDropButLimitsRepeatedLogs(t *testing.T) {
	telemetry := NewPublishTelemetry()
	start := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	if count, log := telemetry.Observe("dropped_queue_full", start); count != 1 || !log {
		t.Fatalf("first drop = (%d, %t)", count, log)
	}
	if count, log := telemetry.Observe("dropped_queue_full", start.Add(time.Second)); count != 2 || log {
		t.Fatalf("repeated drop = (%d, %t)", count, log)
	}
	if count, log := telemetry.Observe("dropped_queue_full", start.Add(time.Minute)); count != 3 || !log {
		t.Fatalf("next window = (%d, %t)", count, log)
	}
	response := httptest.NewRecorder()
	telemetry.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `audit_event_publish_total{source_id="bkn-backend",result="dropped",reason="queue_full"} 3`) {
		t.Fatalf("metrics = %d %q", response.Code, response.Body.String())
	}
}
