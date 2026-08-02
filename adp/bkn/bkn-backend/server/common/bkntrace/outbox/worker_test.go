package outbox

import (
	"net/http"
	"testing"
)

func TestCoreDeliveryOutcome(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantStatus string
		wantCode   string
		wantRetry  bool
	}{
		{name: "success", statusCode: http.StatusAccepted, wantStatus: StatusDelivered, wantRetry: false},
		{name: "conflict", statusCode: http.StatusConflict, wantStatus: StatusConflict, wantCode: "producer_sequence_conflict"},
		{name: "server error", statusCode: http.StatusServiceUnavailable, wantStatus: StatusRetry, wantCode: "core_unavailable", wantRetry: true},
		{name: "auth mismatch", statusCode: http.StatusUnauthorized, wantStatus: StatusRetry, wantCode: "core_unavailable", wantRetry: true},
		{name: "not found", statusCode: http.StatusNotFound, wantStatus: StatusRetry, wantCode: "core_unavailable", wantRetry: true},
		{name: "semantic rejection", statusCode: http.StatusBadRequest, wantStatus: StatusDLQ, wantCode: "core_rejected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, code, retry := coreDeliveryOutcome(tt.statusCode)
			if status != tt.wantStatus || code != tt.wantCode || retry != tt.wantRetry {
				t.Fatalf("coreDeliveryOutcome(%d) = (%q, %q, %t), want (%q, %q, %t)", tt.statusCode, status, code, retry, tt.wantStatus, tt.wantCode, tt.wantRetry)
			}
		})
	}
}
