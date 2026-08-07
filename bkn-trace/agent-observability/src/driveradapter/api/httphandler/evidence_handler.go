package httphandler

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iauthorizationscope"
)

const maxEvidenceBodyBytes = 1 << 20
const evidenceIngestTokenEnv = "BKN_TRACE_EVIDENCE_INGEST_TOKEN"
const evidenceAllowUnauthenticatedIngestEnv = "BKN_TRACE_ALLOW_UNAUTHENTICATED_INGEST"
const evidenceIngestTokenHeader = "X-BKN-Trace-Ingest-Token"
const evidenceAllowUnauthenticatedQueryEnv = "BKN_TRACE_ALLOW_UNAUTHENTICATED_QUERY"
const evidenceHydraAdminURLEnv = "BKN_TRACE_HYDRA_ADMIN_URL"
const evidenceDeploymentTenantIDEnv = "BKN_TRACE_DEPLOYMENT_TENANT_ID"

type EvidenceHandlerSecurityConfig struct {
	IngestToken                string
	HydraAdminURL              string
	DeploymentTenantID         string
	QueryHTTPClient            *http.Client
	AllowUnauthenticatedIngest bool
	AllowUnauthenticatedQuery  bool
	AuthorizationScopeResolver iauthorizationscope.Resolver
}

type EvidenceHandler struct {
	evidenceService            *evidencesvc.Service
	ingestToken                string
	hydraAdminURL              string
	deploymentTenantID         string
	queryHTTPClient            *http.Client
	allowUnauthenticatedIngest bool
	allowUnauthenticatedQuery  bool
	authorizationScopeResolver iauthorizationscope.Resolver
}

type trustedQueryScopeContextKey struct{}
type trustedInternalCallerContextKey struct{}

func trustedQueryScopeFromContext(ctx context.Context) (evidencevo.QueryScope, bool) {
	scope, ok := ctx.Value(trustedQueryScopeContextKey{}).(evidencevo.QueryScope)
	return scope, ok
}

func NewEvidenceHandler(evidenceService *evidencesvc.Service) *EvidenceHandler {
	return NewEvidenceHandlerWithAuthorizationScopeResolver(evidenceService, nil)
}

func NewEvidenceHandlerWithAuthorizationScopeResolver(
	evidenceService *evidencesvc.Service,
	resolver iauthorizationscope.Resolver,
) *EvidenceHandler {
	allowUnauthenticated := strings.EqualFold(strings.TrimSpace(os.Getenv(evidenceAllowUnauthenticatedIngestEnv)), "true")
	allowUnauthenticatedQuery := strings.EqualFold(strings.TrimSpace(os.Getenv(evidenceAllowUnauthenticatedQueryEnv)), "true")
	return NewEvidenceHandlerWithSecurityConfig(evidenceService, EvidenceHandlerSecurityConfig{
		IngestToken:                os.Getenv(evidenceIngestTokenEnv),
		HydraAdminURL:              os.Getenv(evidenceHydraAdminURLEnv),
		DeploymentTenantID:         os.Getenv(evidenceDeploymentTenantIDEnv),
		AllowUnauthenticatedIngest: allowUnauthenticated,
		AllowUnauthenticatedQuery:  allowUnauthenticatedQuery,
		AuthorizationScopeResolver: resolver,
	})
}

func NewEvidenceHandlerWithIngestToken(evidenceService *evidencesvc.Service, ingestToken string) *EvidenceHandler {
	return NewEvidenceHandlerWithSecurity(evidenceService, ingestToken, false)
}

func NewEvidenceHandlerWithSecurity(evidenceService *evidencesvc.Service, ingestToken string, allowUnauthenticatedIngest bool) *EvidenceHandler {
	return NewEvidenceHandlerWithSecurityConfig(evidenceService, EvidenceHandlerSecurityConfig{
		IngestToken: ingestToken, AllowUnauthenticatedIngest: allowUnauthenticatedIngest,
	})
}

