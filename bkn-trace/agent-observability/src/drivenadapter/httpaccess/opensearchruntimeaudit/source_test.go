package opensearchruntimeaudit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type fakeClient struct {
	body   []byte
	result []byte
}

func (c *fakeClient) Search(_ context.Context, _ string, body []byte) ([]byte, error) {
	c.body = body
	return c.result, nil
}

func TestSearchPushesTraceFilterAndProjectsRuntimeOperation(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(map[string]any{"hits": map[string]any{"total": map[string]any{"value": 1, "relation": "eq"}, "hits": []any{map[string]any{"_source": map[string]any{"owner": map[string]any{"tenant_id": "tenant", "business_domain_id": "domain", "effective_subject_id": "user", "application_principal_id": "app"}, "operation_id": "op", "attempt": 1, "receipt_id": "receipt", "conversation_id": "conversation", "interaction_id": "interaction", "request_id": "request", "trace_id": "0123456789abcdef0123456789abcdef", "tool_name": "run_sql", "receipt_status": "completed", "issued_at": now.Format(time.RFC3339Nano)}}}}})
	client := &fakeClient{result: payload}
	page, err := New(client, "projection").Search(context.Background(), observabilityvo.LogQuery{AuthorizedTenantID: "tenant", AuthorizedBusinessDomain: "domain", TraceID: "0123456789abcdef0123456789abcdef"})
	if err != nil || len(page.Records) != 1 || page.Records[0].OperationID != "op" || page.Records[0].TraceID == "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if !contains(string(client.body), `"trace_id":"0123456789abcdef0123456789abcdef"`) {
		t.Fatalf("trace filter missing: %s", client.body)
	}
}
func contains(value, expected string) bool {
	for i := 0; i+len(expected) <= len(value); i++ {
		if value[i:i+len(expected)] == expected {
			return true
		}
	}
	return false
}
