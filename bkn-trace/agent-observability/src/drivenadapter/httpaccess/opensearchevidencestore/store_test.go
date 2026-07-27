package opensearchevidencestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencestore"
)

func TestStoreEvidenceIndexesNormalizedTrace(t *testing.T) {
	var paths []string
	var indexMapping map[string]any
	var body map[string]any
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			if err := json.NewDecoder(r.Body).Decode(&indexMapping); err != nil {
				t.Fatalf("decode index mapping: %v", err)
			}
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search" {
			return jsonResponse(`{"hits":{"hits":[]}}`), nil
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/") {
			return statusJSONResponse(http.StatusNotFound, `{"found":false}`), nil
		}
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/") {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode document body: %v", err)
			}
			return jsonResponse(`{"result":"created"}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})

	store := New(client, "bkn-trace-evidence-test")
	store.now = func() time.Time { return time.Date(2026, 7, 23, 1, 2, 3, 4, time.UTC) }

	if err := store.StoreEvidence(context.Background(), normalizedTrace()); err != nil {
		t.Fatalf("store evidence: %v", err)
	}

	if len(paths) != 4 || paths[0] != "/bkn-trace-evidence-test" || !strings.HasPrefix(paths[1], "/bkn-trace-evidence-test/_doc/") || paths[2] != "/bkn-trace-evidence-test/_search" || !strings.HasPrefix(paths[3], "/bkn-trace-evidence-test/_doc/") {
		t.Fatalf("unexpected request paths: %v", paths)
	}
	if paths[1] != paths[3] || !strings.Contains(paths[1], "aggregate-") {
		t.Fatalf("expected deterministic aggregate document id, got %v", paths)
	}
	mappingBytes, _ := json.Marshal(indexMapping)
	if !strings.Contains(string(mappingBytes), `"events":{"enabled":false,"type":"object"}`) {
		t.Fatalf("events must not create dynamic mappings: %s", string(mappingBytes))
	}
	if strings.Contains(string(mappingBytes), `"payload"`) {
		t.Fatalf("payload must not have an indexed mapping: %s", string(mappingBytes))
	}
	if body["trace_id"] != "trace_index_001" || body["bkn.request.id"] != "req_index_001" ||
		body["bkn.conversation.id"] != "conversation_index_001" {
		t.Fatalf("unexpected identity fields: %+v", body)
	}
	if !strings.Contains(string(mappingBytes), `"conversation":{"properties":{"id":{"type":"keyword"}}}`) {
		t.Fatalf("conversation id must be indexed for cross-request lookup: %s", string(mappingBytes))
	}
	if body["ingested_at"] != "2026-07-23T01:02:03.000000004Z" {
		t.Fatalf("unexpected ingested_at: %+v", body["ingested_at"])
	}
	if body["observed_start"] != "2026-07-22T04:00:00Z" {
		t.Fatalf("evidence document must persist a reliable event-time projection: %+v", body["observed_start"])
	}
}

func TestStoreEvidenceTreatsIdenticalEventReplayAsIdempotent(t *testing.T) {
	indexed := false
	indexAttempts := 0
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/") {
			if !indexed {
				return statusJSONResponse(http.StatusNotFound, `{"found":false}`), nil
			}
			doc := toDocument(normalizedTrace(), time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC))
			doc.Aggregate = true
			body, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal existing document: %v", err)
			}
			return jsonResponse(`{"_seq_no":0,"_primary_term":1,"_source":` + string(body) + `}`), nil
		}
		if r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search" {
			return jsonResponse(`{"hits":{"hits":[]}}`), nil
		}
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/") {
			indexed = true
			indexAttempts++
			return jsonResponse(`{"result":"created"}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	store := New(client, "bkn-trace-evidence-test")

	if err := store.StoreEvidence(context.Background(), normalizedTrace()); err != nil {
		t.Fatalf("first store: %v", err)
	}
	if err := store.StoreEvidence(context.Background(), normalizedTrace()); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if indexAttempts != 1 {
		t.Fatalf("expected one index write, got %d", indexAttempts)
	}
}

