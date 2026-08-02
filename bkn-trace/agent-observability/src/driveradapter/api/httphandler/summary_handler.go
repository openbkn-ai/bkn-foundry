package httphandler

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

// ListBusinessProvenanceConversations godoc
// @Summary List observable business conversations
// @Description Returns one authorized business-provenance row per conversation, aggregated from real interactions and requests.
// @Tags business-provenance
// @Produce json
// @Param limit query int false "Page size, 1..200"
// @Param cursor query string false "Opaque pagination cursor"
// @Param from query string false "Started at or after this RFC3339 timestamp"
// @Param to query string false "Started at or before this RFC3339 timestamp"
// @Param status query string false "Execution status"
// @Param agent_or_app query string false "Agent or application"
// @Param business_domain query string false "Business domain"
// @Param knowledge_network query string false "Knowledge network"
// @Param evidence_completeness query string false "Evidence completeness"
// @Param keyword query string false "Question, result, ID, business ref, or error keyword"
// @Success 200 {object} evidencevo.ConversationSummaryPage
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /business-provenance/conversations [get]
func (h *EvidenceHandler) ListBusinessProvenanceConversations(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	options, ok := h.summaryQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}
	page, err := h.evidenceService.ListConversations(r.Context(), options)
	if err != nil {
		writeSummaryQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// ListBusinessProvenanceInteractions godoc
// @Summary List observable business interactions
// @Description Returns one authorized business-provenance row per interaction, aggregated from real requests.
// @Tags business-provenance
// @Produce json
// @Param limit query int false "Page size, 1..200"
// @Param cursor query string false "Opaque pagination cursor"
// @Param conversation_id query string false "Conversation ID"
// @Param from query string false "Started at or after this RFC3339 timestamp"
// @Param to query string false "Started at or before this RFC3339 timestamp"
// @Param status query string false "Execution status"
// @Param agent_or_app query string false "Agent or application"
// @Param business_domain query string false "Business domain"
// @Param knowledge_network query string false "Knowledge network"
// @Param evidence_completeness query string false "Evidence completeness"
// @Param keyword query string false "Question, result, ID, business ref, or error keyword"
// @Success 200 {object} evidencevo.InteractionSummaryPage
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /business-provenance/interactions [get]
func (h *EvidenceHandler) ListBusinessProvenanceInteractions(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	options, ok := h.summaryQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}
	page, err := h.evidenceService.ListInteractions(r.Context(), options)
	if err != nil {
		writeSummaryQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// ListRequests godoc
// @Summary List observable business requests
// @Description Returns stable request summaries generated from authorized evidence and artifacts.
// @Tags requests
// @Produce json
// @Param limit query int false "Page size, 1..200"
// @Param cursor query string false "Opaque pagination cursor"
// @Param conversation_id query string false "Caller-owned conversation ID"
// @Param interaction_id query string false "One user interaction ID"
// @Param from query string false "Started at or after this RFC3339 timestamp"
// @Param to query string false "Started at or before this RFC3339 timestamp"
// @Param status query string false "Execution status"
// @Param agent_or_app query string false "Agent or application"
// @Param business_domain query string false "Business domain"
// @Param knowledge_network query string false "Knowledge network"
// @Param evidence_completeness query string false "Evidence completeness"
// @Param keyword query string false "Question, result, ID, business ref, or error keyword"
// @Success 200 {object} evidencevo.RequestSummaryPage
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
func (h *EvidenceHandler) ListRequests(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	options, ok := h.summaryQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}
	page, err := h.evidenceService.ListRequests(r.Context(), options)
	if err != nil {
		writeSummaryQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// GetRequestSummary godoc
// @Summary Get one observable business request
// @Description Returns an authorized request summary and its business-content availability.
// @Tags requests
// @Produce json
// @Param request_id path string true "BKN request ID"
// @Success 200 {object} evidencevo.RequestSummary
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
func (h *EvidenceHandler) GetRequestSummary(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	requestID := requestIDFromSummaryPath(r.URL.Path, false)
	if requestID == "" {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "request_id is required"})
		return
	}
	options, ok := h.summaryQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}
	summary, found, err := h.evidenceService.GetRequestSummary(r.Context(), requestID, options.Scope)
	if err != nil {
		writeSummaryQueryError(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{Code: "NOT_FOUND", Message: "request not found"})
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// GetInteractionSummary godoc
// @Summary Get one observable business interaction
// @Description Aggregates every authorized request and trace in one caller-owned interaction.
// @Tags interactions
// @Produce json
// @Param interaction_id path string true "BKN interaction ID"
// @Success 200 {object} evidencevo.InteractionSummary
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
func (h *EvidenceHandler) GetInteractionSummary(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	interactionID := interactionIDFromSummaryPath(r.URL.Path)
	if interactionID == "" {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "interaction_id is required"})
		return
	}
	options, ok := h.summaryQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}
	summary, found, err := h.evidenceService.GetInteractionSummary(r.Context(), interactionID, options.Scope)
	if err != nil {
		writeSummaryQueryError(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{Code: "NOT_FOUND", Message: "interaction not found"})
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// ListRequestTraces godoc
// @Summary List technical traces belonging to a business request
// @Description Supports one business request to multiple distributed traces and returns request_id on every trace.
// @Tags requests
// @Produce json
// @Param request_id path string true "BKN request ID"
// @Param limit query int false "Page size, 1..200"
// @Param cursor query string false "Opaque pagination cursor"
// @Param conversation_id query string false "Caller-owned conversation ID"
// @Param interaction_id query string false "One user interaction ID"
// @Success 200 {object} evidencevo.TraceSummaryPage
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /requests/{request_id}/traces [get]
func (h *EvidenceHandler) ListRequestTraces(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	requestID := requestIDFromSummaryPath(r.URL.Path, true)
	if requestID == "" {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "request_id is required"})
		return
	}
	options, ok := h.summaryQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}
	page, err := h.evidenceService.ListRequestTraces(r.Context(), requestID, options)
	if err != nil {
		writeSummaryQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// ListTraceExecutions godoc
// @Summary List technical trace executions
// @Description Returns stable trace summaries with reverse request links, generated from authorized trace evidence.
// @Tags traces
// @Produce json
// @Param limit query int false "Page size, 1..200"
// @Param cursor query string false "Opaque pagination cursor"
// @Param trace_id query string false "Exact trace ID; bypasses list projection scan"
// @Param conversation_id query string false "Caller-owned conversation ID"
// @Param interaction_id query string false "One user interaction ID"
// @Param from query string false "Started at or after this RFC3339 timestamp"
// @Param to query string false "Started at or before this RFC3339 timestamp"
// @Param status query string false "Execution status"
// @Param agent_or_app query string false "Agent or application"
// @Param business_domain query string false "Business domain"
// @Param keyword query string false "Trace, request, operation, or error keyword"
// @Success 200 {object} evidencevo.TraceSummaryPage
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /trace-executions [get]
func (h *EvidenceHandler) ListTraceExecutions(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	options, ok := h.summaryQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}
	page, err := h.evidenceService.ListTraceExecutions(r.Context(), options)
	if err != nil {
		writeSummaryQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *EvidenceHandler) summaryQueryOptionsFromRequest(w http.ResponseWriter, r *http.Request) (evidencevo.SummaryQueryOptions, bool) {
	if !h.authorizeQueryGateway(w, r) {
		return evidencevo.SummaryQueryOptions{}, false
	}
	scope, ok := h.queryScopeFromRequest(w, r, false)
	if !ok {
		return evidencevo.SummaryQueryOptions{}, false
	}
	options := evidencevo.SummaryQueryOptions{
		Scope: scope, Cursor: strings.TrimSpace(r.URL.Query().Get("cursor")),
		TraceID:              strings.TrimSpace(r.URL.Query().Get("trace_id")),
		ConversationID:       strings.TrimSpace(r.URL.Query().Get("conversation_id")),
		InteractionID:        strings.TrimSpace(r.URL.Query().Get("interaction_id")),
		Status:               strings.TrimSpace(r.URL.Query().Get("status")),
		AgentOrApp:           strings.TrimSpace(r.URL.Query().Get("agent_or_app")),
		BusinessDomain:       strings.TrimSpace(r.URL.Query().Get("business_domain")),
		KnowledgeNetwork:     strings.TrimSpace(r.URL.Query().Get("knowledge_network")),
		EvidenceCompleteness: strings.TrimSpace(r.URL.Query().Get("evidence_completeness")),
		Keyword:              strings.TrimSpace(r.URL.Query().Get("keyword")),
	}
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit <= 0 || limit > evidencesvc.MaxSummaryQueryLimit {
			writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "limit must be an integer between 1 and 200"})
			return evidencevo.SummaryQueryOptions{}, false
		}
		options.Limit = limit
	}
	for _, item := range []struct {
		name   string
		target *time.Time
	}{
		{"from", &options.From},
		{"to", &options.To},
	} {
		value := strings.TrimSpace(r.URL.Query().Get(item.name))
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: item.name + " must be an RFC3339 timestamp"})
			return evidencevo.SummaryQueryOptions{}, false
		}
		*item.target = parsed
	}
	if !options.From.IsZero() && !options.To.IsZero() && options.To.Before(options.From) {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "to must not be before from"})
		return evidencevo.SummaryQueryOptions{}, false
	}
	return options, true
}

func requestIDFromSummaryPath(path string, traces bool) string {
	const prefix = "/api/agent-observability/v1/requests/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	value := strings.TrimPrefix(path, prefix)
	if traces {
		if !strings.HasSuffix(value, "/traces") {
			return ""
		}
		value = strings.TrimSuffix(value, "/traces")
	} else if strings.Contains(value, "/") {
		return ""
	}
	decoded, err := url.PathUnescape(strings.Trim(value, "/"))
	if err != nil || strings.Contains(decoded, "/") {
		return ""
	}
	return strings.TrimSpace(decoded)
}

func interactionIDFromSummaryPath(path string) string {
	const prefix = "/api/agent-observability/v1/interactions/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	value := strings.TrimPrefix(path, prefix)
	decoded, err := url.PathUnescape(strings.Trim(value, "/"))
	if err != nil || decoded == "" || strings.Contains(decoded, "/") {
		return ""
	}
	return strings.TrimSpace(decoded)
}

func writeSummaryQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, evidencesvc.ErrSummaryCursorInvalid) {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "cursor is invalid"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{Code: "QUERY_FAILED", Message: "failed to query execution summaries"})
}
