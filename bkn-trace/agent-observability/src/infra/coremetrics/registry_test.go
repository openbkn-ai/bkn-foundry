package coremetrics_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/coremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
)

func TestRegistryExportsFrozenCoreMetricNames(t *testing.T) {
	t.Parallel()

	registry := coremetrics.New()
	registry.Increment(icoremetrics.ConversationsTotal)
	registry.Set(icoremetrics.ProjectionLagSeconds, 2.5)
	response := httptest.NewRecorder()
	registry.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))

	body := response.Body.String()
	for _, expected := range []string{
		"bkn_trace_conversations_total 1",
		"bkn_trace_projection_lag_seconds 2.5",
		"bkn_trace_assembly_lag_seconds 0",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics response is missing %q:\n%s", expected, body)
		}
	}
}