func TestStoreEvidenceRejectsEventIDConflict(t *testing.T) {
	existing := normalizedTrace()
	existingDoc := toDocument(existing, time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC))
	existingBody, err := json.Marshal(existingDoc)
	if err != nil {
		t.Fatalf("marshal existing document: %v", err)
	}
	indexAttempts := 0
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/") {
			return jsonResponse(`{"_seq_no":3,"_primary_term":1,"_source":` + string(existingBody) + `}`), nil
		}
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/") {
			indexAttempts++
			return jsonResponse(`{"result":"created"}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	store := New(client, "bkn-trace-evidence-test")
	changed := normalizedTrace()
	changed.Events[0].Payload["claim_id"] = "claim_changed"

	err = store.StoreEvidence(context.Background(), changed)
	if err == nil || !strings.Contains(err.Error(), "BKN_TRACE_EVENT_ID_CONFLICT") {
		t.Fatalf("expected event id conflict, got %v", err)
	}
	if indexAttempts != 0 {
		t.Fatalf("conflicting event must not be indexed, got %d writes", indexAttempts)
	}
}

func TestStoreEvidenceRejectsOwnershipDriftAtAtomicBoundary(t *testing.T) {
	backend := newAggregateBackend()
	store := New(newFakeOpenSearchClient(backend.roundTrip), "bkn-trace-evidence-test")
	initial := normalizedTrace()
	if err := store.StoreEvidence(context.Background(), initial); err != nil {
		t.Fatalf("store initial trace: %v", err)
	}
	changed := normalizedTrace()
	changed.Events[0].EventID = "evt_other_owner"
	changed.AccountID = "acct_other"

	err := store.StoreEvidence(context.Background(), changed)
	if !errors.Is(err, ievidencestore.ErrOwnershipConflict) {
		t.Fatalf("expected atomic ownership conflict, got %v", err)
	}
}

func TestStoreEvidenceRejectsTraceCapacityBeforeWrite(t *testing.T) {
	backend := newAggregateBackend()
	store := New(newFakeOpenSearchClient(backend.roundTrip), "bkn-trace-evidence-test")
	trace := normalizedTrace()
	trace.Events[0].Payload["large"] = strings.Repeat("x", evidencevo.MaxTraceSerializedBytes)

	err := store.StoreEvidence(context.Background(), trace)
	if !errors.Is(err, ievidencestore.ErrTraceCapacityExceeded) {
		t.Fatalf("expected trace capacity error, got %v", err)
	}
}

func TestStoreEvidencePaginatesLegacyHistoryBeyondOneThousand(t *testing.T) {
	searchCalls := 0
	indexAttempts := 0
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/") {
			return statusJSONResponse(http.StatusNotFound, `{"found":false}`), nil
		}
		if r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search" {
			var query map[string]any
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Fatalf("decode paged query: %v", err)
			}
			if searchCalls > 0 && query["search_after"] == nil {
				t.Fatal("subsequent legacy history page must use search_after")
			}
			start := searchCalls * evidenceSearchPageSize
			end := start + evidenceSearchPageSize
			if end > 1001 {
				end = 1001
			}
			hits := make([]map[string]any, 0, end-start)
			for i := start; i < end; i++ {
				docID := fmt.Sprintf("legacy-%04d", i)
				hits = append(hits, map[string]any{
					"_id":  docID,
					"sort": []any{"2026-07-23T01:02:03Z", docID},
					"_source": map[string]any{
						"document_id": docID, "trace_id": "trace_index_001", "bkn.request.id": "req_index_001",
						"bkn.tenant.id": "tenant_index", "business_domain": "bd_index", "bkn.account.id": "acct_index", "bkn.account.type": "app",
						"events": []any{}, "ingested_at": "2026-07-23T01:02:03Z",
					},
				})
			}
			searchCalls++
			responseBody, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
			return jsonResponse(string(responseBody)), nil
		}
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/") {
			indexAttempts++
			return jsonResponse(`{"result":"created"}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	store := New(client, "bkn-trace-evidence-test")

	if err := store.StoreEvidence(context.Background(), normalizedTrace()); err != nil {
		t.Fatalf("expected paged migration to succeed, got %v", err)
	}
	if searchCalls != 3 || indexAttempts != 1 {
		t.Fatalf("expected three search pages and one aggregate create, searches=%d writes=%d", searchCalls, indexAttempts)
	}
}

