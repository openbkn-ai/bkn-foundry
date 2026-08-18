package httphandler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	observabilitylocale "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/locale"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/oteltracevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

type technicalTraceSummarySource interface {
	ListTraceExecutions(context.Context, evidencevo.SummaryQueryOptions) (evidencevo.TraceSummaryPage, error)
}

type technicalOperationSource interface {
	ListOperationExecutionsByTraceIDScoped(context.Context, evidencevo.QueryScope, string) ([]sessionvo.OperationExecution, error)
}

type TraceHandler struct {
	traceQueryService *tracesvc.TraceQueryService
	traceSummaries    technicalTraceSummarySource
	operations        technicalOperationSource
}

func NewTraceHandlerWithTechnicalSources(
	traceQueryService *tracesvc.TraceQueryService,
	traceSummaries technicalTraceSummarySource,
	operations technicalOperationSource,
) *TraceHandler {
	handler := NewTraceHandler(traceQueryService)
	handler.traceSummaries = traceSummaries
	handler.operations = operations
	return handler
}

func NewTraceHandler(traceQueryService *tracesvc.TraceQueryService) *TraceHandler {
	return &TraceHandler{traceQueryService: traceQueryService}
}

func (h *TraceHandler) GetTraceSubresource(w http.ResponseWriter, r *http.Request) bool {
	if traceIDFromDetailPath(r.URL.Path) != "" {
		h.GetTechnicalTraceDetail(w, r)
		return true
	}
	return false
}

// GetTechnicalTraceDetail godoc
// @Summary Get one typed technical trace
// @Description Returns the authorized trace summary, Span graph, and ordered raw Operation call facts. Operation facts remain visible without Span data; Span nodes remain visible without Operation facts.
// @Tags traces
// @Produce json
// @Param trace_id path string true "Trace ID"
// @Success 200 {object} tracesvc.TechnicalTraceDetail
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Failure 504 {object} rdto.ErrorResponse
// @Router /traces/{trace_id} [get]
func (h *TraceHandler) GetTechnicalTraceDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported",
		})
		return
	}
	traceID := traceIDFromDetailPath(r.URL.Path)
	if traceID == "" {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "trace_id is required"})
		return
	}
	if h.traceSummaries == nil || h.operations == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "TRACE_DETAIL_UNAVAILABLE", Message: "typed trace detail is not configured"})
		return
	}
	scope, ok := trustedQueryScopeFromContext(r.Context())
	if !ok {
		writeJSON(w, r, http.StatusUnauthorized, rdto.ErrorResponse{Code: "UNAUTHORIZED", Message: "trusted trace query scope is required"})
		return
	}
	scope.View = evidencevo.AccessViewTechnical
	executions, err := h.operations.ListOperationExecutionsByTraceIDScoped(r.Context(), scope, traceID)
	if err != nil {
		writeJSON(w, r, http.StatusInternalServerError, rdto.ErrorResponse{Code: "QUERY_FAILED", Message: "failed to query operation facts"})
		return
	}
	page, err := h.traceSummaries.ListTraceExecutions(r.Context(), evidencevo.SummaryQueryOptions{
		TraceID: traceID, Scope: scope, Limit: 1,
	})
	if err != nil {
		writeJSON(w, r, http.StatusInternalServerError, rdto.ErrorResponse{Code: "QUERY_FAILED", Message: "failed to query trace summary"})
		return
	}
	if len(page.Entries) == 0 && len(executions) == 0 {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "NOT_FOUND", Message: "trace not found"})
		return
	}
	summary := evidencevo.TraceSummary{TraceID: traceID, Status: "unknown"}
	if len(page.Entries) > 0 {
		summary = page.Entries[0]
	} else if len(executions) > 0 {
		summary.RootService = executions[0].Fact.SourceModule
		summary.RootOperation = executions[0].Fact.ToolName
	}
	graph, found, err := h.traceQueryService.GetTraceGraphByTraceID(r.Context(), traceID)
	if err != nil {
		writeJSON(w, r, http.StatusGatewayTimeout, rdto.ErrorResponse{Code: "QUERY_FAILED", Message: "failed to query trace spans"})
		return
	}
	var graphPointer *oteltracevo.TraceGraphResponse
	if found {
		graphPointer = &graph
	}
	writeJSON(w, r, http.StatusOK, tracesvc.BuildTechnicalTraceDetail(summary, graphPointer, executions))
}

func traceIDFromDetailPath(path string) string {
	const prefix = "/api/agent-observability/v1/traces/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	traceID := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if traceID == "" || strings.Contains(traceID, "/") {
		return ""
	}
	return strings.TrimSpace(traceID)
}

func writeJSON(w http.ResponseWriter, r *http.Request, statusCode int, payload any) {
	traceID := ensureWriterTraceID(w)
	if response, ok := payload.(rdto.ErrorResponse); ok {
		if response.ErrorCode == "" {
			response.ErrorCode = response.Code
		}
		if response.Code == "" {
			response.Code = response.ErrorCode
		}
		response.TraceID = traceID
		payload = response
	}
	if observabilitylocale.IsNegotiated(r.Context()) {
		if localizedPayload, localized := localizeErrorPayload(r.Context(), payload); localized {
			payload = localizedPayload
			observabilitylocale.MarkLocalizedResponse(w, r.Context())
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func localizeErrorPayload(ctx context.Context, payload any) (any, bool) {
	switch response := payload.(type) {
	case rdto.ErrorResponse:
		response.Message = observabilitylocale.Translate(ctx, response.Message)
		response.Details = localizeErrorDetails(ctx, response.Details)
		return response, true
	case lifecycleErrorEnvelope:
		response.Error.Message = observabilitylocale.Translate(ctx, response.Error.Message)
		return response, true
	case observabilityErrorEnvelope:
		response.Error.Message = observabilitylocale.Translate(ctx, response.Error.Message)
		return response, true
	default:
		return payload, false
	}
}

func localizeErrorDetails(ctx context.Context, details any) any {
	switch validationErrors := details.(type) {
	case evidencevo.ValidationErrors:
		localized := make(evidencevo.ValidationErrors, len(validationErrors))
		copy(localized, validationErrors)
		for index := range localized {
			localized[index].Message = observabilitylocale.Translate(ctx, localized[index].Message)
		}
		return localized
	case []evidencevo.ValidationError:
		localized := make([]evidencevo.ValidationError, len(validationErrors))
		copy(localized, validationErrors)
		for index := range localized {
			localized[index].Message = observabilitylocale.Translate(ctx, localized[index].Message)
		}
		return localized
	default:
		return details
	}
}
