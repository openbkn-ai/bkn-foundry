package tracesvc

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/opensearchvo"
)

type fakeTracePort struct {
	query  json.RawMessage
	result opensearchvo.SearchResult
}

func (p *fakeTracePort) SearchTraces(_ context.Context, query json.RawMessage) (opensearchvo.SearchResult, error) {
	p.query = query
	return p.result, nil
}

func TestGetTraceGraphByTraceIDBuildsTreeStatusAndDuration(t *testing.T) {
	port := &fakeTracePort{result: opensearchvo.SearchResult(traceGraphSearchResult())}
	service := New(port)

	response, found, err := service.GetTraceGraphByTraceID(context.Background(), "trace_graph_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected trace graph to be found")
	}
	if !strings.Contains(string(port.query), `"traceId.keyword":"trace_graph_001"`) {
		t.Fatalf("expected trace id term query, got %s", string(port.query))
	}
	if response.TraceID != "trace_graph_001" || response.Status != "error" || response.DurationNano != 110 {
		t.Fatalf("unexpected graph summary: %+v", response)
	}
	if response.Partial || len(response.PartialReasons) != 0 {
		t.Fatalf("expected complete graph, got %+v", response.PartialReasons)
	}
	if response.Page.NodeCount != 3 || response.Page.EdgeCount != 2 {
		t.Fatalf("unexpected page: %+v", response.Page)
	}
	if len(response.Data.Nodes) != 3 || len(response.Data.Edges) != 2 {
		t.Fatalf("unexpected graph data: %+v", response.Data)
	}
	if response.Data.Nodes[0].SpanID != "root" || response.Data.Nodes[0].ParentSpanID != "" || response.Data.Nodes[0].Status != "ok" {
		t.Fatalf("root node should be first and ok: %+v", response.Data.Nodes[0])
	}
	if response.Data.Nodes[2].Status != "error" || response.Data.Nodes[2].ErrorMessage != "tool failed" {
		t.Fatalf("expected error node with message: %+v", response.Data.Nodes[2])
	}
}

func TestGetTraceGraphByTraceIDMarksOrphanSpanPartial(t *testing.T) {
	port := &fakeTracePort{result: opensearchvo.SearchResult(traceGraphOrphanSearchResult())}
	service := New(port)

	response, found, err := service.GetTraceGraphByTraceID(context.Background(), "trace_graph_orphan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected trace graph to be found")
	}
	if !response.Partial || !contains(response.PartialReasons, "orphan_span") {
		t.Fatalf("expected orphan partial reason, got %+v", response)
	}
	if response.Page.NodeCount != 1 || response.Page.EdgeCount != 0 {
		t.Fatalf("orphan span must not create dangling edge: %+v", response.Page)
	}
}

func TestGetTraceGraphByTraceIDMarksQueryTruncated(t *testing.T) {
	port := &fakeTracePort{result: opensearchvo.SearchResult(traceGraphTruncatedSearchResult())}
	service := New(port)

	response, found, err := service.GetTraceGraphByTraceID(context.Background(), "trace_graph_truncated")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected trace graph to be found")
	}
	if !strings.Contains(string(port.query), `"size":1001`) {
		t.Fatalf("expected limit+1 query for truncation detection, got %s", string(port.query))
	}
	if !response.Partial || !contains(response.PartialReasons, "trace_query_truncated") {
		t.Fatalf("expected truncation partial reason, got %+v", response)
	}
	if !response.Page.Truncated || response.Page.NodeCount != 1000 {
		t.Fatalf("expected truncated page with capped nodes, got %+v", response.Page)
	}
}

func TestGetTraceGraphByTraceIDDeduplicatesRepeatedSpans(t *testing.T) {
	port := &fakeTracePort{result: opensearchvo.SearchResult(traceGraphDuplicateSearchResult())}
	service := New(port)

	response, found, err := service.GetTraceGraphByTraceID(context.Background(), "trace_graph_duplicate")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected trace graph to be found")
	}
	if response.Page.NodeCount != 1 || len(response.Data.Nodes) != 1 {
		t.Fatalf("expected duplicate span to be collapsed, got %+v", response)
	}
	if response.Data.Nodes[0].SpanID != "duplicate" || response.Data.Nodes[0].ServiceName != "bkn-agent" {
		t.Fatalf("expected first span record to win, got %+v", response.Data.Nodes[0])
	}
}

func TestGetTraceGraphByTraceIDClampsInvalidDurations(t *testing.T) {
	port := &fakeTracePort{result: opensearchvo.SearchResult(traceGraphInvalidTimeSearchResult())}
	service := New(port)

	response, found, err := service.GetTraceGraphByTraceID(context.Background(), "trace_graph_bad_time")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected trace graph to be found")
	}
	if response.DurationNano < 0 || response.Data.Nodes[0].DurationNano < 0 {
		t.Fatalf("durations must not be negative: %+v", response)
	}
	if !response.Partial || !contains(response.PartialReasons, "invalid_span_timestamp") {
		t.Fatalf("expected invalid timestamp partial reason, got %+v", response)
	}
}

