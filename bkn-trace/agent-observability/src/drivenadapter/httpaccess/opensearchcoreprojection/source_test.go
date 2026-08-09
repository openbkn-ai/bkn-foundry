package opensearchcoreprojection_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

func TestSourceBuildsAuthorizedExecutionProjectionFromCoreReceiptsAndArtifacts(t *testing.T) {
	t.Parallel()

	searches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bkn-trace-core/_search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		searches++
		body, _ := io.ReadAll(r.Body)
		if !json.Valid(body) {
			t.Fatalf("invalid search body: %s", body)
		}
		queryBody := string(body)
		if strings.Contains(queryBody, "match_phrase") {
			t.Fatalf("authorization filters must not use analyzed phrase matching: %s", body)
		}
		if searches == 1 {
			for _, field := range []string{
				"owner.tenant_id.keyword", "owner.business_domain_id.keyword",
				"request_id.keyword", "trace_id.keyword", "interaction_id.keyword",
			} {
				if !strings.Contains(queryBody, field) {
					t.Fatalf("identifier filter %q must use exact keyword matching: %s", field, body)
				}
			}
			for _, field := range []string{
				"owner.tenant_id", "owner.business_domain_id",
				"request_id", "trace_id", "interaction_id",
			} {
				if strings.Contains(queryBody, `"`+field+`":{"value"`) {
					t.Fatalf("identifier filter %q must not query the analyzed text field: %s", field, body)
				}
			}
		}
		_, _ = io.WriteString(w, `{
			"hits":{"hits":[{"_source":{
				"receipt_id":"rcpt-1","schema_version":"3.0.0",
				"owner":{"tenant_id":"tenant-1","business_domain_id":"domain-1","application_principal_id":"openbkn-sdk","effective_subject_type":"user","effective_subject_id":"user-1"},
				"conversation_id":"conv-1","interaction_id":"int-1","operation_id":"op-1",
				"operation_key":"forecast-query","tool_name":"run_sql","receipt_status":"completed","evidence_durability":"durable",
				"request_id":"req-1","trace_id":"11111111111111111111111111111111",
				"business_refs":[
					{"ref_type":"knowledge_network","ref_id":"kn:supplychain_hd0202","business_domain_id":"domain-1","version":"v3"},
					{"ref_type":"object_type","ref_id":"object:supplychain_hd0202:forecast","business_domain_id":"domain-1","version":"v3"}
				],
				"knowledge_network_ids":["supplychain_hd0202"],
				"artifact_refs":[],"partial_reasons":[],
				"issued_at":"2026-08-02T07:35:26Z","terminal_at":"2026-08-02T07:35:27Z"
			}}, {"_source":{
				"receipt_id":"rcpt-2","schema_version":"3.0.0",
				"owner":{"tenant_id":"tenant-1","business_domain_id":"domain-1","application_principal_id":"openbkn-sdk","effective_subject_type":"user","effective_subject_id":"user-1"},
				"conversation_id":"conv-1","interaction_id":"int-1","operation_id":"op-2",
				"operation_key":"data-query","tool_name":"run_sql","receipt_status":"completed","evidence_durability":"durable",
				"request_id":"req-2","trace_id":"44444444444444444444444444444444",
				"business_refs":[{"ref_type":"data_resource","ref_id":"resource:forecast","business_domain_id":"domain-1","version":"v3"}],
				"artifact_refs":[],"partial_reasons":[],
				"issued_at":"2026-08-02T07:35:27Z","terminal_at":"2026-08-02T07:35:28Z"
			}}]}}
		`)
	}))
	t.Cleanup(server.Close)

	artifacts := artifactProjectionSource{result: iprojectionsource.Result{Artifacts: []evidencevo.EvidenceArtifact{
		{
			ArtifactID: "question-1", ArtifactType: evidencevo.ArtifactTypeQuestion,
			RequestID: "req-lifecycle-start", TraceID: "22222222222222222222222222222222", InteractionID: "int-1",
			Content: "6月份有哪些需求预测单？", ObservedAt: "2026-08-02T07:35:25Z",
			TenantID: "tenant-1", BusinessDomain: "domain-1", AccountID: "user-1", AccountType: "user",
		},
		{
			ArtifactID: "answer-1", ArtifactType: evidencevo.ArtifactTypeResult,
			RequestID: "req-lifecycle-complete", TraceID: "33333333333333333333333333333333", InteractionID: "int-1",
			Content: map[string]any{"summary": "6月份共有63条，需求总量11594。"}, ObservedAt: "2026-08-02T07:35:28Z",
			TenantID: "tenant-1", BusinessDomain: "domain-1", AccountID: "user-1", AccountType: "user",
		},
	}}}
	source := opensearchcoreprojection.New(
		opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second),
		"bkn-trace-core",
		artifacts,
	)

	result, err := source.LoadExecutionProjection(context.Background(), iprojectionsource.Query{
		RequestID:     "req-1",
		TraceID:       "11111111111111111111111111111111",
		InteractionID: "int-1",
		Scope: evidencevo.QueryScope{
			TenantID: "tenant-1", BusinessDomain: "domain-1", AccountID: "user-1", AccountType: "user",
			AccessProfile: &evidencevo.AccessProfile{
				TenantID: "tenant-1", BusinessDomain: "domain-1", EffectiveSubjectID: "user-1",
				ApplicationPrincipalID: "openbkn-studio", AccountActive: true, TenantActive: true,
			},
		},
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("load projection: %v", err)
	}
	if len(result.Traces) != 1 {
		t.Fatalf("expected the requested trace only, got %#v", result.Traces)
	}
	for _, trace := range result.Traces {
		if trace.ConversationID != "conv-1" || len(trace.Events) != 3 {
			t.Fatalf("the requested call must inherit the interaction question/result: %#v", trace)
		}
	}
	trace := result.Traces[0]
	receiptEvent := trace.Events[0]
	if receiptEvent.OperationID != "op-1" || receiptEvent.OperationName != "run_sql" ||
		receiptEvent.Payload["operation_key"] != "forecast-query" {
		t.Fatalf("receipt operation identity was not preserved: %#v", receiptEvent)
	}
	if len(trace.KnowledgeNetworkIDs) != 1 || trace.KnowledgeNetworkIDs[0] != "supplychain_hd0202" {
		t.Fatalf("knowledge network was not derived: %#v", trace.KnowledgeNetworkIDs)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("authorized artifacts were not preserved: %#v", result.Artifacts)
	}
	requests, _ := evidencevo.BuildExecutionSummaries(result.Traces, result.Artifacts)
	if len(requests) != 1 {
		t.Fatalf("expected one request summary, got %#v", requests)
	}
	for _, request := range requests {
		if request.DurationMS != 1000 {
			t.Fatalf("interaction context artifacts must not widen operation timing: %#v", requests)
		}
	}
}

