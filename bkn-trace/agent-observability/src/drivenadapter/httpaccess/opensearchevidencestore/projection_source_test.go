package opensearchevidencestore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

func TestOpenSearchProjectionSourceUsesScopedAggregateEvidenceAndArtifacts(t *testing.T) {
	var evidenceQuery string
	var artifactQuery string
	ownedTrace := normalizedTrace()
	ownedTrace.BusinessDomain = "bd_index"
	ownedArtifact := normalizedOpenSearchArtifact(t)
	ownedArtifact.TenantID = "tenant_index"
	ownedArtifact.BusinessDomain = "bd_index"
	ownedArtifact.AccountID = "acct_index"
	otherArtifact := ownedArtifact
	otherArtifact.ArtifactID = "artifact_projection_other"
	otherArtifact.AccountID = "other-account"

	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPut && (r.URL.Path == "/bkn-trace-evidence-test" || r.URL.Path == "/bkn-trace-evidence-test-artifacts"):
			return jsonResponse(`{"acknowledged":true}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search":
			body, _ := io.ReadAll(r.Body)
			evidenceQuery = string(body)
			doc := toDocument(ownedTrace, mustTime(t, "2026-07-26T08:00:00Z"))
			doc.Aggregate = true
			response, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": []any{
				map[string]any{"_id": "aggregate-owned", "_source": doc, "sort": []any{doc.DocumentID}},
			}}})
			return jsonResponse(string(response)), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test-artifacts/_search":
			body, _ := io.ReadAll(r.Body)
			artifactQuery = string(body)
			response, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": []any{
				map[string]any{"_id": ownedArtifact.ArtifactID, "_source": ownedArtifact},
				map[string]any{"_id": otherArtifact.ArtifactID, "_source": otherArtifact},
			}}})
			return jsonResponse(string(response)), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	})
	store := New(client, "bkn-trace-evidence-test")
	scope := evidencevo.QueryScope{
		TenantID: "tenant_index", BusinessDomain: "bd_index", AccountID: "acct_index", AccountType: "app",
	}

	traces, err := store.ListEvidence(context.Background(), scope)
	if err != nil || len(traces) != 1 || traces[0].TraceID != ownedTrace.TraceID {
		t.Fatalf("expected owned aggregate trace: %+v err=%v", traces, err)
	}
	artifacts, err := store.ListArtifacts(context.Background(), scope)
	if err != nil || len(artifacts) != 1 || artifacts[0].ArtifactID != ownedArtifact.ArtifactID {
		t.Fatalf("return filtering must remove leaked hit: %+v err=%v", artifacts, err)
	}
	for _, query := range []string{evidenceQuery, artifactQuery} {
		for _, field := range []string{"bkn.tenant.id", "business_domain", "bkn.account.id", "bkn.account.type"} {
			if !strings.Contains(query, field) {
				t.Fatalf("projection query must filter %s: %s", field, query)
			}
		}
	}
	if !strings.Contains(evidenceQuery, `"aggregate"`) {
		t.Fatalf("evidence projection must read aggregate documents only: %s", evidenceQuery)
	}
}

func TestOpenSearchProjectionSourcePaginatesAllEvidence(t *testing.T) {
	searchCalls := 0
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if r.Method != http.MethodPost || r.URL.Path != "/bkn-trace-evidence-test/_search" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		query, _ := io.ReadAll(r.Body)
		searchCalls++
		count := evidenceSearchPageSize
		start := 0
		if strings.Contains(string(query), `"search_after"`) {
			count = 1
			start = evidenceSearchPageSize
		}
		hits := make([]any, 0, count)
		for i := 0; i < count; i++ {
			trace := normalizedTrace()
			trace.TraceID = fmt.Sprintf("trace_projection_%04d", start+i)
			trace.RequestID = fmt.Sprintf("req_projection_%04d", start+i)
			doc := toDocument(trace, mustTime(t, "2026-07-26T08:00:00Z"))
			doc.Aggregate = true
			hits = append(hits, map[string]any{
				"_id": doc.DocumentID, "_source": doc,
				"sort": []any{doc.DocumentID},
			})
		}
		response, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
		return jsonResponse(string(response)), nil
	})
	store := New(client, "bkn-trace-evidence-test")

	traces, err := store.ListEvidence(context.Background(), evidencevo.QueryScope{
		TenantID: "tenant_index", BusinessDomain: "bd_index", AccountID: "acct_index", AccountType: "app",
	})

	if err != nil || len(traces) != evidenceSearchPageSize+1 || searchCalls != 2 {
		t.Fatalf("expected all evidence pages: count=%d calls=%d err=%v", len(traces), searchCalls, err)
	}
}

func TestOpenSearchEvidenceProjectionUsesImmutableCursorAcrossConcurrentUpdates(t *testing.T) {
	searchCalls := 0
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test":
			return jsonResponse(`{"acknowledged":true}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search":
			var query map[string]any
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Fatal(err)
			}
			sortFields, _ := query["sort"].([]any)
			encodedSort, _ := json.Marshal(sortFields)
			if strings.Contains(string(encodedSort), "ingested_at") ||
				!strings.Contains(string(encodedSort), "document_id") {
				t.Fatalf("projection cursor must use immutable document_id only: %s", encodedSort)
			}

			searchCalls++
			documentIDs := []string{"aggregate-a", "aggregate-b"}
			if searchCalls == 2 {
				searchAfter, _ := query["search_after"].([]any)
				if len(searchAfter) != 1 || searchAfter[0] != "aggregate-b" {
					t.Fatalf("second page must continue from immutable document id: %+v", searchAfter)
				}
				// aggregate-a was concurrently updated, but its immutable ID remains before the cursor.
				documentIDs = []string{"aggregate-c"}
			}
			hits := make([]any, 0, len(documentIDs))
			for _, documentID := range documentIDs {
				trace := normalizedTrace()
				trace.TraceID = "trace-" + documentID
				trace.RequestID = "req-" + documentID
				doc := toDocument(trace, mustTime(t, "2026-07-26T08:00:00Z"))
				doc.DocumentID = documentID
				doc.Aggregate = true
				if searchCalls == 2 {
					doc.IngestedAt = "2026-07-27T08:00:00Z"
				}
				hits = append(hits, map[string]any{
					"_id": documentID, "_source": doc, "sort": []any{documentID},
				})
			}
			response, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
			return jsonResponse(string(response)), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	})
	store := New(client, "bkn-trace-evidence-test")
	query := iprojectionsource.Query{Scope: evidencevo.QueryScope{
		TenantID: "tenant_index", BusinessDomain: "bd_index", AccountID: "acct_index", AccountType: "app",
	}}

	first, err := store.listEvidenceProjectionPage(context.Background(), query, nil, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page: hits=%+v err=%v", first, err)
	}
	second, err := store.listEvidenceProjectionPage(context.Background(), query, first[len(first)-1].Sort, 2)
	if err != nil || len(second) != 1 {
		t.Fatalf("second page: hits=%+v err=%v", second, err)
	}
	traces := projectionTracesFromHits(append(first, second...), query.Scope)
	if len(traces) != 3 ||
		traces[0].TraceID != "trace-aggregate-a" ||
		traces[1].TraceID != "trace-aggregate-b" ||
		traces[2].TraceID != "trace-aggregate-c" {
		t.Fatalf("stable cursor must return every document exactly once: %+v", traces)
	}
}

