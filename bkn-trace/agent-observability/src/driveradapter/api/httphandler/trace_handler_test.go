// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/opensearchvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

type fakeTraceHandlerPort struct {
	result opensearchvo.SearchResult
}

type fakeTechnicalTraceSummarySource struct {
	page evidencevo.TraceSummaryPage
}

func (s fakeTechnicalTraceSummarySource) ListTraceExecutions(
	_ context.Context,
	_ evidencevo.SummaryQueryOptions,
) (evidencevo.TraceSummaryPage, error) {
	return s.page, nil
}

type fakeTechnicalOperationSource struct {
	executions []sessionvo.OperationExecution
}

func (s fakeTechnicalOperationSource) ListOperationExecutionsByTraceIDScoped(
	_ context.Context,
	_ evidencevo.QueryScope,
	_ string,
) ([]sessionvo.OperationExecution, error) {
	return s.executions, nil
}

func (p *fakeTraceHandlerPort) SearchTraces(_ context.Context, _ json.RawMessage) (opensearchvo.SearchResult, error) {
	return p.result, nil
}

func TestTraceHandlerReturnsTypedTraceDetailWithRawOperationFacts(t *testing.T) {
	finished := time.Date(2026, 8, 9, 10, 0, 1, 0, time.UTC)
	handler := NewTraceHandlerWithTechnicalSources(
		tracesvc.New(&fakeTraceHandlerPort{result: opensearchvo.SearchResult(handlerTraceGraphSearchResult())}),
		fakeTechnicalTraceSummarySource{page: evidencevo.TraceSummaryPage{Entries: []evidencevo.TraceSummary{{
			TraceID: "trace_handler_001", Status: "completed",
		}}}},
		fakeTechnicalOperationSource{executions: []sessionvo.OperationExecution{{
			Fact: sessionvo.OperationCallFact{
				OperationID: "op-search-schema", Attempt: 1, TraceID: "trace_handler_001",
				ToolName: "search_schema", Status: sessionvo.AttemptCompleted,
				Input: sessionvo.PayloadEnvelope{
					Mode: sessionvo.PayloadInline, MediaType: "application/json", ByteLength: 20,
					Inline: json.RawMessage(`{"query":"物料"}`),
				},
			},
			Receipt:           sessionvo.Receipt{Status: sessionvo.ReceiptCompleted},
			InteractionStatus: sessionvo.InteractionCompleted,
		}, {
			Fact: sessionvo.OperationCallFact{
				OperationID: "op-run-sql", Attempt: 1, TraceID: "trace_handler_001",
				ToolName: "run_sql", Status: sessionvo.AttemptCompleted,
				Input: sessionvo.PayloadEnvelope{
					Mode: sessionvo.PayloadInline, MediaType: "application/json", ByteLength: 35,
					Inline: json.RawMessage(`{"resource_id":"r1","sql":"SELECT 1"}`),
				},
				Output: &sessionvo.PayloadEnvelope{
					Mode: sessionvo.PayloadInline, MediaType: "application/json", ByteLength: 15,
					Inline: json.RawMessage(`{"row_count":1}`),
				},
				FinishedAt: &finished,
			},
			Receipt:           sessionvo.Receipt{Status: sessionvo.ReceiptCompleted},
			InteractionStatus: sessionvo.InteractionCompleted,
		}}},
	)
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/trace_handler_001", nil)
	req = req.WithContext(context.WithValue(req.Context(), trustedQueryScopeContextKey{}, evidencevo.QueryScope{
		TenantID: "tenant-1", AccountID: "user-1", AccountType: "user",
	}))
	rec := httptest.NewRecorder()

	if !handler.GetTraceSubresource(rec, req) {
		t.Fatal("typed trace detail route was not handled")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, expected := range []string{
		`"trace_id":"trace_handler_001"`, `"operation_id":"op-search-schema"`,
		`"operation_id":"op-run-sql"`,
		`"sql":"SELECT 1"`, `"row_count":1`, `"span_id":"root"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("typed detail missing %s: %s", expected, body)
		}
	}
	var detail tracesvc.TechnicalTraceDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode typed detail: %v", err)
	}
	if detail.Summary.TraceID != "trace_handler_001" || len(detail.Operations) != 2 {
		t.Fatalf("one Trace summary must retain two distinct Operation nodes: %s", body)
	}
}

func TestTraceHandlerReturnsOperationOnlyDetailWithoutEvidenceProjection(t *testing.T) {
	t.Parallel()

	handler := NewTraceHandlerWithTechnicalSources(
		tracesvc.New(&fakeTraceHandlerPort{result: opensearchvo.SearchResult(`{"hits":{"hits":[]}}`)}),
		fakeTechnicalTraceSummarySource{page: evidencevo.TraceSummaryPage{}},
		fakeTechnicalOperationSource{executions: []sessionvo.OperationExecution{{
			Fact: sessionvo.OperationCallFact{
				OperationID: "op-only", Attempt: 1, TraceID: "trace-operation-only",
				ToolName: "search_schema", SourceModule: "context-loader", Status: sessionvo.AttemptCompleted,
			},
			Receipt:           sessionvo.Receipt{Status: sessionvo.ReceiptCompleted},
			InteractionStatus: sessionvo.InteractionCompleted,
		}}},
	)
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/trace-operation-only", nil)
	req = req.WithContext(context.WithValue(req.Context(), trustedQueryScopeContextKey{}, evidencevo.QueryScope{
		TenantID: "tenant-1", AccountID: "user-1", AccountType: "user",
	}))
	rec := httptest.NewRecorder()

	handler.GetTechnicalTraceDetail(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"operation_id":"op-only"`) ||
		!strings.Contains(rec.Body.String(), `"root_service":"context-loader"`) {
		t.Fatalf("authorized Operation fact must not depend on Evidence projection or Span: %d %s", rec.Code, rec.Body.String())
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
