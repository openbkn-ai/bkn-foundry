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
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

// ListTraceRootServices godoc
// @Summary Discover authorized technical Trace root service options
// @Description On-demand bounded discovery using the current Trace filters and authorization. The service filter itself is excluded. No Span counts or question/result bodies are loaded. Results contain at most 100 canonical names; has_more asks the caller to narrow q. A failed or budget-limited scan never returns a partial catalog.
// @Tags traces
// @Produce json
// @Param from query string false "Started at or after this RFC3339 timestamp"
// @Param to query string false "Started at or before this RFC3339 timestamp"
// @Param trace_id query string false "Exact Trace ID"
// @Param status query string false "Execution status"
// @Param tool query string false "Exact root operation"
// @Param error_keyword query string false "Recorded error summary substring"
// @Param q query string false "Case-insensitive service-name substring; at most 256 Unicode characters"
// @Param limit query int false "Maximum options, 1..100; default 100"
// @Success 200 {object} evidencevo.TraceRootServiceOptions
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 422 {object} rdto.ErrorResponse
// @Failure 503 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /trace-root-services [get]
func (h *EvidenceHandler) ListTraceRootServices(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	options, ok := h.summaryQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	valid := err == nil
	for name, items := range values {
		switch name {
		case "from", "to", "trace_id", "status", "tool", "error_keyword", "q", "limit", "service":
		default:
			valid = false
		}
		if len(items) != 1 {
			valid = false
		}
	}
	search := strings.TrimSpace(values.Get("q"))
	if !valid || !utf8.ValidString(search) || utf8.RuneCountInString(search) > 256 || options.Limit > evidencesvc.MaxTraceRootServiceOptions {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "root service option parameters are invalid"})
		return
	}
	limit := options.Limit
	options.Limit = 0
	options.Scope.View = evidencevo.AccessViewTechnical
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	result, err := h.evidenceService.ListTraceRootServices(ctx, options, search, limit)
	if err == nil {
		err = ctx.Err()
	}
	switch {
	case errors.Is(err, evidencesvc.ErrTraceRootServicesUnavailable):
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "ROOT_SERVICE_OPTIONS_UNAVAILABLE", Message: "root service options are unavailable"})
	case errors.Is(err, evidencesvc.ErrTraceRootServicesUnsupported):
		writeJSON(w, r, http.StatusUnprocessableEntity, rdto.ErrorResponse{Code: "ROOT_SERVICE_OPTIONS_FILTERS_UNSUPPORTED", Message: "root service options do not support selected filters"})
	case errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) && !errors.Is(err, context.DeadlineExceeded):
		writeJSON(w, r, http.StatusUnprocessableEntity, rdto.ErrorResponse{Code: "SUMMARY_QUERY_BUDGET_EXCEEDED", Message: "root service options exceed query budget; narrow the time range or filters"})
	case err != nil:
		writeSummaryQueryError(w, r, err)
	default:
		writeJSON(w, r, http.StatusOK, result)
	}
}