func TestRequestProjectionHydratesArtifactsByReceiptInteraction(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"hits":{"hits":[{"_source":{
			"receipt_id":"rcpt-request","schema_version":"3.0.0",
			"owner":{"tenant_id":"tenant-1","business_domain_id":"domain-1","application_principal_id":"openbkn-sdk","effective_subject_type":"user","effective_subject_id":"user-1"},
			"conversation_id":"conv-1","interaction_id":"int-1","operation_id":"op-1",
			"tool_name":"search_schema","receipt_status":"completed","evidence_durability":"durable",
			"request_id":"req-1","trace_id":"11111111111111111111111111111111",
			"issued_at":"2026-08-02T07:35:26Z","terminal_at":"2026-08-02T07:35:27Z"
		}}]}}`)
	}))
	t.Cleanup(server.Close)

	artifacts := &recordingArtifactProjectionSource{result: iprojectionsource.Result{Artifacts: []evidencevo.EvidenceArtifact{
		{
			ArtifactID: "question-1", ArtifactType: evidencevo.ArtifactTypeQuestion,
			RequestID: "req-lifecycle", TraceID: "22222222222222222222222222222222", InteractionID: "int-1",
			Content: "6月份有哪些需求预测单？", ObservedAt: "2026-08-02T07:35:25Z",
			TenantID: "tenant-1", BusinessDomain: "domain-1", AccountID: "user-1", AccountType: "user",
		},
		{
			ArtifactID: "other-operation-data", ArtifactType: evidencevo.ArtifactTypeDataResult,
			RequestID: "req-2", TraceID: "22222222222222222222222222222223", InteractionID: "int-1",
			OperationID: "op-2", Content: map[string]any{"total": 11594}, ObservedAt: "2026-08-02T07:35:27Z",
			TenantID: "tenant-1", BusinessDomain: "domain-1", AccountID: "user-1", AccountType: "user",
		},
	}}}
	source := opensearchcoreprojection.New(
		opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "bkn-trace-core", artifacts,
	)

	result, err := source.LoadExecutionProjection(context.Background(), iprojectionsource.Query{
		RequestID: "req-1", Scope: evidencevo.QueryScope{
			TenantID: "tenant-1", BusinessDomain: "domain-1", AccountID: "user-1", AccountType: "user",
		},
	})
	if err != nil {
		t.Fatalf("load request projection: %v", err)
	}
	if len(artifacts.queries) != 1 || len(artifacts.queries[0].AuthorizedInteractionIDs) != 1 ||
		artifacts.queries[0].AuthorizedInteractionIDs[0] != "int-1" || artifacts.queries[0].RequestID != "" {
		t.Fatalf("request artifacts must be hydrated from receipt interaction: %+v", artifacts.queries)
	}
	if len(result.Traces) != 1 || len(result.Traces[0].Events) != 2 || len(result.Artifacts) != 1 {
		t.Fatalf("only interaction-scoped artifacts may be projected onto another request: traces=%+v artifacts=%+v", result.Traces, result.Artifacts)
	}
}

func TestSourceProjectsQueryArtifactAsDataQueryReference(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"hits":{"hits":[{"_source":{
			"receipt_id":"rcpt-query","schema_version":"3.0.0",
			"owner":{"tenant_id":"tenant-1","business_domain_id":"domain-1","effective_subject_type":"user","effective_subject_id":"user-1"},
			"conversation_id":"conv-1","interaction_id":"int-1","operation_id":"op-query",
			"tool_name":"run_sql","receipt_status":"completed","evidence_durability":"durable",
			"request_id":"req-query","trace_id":"11111111111111111111111111111111",
			"issued_at":"2026-08-02T07:35:26Z","terminal_at":"2026-08-02T07:35:27Z"
		}}]}}`)
	}))
	t.Cleanup(server.Close)

	source := opensearchcoreprojection.New(
		opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "bkn-trace-core",
		artifactProjectionSource{result: iprojectionsource.Result{Artifacts: []evidencevo.EvidenceArtifact{{
			ArtifactID: "query-1", ArtifactType: evidencevo.ArtifactTypeQuery,
			RequestID: "req-query", TraceID: "11111111111111111111111111111111",
			InteractionID: "int-1", OperationID: "op-query", Content: "SELECT * FROM inventory",
			ObservedAt: "2026-08-02T07:35:26Z", TenantID: "tenant-1", BusinessDomain: "domain-1",
			AccountID: "user-1", AccountType: "user",
		}}}},
	)

	result, err := source.LoadExecutionProjection(context.Background(), iprojectionsource.Query{
		RequestID: "req-query", Scope: evidencevo.QueryScope{
			TenantID: "tenant-1", BusinessDomain: "domain-1", AccountID: "user-1", AccountType: "user",
		},
	})
	if err != nil {
		t.Fatalf("load projection: %v", err)
	}
	if len(result.Traces) != 1 {
		t.Fatalf("expected one trace, got %#v", result.Traces)
	}
	for _, event := range result.Traces[0].Events {
		if event.EventType == "data.query.observed" && event.OperationID == "op-query" &&
			event.Payload["query_artifact_ref"] == "artifact:query-1" {
			return
		}
	}
	t.Fatalf("query artifact must be linked as data.query.observed.query_artifact_ref: %#v", result.Traces[0].Events)
}