func NewEvidenceHandlerWithSecurityConfig(evidenceService *evidencesvc.Service, config EvidenceHandlerSecurityConfig) *EvidenceHandler {
	queryHTTPClient := config.QueryHTTPClient
	if queryHTTPClient == nil {
		queryHTTPClient = &http.Client{Timeout: 3 * time.Second}
	}
	return &EvidenceHandler{
		evidenceService:            evidenceService,
		ingestToken:                strings.TrimSpace(config.IngestToken),
		hydraAdminURL:              strings.TrimRight(strings.TrimSpace(config.HydraAdminURL), "/"),
		deploymentTenantID:         strings.TrimSpace(config.DeploymentTenantID),
		queryHTTPClient:            queryHTTPClient,
		allowUnauthenticatedIngest: config.AllowUnauthenticatedIngest,
		allowUnauthenticatedQuery:  config.AllowUnauthenticatedQuery,
		authorizationScopeResolver: config.AuthorizationScopeResolver,
	}
}

// IngestEvidenceEvents godoc
// @Summary Ingest BKN Trace phase-two evidence events
// @Description Accepts claim.created, evidence.refs.created, and business.refs.resolved events and stores the normalized evidence model.
// @Tags evidence
// @Accept json
// @Produce json
// @Param request body string true "BKN Trace phase-two evidence event batch"
// @Success 202 {object} evidencevo.IngestResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
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

// IngestEvidenceArtifact godoc
// @Summary Ingest a BKN Trace 2.2 evidence artifact
// @Description Stores authorized business content separately from evidence events. Inline content and snapshot_ref are mutually exclusive.
// @Tags evidence
// @Accept json
// @Produce json
// @Param request body evidencevo.EvidenceArtifact true "BKN Trace 2.2 evidence artifact"
// @Success 201 {object} evidencevo.ArtifactIngestResponse
// @Success 200 {object} evidencevo.ArtifactIngestResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 409 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /evidence/artifacts [post]
func (h *EvidenceHandler) IngestEvidenceArtifact(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only POST is supported"})
		return
	}
	if !h.authorizeEvidenceIngest(w, r) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEvidenceBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "failed to read request body"})
		return
	}
	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "evidence artifact body is required"})
		return
	}
	response, validationErrors, err := h.evidenceService.IngestArtifact(r.Context(), body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{Code: "INGEST_FAILED", Message: "failed to ingest evidence artifact"})
		return
	}
	if len(validationErrors) > 0 {
		status := http.StatusBadRequest
		if validationErrors[0].Code == "BKN_TRACE_ARTIFACT_ID_CONFLICT" {
			status = http.StatusConflict
		}
		writeJSON(w, status, rdto.ErrorResponse{
			Code: validationErrors[0].Code, Message: validationErrors[0].Message, Details: validationErrors,
		})
		return
	}
	status := http.StatusOK
	if response.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, response)
}

// GetEvidenceArtifact godoc
// @Summary Get an authorized evidence artifact by ID
// @Description Returns an artifact through its own record scope, or through an authorized Interaction when interaction_id is supplied.
// @Tags evidence
// @Produce json
// @Param artifact_id path string true "Artifact ID"
// @Param interaction_id query string false "Authorized Interaction ID"
// @Success 200 {object} evidencevo.EvidenceArtifact
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /evidence/artifacts/{artifact_id} [get]
func (h *EvidenceHandler) GetEvidenceArtifact(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	artifactID := artifactIDFromPath(r.URL.Path)
	if artifactID == "" {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "artifact_id is required"})
		return
	}
	options, ok := h.evidenceQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}
	artifact, found, err := h.evidenceService.GetArtifactForInteraction(
		r.Context(), artifactID, strings.TrimSpace(r.URL.Query().Get("interaction_id")), options.Scope,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{Code: "QUERY_FAILED", Message: "failed to query evidence artifact"})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{Code: "NOT_FOUND", Message: "evidence artifact not found"})
		return
	}
	writeJSON(w, http.StatusOK, artifact)
}

func artifactIDFromPath(path string) string {
	const prefix = "/api/agent-observability/v1/evidence/artifacts/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	artifactID, err := url.PathUnescape(strings.TrimPrefix(path, prefix))
	if err != nil || !evidencevo.ValidArtifactID(artifactID) {
		return ""
	}
	return artifactID
}

