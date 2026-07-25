package opensearchevidencestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
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

	if len(paths) != 3 || paths[0] != "/bkn-trace-evidence-test" || paths[1] != "/bkn-trace-evidence-test/_search" || !strings.HasPrefix(paths[2], "/bkn-trace-evidence-test/_doc/") {
		t.Fatalf("unexpected request paths: %v", paths)
	}
	mappingBytes, _ := json.Marshal(indexMapping)
	if !strings.Contains(string(mappingBytes), `"events":{"enabled":false,"type":"object"}`) {
		t.Fatalf("events must not create dynamic mappings: %s", string(mappingBytes))
	}
	if strings.Contains(string(mappingBytes), `"payload"`) {
		t.Fatalf("payload must not have an indexed mapping: %s", string(mappingBytes))
	}
	if body["trace_id"] != "trace_index_001" || body["bkn.request.id"] != "req_index_001" {
		t.Fatalf("unexpected identity fields: %+v", body)
	}
	if body["ingested_at"] != "2026-07-23T01:02:03.000000004Z" {
		t.Fatalf("unexpected ingested_at: %+v", body["ingested_at"])
	}
}

func TestStoreEvidenceTreatsIdenticalEventReplayAsIdempotent(t *testing.T) {
	indexed := false
	indexAttempts := 0
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search" {
			if !indexed {
				return jsonResponse(`{"hits":{"hits":[]}}`), nil
			}
			body, err := json.Marshal(toDocument(normalizedTrace(), time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)))
			if err != nil {
				t.Fatalf("marshal existing document: %v", err)
			}
			return jsonResponse(`{"hits":{"hits":[{"_source":` + string(body) + `}]}}`), nil
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
		if r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search" {
			return jsonResponse(`{"hits":{"hits":[{"_source":` + string(existingBody) + `}]}}`), nil
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

func TestStoreEvidenceRejectsWriteWhenIdempotencyHistoryIsTruncated(t *testing.T) {
	hits := make([]map[string]any, maxEvidenceSearchResults+1)
	for i := range hits {
		hits[i] = map[string]any{"_source": map[string]any{
			"trace_id": "trace_index_001", "bkn.request.id": "req_index_001", "events": []any{},
		}}
	}
	responseBody, err := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
	if err != nil {
		t.Fatalf("marshal search response: %v", err)
	}
	indexAttempts := 0
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && r.URL.Path == "/bkn-trace-evidence-test" {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if r.Method == http.MethodPost && r.URL.Path == "/bkn-trace-evidence-test/_search" {
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

	err = store.StoreEvidence(context.Background(), normalizedTrace())
	if err == nil || !strings.Contains(err.Error(), "idempotency history is truncated") {
		t.Fatalf("expected fail-closed truncated history error, got %v", err)
	}
	if indexAttempts != 0 {
		t.Fatalf("must not write with incomplete idempotency history, got %d writes", indexAttempts)
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
	if strings.Contains(string(queryBytes), `"document_id"`) {
		t.Fatalf("document_id sort requires explicit mapping and must not be emitted, got %s", string(queryBytes))
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

func TestGetEvidenceByTraceIDFetchesLimitPlusOneAndTruncates(t *testing.T) {
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
	if query["size"] != float64(2) {
		t.Fatalf("expected size limit+1, got %+v", query["size"])
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

func normalizedTrace() evidencevo.NormalizedTrace {
	return evidencevo.NormalizedTrace{
		TraceID:       "trace_index_001",
		RequestID:     "req_index_001",
		SchemaVersion: evidencevo.ContractVersion,
		Events: []evidencevo.EvidenceEvent{
			{
				EventID:       "evt_claim",
				EventType:     "claim.created",
				SchemaVersion: evidencevo.ContractVersion,
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
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}