func TestGetEvidenceByTraceIDParsesSearchHits(t *testing.T) {
	var query map[string]any
	var ensured bool
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			ensured = true
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/bkn-trace-evidence-test/_search" {
			t.Fatalf("unexpected search path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
			t.Fatalf("decode query: %v", err)
		}
		return jsonResponse(`{
		  "hits": {
		    "hits": [
		      {
		        "_source": {
		          "document_id": "doc-1",
		          "trace_id": "trace_index_001",
		          "bkn.request.id": "req_index_001",
		          "bkn.trace.schema.version": "2.0.0",
		          "events": [
		            {
		              "event_id": "evt_claim",
		              "event_type": "claim.created",
		              "bkn.trace.schema.version": "2.0.0",
		              "trace_id": "trace_index_001",
		              "bkn.request.id": "req_index_001",
		              "payload": {"claim_id": "claim_index", "visibility": "visible"}
		            }
		          ],
		          "claim_ids": ["claim_index"],
		          "accepted_event_count": 1,
		          "claim_count": 1,
		          "evidence_ref_count": 0,
		          "business_ref_count": 0,
		          "ingested_at": "2026-07-23T01:02:03Z"
		        }
		      }
		    ]
		  }
		}`), nil
	})

	store := New(client, "bkn-trace-evidence-test")

	result, err := store.GetEvidenceByTraceID(context.Background(), "trace_index_001", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("query evidence: %v", err)
	}
	if !ensured {
		t.Fatal("expected evidence index to be ensured before search")
	}
	if len(result.Traces) != 1 {
		t.Fatalf("expected one trace, got %d", len(result.Traces))
	}
	if result.Traces[0].TraceID != "trace_index_001" || result.Traces[0].RequestID != "req_index_001" || result.Traces[0].ClaimCount != 1 {
		t.Fatalf("unexpected trace: %+v", result.Traces[0])
	}
	queryBytes, _ := json.Marshal(query)
	if !strings.Contains(string(queryBytes), `"trace_id.keyword"`) {
		t.Fatalf("expected keyword exact term fallback, got %s", string(queryBytes))
	}
	if !strings.Contains(string(queryBytes), `"document_id"`) {
		t.Fatalf("expected stable document_id tiebreaker for search_after pagination, got %s", string(queryBytes))
	}
}

func TestGetEvidenceByTraceIDPushesOwnershipScopeIntoSearch(t *testing.T) {
	var query map[string]any
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
			t.Fatalf("decode query: %v", err)
		}
		return jsonResponse(`{"hits":{"hits":[]}}`), nil
	})
	store := New(client, "bkn-trace-evidence-test")
	scope := evidencevo.QueryScope{TenantID: "tenant_index", BusinessDomain: "bd_index", AccountID: "acct_index", AccountType: "app"}

	if _, err := store.GetEvidenceByTraceID(context.Background(), "trace_index_001", evidencevo.EvidenceQueryOptions{Scope: scope}); err != nil {
		t.Fatalf("query evidence: %v", err)
	}
	body, _ := json.Marshal(query)
	for _, field := range []string{"bkn.tenant.id.keyword", "business_domain.keyword", "bkn.account.id.keyword", "bkn.account.type.keyword"} {
		if !strings.Contains(string(body), field) {
			t.Fatalf("ownership field %s missing from query: %s", field, body)
		}
	}
}

func TestGetEvidenceByRequestIDUsesRequestIDField(t *testing.T) {
	var query map[string]any
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
			t.Fatalf("decode query: %v", err)
		}
		return jsonResponse(`{"hits":{"hits":[]}}`), nil
	})

	store := New(client, "bkn-trace-evidence-test")

	if _, err := store.GetEvidenceByRequestID(context.Background(), "req_index_001", evidencevo.EvidenceQueryOptions{}); err != nil {
		t.Fatalf("query evidence: %v", err)
	}
	queryBytes, _ := json.Marshal(query)
	if !strings.Contains(string(queryBytes), `"bkn.request.id.keyword"`) {
		t.Fatalf("expected request id keyword term, got %s", string(queryBytes))
	}
}

