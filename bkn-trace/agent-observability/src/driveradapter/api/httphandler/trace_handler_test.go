package httphandler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/opensearchvo"
)

type fakeTraceHandlerPort struct {
	result opensearchvo.SearchResult
}

func (p *fakeTraceHandlerPort) SearchTraces(_ context.Context, _ json.RawMessage) (opensearchvo.SearchResult, error) {
	return p.result, nil
}

func TestTraceHandlerReturnsTraceGraphByTraceID(t *testing.T) {
	handler := NewTraceHandler(tracesvc.New(&fakeTraceHandlerPort{result: opensearchvo.SearchResult(handlerTraceGraphSearchResult())}))
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/trace_handler_001/trace-graph", nil)
	rec := httptest.NewRecorder()

	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"trace_id":"trace_handler_001"`) || !strings.Contains(body, `"status":"error"`) {
		t.Fatalf("unexpected trace graph body: %s", body)
	}
	if !strings.Contains(body, `"edge_type":"parent_child"`) || !strings.Contains(body, `"duration_nano":90`) {
		t.Fatalf("expected graph edge and duration: %s", body)
	}
}

func TestTraceHandlerReturnsNotFoundForMissingTraceGraph(t *testing.T) {
	handler := NewTraceHandler(tracesvc.New(&fakeTraceHandlerPort{result: opensearchvo.SearchResult(`{"hits":{"hits":[]}}`)}))
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/trace-graph", nil)
	rec := httptest.NewRecorder()

	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func handlerTraceGraphSearchResult() []byte {
	return []byte(`{
  "hits": {
    "hits": [
      {
        "_source": {
          "resourceSpans": [
            {
              "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "agent-observability"}}]},
              "scopeSpans": [
                {
                  "spans": [
                    {
                      "traceId": "trace_handler_001",
                      "spanId": "root",
                      "name": "GET /trace",
                      "kind": "SERVER",
                      "startTimeUnixNano": "10",
                      "endTimeUnixNano": "100",
                      "status": {"code": "STATUS_CODE_OK"}
                    },
                    {
                      "traceId": "trace_handler_001",
                      "spanId": "child",
                      "parentSpanId": "root",
                      "name": "opensearch.search",
                      "kind": "CLIENT",
                      "startTimeUnixNano": "20",
                      "endTimeUnixNano": "80",
                      "status": {"code": "STATUS_CODE_ERROR", "message": "query failed"}
                    }
                  ]
                }
              ]
            }
          ]
        }
      }
    ]
  }
}`)
}
