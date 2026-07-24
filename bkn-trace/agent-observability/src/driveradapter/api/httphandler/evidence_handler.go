package httphandler

import (
	"crypto/subtle"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

const maxEvidenceBodyBytes = 1 << 20
const evidenceIngestTokenEnv = "BKN_TRACE_EVIDENCE_INGEST_TOKEN"
const evidenceIngestTokenHeader = "X-BKN-Trace-Ingest-Token"

type EvidenceHandler struct {
	evidenceService *evidencesvc.Service
	ingestToken     string
}

func NewEvidenceHandler(evidenceService *evidencesvc.Service) *EvidenceHandler {
	return NewEvidenceHandlerWithIngestToken(evidenceService, os.Getenv(evidenceIngestTokenEnv))
}

func NewEvidenceHandlerWithIngestToken(evidenceService *evidencesvc.Service, ingestToken string) *EvidenceHandler {
	return &EvidenceHandler{
		evidenceService: evidenceService,
		ingestToken:     strings.TrimSpace(ingestToken),
	}
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
	if !h.authorizeEvidenceIngest(w, r) {
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
// @Param limit query int false "Maximum evidence trace batches to read, 1..1000"
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

	options, ok := evidenceQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}

	response, found, err := h.evidenceService.GetEvidenceChainByTraceID(r.Context(), traceID, options)
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

func (h *EvidenceHandler) GetTraceSubresource(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/evidence-chain") {
		h.GetEvidenceChainByTraceID(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/business-graph") {
		h.GetBusinessGraphByTraceID(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/snapshot-preview") {
		h.GetSnapshotPreviewByTraceID(w, r)
		return
	}
	writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{
		Code:    "NOT_FOUND",
		Message: "trace subresource not found",
	})
}

// GetBusinessGraphByTraceID godoc
// @Summary Get business semantic graph by trace ID
// @Description Returns claim and business semantic nodes/edges derived from business.refs.resolved events.
// @Tags evidence
// @Produce json
// @Param trace_id path string true "Trace ID"
// @Param limit query int false "Maximum evidence trace batches to read, 1..1000"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /api/agent-observability/v1/traces/{trace_id}/business-graph [get]
func (h *EvidenceHandler) GetBusinessGraphByTraceID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code:    "METHOD_NOT_ALLOWED",
			Message: "only GET is supported",
		})
		return
	}

	traceID := traceIDFromBusinessGraphPath(r.URL.Path)
	if traceID == "" {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "trace_id is required",
		})
		return
	}

	options, ok := evidenceQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}

	response, found, err := h.evidenceService.GetBusinessGraphByTraceID(r.Context(), traceID, options)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{
			Code:    "QUERY_FAILED",
			Message: "failed to query business graph",
		})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{
			Code:    "NOT_FOUND",
			Message: "business graph not found",
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
// @Param limit query int false "Maximum evidence trace batches to read, 1..1000"
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

	options, ok := evidenceQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}

	response, found, err := h.evidenceService.GetEvidenceChainByRequestID(r.Context(), requestID, options)
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

