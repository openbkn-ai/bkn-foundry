package httphandler

import (
	"io"
	"net/http"

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
		})
		return
	}

	writeJSON(w, http.StatusAccepted, response)
}