func TestEvidenceProjectionSortsCollectedTracesByBusinessTime(t *testing.T) {
	later := normalizedTrace()
	later.TraceID = "trace-a-later"
	later.Events[0].ObservedAt = "2026-07-26T09:00:00Z"
	earlier := normalizedTrace()
	earlier.TraceID = "trace-z-earlier"
	earlier.Events[0].ObservedAt = "2026-07-26T08:00:00Z"
	laterDocument := toDocument(later, mustTime(t, "2026-07-26T10:00:00Z"))
	laterDocument.Aggregate = true
	earlierDocument := toDocument(earlier, mustTime(t, "2026-07-26T11:00:00Z"))
	earlierDocument.Aggregate = true

	traces := projectionTracesFromHits([]evidenceHit{
		{ID: laterDocument.DocumentID, Source: laterDocument},
		{ID: earlierDocument.DocumentID, Source: earlierDocument},
	}, evidencevo.QueryScope{
		TenantID: later.TenantID, BusinessDomain: later.BusinessDomain,
		AccountID: later.AccountID, AccountType: later.AccountType,
	})

	if len(traces) != 2 || traces[0].TraceID != earlier.TraceID || traces[1].TraceID != later.TraceID {
		t.Fatalf("collected projection must use business time, not mutable ingest time or trace id: %+v", traces)
	}
}

