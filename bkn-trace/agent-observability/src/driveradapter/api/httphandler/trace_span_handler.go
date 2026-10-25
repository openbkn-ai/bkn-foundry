// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/oteltracevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

func traceIDFromSpanPath(path string) string {
	const prefix = "/api/agent-observability/v1/traces/"
	path = strings.TrimRight(path, "/")
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/spans") {
		return ""
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/spans")
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return strings.TrimSpace(id)
}

// GetTraceSpanPage godoc
// @Summary Browse one bounded page of technical trace spans
// @Description Requires current global trace access and canonical trace visibility on every page. Signed cursors describe live Span-ID order; pages do not enlarge the graph or claim an exact total.
// @Tags traces
// @Produce json
// @Param trace_id path string true "Trace ID"
// @Param cursor query string false "Signed continuation cursor"
// @Success 200 {object} tracesvc.SpanPage
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 403 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 413 {object} rdto.ErrorResponse
// @Failure 422 {object} rdto.ErrorResponse
// @Failure 503 {object} rdto.ErrorResponse
// @Failure 504 {object} rdto.ErrorResponse
// @Router /traces/{trace_id}/spans [get]
func (h *TraceHandler) GetTraceSpanPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	traceID := traceIDFromSpanPath(r.URL.Path)
	if traceID == "" {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "trace_id is required"})
		return
	}
	scope, ok := trustedQueryScopeFromContext(r.Context())
	if !ok {
		writeJSON(w, r, http.StatusUnauthorized, rdto.ErrorResponse{Code: "UNAUTHORIZED", Message: "trusted trace query scope is required"})
		return
	}
	scope.View = evidencevo.AccessViewTechnical
	if scope.AccessProfile == nil || !evidencevo.HasGlobalTraceAccess(*scope.AccessProfile) {
		writeJSON(w, r, http.StatusForbidden, rdto.ErrorResponse{Code: "TRACE_SPAN_SCOPE_REQUIRED", Message: "global trace access is required to browse spans"})
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeSpanCursorError(w, r)
		return
	}
	for name, v := range values {
		if name != "cursor" || len(v) != 1 {
			writeSpanCursorError(w, r)
			return
		}
	}
	after, err := decodeTraceSpanCursor(h.spanCursorKey, values.Get("cursor"), traceID, scope, time.Now())
	if err != nil {
		writeSpanCursorError(w, r)
		return
	}
	if h.traceSummaries == nil || h.operations == nil || h.traceQueryService == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "TRACE_SPAN_UNAVAILABLE", Message: "trace span browsing is unavailable"})
		return
	}
	// Reauthorize from canonical sources on every request. A valid cursor is
	// pagination state only; it never grants access to a trace.
	visible, err := h.canBrowseTechnicalTrace(r.Context(), scope, traceID)
	if contextErr := r.Context().Err(); contextErr != nil {
		err = contextErr
	}
	if err != nil {
		h.writeSpanPageError(w, r, err)
		return
	}
	if !visible {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "NOT_FOUND", Message: "trace not found"})
		return
	}
	page, err := h.traceQueryService.ListTraceSpanPage(r.Context(), traceID, after)
	if contextErr := r.Context().Err(); contextErr != nil {
		err = contextErr
	}
	if err != nil {
		h.writeSpanPageError(w, r, err)
		return
	}
	if page.TraceID != traceID {
		h.writeSpanPageError(w, r, tracesvc.ErrSpanPageIncomplete)
		return
	}
	if page.HasMore {
		cursor, err := encodeTraceSpanCursor(h.spanCursorKey, traceID, page.AfterKey, scope, time.Now())
		if err != nil {
			h.writeSpanPageError(w, r, tracesvc.ErrSpanPageIncomplete)
			return
		}
		page.NextCursor = cursor
	} else {
		page.NextCursor = ""
	}
	writeJSON(w, r, http.StatusOK, page)
}

func (h *TraceHandler) canBrowseTechnicalTrace(ctx context.Context, scope evidencevo.QueryScope, id string) (bool, error) {
	page, err := h.traceSummaries.ListTraceExecutions(ctx, evidencevo.SummaryQueryOptions{TraceID: id, Scope: scope, Limit: 1})
	if err != nil {
		return false, err
	}
	if _, found := matchingTechnicalTraceSummary(page, id); found {
		return true, nil
	}
	operations, err := h.operations.ListOperationExecutionsByTraceIDScoped(ctx, scope, id)
	if err != nil {
		return false, err
	}
	return len(matchingTraceOperations(operations, id)) > 0, nil
}

func matchingTechnicalTraceSummary(page evidencevo.TraceSummaryPage, id string) (evidencevo.TraceSummary, bool) {
	for _, summary := range page.Entries {
		if summary.TraceID == id {
			return summary, true
		}
	}
	return evidencevo.TraceSummary{}, false
}
func matchingTraceOperations(operations []sessionvo.OperationExecution, id string) []sessionvo.OperationExecution {
	result := make([]sessionvo.OperationExecution, 0, len(operations))
	for _, operation := range operations {
		if operation.Fact.TraceID == id {
			result = append(result, operation)
		}
	}
	return result
}

func writeSpanCursorError(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_CURSOR", Message: "trace span cursor is invalid or expired"})
}
func (h *TraceHandler) writeSpanPageError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := http.StatusServiceUnavailable, "TRACE_SPAN_UNAVAILABLE", "trace span browsing is unavailable"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = http.StatusGatewayTimeout, "QUERY_FAILED", "failed to query trace spans"
	case errors.Is(err, tracesvc.ErrSpanPageCursor):
		writeSpanCursorError(w, r)
		return
	case errors.Is(err, tracesvc.ErrSpanStorageUnsupported):
		status, code, message = http.StatusUnprocessableEntity, "TRACE_SPAN_FORMAT_UNSUPPORTED", "legacy trace span format does not support browsing"
	case errors.Is(err, tracesvc.ErrSpanPageBudget):
		status, code, message = http.StatusRequestEntityTooLarge, "TRACE_SPAN_BUDGET_EXCEEDED", "trace span page exceeds query budget"
	case errors.Is(err, tracesvc.ErrSpanPageIncomplete):
		code, message = "TRACE_SPAN_INCOMPLETE", "trace span search returned incomplete results"
	}
	writeJSON(w, r, status, rdto.ErrorResponse{Code: code, Message: message})
}

func (h *TraceHandler) applySpanDisplay(detail *tracesvc.TechnicalTraceDetail, global bool, graph *oteltracevo.TraceGraphResponse) {
	switch {
	case !global:
		detail.SpanDisplay.BrowseReason = "global_trace_access_required"
	case graph != nil && graph.Page.LegacyEnvelope:
		detail.SpanDisplay.BrowseReason = "legacy_span_envelope"
	case graph == nil || !h.traceQueryService.SupportsSpanBrowsing():
		detail.SpanDisplay.BrowseReason = "span_page_unavailable"
	default:
		detail.SpanDisplay.BrowseAllowed = true
	}
}