func TestGetEvidenceByRequestIDFindsAggregateAfterOneThousandLegacyDocuments(t *testing.T) {
	searchCalls := 0
	aggregate := toDocument(normalizedTrace(), time.Date(2026, 7, 23, 2, 0, 0, 0, time.UTC))
	aggregate.Aggregate = true
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if r.Method != http.MethodPost || r.URL.Path != "/bkn-trace-evidence-test/_search" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		start := searchCalls * evidenceSearchPageSize
		end := start + evidenceSearchPageSize
		if end > 1002 {
			end = 1002
		}
		hits := make([]map[string]any, 0, end-start)
		for i := start; i < end; i++ {
			docID := fmt.Sprintf("legacy-%04d", i)
			source := any(map[string]any{"document_id": docID, "trace_id": "trace_index_001", "bkn.request.id": "req_index_001", "events": []any{}, "ingested_at": "2026-07-23T01:02:03Z"})
			if i == 1001 {
				docID = aggregate.DocumentID
				source = aggregate
			}
			hits = append(hits, map[string]any{"_id": docID, "sort": []any{"2026-07-23T01:02:03Z", docID}, "_source": source})
		}
		searchCalls++
		responseBody, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
		return jsonResponse(string(responseBody)), nil
	})

	result, err := New(client, "bkn-trace-evidence-test").GetEvidenceByRequestID(context.Background(), "req_index_001", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("query evidence: %v", err)
	}
	if searchCalls != 3 || result.Truncated || len(result.Traces) != 1 || result.Traces[0].ClaimCount != 1 {
		t.Fatalf("expected complete aggregate after paged legacy history, calls=%d result=%+v", searchCalls, result)
	}
}

func TestTracesFromHitsKeepsEveryAggregateForSharedRequest(t *testing.T) {
	legacy := toDocument(normalizedTrace(), time.Now())
	firstAggregate := legacy
	firstAggregate.Aggregate = true
	secondTrace := normalizedTrace()
	secondTrace.TraceID = "trace_index_002"
	secondAggregate := toDocument(secondTrace, time.Now())
	secondAggregate.Aggregate = true

	traces := tracesFromHits([]evidenceHit{
		{Source: legacy},
		{Source: firstAggregate},
		{Source: secondAggregate},
	})
	if len(traces) != 2 || traces[0].TraceID != "trace_index_001" || traces[1].TraceID != "trace_index_002" {
		t.Fatalf("expected both request aggregates without legacy duplicate, got %+v", traces)
	}
}

func TestGetEvidenceByTraceIDTraversesHistoryBeforeApplyingResponseLimit(t *testing.T) {
	var query map[string]any
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
			t.Fatalf("decode query: %v", err)
		}
		return jsonResponse(`{
		  "hits": {
		    "hits": [
		      {"_source": {"trace_id": "trace_index_001", "bkn.request.id": "req_index_001", "events": []}},
		      {"_source": {"trace_id": "trace_index_002", "bkn.request.id": "req_index_001", "events": []}}
		    ]
		  }
		}`), nil
	})

	store := New(client, "bkn-trace-evidence-test")

	result, err := store.GetEvidenceByTraceID(context.Background(), "trace_index_001", evidencevo.EvidenceQueryOptions{Limit: 1})
	if err != nil {
		t.Fatalf("query evidence: %v", err)
	}
	if query["size"] != float64(evidenceSearchPageSize) {
		t.Fatalf("expected internal paged history query, got %+v", query["size"])
	}
	if !result.Truncated || len(result.Traces) != 1 {
		t.Fatalf("expected truncated single result, got %+v", result)
	}
}

func TestStoreEvidenceRetriesEnsureIndexAfterTransientFailure(t *testing.T) {
	ensureAttempts := 0
	indexAttempts := 0
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			ensureAttempts++
			if ensureAttempts == 1 {
				return nil, errors.New("opensearch temporarily unavailable")
			}
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/") {
			indexAttempts++
			return jsonResponse(`{"result":"created"}`), nil
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/") {
			return statusJSONResponse(http.StatusNotFound, `{"found":false}`), nil
		}
		if r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search" {
			return jsonResponse(`{"hits":{"hits":[]}}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})

	store := New(client, "bkn-trace-evidence-test")

	if err := store.StoreEvidence(context.Background(), normalizedTrace()); err == nil {
		t.Fatal("expected first ensure index attempt to fail")
	}
	if err := store.StoreEvidence(context.Background(), normalizedTrace()); err != nil {
		t.Fatalf("expected second store evidence to retry and pass: %v", err)
	}
	if ensureAttempts != 2 {
		t.Fatalf("expected ensure index to retry after failure, got %d attempts", ensureAttempts)
	}
	if indexAttempts != 1 {
		t.Fatalf("expected document indexed once after successful ensure, got %d", indexAttempts)
	}
}

func TestStoreEvidenceUpdatesMappingForExistingIndex(t *testing.T) {
	mappingUpdates := 0
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test":
			return statusJSONResponse(http.StatusBadRequest, `{"error":{"type":"resource_already_exists_exception"}}`), nil
		case r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test/_mapping":
			mappingUpdates++
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read mapping update: %v", err)
			}
			for _, field := range []string{`"business_domain"`, `"account"`, `"tenant"`} {
				if !strings.Contains(string(body), field) {
					t.Fatalf("mapping update missing %s: %s", field, body)
				}
			}
			return jsonResponse(`{"acknowledged":true}`), nil
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/"):
			return statusJSONResponse(http.StatusNotFound, `{"found":false}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search":
			return jsonResponse(`{"hits":{"hits":[]}}`), nil
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/"):
			return jsonResponse(`{"result":"created"}`), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	})

	if err := New(client, "bkn-trace-evidence-test").StoreEvidence(context.Background(), normalizedTrace()); err != nil {
		t.Fatalf("store evidence: %v", err)
	}
	if mappingUpdates != 1 {
		t.Fatalf("existing index mapping updates=%d, want 1", mappingUpdates)
	}
}