func TestSourceUsesManagedKnowledgeNetworksAsBusinessCandidates(t *testing.T) {
	t.Parallel()

	searches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searches++
		body, _ := io.ReadAll(r.Body)
		if searches == 1 && (!strings.Contains(string(body), `"knowledge_network_ids.keyword"`) ||
			!strings.Contains(string(body), `"kn-managed"`)) {
			t.Fatalf("managed KN candidate filter missing: %s", body)
		}
		_, _ = io.WriteString(w, `{"hits":{"hits":[{"_source":{
			"receipt_id":"rcpt-managed","schema_version":"3.0.0",
			"owner":{"tenant_id":"tenant-1","business_domain_id":"domain-1","application_principal_id":"other-app","effective_subject_type":"user","effective_subject_id":"other-user"},
			"conversation_id":"conv-1","interaction_id":"int-1","operation_id":"op-1",
			"tool_name":"query_object_instance","receipt_status":"completed","evidence_durability":"durable",
			"request_id":"req-1","trace_id":"11111111111111111111111111111111",
			"knowledge_network_ids":["kn-managed"],
			"issued_at":"2026-08-02T07:35:26Z","terminal_at":"2026-08-02T07:35:27Z"
		}}]}}`)
	}))
	t.Cleanup(server.Close)

	source := opensearchcoreprojection.New(
		opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "bkn-trace-core", nil,
	)
	result, err := source.LoadExecutionProjection(context.Background(), iprojectionsource.Query{
		Scope: evidencevo.QueryScope{
			TenantID: "tenant-1", BusinessDomain: "domain-1", AccountID: "builder-1", AccountType: "user",
			AccessProfile: &evidencevo.AccessProfile{
				TenantID: "tenant-1", BusinessDomain: "domain-1", EffectiveSubjectID: "builder-1",
				Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn-managed"},
				AccountActive: true, TenantActive: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("load projection: %v", err)
	}
	if len(result.Traces) != 1 || result.Traces[0].AccountID != "other-user" {
		t.Fatalf("managed KN receipt was not projected: %#v", result.Traces)
	}
}

func TestSourceProjectsAuthorizedInteractionForManagedBuilder(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"hits":{"hits":[{"_source":{
			"receipt_id":"rcpt-schema","schema_version":"3.0.0",
			"owner":{"tenant_id":"tenant-1","business_domain_id":"domain-1","application_principal_id":"cursor","effective_subject_type":"user","effective_subject_id":"admin"},
			"conversation_id":"conv-1","interaction_id":"int-1","operation_id":"op-schema",
			"tool_name":"search_schema","receipt_status":"completed","evidence_durability":"durable",
			"request_id":"req-schema","trace_id":"11111111111111111111111111111111",
			"knowledge_network_ids":["kn-managed"],
			"issued_at":"2026-08-02T07:35:26Z","terminal_at":"2026-08-02T07:35:27Z"
		}}, {"_source":{
			"receipt_id":"rcpt-sql","schema_version":"3.0.0",
			"owner":{"tenant_id":"tenant-1","business_domain_id":"domain-1","application_principal_id":"cursor","effective_subject_type":"user","effective_subject_id":"admin"},
			"conversation_id":"conv-1","interaction_id":"int-1","operation_id":"op-sql",
			"tool_name":"run_sql","receipt_status":"completed","evidence_durability":"durable",
			"request_id":"req-sql","trace_id":"22222222222222222222222222222222",
			"business_refs":[{"ref_type":"data_resource","ref_id":"resource:inventory","business_domain_id":"domain-1","version":"v1"}],
			"issued_at":"2026-08-02T07:35:28Z","terminal_at":"2026-08-02T07:35:29Z"
		}}]}}`)
	}))
	t.Cleanup(server.Close)

	artifacts := artifactProjectionSource{result: iprojectionsource.Result{Artifacts: []evidencevo.EvidenceArtifact{
		{
			ArtifactID: "question-1", ArtifactType: evidencevo.ArtifactTypeQuestion,
			RequestID: "req-start", InteractionID: "int-1", Content: "查询库存",
			ObservedAt: "2026-08-02T07:35:25Z", TenantID: "tenant-1", BusinessDomain: "domain-1",
			AccountID: "admin", AccountType: "user",
		},
		{
			ArtifactID: "result-1", ArtifactType: evidencevo.ArtifactTypeResult,
			RequestID: "req-finish", InteractionID: "int-1", Content: "库存为 100",
			ObservedAt: "2026-08-02T07:35:30Z", TenantID: "tenant-1", BusinessDomain: "domain-1",
			AccountID: "admin", AccountType: "user",
		},
	}}}
	source := opensearchcoreprojection.New(
		opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "bkn-trace-core", artifacts,
	)

	result, err := source.LoadExecutionProjection(context.Background(), iprojectionsource.Query{
		InteractionID: "int-1",
		Scope: evidencevo.QueryScope{AccessProfile: &evidencevo.AccessProfile{
			TenantID: "tenant-1", BusinessDomain: "domain-1", EffectiveSubjectID: "builder",
			Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn-managed"},
			AccountActive: true, TenantActive: true,
		}},
	})
	if err != nil {
		t.Fatalf("load projection: %v", err)
	}
	if len(result.Traces) != 2 {
		t.Fatalf("authorized builder must receive every operation in the interaction, got %#v", result.Traces)
	}
	if len(result.Artifacts) != 4 {
		t.Fatalf("authorized builder must receive interaction question and result for every operation, got %#v", result.Artifacts)
	}
}

func TestSourceDoesNotDiscloseInteractionWithUnmanagedKnowledgeNetwork(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"hits":{"hits":[{"_source":{
			"receipt_id":"rcpt-managed","schema_version":"3.0.0",
			"owner":{"tenant_id":"tenant-1","business_domain_id":"domain-1","effective_subject_type":"user","effective_subject_id":"owner"},
			"interaction_id":"int-1","operation_id":"op-managed","tool_name":"search_schema",
			"request_id":"req-managed","trace_id":"11111111111111111111111111111111",
			"knowledge_network_ids":["kn-managed"],"issued_at":"2026-08-02T07:35:26Z"
		}}, {"_source":{
			"receipt_id":"rcpt-unmanaged","schema_version":"3.0.0",
			"owner":{"tenant_id":"tenant-1","business_domain_id":"domain-1","effective_subject_type":"user","effective_subject_id":"owner"},
			"interaction_id":"int-1","operation_id":"op-unmanaged","tool_name":"search_schema",
			"request_id":"req-unmanaged","trace_id":"22222222222222222222222222222222",
			"knowledge_network_ids":["kn-unmanaged"],"issued_at":"2026-08-02T07:35:27Z"
		}}]}}`)
	}))
	t.Cleanup(server.Close)

	source := opensearchcoreprojection.New(opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "bkn-trace-core", nil)
	result, err := source.LoadExecutionProjection(context.Background(), iprojectionsource.Query{
		InteractionID: "int-1",
		Scope: evidencevo.QueryScope{AccessProfile: &evidencevo.AccessProfile{
			TenantID: "tenant-1", BusinessDomain: "domain-1", EffectiveSubjectID: "builder",
			Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn-managed"},
			AccountActive: true, TenantActive: true,
		}},
	})
	if err != nil {
		t.Fatalf("load projection: %v", err)
	}
	if len(result.Traces) != 0 || len(result.Artifacts) != 0 {
		t.Fatalf("partially managed interaction must not be disclosed: %#v", result)
	}
}