// GetEvidenceChainByTraceID godoc
// @Summary Get evidence chain by trace ID
// @Description Returns normalized claim, evidence refs, business refs, pagination, partial reasons, and visibility summary for a trace.
// @Tags evidence
// @Produce json
// @Param trace_id path string true "Trace ID"
// @Param limit query int false "Maximum evidence trace batches to read, 1..1000"
// @Success 200 {object} evidencevo.EvidenceChainResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /traces/{trace_id}/evidence-chain [get]
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

	options, ok := h.evidenceQueryOptionsFromRequest(w, r)
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

// SearchEvidenceByTrace godoc
// @Summary Query evidence chain by trace ID or BKN request ID
// @Description Backward-compatible endpoint for SDK/Studio callers. Returns the normalized evidence-chain response for trace_id or request_id.
// @Tags evidence
// @Produce json
// @Param trace_id query string false "Trace ID"
// @Param request_id query string false "BKN request ID"
// @Param limit query int false "Maximum evidence trace batches to read, 1..1000"
// @Success 200 {object} evidencevo.EvidenceChainResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /evidence/by-trace [get]
func (h *EvidenceHandler) SearchEvidenceByTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code:    "METHOD_NOT_ALLOWED",
			Message: "only GET is supported",
		})
		return
	}

	traceID := strings.TrimSpace(r.URL.Query().Get("trace_id"))
	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	if (traceID == "") == (requestID == "") {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "exactly one of trace_id or request_id is required",
		})
		return
	}

	options, ok := h.evidenceQueryOptionsFromRequest(w, r)
	if !ok {
		return
	}

	var (
		response evidencevo.EvidenceChainResponse
		found    bool
		err      error
	)
	if traceID != "" {
		response, found, err = h.evidenceService.GetEvidenceChainByTraceID(r.Context(), traceID, options)
	} else {
		response, found, err = h.evidenceService.GetEvidenceChainByRequestID(r.Context(), requestID, options)
	}
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
// @Success 200 {object} evidencevo.BusinessGraphResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /traces/{trace_id}/business-graph [get]
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

	options, ok := h.evidenceQueryOptionsFromRequest(w, r)
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
// @Success 200 {object} evidencevo.EvidenceChainResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /traces/by-request [get]
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

	options, ok := h.evidenceQueryOptionsFromRequest(w, r)
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
// @Success 200 {object} evidencevo.SnapshotPreviewResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /traces/{trace_id}/snapshot-preview [get]
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

	options, ok := h.evidenceQueryOptionsFromRequest(w, r)
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
// @Success 200 {object} evidencevo.SnapshotPreviewResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /traces/by-request/snapshot-preview [get]
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

	options, ok := h.evidenceQueryOptionsFromRequest(w, r)
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
// @Success 200 {object} evidencevo.BusinessGraphResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /traces/by-request/business-graph [get]
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

	options, ok := h.evidenceQueryOptionsFromRequest(w, r)
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
// @Success 200 {object} evidencevo.EvidenceNodeResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 500 {object} rdto.ErrorResponse
// @Router /evidence-nodes/{node_id} [get]
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

	options, ok := h.evidenceQueryOptionsFromRequest(w, r)
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

func (h *EvidenceHandler) evidenceQueryOptionsFromRequest(w http.ResponseWriter, r *http.Request) (evidencevo.EvidenceQueryOptions, bool) {
	if !h.authorizeQueryGateway(w, r) {
		return evidencevo.EvidenceQueryOptions{}, false
	}
	scope, ok := h.queryScopeFromRequest(w, r, false)
	if !ok {
		return evidencevo.EvidenceQueryOptions{}, false
	}
	rawLimit := strings.TrimSpace(r.URL.Query().Get("limit"))
	if rawLimit == "" {
		return evidencevo.EvidenceQueryOptions{Scope: scope}, true
	}
	limit, err := strconv.Atoi(rawLimit)
	if err != nil || limit <= 0 || limit > evidencesvc.MaxEvidenceQueryLimit {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code:    "INVALID_ARGUMENT",
			Message: "limit must be an integer between 1 and 1000",
		})
		return evidencevo.EvidenceQueryOptions{}, false
	}
	return evidencevo.EvidenceQueryOptions{Limit: limit, Scope: scope}, true
}