// GetSnapshotPreviewByTraceID godoc
// @Summary Get evidence snapshot preview by trace ID
// @Description Returns a metadata-only governed snapshot manifest preview without creating or exposing object storage locations.
// @Tags evidence
// @Produce json
// @Param trace_id path string true "Trace ID"
// @Param limit query int false "Maximum evidence trace batches to read, 1..1000"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /api/agent-observability/v1/traces/{trace_id}/snapshot-preview [get]
func (h *EvidenceHandler) GetSnapshotPreviewByTraceID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code:    "METHOD_NOT_ALLOWED",
			Message: "only GET is supported",
		})
		return
	}

	traceID := traceIDFromSnapshotPreviewPath(r.URL.Path)
	if traceID == "" {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "trace_id is required",
		})
		return
	}

	options, ok := evidenceQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}

	response, found, err := h.evidenceService.GetSnapshotPreviewByTraceID(r.Context(), traceID, options)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{
			Code:    "QUERY_FAILED",
			Message: "failed to query snapshot preview",
		})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{
			Code:    "NOT_FOUND",
			Message: "snapshot preview not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// GetSnapshotPreviewByRequestID godoc
// @Summary Get evidence snapshot preview by BKN request ID
// @Description Returns a metadata-only governed snapshot manifest preview without creating or exposing object storage locations.
// @Tags evidence
// @Produce json
// @Param request_id query string true "BKN request ID"
// @Param limit query int false "Maximum evidence trace batches to read, 1..1000"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /api/agent-observability/v1/traces/by-request/snapshot-preview [get]
func (h *EvidenceHandler) GetSnapshotPreviewByRequestID(w http.ResponseWriter, r *http.Request) {
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

	options, ok := evidenceQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}

	response, found, err := h.evidenceService.GetSnapshotPreviewByRequestID(r.Context(), requestID, options)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{
			Code:    "QUERY_FAILED",
			Message: "failed to query snapshot preview",
		})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{
			Code:    "NOT_FOUND",
			Message: "snapshot preview not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// GetBusinessGraphByRequestID godoc
// @Summary Get business semantic graph by BKN request ID
// @Description Returns claim and business semantic nodes/edges derived from business.refs.resolved events.
// @Tags evidence
// @Produce json
// @Param request_id query string true "BKN request ID"
// @Param limit query int false "Maximum evidence trace batches to read, 1..1000"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /api/agent-observability/v1/traces/by-request/business-graph [get]
func (h *EvidenceHandler) GetBusinessGraphByRequestID(w http.ResponseWriter, r *http.Request) {
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

	options, ok := evidenceQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}

	response, found, err := h.evidenceService.GetBusinessGraphByRequestID(r.Context(), requestID, options)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{
			Code:    "QUERY_FAILED",
			Message: "failed to query business graph",
		})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{
			Code:    "NOT_FOUND",
			Message: "business graph not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// GetEvidenceNode godoc
// @Summary Get evidence node details
// @Description Returns one visible claim, evidence ref, or business ref node scoped by trace_id or request_id.
// @Tags evidence
// @Produce json
// @Param node_id path string true "Evidence node ID, for example claim:claim_001"
// @Param trace_id query string false "Trace ID scope"
// @Param request_id query string false "BKN request ID scope"
// @Param limit query int false "Maximum evidence trace batches to read, 1..1000"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /api/agent-observability/v1/evidence-nodes/{node_id} [get]
func (h *EvidenceHandler) GetEvidenceNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code:    "METHOD_NOT_ALLOWED",
			Message: "only GET is supported",
		})
		return
	}

	nodeID := evidenceNodeIDFromPath(r.URL.Path)
	if nodeID == "" {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "node_id is required",
		})
		return
	}

	options, ok := evidenceQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}

	traceID := strings.TrimSpace(r.URL.Query().Get("trace_id"))
	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	if (traceID == "" && requestID == "") || (traceID != "" && requestID != "") {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "exactly one of trace_id or request_id is required",
		})
		return
	}

	var (
		response evidencevo.EvidenceNodeResponse
		found    bool
		err      error
	)
	if traceID != "" {
		response, found, err = h.evidenceService.GetEvidenceNodeByTraceID(r.Context(), traceID, nodeID, options)
	} else {
		response, found, err = h.evidenceService.GetEvidenceNodeByRequestID(r.Context(), requestID, nodeID, options)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{
			Code:    "QUERY_FAILED",
			Message: "failed to query evidence node",
		})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{
			Code:    "NOT_FOUND",
			Message: "evidence node not found",
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

func evidenceNodeIDFromPath(path string) string {
	const prefix = "/api/agent-observability/v1/evidence-nodes/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	nodeID, err := url.PathUnescape(strings.TrimPrefix(path, prefix))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(nodeID)
}

func evidenceQueryOptionsFromRequest(w http.ResponseWriter, r *http.Request) (evidencevo.EvidenceQueryOptions, bool) {
	rawLimit := strings.TrimSpace(r.URL.Query().Get("limit"))
	if rawLimit == "" {
		return evidencevo.EvidenceQueryOptions{}, true
	}
	limit, err := strconv.Atoi(rawLimit)
	if err != nil || limit <= 0 || limit > evidencesvc.MaxEvidenceQueryLimit {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "limit must be an integer between 1 and 1000",
		})
		return evidencevo.EvidenceQueryOptions{}, false
	}
	return evidencevo.EvidenceQueryOptions{Limit: limit}, true
}

func traceIDFromBusinessGraphPath(path string) string {
	const prefix = "/api/agent-observability/v1/traces/"
	const suffix = "/business-graph"
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

func traceIDFromSnapshotPreviewPath(path string) string {
	const prefix = "/api/agent-observability/v1/traces/"
	const suffix = "/snapshot-preview"
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

func (h *EvidenceHandler) authorizeEvidenceIngest(w http.ResponseWriter, r *http.Request) bool {
	if h.ingestToken == "" {
		return true
	}
	if secureTokenEqual(r.Header.Get(evidenceIngestTokenHeader), h.ingestToken) {
		return true
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authHeader) > len("Bearer ") && strings.EqualFold(authHeader[:len("Bearer ")], "Bearer ") {
		if secureTokenEqual(strings.TrimSpace(authHeader[len("Bearer "):]), h.ingestToken) {
			return true
		}
	}

	writeJSON(w, http.StatusUnauthorized, rdto.ErrorResponse{
		Code:    "UNAUTHORIZED",
		Message: "evidence ingest authentication is required",
	})
	return false
}

func secureTokenEqual(actual, expected string) bool {
	actual = strings.TrimSpace(actual)
	expected = strings.TrimSpace(expected)
	if actual == "" || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