func TestTwoStoreInstancesAtomicallyRejectEventIDConflict(t *testing.T) {
	backend := newAggregateBackend()
	client := newFakeOpenSearchClient(backend.roundTrip)
	stores := []*Store{New(client, "bkn-trace-evidence-test"), New(client, "bkn-trace-evidence-test")}
	traces := []evidencevo.NormalizedTrace{normalizedTrace(), normalizedTrace()}
	traces[1].Events[0].Payload["claim_id"] = "claim_changed"

	errs := runConcurrentStores(stores, traces)
	if countErrors(errs, nil) != 1 || countErrors(errs, ievidencestore.ErrEventIDConflict) != 1 {
		t.Fatalf("expected one commit and one event-id conflict, got %v", errs)
	}
}

func TestTwoStoreInstancesAtomicallyRejectActionFork(t *testing.T) {
	backend := newAggregateBackend()
	client := newFakeOpenSearchClient(backend.roundTrip)
	first := New(client, "bkn-trace-evidence-test")
	second := New(client, "bkn-trace-evidence-test")
	base := evidencevo.NormalizedTrace{
		TraceID: "trace_action", RequestID: "req_action", SchemaVersion: evidencevo.ContractVersion,
		Events: []evidencevo.EvidenceEvent{
			{EventID: "evt_recommended", EventType: "action.recommended", OperationID: "op_action", ClaimID: "claim_action", Payload: map[string]any{"action_instance_id": "action_1"}},
			{EventID: "evt_requested", EventType: "action.approval_requested", OperationID: "op_action", ClaimID: "claim_action", CausationID: "evt_recommended", Payload: map[string]any{"action_instance_id": "action_1"}},
		},
	}
	if err := first.StoreEvidence(context.Background(), base); err != nil {
		t.Fatalf("store action setup: %v", err)
	}
	approved := base
	approved.Events = []evidencevo.EvidenceEvent{{EventID: "evt_approved", EventType: "action.approved", OperationID: "op_action", ClaimID: "claim_action", CausationID: "evt_requested", Payload: map[string]any{"action_instance_id": "action_1"}}}
	rejected := base
	rejected.Events = []evidencevo.EvidenceEvent{{EventID: "evt_rejected", EventType: "action.rejected", OperationID: "op_action", ClaimID: "claim_action", CausationID: "evt_requested", Payload: map[string]any{"action_instance_id": "action_1"}}}

	errs := runConcurrentStores([]*Store{first, second}, []evidencevo.NormalizedTrace{approved, rejected})
	if countErrors(errs, nil) != 1 || countErrors(errs, ievidencestore.ErrActionTransitionInvalid) != 1 {
		t.Fatalf("expected one action branch and one transition conflict, got %v", errs)
	}
}