func TestGetTraceGraphByTraceIDNotFound(t *testing.T) {
	port := &fakeTracePort{result: opensearchvo.SearchResult(`{"hits":{"hits":[]}}`)}
	service := New(port)

	_, found, err := service.GetTraceGraphByTraceID(context.Background(), "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected not found")
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func traceGraphSearchResult() []byte {
	return []byte(`{
  "hits": {
    "hits": [
      {
        "_source": {
          "resourceSpans": [
            {
              "resource": {
                "attributes": [
                  {"key": "service.name", "value": {"stringValue": "bkn-agent"}}
                ]
              },
              "scopeSpans": [
                {
                  "scope": {"name": "bkn-agent"},
                  "spans": [
                    {
                      "traceId": "trace_graph_001",
                      "spanId": "root",
                      "name": "POST /chat",
                      "kind": "SERVER",
                      "startTimeUnixNano": "100",
                      "endTimeUnixNano": "210",
                      "status": {"code": "STATUS_CODE_OK"}
                    },
                    {
                      "traceId": "trace_graph_001",
                      "spanId": "child",
                      "parentSpanId": "root",
                      "name": "tool.search",
                      "kind": "CLIENT",
                      "startTimeUnixNano": "120",
                      "endTimeUnixNano": "180",
                      "status": {"code": "STATUS_CODE_OK"}
                    },
                    {
                      "traceId": "trace_graph_001",
                      "spanId": "error",
                      "parentSpanId": "child",
                      "name": "tool.invoke",
                      "kind": "CLIENT",
                      "startTimeUnixNano": "150",
                      "endTimeUnixNano": "170",
                      "status": {"code": "STATUS_CODE_ERROR", "message": "tool failed"}
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

func traceGraphOrphanSearchResult() []byte {
	return []byte(`{
  "hits": {
    "hits": [
      {
        "_source": {
          "resourceSpans": [
            {
              "scopeSpans": [
                {
                  "spans": [
                    {
                      "traceId": "trace_graph_orphan",
                      "spanId": "orphan",
                      "parentSpanId": "missing_parent",
                      "name": "worker.step",
                      "kind": "INTERNAL",
                      "startTimeUnixNano": "10",
                      "endTimeUnixNano": "20",
                      "status": {"code": "STATUS_CODE_OK"}
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

func traceGraphTruncatedSearchResult() []byte {
	spans := make([]string, 0, 1001)
	for i := 0; i < 1001; i++ {
		parent := ""
		if i > 0 {
			parent = `,"parentSpanId":"span_0"`
		}
		spans = append(spans, `{"traceId":"trace_graph_truncated","spanId":"span_`+strconv.Itoa(i)+`"`+parent+`,"name":"step","kind":"INTERNAL","startTimeUnixNano":"`+strconv.Itoa(i)+`","endTimeUnixNano":"`+strconv.Itoa(i+1)+`","status":{"code":"STATUS_CODE_OK"}}`)
	}
	return []byte(`{"hits":{"hits":[{"_source":{"resourceSpans":[{"scopeSpans":[{"spans":[` + strings.Join(spans, ",") + `]}]}]}}]}}`)
}

func traceGraphInvalidTimeSearchResult() []byte {
	return []byte(`{
  "hits": {
    "hits": [
      {
        "_source": {
          "resourceSpans": [
            {
              "scopeSpans": [
                {
                  "spans": [
                    {
                      "traceId": "trace_graph_bad_time",
                      "spanId": "bad_time",
                      "name": "bad.time",
                      "kind": "INTERNAL",
                      "startTimeUnixNano": "20",
                      "endTimeUnixNano": "bad",
                      "status": {"code": "STATUS_CODE_OK"}
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

func traceGraphDuplicateSearchResult() []byte {
	return []byte(`{
  "hits": {
    "hits": [
      {
        "_source": {
          "resourceSpans": [
            {
              "resource": {
                "attributes": [
                  {"key": "service.name", "value": {"stringValue": "bkn-agent"}}
                ]
              },
              "scopeSpans": [
                {
                  "spans": [
                    {
                      "traceId": "trace_graph_duplicate",
                      "spanId": "duplicate",
                      "name": "agent.run",
                      "kind": "SERVER",
                      "startTimeUnixNano": "10",
                      "endTimeUnixNano": "20",
                      "status": {"code": "STATUS_CODE_OK"}
                    }
                  ]
                }
              ]
            }
          ]
        }
      },
      {
        "_source": {
          "traceId": "trace_graph_duplicate",
          "spanId": "duplicate",
          "name": "agent.run.duplicate",
          "kind": "SERVER",
          "startTimeUnixNano": "10",
          "endTimeUnixNano": "20",
          "status": {"code": "STATUS_CODE_OK"}
        }
      }
    ]
  }
}`)
}
