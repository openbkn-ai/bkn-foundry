package opensearchevidencestore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

func TestStoreEvidenceIndexesNormalizedTrace(t *testing.T) {
	var path string
	var body map[string]any
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		path = r.URL.Path
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return jsonResponse(`{"result":"created"}`), nil
	})

	store := New(client, "bkn-trace-evidence-test")
	store.now = func() time.Time { return time.Date(2026, 7, 23, 1, 2, 3, 4, time.UTC) }

	if err := store.StoreEvidence(context.Background(), normalizedTrace()); err != nil {
		t.Fatalf("store evidence: %v", err)
	}

	if !strings.HasPrefix(path, "/bkn-trace-evidence-test/_doc/") {
		t.Fatalf("unexpected index path: %s", path)
	}
	if body["trace_id"] != "trace_index_001" || body["bkn.request.id"] != "req_index_001" {
		t.Fatalf("unexpected identity fields: %+v", body)
	}
	if body["ingested_at"] != "2026-07-23T01:02:03.000000004Z" {
		t.Fatalf("unexpected ingested_at: %+v", body["ingested_at"])
	}
}

func TestGetEvidenceByTraceIDParsesSearchHits(t *testing.T) {
	var query map[string]any
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
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