func TestSourceUsesApplicationPrincipalAsTechnicalOwnerCandidate(t *testing.T) {
	t.Parallel()

	searches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searches++
		body, _ := io.ReadAll(r.Body)
		if searches == 1 && (!strings.Contains(string(body), `"owner.application_principal_id.keyword"`) ||
			!strings.Contains(string(body), `"app-a"`)) {
			t.Fatalf("application owner candidate missing: %s", body)
		}
		_, _ = io.WriteString(w, `{"hits":{"hits":[{"_source":{
			"receipt_id":"rcpt-app","schema_version":"3.0.0",
			"owner":{"tenant_id":"tenant-1","business_domain_id":"domain-1","application_principal_id":"app-a","effective_subject_type":"user","effective_subject_id":"other-user"},
			"conversation_id":"conv-1","interaction_id":"int-1","operation_id":"op-1",
			"tool_name":"query_object_instance","receipt_status":"completed","evidence_durability":"durable",
			"request_id":"req-1","trace_id":"11111111111111111111111111111111",
			"issued_at":"2026-08-02T07:35:26Z","terminal_at":"2026-08-02T07:35:27Z"
		}}]}}`)
	}))
	t.Cleanup(server.Close)

	source := opensearchcoreprojection.New(
		opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "bkn-trace-core", nil,
	)
	result, err := source.LoadExecutionProjection(context.Background(), iprojectionsource.Query{
		Scope: evidencevo.QueryScope{
			View: evidencevo.AccessViewTechnical,
			AccessProfile: &evidencevo.AccessProfile{
				TenantID: "tenant-1", BusinessDomain: "domain-1",
				EffectiveSubjectID: "user-a", ApplicationPrincipalID: "app-a",
				AccountActive: true, TenantActive: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("load projection: %v", err)
	}
	if len(result.Traces) != 1 || result.Traces[0].AccountID != "other-user" {
		t.Fatalf("application-owned receipt was not projected: %#v", result.Traces)
	}
}

type artifactProjectionSource struct {
	result iprojectionsource.Result
}

type recordingArtifactProjectionSource struct {
	result  iprojectionsource.Result
	queries []iprojectionsource.Query
}

func (s *recordingArtifactProjectionSource) LoadExecutionProjection(_ context.Context, query iprojectionsource.Query) (iprojectionsource.Result, error) {
	s.queries = append(s.queries, query)
	return s.result, nil
}

func (s artifactProjectionSource) LoadExecutionProjection(context.Context, iprojectionsource.Query) (iprojectionsource.Result, error) {
	return s.result, nil
}
