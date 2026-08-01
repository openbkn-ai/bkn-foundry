package opensearchlogaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type fakeSearchClient struct {
	query    []byte
	response []byte
}

func (client *fakeSearchClient) Search(_ context.Context, _ string, query []byte) ([]byte, error) {
	client.query = append([]byte(nil), query...)
	return client.response, nil
}

func TestSearchPushesTrustedScopeAndMapsSS4ODocuments(t *testing.T) {
	backend := &fakeSearchClient{response: []byte(`{
		"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"source-log-a","_source":{
				"attributes":{"log_id":"context-loader:source-log-a","source_id":"context-loader","source_log_id":"source-log-a","tenant_id":"tenant-a","business_domain_id":"domain-a","log_category":"runtime.business","event_name":"knowledge.read.completed","effective_subject_id":"builder-a","request_id":"req-a","conversation_id":"conversation-a","interaction_id":"interaction-a","operation_id":"operation-a","knowledge_network_ids":["kn-a"],"ingress_principal":"otel-gateway","trust_level":"trusted"},
			"body":"读取需求预测对象","observedTimestamp":"2026-08-01T11:35:47Z","@timestamp":"2026-08-01T11:35:46Z",
			"resource":{"service.name":"context-loader","deployment.environment":"production"},
			"severity":{"text":"INFO","number":9},"traceId":"trace-a","spanId":"span-a"
			},"sort":["2026-08-01T11:35:46.123456Z","source-log-a"]}]}}`)}
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
	for _, expected := range []string{"attributes.log_id", "attributes.source_log_id"} {
		if !hasExistsFilter(query, expected) {
			t.Fatalf("required R6.2 field %s was not enforced: %s", expected, encoded)
		}
	}
	if len(page.Records) != 1 {
		t.Fatalf("expected one mapped log, got %+v", page)
	}
	record := page.Records[0]
	if record.LogID != "context-loader:source-log-a" || record.SourceID != "context-loader" || record.SourceLogID != "source-log-a" || record.TenantID != "tenant-a" || record.Category != observabilityvo.CategoryRuntimeBusiness ||
		record.ServiceName != "context-loader" || record.TraceID != "trace-a" || len(record.KnowledgeNetworkIDs) != 1 {
		t.Fatalf("unexpected SS4O projection: %+v", record)
	}
	if record.CursorPosition == nil || len(record.CursorPosition.SearchAfter) != 2 || record.CursorPosition.SearchAfter[0] != "2026-08-01T11:35:46.123456Z" {
		t.Fatalf("OpenSearch sort values were not preserved: %+v", record.CursorPosition)
	}
}

func TestSearchReplaysNativeSearchAfterValues(t *testing.T) {
	backend := &fakeSearchClient{response: []byte(`{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`)}
	client := New(backend, "logs")
	positionTime := time.Date(2026, 8, 1, 10, 0, 0, 123456789, time.UTC)
	_, err := client.Search(context.Background(), observabilityvo.LogQuery{
		AuthorizedTenantID: "tenant-a", AuthorizedCategories: []string{observabilityvo.CategoryRuntimeSystem},
		PageBefore: &observabilityvo.SourcePosition{
			EventTimestamp: positionTime, LogID: "source-log-a",
			SearchAfter: []any{"2026-08-01T10:00:00.123456Z", "source-log-a"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var query map[string]any
	if err := json.Unmarshal(backend.query, &query); err != nil {
		t.Fatal(err)
	}
	searchAfter, _ := query["search_after"].([]any)
	if len(searchAfter) != 2 || searchAfter[0] != "2026-08-01T10:00:00.123456Z" || searchAfter[1] != "source-log-a" {
		t.Fatalf("native search_after was reconstructed instead of replayed: %#v", searchAfter)
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
	for _, expected := range []string{"minimum_should_match", "attributes.effective_subject_id.keyword", "attributes.application_id.keyword", "attributes.source_log_id.keyword", "search_after", "log-20"} {
		if !containsBytes(backend.query, expected) {
			t.Fatalf("missing scoped keyset element %q: %s", expected, backend.query)
		}
	}
}

func TestListLogIDCanRoundTripThroughGet(t *testing.T) {
	backend := &fakeSearchClient{response: []byte(`{
		"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"source-log-a","_source":{
			"attributes":{"schema_version":"1.0.0","log_id":"context-loader:source-log-a","source_id":"context-loader","source_log_id":"source-log-a","tenant_id":"tenant-a","business_domain_id":"domain-a","log_category":"runtime.business","event_name":"knowledge.read.completed","outcome":"success","safe_summary":"读取需求预测对象","trust_level":"trusted"},
			"observedTimestamp":"2026-08-01T11:35:47Z","@timestamp":"2026-08-01T11:35:46Z",
			"resource":{"service.name":"context-loader","deployment.environment":"production"},
			"severity":{"text":"INFO","number":9}
		}}]}}`)}
	client := New(backend, "ss4o_logs-default-namespace")
	page, err := client.Search(context.Background(), observabilityvo.LogQuery{
		AuthorizedTenantID: "tenant-a", AuthorizedBusinessDomain: "domain-a",
		AuthorizedCategories: []string{observabilityvo.CategoryRuntimeBusiness},
	})
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("list log: records=%d err=%v", len(page.Records), err)
	}
	listedLogID := page.Records[0].LogID
	ctx := observabilityvo.WithSourceAccessScope(context.Background(), observabilityvo.SourceAccessScope{
		TenantID: "tenant-a", BusinessDomain: "domain-a",
	})
	record, found, err := client.Get(ctx, listedLogID)
	if err != nil || !found {
		t.Fatalf("get log: found=%v err=%v", found, err)
	}
	if record.LogID != listedLogID || record.SourceLogID != "source-log-a" {
		t.Fatalf("list log_id did not round-trip through detail: listed=%q record=%+v", listedLogID, record)
	}
	for _, expected := range []string{"attributes.log_id.keyword", listedLogID, "attributes.tenant_id.keyword", "tenant-a", "attributes.business_domain_id.keyword", "domain-a"} {
		if !containsBytes(backend.query, expected) {
			t.Fatalf("detail query is missing %q: %s", expected, backend.query)
		}
	}
}

func TestGetFailsClosedWithoutTrustedTenantScope(t *testing.T) {
	backend := &fakeSearchClient{}
	client := New(backend, "ss4o_logs-default-namespace")
	_, found, err := client.Get(context.Background(), "context-loader:source-log-a")
	if err == nil || found || len(backend.query) != 0 {
		t.Fatalf("detail lookup must fail before querying without trusted tenant scope: found=%v err=%v query=%s", found, err, backend.query)
	}
}

func hasExistsFilter(query map[string]any, expected string) bool {
	queryObject, _ := query["query"].(map[string]any)
	boolObject, _ := queryObject["bool"].(map[string]any)
	filters, _ := boolObject["filter"].([]any)
	for _, item := range filters {
		filter, _ := item.(map[string]any)
		exists, _ := filter["exists"].(map[string]any)
		if exists["field"] == expected {
			return true
		}
	}
	return false
}

func containsBytes(payload []byte, value string) bool {
	for i := 0; i+len(value) <= len(payload); i++ {
		if string(payload[i:i+len(value)]) == value {
			return true
		}
	}
	return false
}
