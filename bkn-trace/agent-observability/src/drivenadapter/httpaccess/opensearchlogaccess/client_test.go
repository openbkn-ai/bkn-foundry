package opensearchlogaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

type fakeSearchClient struct {
	query    []byte
	response []byte
	document opensearch.Document
}

func (client *fakeSearchClient) Search(_ context.Context, _ string, query []byte) ([]byte, error) {
	client.query = append([]byte(nil), query...)
	return client.response, nil
}

func (client *fakeSearchClient) GetDocument(context.Context, string, string) (opensearch.Document, error) {
	return client.document, nil
}

func TestSearchPushesTrustedScopeAndMapsSS4ODocuments(t *testing.T) {
	backend := &fakeSearchClient{response: []byte(`{
		"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"source-log-a","_source":{
			"attributes":{"source_id":"context-loader","tenant_id":"tenant-a","business_domain_id":"domain-a","log_category":"runtime.business","event_name":"knowledge.read.completed","effective_subject_id":"builder-a","request_id":"req-a","conversation_id":"conversation-a","interaction_id":"interaction-a","operation_id":"operation-a","knowledge_network_ids":["kn-a"],"ingress_principal":"otel-gateway","trust_level":"trusted"},
			"body":"读取需求预测对象","observedTimestamp":"2026-08-01T11:35:47Z","@timestamp":"2026-08-01T11:35:46Z",
			"resource":{"service.name":"context-loader","deployment.environment":"production"},
			"severity":{"text":"INFO","number":9},"traceId":"trace-a","spanId":"span-a"
		}}]}}`)}
	client := New(backend, "ss4o_logs-default-namespace")
	page, err := client.Search(context.Background(), observabilityvo.LogQuery{
		TraceID: "trace-a", Limit: 20,
		AuthorizedTenantID: "tenant-a", AuthorizedBusinessDomain: "domain-a",
		AuthorizedSubjectID:           "builder-a",
		AuthorizedCategories:          []string{observabilityvo.CategoryRuntimeBusiness},
		AuthorizedKnowledgeNetworkIDs: []string{"kn-a"},
		RequireRecordScope:            true,
	})
	if err != nil {
		t.Fatalf("search logs: %v", err)
	}
	var query map[string]any
	if err := json.Unmarshal(backend.query, &query); err != nil {
		t.Fatalf("decode native query: %v", err)
	}
	encoded, _ := json.Marshal(query)
	for _, expected := range []string{"attributes.tenant_id.keyword", "attributes.business_domain_id.keyword", "attributes.log_category.keyword", "attributes.knowledge_network_ids.keyword", "traceId.keyword"} {
		if !containsBytes(encoded, expected) {
			t.Fatalf("trusted filter %s was not pushed down: %s", expected, encoded)
		}
	}
	if len(page.Records) != 1 {
		t.Fatalf("expected one mapped log, got %+v", page)
	}
	record := page.Records[0]
	if record.LogID != "source-log-a" || record.SourceID != "context-loader" || record.TenantID != "tenant-a" || record.Category != observabilityvo.CategoryRuntimeBusiness ||
		record.ServiceName != "context-loader" || record.TraceID != "trace-a" || len(record.KnowledgeNetworkIDs) != 1 {
		t.Fatalf("unexpected SS4O projection: %+v", record)
	}
}

func TestSearchUsesCandidateScopeInsteadOfRequiringEveryManagedNetwork(t *testing.T) {
	backend := &fakeSearchClient{response: []byte(`{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`)}
	positionTime := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	client := New(backend, "logs")
	_, err := client.Search(context.Background(), observabilityvo.LogQuery{
		AuthorizedTenantID: "tenant-a", AuthorizedBusinessDomain: "domain-a",
		AuthorizedSubjectID: "builder-a", AuthorizedApplicationID: "app-a",
		AuthorizedCategories:          []string{observabilityvo.CategoryRuntimeBusiness},
		AuthorizedKnowledgeNetworkIDs: []string{"kn-a", "kn-b"}, RequireRecordScope: true,
		PageBefore: &observabilityvo.SourcePosition{EventTimestamp: positionTime, LogID: "log-20"},
	})
	if err != nil {
		t.Fatalf("search logs: %v", err)
	}
	if bytes.Count(backend.query, []byte("attributes.knowledge_network_ids.keyword")) != 1 {
		t.Fatalf("managed networks must be one candidate terms query, not all-of terms: %s", backend.query)
	}
	for _, expected := range []string{"minimum_should_match", "attributes.effective_subject_id.keyword", "attributes.application_id.keyword", "search_after", "log-20"} {
		if !containsBytes(backend.query, expected) {
			t.Fatalf("missing scoped keyset element %q: %s", expected, backend.query)
		}
	}
}

func TestGetMapsDocumentAndPreservesMissingTrustedScopeAsEmpty(t *testing.T) {
	backend := &fakeSearchClient{document: opensearch.Document{Source: []byte(`{
		"attributes":{"service.name":"bkn-backend"},"body":"query_hash=sha256:abc",
		"observedTimestamp":"2026-08-01T11:35:47Z","@timestamp":"2026-08-01T11:35:46Z",
		"resource":{"service.name":"bkn-backend"},"severity":{"text":"INFO","number":9},"traceId":"trace-a"
	}`)}}
	client := New(backend, "ss4o_logs-default-namespace")
	record, found, err := client.Get(context.Background(), "source-log-a")
	if err != nil || !found {
		t.Fatalf("get log: found=%v err=%v", found, err)
	}
	if record.LogID != "source-log-a" || record.TenantID != "" || record.Category != "" {
		t.Fatalf("adapter must not invent trusted scope for legacy logs: %+v", record)
	}
}

func containsBytes(payload []byte, value string) bool {
	for i := 0; i+len(value) <= len(payload); i++ {
		if string(payload[i:i+len(value)]) == value {
			return true
		}
	}
	return false
}