func TestTwoStoreInstancesAtomicallyRejectCausationCycle(t *testing.T) {
	backend := newAggregateBackend()
	client := newFakeOpenSearchClient(backend.roundTrip)
	stores := []*Store{New(client, "bkn-trace-evidence-test"), New(client, "bkn-trace-evidence-test")}
	base := normalizedTrace()
	base.Events = nil
	first := base
	first.Events = []evidencevo.EvidenceEvent{{EventID: "evt_a", EventType: "data.query.observed", CausationID: "evt_b", Payload: map[string]any{}}}
	second := base
	second.Events = []evidencevo.EvidenceEvent{{EventID: "evt_b", EventType: "retrieval.completed", CausationID: "evt_a", Payload: map[string]any{}}}

	errs := runConcurrentStores(stores, []evidencevo.NormalizedTrace{first, second})
	if countErrors(errs, nil) != 1 || countErrors(errs, ievidencestore.ErrCausationInvalid) != 1 {
		t.Fatalf("expected one partial fact and one causation-cycle rejection, got %v", errs)
	}
}

func runConcurrentStores(stores []*Store, traces []evidencevo.NormalizedTrace) []error {
	var wg sync.WaitGroup
	errs := make([]error, len(stores))
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = stores[i].StoreEvidence(context.Background(), traces[i])
		}(i)
	}
	wg.Wait()
	return errs
}

func countErrors(errs []error, target error) int {
	count := 0
	for _, err := range errs {
		if target == nil && err == nil || target != nil && errors.Is(err, target) {
			count++
		}
	}
	return count
}

type aggregateBackend struct {
	mu   sync.Mutex
	docs map[string]struct {
		body []byte
		seq  int64
	}
}

func newAggregateBackend() *aggregateBackend {
	return &aggregateBackend{docs: map[string]struct {
		body []byte
		seq  int64
	}{}}
}

func (b *aggregateBackend) roundTrip(r *http.Request) (*http.Response, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
		return jsonResponse(`{"acknowledged":true}`), nil
	}
	if r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search" {
		return jsonResponse(`{"hits":{"hits":[]}}`), nil
	}
	if !strings.HasPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/") {
		return nil, fmt.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}
	id := strings.TrimPrefix(r.URL.Path, "/bkn-trace-evidence-test/_doc/")
	stored, exists := b.docs[id]
	if r.Method == http.MethodGet {
		if !exists {
			return statusJSONResponse(http.StatusNotFound, `{"found":false}`), nil
		}
		return jsonResponse(fmt.Sprintf(`{"_seq_no":%d,"_primary_term":1,"_source":%s}`, stored.seq, stored.body)), nil
	}
	if r.Method != http.MethodPut {
		return nil, fmt.Errorf("unexpected document method %s", r.Method)
	}
	if r.URL.Query().Get("op_type") == "create" && exists {
		return statusJSONResponse(http.StatusConflict, `{"error":{"type":"version_conflict_engine_exception"}}`), nil
	}
	if expected := r.URL.Query().Get("if_seq_no"); expected != "" && (!exists || expected != fmt.Sprint(stored.seq)) {
		return statusJSONResponse(http.StatusConflict, `{"error":{"type":"version_conflict_engine_exception"}}`), nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	nextSeq := int64(0)
	if exists {
		nextSeq = stored.seq + 1
	}
	b.docs[id] = struct {
		body []byte
		seq  int64
	}{body: append([]byte(nil), body...), seq: nextSeq}
	return jsonResponse(`{"result":"updated"}`), nil
}

func normalizedTrace() evidencevo.NormalizedTrace {
	return evidencevo.NormalizedTrace{
		TraceID:        "trace_index_001",
		RequestID:      "req_index_001",
		ConversationID: "conversation_index_001",
		TenantID:       "tenant_index",
		BusinessDomain: "bd_index",
		AccountID:      "acct_index",
		AccountType:    "app",
		SchemaVersion:  evidencevo.ContractVersion,
		Events: []evidencevo.EvidenceEvent{
			{
				EventID:       "evt_claim",
				EventType:     "claim.created",
				SchemaVersion: evidencevo.ContractVersion,
				ObservedAt:    "2026-07-22T04:00:00Z",
				TraceID:       "trace_index_001",
				RequestID:     "req_index_001",
				Payload: map[string]any{
					"claim_id":   "claim_index",
					"visibility": "visible",
				},
			},
		},
		ClaimIDs:       []string{"claim_index"},
		AcceptedEvents: 1,
		ClaimCount:     1,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newFakeOpenSearchClient(fn roundTripFunc) *opensearch.Client {
	return opensearch.NewWithHTTPClient("http://opensearch.test", opensearch.AuthConfig{}, &http.Client{
		Transport: fn,
	})
}

func jsonResponse(body string) *http.Response {
	return statusJSONResponse(http.StatusOK, body)
}

func statusJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}