func (h *EvidenceHandler) queryScopeFromRequest(w http.ResponseWriter, r *http.Request, skipSubjectAuthorization bool) (evidencevo.QueryScope, bool) {
	if scope, ok := trustedQueryScopeFromContext(r.Context()); ok {
		return scope, true
	}
	accountID := strings.TrimSpace(r.Header.Get("x-account-id"))
	accountType := strings.TrimSpace(r.Header.Get("x-account-type"))
	tenantID := strings.TrimSpace(r.Header.Get("x-tenant-id"))
	if tenantID == "" {
		tenantID = strings.TrimSpace(r.Header.Get("x-bkn-tenant-id"))
	}
	if h.deploymentTenantID != "" {
		if tenantID != "" && tenantID != h.deploymentTenantID {
			writeQueryAuthorizationError(w, r, http.StatusUnauthorized, "QUERY_IDENTITY_MISMATCH", "request tenant does not match the deployment tenant")
			return evidencevo.QueryScope{}, false
		}
		tenantID = h.deploymentTenantID
	}
	businessDomain := strings.TrimSpace(r.Header.Get("x-business-domain"))
	if accountID == "" || accountType == "" || strings.EqualFold(accountType, "anonymous") || tenantID == "" && businessDomain == "" {
		writeQueryAuthorizationError(w, r, http.StatusUnauthorized, "QUERY_IDENTITY_REQUIRED", "trusted account and tenant or business domain context is required")
		return evidencevo.QueryScope{}, false
	}
	scope := evidencevo.QueryScope{
		TenantID: tenantID, BusinessDomain: businessDomain, AccountID: accountID, AccountType: accountType,
		Authorization: strings.TrimSpace(r.Header.Get("Authorization")),
		// Evidence and assembly queries retain the business view. Technical view is
		// selected only by the unified log service after record-scope authorization.
		View: evidencevo.AccessViewBusiness,
	}
	if skipSubjectAuthorization {
		return scope, true
	}
	if h.authorizationScopeResolver != nil {
		effectiveSubjectID := strings.TrimSpace(r.Header.Get("X-BKN-Effective-Subject-ID"))
		if effectiveSubjectID == "" {
			effectiveSubjectID = accountID
		}
		applicationPrincipalID := strings.TrimSpace(r.Header.Get("X-BKN-Application-Principal-ID"))
		if applicationPrincipalID == "" && (accountType == "app" || accountType == "service") {
			applicationPrincipalID = accountID
		}
		profile, err := h.authorizationScopeResolver.Resolve(r.Context(), scope.Authorization, iauthorizationscope.TrustedIdentity{
			TenantID: tenantID, BusinessDomain: businessDomain, ActorID: accountID,
			EffectiveSubjectID:     effectiveSubjectID,
			ApplicationPrincipalID: applicationPrincipalID,
			DelegationID:           strings.TrimSpace(r.Header.Get("X-BKN-Delegation-ID")),
		})
		if err != nil {
			writeQueryAuthorizationError(w, r, http.StatusUnauthorized, "QUERY_ACCESS_DENIED", "current account and record scope could not be authorized")
			return evidencevo.QueryScope{}, false
		}
		scope.AccessProfile = &profile
		if profile.EffectiveSubjectID != "" {
			scope.AccountID = profile.EffectiveSubjectID
		}
	}
	return scope, true
}

func (h *EvidenceHandler) RequireTrustedQueryIdentity(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.authorizeQueryGateway(w, r) {
			return
		}
		scope, ok := h.queryScopeFromRequest(w, r, false)
		if !ok {
			return
		}
		ctx := context.WithValue(r.Context(), trustedQueryScopeContextKey{}, scope)
		next(w, r.WithContext(ctx))
	}
}

