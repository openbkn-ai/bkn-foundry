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
	calls  int
}

func (client *fakeClient) Search(_ context.Context, _ string, body []byte) ([]byte, error) {
	client.calls++
	client.body = append([]byte(nil), body...)
	return client.result, nil
}

func TestSearchUsesExactReceiptFiltersScopeAndCursor(t *testing.T) {
	terminalAt := time.Date(2026, 8, 20, 12, 1, 0, 0, time.UTC)
	client := &fakeClient{result: searchPayload(t, "gte", terminalAt, []any{"2026-08-20T12:01:00Z", "receipt"})}
	position := &observabilityvo.SourcePosition{SearchAfter: []any{"2026-08-20T12:02:00Z", "previous"}}
	page, err := New(client, "projection").Search(context.Background(), observabilityvo.LogQuery{
		AuthorizedTenantID: "tenant", AuthorizedBusinessDomain: "domain",
		AuthorizedSubjectID: "user", AuthorizedApplicationID: "app",
		AuthorizedKnowledgeNetworkIDs: []string{"kn-a"}, RequireRecordScope: true,
		TraceID: "trace-a", RequestID: "request-a", ConversationID: "conversation-a",
		InteractionID: "interaction-a", OperationID: "operation-a", Limit: 25, PageBefore: position,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if client.calls != 1 || len(page.Records) != 1 {
		t.Fatalf("calls=%d page=%+v", client.calls, page)
	}
	query := decodeBody(t, client.body)
	if query["size"] != float64(25) {
		t.Fatalf("size=%v", query["size"])
	}
	if got := query["search_after"].([]any); len(got) != 2 || got[1] != "previous" {
		t.Fatalf("search_after=%v", got)
	}
	rendered := string(client.body)
	for _, expected := range []string{
		`"receipt_status.keyword":["completed","failed"]`,
		`"trace_id.keyword":"trace-a"`, `"request_id.keyword":"request-a"`,
		`"conversation_id.keyword":"conversation-a"`, `"interaction_id.keyword":"interaction-a"`,
		`"operation_id.keyword":"operation-a"`, `"knowledge_network_ids.keyword":["kn-a"]`,
		`"terminal_at":{"order":"desc"}`, `"receipt_id.keyword":{"order":"asc"}`,
	} {
		if !containsText(rendered, expected) {
			t.Errorf("query missing %s: %s", expected, rendered)
		}
	}
	record := page.Records[0]
	if page.CountAccuracy != "partial" || record.CursorPosition == nil || len(record.CursorPosition.SearchAfter) != 2 {
		t.Fatalf("count/cursor not projected: %+v", page)
	}
	if record.EventTimestamp != terminalAt || record.EventTime != terminalAt || record.RecordedAt != terminalAt {
		t.Fatalf("terminal timestamp not used: %+v", record)
	}
	if record.BusinessModule != "domain_knowledge_network" || record.Action != "execute" ||
		record.TargetType != "operation" || record.TargetID != "operation-a" || record.TargetNameSnapshot != "run_sql" ||
		record.ActorNameSnapshot != "user" || record.ActorType != "service_account" || record.AuthMethod == "" || record.SourceChannel != "api" {
		t.Fatalf("operation audit projection incomplete: %+v", record)
	}
}

func TestSearchRejectsUnsupportedOrIncompatibleFiltersWithoutQuerying(t *testing.T) {
	for name, query := range map[string]observabilityvo.LogQuery{
		"span":                  {SpanID: "span-a"},
		"module":                {BusinessModule: "model_management"},
		"event":                 {EventNames: []string{"conversation.created"}},
		"conflicting operation": {OperationID: "operation-a", TargetID: "operation-b"},
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{}
			query.AuthorizedTenantID, query.AuthorizedBusinessDomain = "tenant", "domain"
			page, err := New(client, "projection").Search(context.Background(), query)
			if err != nil || client.calls != 0 || page.CountAccuracy != "exact" {
				t.Fatalf("page=%+v calls=%d err=%v", page, client.calls, err)
			}
		})
	}
}

func TestSearchMarksCountPartialWhenFreeTextNeedsLocalFiltering(t *testing.T) {
	terminalAt := time.Date(2026, 8, 20, 12, 1, 0, 0, time.UTC)
	client := &fakeClient{result: searchPayload(t, "eq", terminalAt, nil)}
	page, err := New(client, "projection").Search(context.Background(), observabilityvo.LogQuery{
		AuthorizedTenantID: "tenant", AuthorizedBusinessDomain: "domain", Query: "run_sql",
	})
	if err != nil || page.CountAccuracy != "partial" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestGetUsesScopedReceiptLookup(t *testing.T) {
	terminalAt := time.Date(2026, 8, 20, 12, 1, 0, 0, time.UTC)
	client := &fakeClient{result: searchPayload(t, "eq", terminalAt, nil)}
	ctx := observabilityvo.WithSourceAccessScope(context.Background(), observabilityvo.SourceAccessScope{
		TenantID: "tenant", BusinessDomain: "domain",
	})
	record, found, err := New(client, "projection").Get(ctx, "bkn-trace-runtime:receipt")
	if err != nil || !found || record.SourceLogID != "receipt" {
		t.Fatalf("record=%+v found=%v err=%v", record, found, err)
	}
	for _, expected := range []string{`"receipt_id.keyword":"receipt"`, `"owner.tenant_id.keyword":"tenant"`, `"owner.business_domain_id.keyword":"domain"`} {
		if !containsText(string(client.body), expected) {
			t.Errorf("detail query missing %s: %s", expected, client.body)
		}
	}

	if _, found, err := New(client, "projection").Get(ctx, "other:receipt"); err != nil || found || client.calls != 1 {
		t.Fatalf("foreign id should not query: found=%v calls=%d err=%v", found, client.calls, err)
	}
}

func searchPayload(t *testing.T, relation string, terminalAt time.Time, sortValues []any) []byte {
	t.Helper()
	hit := map[string]any{"_source": map[string]any{
		"owner":        map[string]any{"tenant_id": "tenant", "business_domain_id": "domain", "effective_subject_type": "service", "effective_subject_id": "user", "application_principal_id": "app"},
		"operation_id": "operation-a", "attempt": 1, "receipt_id": "receipt", "conversation_id": "conversation-a",
		"interaction_id": "interaction-a", "request_id": "request-a", "trace_id": "trace-a", "tool_name": "run_sql",
		"receipt_status": "completed", "issued_at": terminalAt.Add(-time.Minute).Format(time.RFC3339Nano), "terminal_at": terminalAt.Format(time.RFC3339Nano),
	}}
	if len(sortValues) > 0 {
		hit["sort"] = sortValues
	}
	payload, err := json.Marshal(map[string]any{"hits": map[string]any{
		"total": map[string]any{"value": 1, "relation": relation}, "hits": []any{hit},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func containsText(value, expected string) bool {
	for index := 0; index+len(expected) <= len(value); index++ {
		if value[index:index+len(expected)] == expected {
			return true
		}
	}
	return false
}