func TestOpenSearchProjectionSourcePaginatesAllArtifacts(t *testing.T) {
	searchCalls := 0
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test-artifacts" {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if r.Method != http.MethodPost || r.URL.Path != "/bkn-trace-evidence-test-artifacts/_search" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		query, _ := io.ReadAll(r.Body)
		searchCalls++
		count := evidenceSearchPageSize
		start := 0
		if strings.Contains(string(query), `"search_after"`) {
			count = 1
			start = evidenceSearchPageSize
		}
		hits := make([]any, 0, count)
		for i := 0; i < count; i++ {
			artifact := normalizedOpenSearchArtifact(t)
			artifact.ArtifactID = fmt.Sprintf("artifact_projection_%04d", start+i)
			artifact.TenantID = "tenant_index"
			artifact.BusinessDomain = "bd_index"
			artifact.AccountID = "acct_index"
			hits = append(hits, map[string]any{
				"_id": artifact.ArtifactID, "_source": artifact,
				"sort": []any{artifact.ObservedAt, artifact.ArtifactID},
			})
		}
		response, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
		return jsonResponse(string(response)), nil
	})
	store := New(client, "bkn-trace-evidence-test")

	artifacts, err := store.ListArtifacts(context.Background(), evidencevo.QueryScope{
		TenantID: "tenant_index", BusinessDomain: "bd_index", AccountID: "acct_index", AccountType: "app",
	})

	if err != nil || len(artifacts) != evidenceSearchPageSize+1 || searchCalls != 2 {
		t.Fatalf("expected all artifact pages: count=%d calls=%d err=%v", len(artifacts), searchCalls, err)
	}
}