func (h *EvidenceHandler) RequireTrustedLifecycleIdentity(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isTrustedInternalCaller(r.Context()) {
			writeLifecycleError(
				w, r, http.StatusUnauthorized, "permission_denied",
				"managed lifecycle requests are accepted only through the internal OpenBKN gateway",
			)
			return
		}
		scope, ok := h.queryScopeFromRequest(w, r, true)
		if !ok {
			return
		}
		if scope.TenantID == "" || scope.BusinessDomain == "" {
			writeLifecycleError(
				w, r, http.StatusUnauthorized, "permission_denied",
				"trusted tenant and business domain context is required",
			)
			return
		}
		r.Header.Set("X-BKN-Tenant-ID", scope.TenantID)
		r.Header.Set("X-Business-Domain-ID", scope.BusinessDomain)
		if _, ok := trustedOwnerFromRequest(r); !ok {
			writeLifecycleError(
				w, r, http.StatusUnauthorized, "permission_denied",
				"trusted gateway must provide the complete lifecycle owner identity",
			)
			return
		}

		ctx := context.WithValue(r.Context(), trustedQueryScopeContextKey{}, scope)
		next(w, r.WithContext(ctx))
	}
}

// InternalLifecycle marks requests accepted by the private Core listener.
// It is intentionally applied only while registering the internal listener;
// request headers can never grant this privilege.
func (h *EvidenceHandler) InternalLifecycle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), trustedInternalCallerContextKey{}, true)
		next(w, r.WithContext(ctx))
	}
}

func isTrustedInternalCaller(ctx context.Context) bool {
	trusted, _ := ctx.Value(trustedInternalCallerContextKey{}).(bool)
	return trusted
}

func (h *EvidenceHandler) authorizeQueryGateway(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := trustedQueryScopeFromContext(r.Context()); ok {
		return true
	}
	if h.hydraAdminURL != "" && strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		return h.authorizeOAuthQuery(w, r)
	}
	if h.allowUnauthenticatedQuery {
		return true
	}
	if h.hydraAdminURL != "" {
		return h.authorizeOAuthQuery(w, r)
	}
	writeQueryAuthorizationError(w, r, http.StatusServiceUnavailable, "QUERY_AUTH_NOT_CONFIGURED", "OAuth query authentication is not configured")
	return false
}

type hydraIntrospectionResponse struct {
	Active   bool   `json:"active"`
	Subject  string `json:"sub"`
	ClientID string `json:"client_id"`
	Ext      struct {
		VisitorType string `json:"visitor_type"`
	} `json:"ext"`
}

func (h *EvidenceHandler) authorizeOAuthQuery(w http.ResponseWriter, r *http.Request) bool {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authorization) <= len("Bearer ") || !strings.EqualFold(authorization[:len("Bearer ")], "Bearer ") {
		writeQueryAuthorizationError(w, r, http.StatusUnauthorized, "QUERY_OAUTH_REQUIRED", "active OAuth bearer token is required")
		return false
	}
	token := strings.TrimSpace(authorization[len("Bearer "):])
	if token == "" {
		writeQueryAuthorizationError(w, r, http.StatusUnauthorized, "QUERY_OAUTH_REQUIRED", "active OAuth bearer token is required")
		return false
	}

	form := url.Values{"token": []string{token}}.Encode()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.hydraAdminURL+"/admin/oauth2/introspect", bytes.NewBufferString(form))
	if err != nil {
		writeQueryAuthorizationError(w, r, http.StatusServiceUnavailable, "QUERY_OAUTH_UNAVAILABLE", "OAuth introspection is unavailable")
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.queryHTTPClient.Do(req)
	if err != nil {
		writeQueryAuthorizationError(w, r, http.StatusServiceUnavailable, "QUERY_OAUTH_UNAVAILABLE", "OAuth introspection is unavailable")
		return false
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		writeQueryAuthorizationError(w, r, http.StatusServiceUnavailable, "QUERY_OAUTH_UNAVAILABLE", "OAuth introspection is unavailable")
		return false
	}

	var introspection hydraIntrospectionResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&introspection); err != nil {
		writeQueryAuthorizationError(w, r, http.StatusServiceUnavailable, "QUERY_OAUTH_UNAVAILABLE", "OAuth introspection is unavailable")
		return false
	}
	accountID := strings.TrimSpace(introspection.Subject)
	accountType := normalizedOAuthAccountType(introspection)
	if !introspection.Active || accountID == "" || accountType == "" {
		writeQueryAuthorizationError(w, r, http.StatusUnauthorized, "QUERY_OAUTH_REQUIRED", "active OAuth bearer token is required")
		return false
	}
	if supplied := strings.TrimSpace(r.Header.Get("x-account-id")); supplied != "" && supplied != accountID {
		writeQueryAuthorizationError(w, r, http.StatusUnauthorized, "QUERY_IDENTITY_MISMATCH", "request identity does not match OAuth token")
		return false
	}
	if supplied := strings.TrimSpace(r.Header.Get("x-account-type")); supplied != "" && !strings.EqualFold(supplied, accountType) {
		writeQueryAuthorizationError(w, r, http.StatusUnauthorized, "QUERY_IDENTITY_MISMATCH", "request identity does not match OAuth token")
		return false
	}
	expectedApplicationPrincipalID := ""
	if accountType == "app" || accountType == "service" {
		expectedApplicationPrincipalID = accountID
	}
	for _, identity := range []struct {
		header   string
		expected string
	}{
		{header: "X-BKN-Effective-Subject-ID", expected: accountID},
		{header: "X-BKN-Application-Principal-ID", expected: expectedApplicationPrincipalID},
		{header: "X-BKN-Delegation-ID", expected: ""},
	} {
		if supplied := strings.TrimSpace(r.Header.Get(identity.header)); supplied != "" && supplied != identity.expected {
			writeQueryAuthorizationError(w, r, http.StatusUnauthorized, "QUERY_IDENTITY_MISMATCH", "request delegation identity does not match OAuth token")
			return false
		}
	}
	r.Header.Set("x-account-id", accountID)
	r.Header.Set("x-account-type", accountType)
	r.Header.Set("X-BKN-Effective-Subject-ID", accountID)
	if expectedApplicationPrincipalID == "" {
		r.Header.Del("X-BKN-Application-Principal-ID")
	} else {
		r.Header.Set("X-BKN-Application-Principal-ID", expectedApplicationPrincipalID)
	}
	r.Header.Del("X-BKN-Delegation-ID")
	r.Header.Set("X-BKN-Authenticated-Client-ID", strings.TrimSpace(introspection.ClientID))
	return true
}

