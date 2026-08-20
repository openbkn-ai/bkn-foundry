package httphandler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

const maxSummaryQueryPage = 100

// ListTraceExecutions returns the Community technical Trace list. Business
// provenance is resolved by the mounted EE route, never by this Core handler.
//
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
// @Router /traces [get]
func (h *EvidenceHandler) ListTraceExecutions(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	options, ok := h.summaryQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}
	options.Scope.View = evidencevo.AccessViewTechnical
	page, err := h.evidenceService.ListTraceExecutions(r.Context(), options)
	if err != nil {
		writeSummaryQueryError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, page)
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
		Service:              strings.TrimSpace(r.URL.Query().Get("service")),
		Tool:                 strings.TrimSpace(r.URL.Query().Get("tool")),
		ErrorKeyword:         strings.TrimSpace(r.URL.Query().Get("error_keyword")),
		BusinessDomain:       strings.TrimSpace(r.URL.Query().Get("business_domain")),
		KnowledgeNetwork:     strings.TrimSpace(r.URL.Query().Get("knowledge_network")),
		EvidenceCompleteness: strings.TrimSpace(r.URL.Query().Get("evidence_completeness")),
		Keyword:              strings.TrimSpace(r.URL.Query().Get("keyword")),
	}
	if rawPage := strings.TrimSpace(r.URL.Query().Get("page")); rawPage != "" {
		page, err := strconv.Atoi(rawPage)
		if err != nil || page <= 0 || page > maxSummaryQueryPage {
			writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "page must be an integer between 1 and 100"})
			return evidencevo.SummaryQueryOptions{}, false
		}
		options.Page = page
	}
	if rawPageSize := strings.TrimSpace(r.URL.Query().Get("page_size")); rawPageSize != "" {
		pageSize, err := strconv.Atoi(rawPageSize)
		if err != nil || pageSize <= 0 || pageSize > evidencesvc.MaxSummaryQueryLimit {
			writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "page_size must be an integer between 1 and 200"})
			return evidencevo.SummaryQueryOptions{}, false
		}
		options.Limit = pageSize
	}
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit <= 0 || limit > evidencesvc.MaxSummaryQueryLimit {
			writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "limit must be an integer between 1 and 200"})
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
			writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: item.name + " must be an RFC3339 timestamp"})
			return evidencevo.SummaryQueryOptions{}, false
		}
		*item.target = parsed
	}
	if !options.From.IsZero() && !options.To.IsZero() && options.To.Before(options.From) {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "to must not be before from"})
		return evidencevo.SummaryQueryOptions{}, false
	}
	return options, true
}

func writeSummaryQueryError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, evidencesvc.ErrSummaryCursorInvalid) {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "cursor is invalid"})
		return
	}
	if errors.Is(err, evidencesvc.ErrSummaryProjectionLag) {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "PROJECTION_LAG", Message: "execution summary projection is catching up; retry the same page"})
		return
	}
	writeJSON(w, r, http.StatusInternalServerError, rdto.ErrorResponse{Code: "QUERY_FAILED", Message: "failed to query execution summaries"})
}
