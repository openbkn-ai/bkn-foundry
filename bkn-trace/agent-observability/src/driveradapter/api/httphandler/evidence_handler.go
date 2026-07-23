package httphandler

import (
	"io"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

const maxEvidenceBodyBytes = 1 << 20

type EvidenceHandler struct {
	evidenceService *evidencesvc.Service
}

func NewEvidenceHandler(evidenceService *evidencesvc.Service) *EvidenceHandler {
	return &EvidenceHandler{evidenceService: evidenceService}
}

// IngestEvidenceEvents godoc
// @Summary Ingest BKN Trace phase-two evidence events
// @Description Accepts claim.created, evidence.refs.created, and business.refs.resolved events and stores the normalized evidence model.
// @Tags evidence
// @Accept json
// @Produce json
// @Param request body string true "BKN Trace phase-two evidence event batch"
// @Success 202 {object} map[string]interface{}
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /api/agent-observability/v1/evidence/events [post]
func (h *EvidenceHandler) IngestEvidenceEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code:    "METHOD_NOT_ALLOWED",
			Message: "only POST is supported",
		})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEvidenceBodyBytes))
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
			Message: "evidence event body is required",
		})
		return
	}

	response, validationErrors, err := h.evidenceService.Ingest(r.Context(), body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{
			Code:    "INGEST_FAILED",
			Message: "failed to ingest evidence events",
		})
		return
	}
	if len(validationErrors) > 0 {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    validationErrors[0].Code,
			Message: validationErrors[0].Message,
			Details: validationErrors,
		})
		return
	}

	writeJSON(w, http.StatusAccepted, response)
}

// GetEvidenceChainByTraceID godoc
// @Summary Get evidence chain by trace ID
// @Description Returns normalized claim, evidence refs, business refs, pagination, partial reasons, and visibility summary for a trace.
// @Tags evidence
// @Produce json
// @Param trace_id path string true "Trace ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /api/agent-observability/v1/traces/{trace_id}/evidence-chain [get]
func (h *EvidenceHandler) GetEvidenceChainByTraceID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code:    "METHOD_NOT_ALLOWED",
			Message: "only GET is supported",
		})
		return
	}

	traceID := traceIDFromEvidenceChainPath(r.URL.Path)
	if traceID == "" {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "trace_id is required",
		})
		return
	}

	response, found, err := h.evidenceService.GetEvidenceChainByTraceID(r.Context(), traceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{
			Code:    "QUERY_FAILED",
			Message: "failed to query evidence chain",
		})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{
			Code:    "NOT_FOUND",
			Message: "evidence chain not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// GetEvidenceChainByRequestID godoc
// @Summary Get evidence chain by BKN request ID
// @Description Returns normalized claim, evidence refs, business refs, pagination, partial reasons, and visibility summary for a request.
// @Tags evidence
// @Produce json
// @Param request_id query string true "BKN request ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /api/agent-observability/v1/traces/by-request [get]
func (h *EvidenceHandler) GetEvidenceChainByRequestID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code:    "METHOD_NOT_ALLOWED",
			Message: "only GET is supported",
		})
		return
	}

	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	if requestID == "" {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "request_id is required",
		})
		return
	}

	response, found, err := h.evidenceService.GetEvidenceChainByRequestID(r.Context(), requestID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{
			Code:    "QUERY_FAILED",
			Message: "failed to query evidence chain",
		})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{
			Code:    "NOT_FOUND",
			Message: "evidence chain not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func traceIDFromEvidenceChainPath(path string) string {
	const prefix = "/api/agent-observability/v1/traces/"
	const suffix = "/evidence-chain"
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