func writeQueryAuthorizationError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if strings.HasPrefix(r.URL.Path, "/api/observability/v1/") {
		writeObservabilityError(w, r, status, strings.ToLower(code), message)
		return
	}
	writeJSON(w, status, rdto.ErrorResponse{Code: code, Message: message})
}

func normalizedOAuthAccountType(response hydraIntrospectionResponse) string {
	if response.Subject != "" && response.Subject == response.ClientID {
		return "app"
	}
	switch strings.ToLower(strings.TrimSpace(response.Ext.VisitorType)) {
	case "user", "realname":
		return "user"
	case "app":
		return "app"
	default:
		return ""
	}
}

func (h *EvidenceHandler) AuthorizeTechnicalTraceQuery(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/api/agent-observability/v1/traces/"
	const suffix = "/trace-graph"
	traceID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
	traceID = strings.Trim(traceID, "/")
	if traceID == "" || strings.Contains(traceID, "/") {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ARGUMENT", Message: "trace_id is required"})
		return false
	}
	options, ok := h.evidenceQueryOptionsFromRequest(w, r)
	if !ok {
		return false
	}
	_, found, err := h.evidenceService.GetEvidenceChainByTraceID(r.Context(), traceID, options)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{Code: "QUERY_FAILED", Message: "failed to authorize trace query"})
		return false
	}
	if !found {
		page, listErr := h.evidenceService.ListRequests(r.Context(), evidencevo.SummaryQueryOptions{
			TraceID: traceID,
			Scope:   options.Scope,
			Limit:   1,
		})
		if listErr != nil {
			writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{Code: "QUERY_FAILED", Message: "failed to authorize trace query"})
			return false
		}
		found = len(page.Entries) > 0
	}
	if !found {
		writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{Code: "NOT_FOUND", Message: "trace not found"})
		return false
	}
	return true
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
		if h.allowUnauthenticatedIngest {
			return true
		}
		writeJSON(w, http.StatusServiceUnavailable, rdto.ErrorResponse{
			Code:    "INGEST_AUTH_NOT_CONFIGURED",
			Message: "evidence ingest authentication is not configured",
		})
		return false
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
