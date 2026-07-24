package httphandler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

type TraceHandler struct {
	traceQueryService *tracesvc.TraceQueryService
}

func NewTraceHandler(traceQueryService *tracesvc.TraceQueryService) *TraceHandler {
	return &TraceHandler{traceQueryService: traceQueryService}
}

// SearchTraces godoc
// @Summary Search traces with raw OpenSearch DSL
// @Description Proxy raw OpenSearch DSL to the configured trace index and return the original OpenSearch response body.
// @Tags traces
// @Accept json
// @Produce json
// @Param request body string true "OpenSearch DSL JSON body"
// @Success 200 {string} string "Raw OpenSearch search response"
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 504 {object} rdto.ErrorResponse
// @Router /api/agent-observability/v1/traces/_search [post]
func (h *TraceHandler) SearchTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code:    "METHOD_NOT_ALLOWED",
			Message: "only POST is supported",
		})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "failed to read request body",
		})
		return
	}

	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "opensearch dsl body is required",
		})
		return
	}

	var raw json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "request body must be valid json",
		})
		return
	}

	traceData, err := h.traceQueryService.SearchTraces(r.Context(), raw)
	if err != nil {
		writeJSON(w, http.StatusGatewayTimeout, rdto.ErrorResponse{
			Code:    "QUERY_FAILED",
			Message: err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(traceData)
}

// SearchTracesByConversationID godoc
// @Summary Search traces by conversation ID
// @Description Build a term filter automatically using attributes.gen_ai.conversation.id.keyword and return the original OpenSearch response body.
// @Tags traces
// @Accept json
// @Produce json
// @Param conversation_id query string true "Conversation ID"
// @Success 200 {string} string "Raw OpenSearch search response"
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Failure 504 {object} rdto.ErrorResponse
// @Router /api/agent-observability/v1/traces/by-conversation [get]
func (h *TraceHandler) SearchTracesByConversationID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code:    "METHOD_NOT_ALLOWED",
			Message: "only GET is supported",
		})
		return
	}

	conversationID := r.URL.Query().Get("conversation_id")
	if conversationID == "" {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "conversation_id is required",
		})
		return
	}

	query, err := json.Marshal(map[string]any{
		"size": 1000,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []map[string]any{
					{
						"term": map[string]string{
							"attributes.gen_ai.conversation.id.keyword": conversationID,
						},
					},
				},
			},
		},
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{
			Code:    "INTERNAL_ERROR",
			Message: "failed to build query",
		})
		return
	}

	traceData, err := h.traceQueryService.SearchTraces(r.Context(), query)
	if err != nil {
		writeJSON(w, http.StatusGatewayTimeout, rdto.ErrorResponse{
			Code:    "QUERY_FAILED",
			Message: err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(traceData)
}

func (h *TraceHandler) GetTraceSubresource(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasSuffix(r.URL.Path, "/trace-graph") {
		return false
	}
	h.GetTraceGraphByTraceID(w, r)
	return true
}

// GetTraceGraphByTraceID godoc
// @Summary Get trace graph by trace ID
// @Description Returns normalized trace tree nodes, parent-child edges, status, duration, and partial reasons for a trace.
// @Tags traces
// @Produce json
// @Param trace_id path string true "Trace ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Failure 504 {object} rdto.ErrorResponse
// @Router /api/agent-observability/v1/traces/{trace_id}/trace-graph [get]
func (h *TraceHandler) GetTraceGraphByTraceID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code:    "METHOD_NOT_ALLOWED",
			Message: "only GET is supported",
		})
		return
	}

	traceID := traceIDFromTraceGraphPath(r.URL.Path)
	if traceID == "" {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "trace_id is required",
		})
		return
	}

	response, found, err := h.traceQueryService.GetTraceGraphByTraceID(r.Context(), traceID)
	if err != nil {
		writeJSON(w, http.StatusGatewayTimeout, rdto.ErrorResponse{
			Code:    "QUERY_FAILED",
			Message: "failed to query trace graph",
		})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{
			Code:    "NOT_FOUND",
			Message: "trace graph not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func traceIDFromTraceGraphPath(path string) string {
	const prefix = "/api/agent-observability/v1/traces/"
	const suffix = "/trace-graph"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	traceID := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	traceID = strings.Trim(traceID, "/")
	if strings.Contains(traceID, "/") {
		return ""
	}
	return strings.TrimSpace(traceID)
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}