func TestOpenSearchProjectionSourcePushesReliableFiltersAndRejectsMismatchedHits(t *testing.T) {
	var evidenceQuery string
	var artifactQuery string
	exactTrace := normalizedTrace()
	exactTrace.TraceID = "trace_exact_projection"
	exactTrace.RequestID = "req_exact_projection"
	exactTrace.BusinessDomain = "bd_index"
	exactTrace.Events[0].ObservedAt = "2026-07-26T08:00:00Z"
	leakedTrace := exactTrace
	leakedTrace.TraceID = "trace_leaked_projection"
	leakedTrace.RequestID = "req_leaked_projection"

	exactArtifact := normalizedOpenSearchArtifact(t)
	exactArtifact.ArtifactID = "artifact_exact_projection"
	exactArtifact.TraceID = exactTrace.TraceID
	exactArtifact.RequestID = exactTrace.RequestID
	exactArtifact.TenantID = "tenant_index"
	exactArtifact.BusinessDomain = "bd_index"
	exactArtifact.AccountID = "acct_index"
	exactDocument, err := toArtifactDocument(exactArtifact)
	if err != nil {
		t.Fatal(err)
	}
	leakedArtifact := exactArtifact
	leakedArtifact.ArtifactID = "artifact_leaked_projection"
	leakedArtifact.RequestID = "req_leaked_projection"
	leakedDocument, err := toArtifactDocument(leakedArtifact)
	if err != nil {
		t.Fatal(err)
	}

	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPut && (r.URL.Path == "/bkn-trace-evidence-test" || r.URL.Path == "/bkn-trace-evidence-test-artifacts"):
			return jsonResponse(`{"acknowledged":true}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search":
			body, _ := io.ReadAll(r.Body)
			evidenceQuery = string(body)
			exactDoc := toDocument(exactTrace, mustTime(t, "2026-07-26T08:00:00Z"))
			exactDoc.Aggregate = true
			leakedDoc := toDocument(leakedTrace, mustTime(t, "2026-07-26T08:00:00Z"))
			leakedDoc.Aggregate = true
			response, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": []any{
				map[string]any{"_id": exactDoc.DocumentID, "_source": exactDoc, "sort": []any{exactDoc.DocumentID}},
				map[string]any{"_id": leakedDoc.DocumentID, "_source": leakedDoc, "sort": []any{leakedDoc.DocumentID}},
			}}})
			return jsonResponse(string(response)), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test-artifacts/_search":
			body, _ := io.ReadAll(r.Body)
			artifactQuery = string(body)
			response, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": []any{
				map[string]any{"_id": exactArtifact.ArtifactID, "_source": exactDocument, "sort": []any{exactArtifact.ObservedAt, exactArtifact.ArtifactID}},
				map[string]any{"_id": leakedArtifact.ArtifactID, "_source": leakedDocument, "sort": []any{leakedArtifact.ObservedAt, leakedArtifact.ArtifactID}},
			}}})
			return jsonResponse(string(response)), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	})
	store := New(client, "bkn-trace-evidence-test")
	from := mustTime(t, "2026-07-26T00:00:00Z")
	to := mustTime(t, "2026-07-27T00:00:00Z")

	result, err := store.LoadExecutionProjection(context.Background(), iprojectionsource.Query{
		Scope: evidencevo.QueryScope{
			TenantID: "tenant_index", BusinessDomain: "bd_index", AccountID: "acct_index", AccountType: "app",
		},
		RequestID: exactTrace.RequestID, TraceID: exactTrace.TraceID,
		BusinessDomain: "bd_index", From: from, To: to, Limit: 20,
	})

	if err != nil || len(result.Traces) != 1 || result.Traces[0].TraceID != exactTrace.TraceID ||
		len(result.Artifacts) != 1 || result.Artifacts[0].ArtifactID != exactArtifact.ArtifactID {
		t.Fatalf("mismatched backend hits must be filtered: result=%+v err=%v", result, err)
	}
	for _, field := range []string{"bkn.request.id", "trace_id", "business_domain"} {
		if !strings.Contains(evidenceQuery, field) || !strings.Contains(artifactQuery, field) {
			t.Fatalf("identity filter %s must be pushed down: evidence=%s artifact=%s", field, evidenceQuery, artifactQuery)
		}
	}
	if !strings.Contains(artifactQuery, `"observed_at"`) || !strings.Contains(artifactQuery, `"range"`) {
		t.Fatalf("artifact time range must be pushed down: %s", artifactQuery)
	}
	if !strings.Contains(evidenceQuery, `"observed_start"`) || !strings.Contains(evidenceQuery, `"range"`) {
		t.Fatalf("evidence event-time range must be pushed down: %s", evidenceQuery)
	}
}

func TestOpenSearchProjectionSourceStopsAtScanCapAndMarksTruncated(t *testing.T) {
	var evidenceQuery string
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPut && (r.URL.Path == "/bkn-trace-evidence-test" || r.URL.Path == "/bkn-trace-evidence-test-artifacts"):
			return jsonResponse(`{"acknowledged":true}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search":
			body, _ := io.ReadAll(r.Body)
			evidenceQuery = string(body)
			hits := make([]any, 0, 3)
			for index := 0; index < 3; index++ {
				trace := normalizedTrace()
				trace.TraceID = fmt.Sprintf("trace_cap_%d", index)
				trace.RequestID = fmt.Sprintf("req_cap_%d", index)
				doc := toDocument(trace, mustTime(t, "2026-07-26T08:00:00Z"))
				doc.Aggregate = true
				hits = append(hits, map[string]any{
					"_id": doc.DocumentID, "_source": doc, "sort": []any{doc.DocumentID},
				})
			}
			response, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
			return jsonResponse(string(response)), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test-artifacts/_search":
			return jsonResponse(`{"hits":{"hits":[]}}`), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	})
	store := New(client, "bkn-trace-evidence-test")

	result, err := store.LoadExecutionProjection(context.Background(), iprojectionsource.Query{
		Scope: evidencevo.QueryScope{
			TenantID: "tenant_index", BusinessDomain: "bd_index", AccountID: "acct_index", AccountType: "app",
		},
		Limit: 2,
	})

	if err != nil || len(result.Traces) != 2 || !result.Truncated {
		t.Fatalf("scan cap must return bounded entries and truncation: result=%+v err=%v", result, err)
	}
	if !strings.Contains(evidenceQuery, `"size":3`) {
		t.Fatalf("scan must request only cap plus one sentinel hit: %s", evidenceQuery)
	}
}

