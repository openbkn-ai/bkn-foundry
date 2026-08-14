package httphandler

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

// The public registration for these pre-0.1.4 summaries was removed. The
// lower-level summary handlers remain package-private routing building blocks
// while the EE overlay reads the same authorized facts in-process.
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

func (h *EvidenceHandler) GetRequestSummary(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	requestID := requestIDFromBusinessProvenancePath(r.URL.Path, "")
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

func (h *EvidenceHandler) ListRequestTraces(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	requestID := requestIDFromBusinessProvenancePath(r.URL.Path, "traces")
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

func requestIDFromBusinessProvenancePath(path, suffix string) string {
	const prefix = "/api/agent-observability/v1/business-provenance/requests/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	value := strings.TrimPrefix(path, prefix)
	if suffix != "" {
		expectedSuffix := "/" + suffix
		if !strings.HasSuffix(value, expectedSuffix) {
			return ""
		}
		value = strings.TrimSuffix(value, expectedSuffix)
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
	const prefix = "/api/agent-observability/v1/business-provenance/interactions/"
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