func TestProjectionSourceExpandsConversationArtifactsBySelectedTraceIDs(t *testing.T) {
	var evidenceQuery, artifactQuery string
	trace := normalizedTrace()
	trace.TraceID = "trace-selected"
	trace.ConversationID = "conversation-selected"
	document := toDocument(trace, mustTime(t, "2026-08-19T09:00:00Z"))
	document.Aggregate = true
	hit, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": []any{
		map[string]any{"_id": document.DocumentID, "_source": document, "sort": []any{document.DocumentID}},
	}}})
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPut && (r.URL.Path == "/bkn-trace-evidence-test" || r.URL.Path == "/bkn-trace-evidence-test-artifacts"):
			return jsonResponse(`{"acknowledged":true}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search":
			body, _ := io.ReadAll(r.Body)
			evidenceQuery = string(body)
			return jsonResponse(string(hit)), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test-artifacts/_search":
			body, _ := io.ReadAll(r.Body)
			artifactQuery = string(body)
			return jsonResponse(`{"hits":{"hits":[]}}`), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	})
	store := New(client, "bkn-trace-evidence-test")
	_, err := store.LoadExecutionProjection(context.Background(), iprojectionsource.Query{
		Scope: evidencevo.QueryScope{
			TenantID: trace.TenantID, BusinessDomain: trace.BusinessDomain,
			AccountID: trace.AccountID, AccountType: trace.AccountType,
		},
		ConversationIDs: []string{trace.ConversationID}, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(evidenceQuery, `"bkn.conversation.id":["conversation-selected"]`) {
		t.Fatalf("evidence query must select the requested conversation: %s", evidenceQuery)
	}
	if !strings.Contains(artifactQuery, `"trace_id":["trace-selected"]`) {
		t.Fatalf("artifact query must use trace IDs resolved from the conversation page: %s", artifactQuery)
	}
	if strings.Contains(artifactQuery, "bkn.conversation.id") {
		t.Fatalf("artifact mapping has no conversation field: %s", artifactQuery)
	}
}

func TestProjectionSourceUsesAuthorizedInteractionsWhenConversationEvidenceIsMissing(t *testing.T) {
	var artifactQuery string
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPut && (r.URL.Path == "/bkn-trace-evidence-test" || r.URL.Path == "/bkn-trace-evidence-test-artifacts"):
			return jsonResponse(`{"acknowledged":true}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search":
			return jsonResponse(`{"hits":{"hits":[]}}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test-artifacts/_search":
			body, _ := io.ReadAll(r.Body)
			artifactQuery = string(body)
			return jsonResponse(`{"hits":{"hits":[]}}`), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	})
	store := New(client, "bkn-trace-evidence-test")
	_, err := store.LoadExecutionProjection(context.Background(), iprojectionsource.Query{
		Scope: evidencevo.QueryScope{
			TenantID: "tenant_index", BusinessDomain: "bd_index", AccountID: "acct_index", AccountType: "app",
		},
		ConversationIDs:          []string{"conversation-degraded"},
		AuthorizedInteractionIDs: []string{"interaction-authorized"},
		Limit:                    20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(artifactQuery, `"interaction_id":["interaction-authorized"]`) {
		t.Fatalf("degraded conversation must keep the Core-authorized artifact handoff: %s", artifactQuery)
	}
	if strings.Contains(artifactQuery, "bkn.conversation.id") || strings.Contains(artifactQuery, `"trace_id"`) {
		t.Fatalf("authorized interaction lookup must not add unavailable conversation/trace fields: %s", artifactQuery)
	}
}

func TestOwnershipMustSkipsAccountFiltersOnlyForExplicitTechnicalView(t *testing.T) {
	profile := &evidencevo.AccessProfile{
		TenantID: "tenant_index", BusinessDomain: "bd_index", AccountActive: true, TenantActive: true,
		EffectiveSubjectID: "admin_index", Roles: []string{"super_admin"},
	}
	must := ownershipMust(evidencevo.QueryScope{
		TenantID: "tenant_index", BusinessDomain: "bd_index",
		AccountID: "admin_index", AccountType: "super_admin",
		AccessProfile: profile, View: evidencevo.AccessViewTechnical,
	})
	body, err := json.Marshal(must)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(body)
	if !strings.Contains(rendered, "bkn.tenant.id") || !strings.Contains(rendered, "business_domain") {
		t.Fatalf("cross-account readers must still be constrained by tenant/domain: %s", rendered)
	}
	if strings.Contains(rendered, "bkn.account.id") || strings.Contains(rendered, "bkn.account.type") {
		t.Fatalf("cross-account projection must not push owner account filters: %s", rendered)
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
