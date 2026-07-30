package httphandler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

const maxLifecycleBodyBytes = 1 << 20

type SessionHandler struct {
	service *sessionsvc.Service
}

func NewSessionHandler(service *sessionsvc.Service) *SessionHandler {
	return &SessionHandler{service: service}
}

type lifecycleErrorEnvelope struct {
	Error lifecycleError `json:"error"`
}

type lifecycleError struct {
	Code           string `json:"code" enums:"conversation_required,conversation_not_found,conversation_closed,conversation_expired,conversation_owner_mismatch,interaction_required,interaction_in_progress,interaction_terminal,operation_required,idempotency_conflict,event_payload_conflict,producer_sequence_conflict,invalid_evidence_event,receipt_pending,terminal_conflict,closure_manifest_invalid,feature_not_installed,capability_not_licensed,permission_denied,resource_not_disclosed,internal_error"`
	Message        string `json:"message"`
	CurrentStatus  string `json:"current_status,omitempty"`
	Retryable      bool   `json:"retryable"`
	RequiredAction string `json:"required_action,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	RetryAfterMS   int    `json:"retry_after_ms"`
}

type ensureConversationRequest struct {
	ExternalConversationKey string `json:"external_conversation_key"`
	IdempotencyKey          string `json:"idempotency_key"`
	OneShot                 bool   `json:"one_shot"`
	TenantID                string `json:"tenant_id,omitempty"`
	BusinessDomainID        string `json:"business_domain_id,omitempty"`
	ApplicationPrincipalID  string `json:"application_principal_id,omitempty"`
	EffectiveSubjectType    string `json:"effective_subject_type,omitempty"`
	EffectiveSubjectID      string `json:"effective_subject_id,omitempty"`
	DelegationID            string `json:"delegation_id,omitempty"`
}

type startInteractionRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	LeaseSeconds   int64  `json:"lease_seconds"`
}

type ensureOperationRequest struct {
	OperationKey        string   `json:"operation_key"`
	ToolName            string   `json:"tool_name"`
	NormalizedInputHash string   `json:"normalized_input_hash"`
	ParentOperationID   string   `json:"parent_operation_id,omitempty"`
	CausationEventIDs   []string `json:"causation_event_ids,omitempty"`
	Required            bool     `json:"required"`
	LeaseToken          string   `json:"lease_token"`
	LeaseEpoch          uint64   `json:"lease_epoch"`
}

type interactionLeaseRequest struct {
	LeaseToken string `json:"lease_token"`
	LeaseEpoch uint64 `json:"lease_epoch"`
}

type finishAttemptRequest struct {
	ReceiptID            string                       `json:"receipt_id"`
	PayloadHash          string                       `json:"payload_hash"`
	EvidenceDurability   sessionvo.EvidenceDurability `json:"evidence_durability"`
	Retryable            bool                         `json:"retryable"`
	RequestID            string                       `json:"request_id,omitempty"`
	TraceID              string                       `json:"trace_id,omitempty"`
	ObservedEvidenceRefs []string                     `json:"observed_evidence_refs,omitempty"`
	BusinessRefs         []sessionvo.BusinessRef      `json:"business_refs,omitempty"`
	ArtifactRefs         []string                     `json:"artifact_refs,omitempty"`
	PartialReasons       []string                     `json:"partial_reasons,omitempty"`
}

type terminalInteractionRequest struct {
	TerminalIdempotencyKey string                        `json:"terminal_idempotency_key"`
	LeaseToken             string                        `json:"lease_token"`
	LeaseEpoch             uint64                        `json:"lease_epoch"`
	ManifestVersion        string                        `json:"completion_manifest_version"`
	AnswerArtifactRef      string                        `json:"answer_artifact_ref,omitempty"`
	Claims                 []string                      `json:"claims,omitempty"`
	ExpectedOperations     []sessionvo.ExpectedOperation `json:"expected_operations,omitempty"`
	ExpectedReceipts       []sessionvo.ExpectedReceipt   `json:"expected_receipts,omitempty"`
	AssemblerDeadline      *time.Time                    `json:"assembler_deadline,omitempty"`
	CompletionReason       string                        `json:"completion_reason"`
}

type operationResult struct {
	Operation sessionvo.Operation `json:"operation"`
	Receipt   sessionvo.Receipt   `json:"receipt"`
}

func RegisterSessionRoutes(
	mux *http.ServeMux,
	basePath string,
	handler *SessionHandler,
	middleware ...func(http.HandlerFunc) http.HandlerFunc,
) {
	wrap := func(next http.HandlerFunc) http.HandlerFunc {
		for index := len(middleware) - 1; index >= 0; index-- {
			next = middleware[index](next)
		}
		return next
	}
	mux.HandleFunc(basePath+"/conversations", wrap(handler.CreateConversation))
	mux.HandleFunc(basePath+"/conversations:ensure-current", wrap(handler.EnsureCurrentConversation))
	mux.HandleFunc(basePath+"/conversations:create-new-generation", wrap(handler.CreateNewGeneration))
	mux.HandleFunc(basePath+"/conversations:resume-by-id", wrap(handler.ResumeConversation))
	mux.HandleFunc(basePath+"/conversations/", wrap(handler.handleConversationSubresource))
	mux.HandleFunc(basePath+"/interactions/", wrap(handler.handleInteractionSubresource))
	mux.HandleFunc(basePath+"/operations/", wrap(handler.handleOperationSubresource))
	mux.HandleFunc(basePath+"/receipts/", wrap(handler.handleReceiptSubresource))
	mux.HandleFunc(basePath+"/requests", wrap(handler.ListRequests))
	mux.HandleFunc(basePath+"/requests/", wrap(handler.GetRequest))
}

func (h *SessionHandler) ResumeConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeLifecycleError(w, r, http.StatusMethodNotAllowed, "permission_denied", "only POST is supported")
		return
	}
	owner, ok := trustedOwnerFromRequest(r)
	if !ok {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "trusted lifecycle identity is required")
		return
	}
	var request struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := decodeLifecycleBody(w, r, &request); err != nil || request.ConversationID == "" {
		writeLifecycleError(w, r, http.StatusBadRequest, "conversation_required", "conversation_id is required")
		return
	}
	value, err := h.service.ResumeConversation(r.Context(), sessionsvc.ResumeConversationCommand{
		Owner: owner, ConversationID: request.ConversationID,
	})
	h.writeLifecycleResult(w, r, value, err, http.StatusOK)
}

func (h *SessionHandler) ListRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeLifecycleError(w, r, http.StatusMethodNotAllowed, "permission_denied", "only GET is supported")
		return
	}
	owner, ok := trustedOwnerFromRequest(r)
	if !ok {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "trusted lifecycle identity is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListRequests(r.Context(), owner, limit)
	h.writeLifecycleResult(w, r, requestPage{Entries: items}, err, http.StatusOK)
}

func (h *SessionHandler) GetRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeLifecycleError(w, r, http.StatusMethodNotAllowed, "permission_denied", "only GET is supported")
		return
	}
	owner, ok := trustedOwnerFromRequest(r)
	if !ok {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "trusted lifecycle identity is required")
		return
	}
	requestID := pathAfterResource(r.URL.Path, "requests")
	value, err := h.service.GetRequest(r.Context(), owner, requestID)
	h.writeLifecycleResult(w, r, value, err, http.StatusOK)
}

func (h *SessionHandler) CreateConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		h.EnsureCurrentConversation(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeLifecycleError(w, r, http.StatusMethodNotAllowed, "permission_denied", "only GET and POST are supported")
		return
	}
	owner, ok := trustedOwnerFromRequest(r)
	if !ok {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "trusted lifecycle identity is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListConversations(r.Context(), owner, limit)
	h.writeLifecycleResult(w, r, conversationPage{Entries: items}, err, http.StatusOK)
}

func (h *SessionHandler) CreateNewGeneration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeLifecycleError(w, r, http.StatusMethodNotAllowed, "permission_denied", "only POST is supported")
		return
	}
	owner, ok := trustedOwnerFromRequest(r)
	if !ok {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "trusted lifecycle identity is required")
		return
	}
	var request ensureConversationRequest
	if err := decodeLifecycleBody(w, r, &request); err != nil {
		writeLifecycleError(w, r, http.StatusBadRequest, "conversation_required", err.Error())
		return
	}
	conversation, err := h.service.CreateNewGeneration(r.Context(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: request.ExternalConversationKey,
		IdempotencyKey: request.IdempotencyKey, OneShot: request.OneShot,
	})
	h.writeLifecycleResult(w, r, conversation, err, http.StatusCreated)
}

func (h *SessionHandler) EnsureCurrentConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeLifecycleError(w, r, http.StatusMethodNotAllowed, "permission_denied", "only POST is supported")
		return
	}
	owner, ok := trustedOwnerFromRequest(r)
	if !ok {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "trusted lifecycle identity is required")
		return
	}
	var request ensureConversationRequest
	if err := decodeLifecycleBody(w, r, &request); err != nil {
		writeLifecycleError(w, r, http.StatusBadRequest, "conversation_required", err.Error())
		return
	}
	conversation, err := h.service.EnsureCurrentConversation(r.Context(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: request.ExternalConversationKey,
		IdempotencyKey: request.IdempotencyKey, OneShot: request.OneShot,
	})
	if err != nil {
		writeSessionDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, conversation)
}

func (h *SessionHandler) handleConversationSubresource(w http.ResponseWriter, r *http.Request) {
	owner, ok := trustedOwnerFromRequest(r)
	if !ok {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "trusted lifecycle identity is required")
		return
	}
	path := pathAfterResource(r.URL.Path, "conversations")
	parts := splitLifecyclePath(path)
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		value, err := h.service.GetConversation(r.Context(), owner, parts[0])
		h.writeLifecycleResult(w, r, value, err, http.StatusOK)
	case len(parts) == 1 && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/close"):
		writeLifecycleError(w, r, http.StatusNotFound, "conversation_not_found", "invalid conversation path")
	case len(parts) == 2 && parts[1] == "close" && r.Method == http.MethodPost:
		var request struct {
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := decodeLifecycleBody(w, r, &request); err != nil {
			writeLifecycleError(w, r, http.StatusBadRequest, "conversation_required", err.Error())
			return
		}
		value, err := h.service.CloseConversation(r.Context(), sessionsvc.CloseConversationCommand{
			Owner: owner, ConversationID: parts[0], IdempotencyKey: request.IdempotencyKey,
		})
		h.writeLifecycleResult(w, r, value, err, http.StatusOK)
	case len(parts) == 2 && parts[1] == "interactions" && r.Method == http.MethodPost:
		var request startInteractionRequest
		if err := decodeLifecycleBody(w, r, &request); err != nil {
			writeLifecycleError(w, r, http.StatusBadRequest, "interaction_required", err.Error())
			return
		}
		value, err := h.service.StartInteraction(r.Context(), sessionsvc.StartInteractionCommand{
			Owner: owner, ConversationID: parts[0], IdempotencyKey: request.IdempotencyKey,
			LeaseDuration: time.Duration(request.LeaseSeconds) * time.Second,
		})
		h.writeLifecycleResult(w, r, value, err, http.StatusCreated)
	case len(parts) == 4 && parts[1] == "interactions" && parts[3] == "operations:ensure" && r.Method == http.MethodPost:
		var request ensureOperationRequest
		if err := decodeLifecycleBody(w, r, &request); err != nil {
			writeLifecycleError(w, r, http.StatusBadRequest, "operation_required", err.Error())
			return
		}
		operation, receipt, err := h.service.EnsureOperation(r.Context(), sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: parts[0], InteractionID: parts[2],
			OperationKey: request.OperationKey, ToolName: request.ToolName,
			NormalizedInputHash: request.NormalizedInputHash,
			ParentOperationID:   request.ParentOperationID,
			CausationEventIDs:   request.CausationEventIDs, Required: request.Required,
			LeaseToken: request.LeaseToken, LeaseEpoch: request.LeaseEpoch,
		})
		h.writeLifecycleResult(w, r, operationResult{Operation: operation, Receipt: receipt}, err, http.StatusCreated)
	default:
		writeLifecycleError(w, r, http.StatusNotFound, "conversation_not_found", "lifecycle route was not found")
	}
}

func (h *SessionHandler) handleInteractionSubresource(w http.ResponseWriter, r *http.Request) {
	owner, ok := trustedOwnerFromRequest(r)
	if !ok {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "trusted lifecycle identity is required")
		return
	}
	path := pathAfterResource(r.URL.Path, "interactions")
	parts := splitLifecyclePath(path)
	if len(parts) == 1 && r.Method == http.MethodGet {
		value, err := h.service.GetInteraction(r.Context(), owner, parts[0])
		h.writeLifecycleResult(w, r, value, err, http.StatusOK)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeLifecycleError(w, r, http.StatusNotFound, "interaction_required", "interaction route was not found")
		return
	}
	var request terminalInteractionRequest
	if err := decodeLifecycleBody(w, r, &request); err != nil {
		writeLifecycleError(w, r, http.StatusBadRequest, "interaction_required", err.Error())
		return
	}
	status, valid := terminalStatusForAction(parts[1])
	if !valid {
		writeLifecycleError(w, r, http.StatusNotFound, "interaction_required", "terminal action was not found")
		return
	}
	value, err := h.service.TerminateInteraction(r.Context(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: parts[0], Status: status,
		TerminalIdempotencyKey: request.TerminalIdempotencyKey,
		LeaseToken:             request.LeaseToken, LeaseEpoch: request.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: request.ManifestVersion, AnswerArtifactRef: request.AnswerArtifactRef,
			Claims: request.Claims, ExpectedOperations: request.ExpectedOperations,
			ExpectedReceipts: request.ExpectedReceipts, AssemblerDeadline: request.AssemblerDeadline,
			CompletionReason: request.CompletionReason,
		},
	})
	h.writeLifecycleResult(w, r, value, err, http.StatusOK)
}

func (h *SessionHandler) handleOperationSubresource(w http.ResponseWriter, r *http.Request) {
	owner, ok := trustedOwnerFromRequest(r)
	if !ok {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "trusted lifecycle identity is required")
		return
	}
	path := pathAfterResource(r.URL.Path, "operations")
	parts := splitLifecyclePath(path)
	if len(parts) == 1 && r.Method == http.MethodGet {
		value, err := h.service.GetOperation(r.Context(), owner, parts[0])
		h.writeLifecycleResult(w, r, value, err, http.StatusOK)
		return
	}
	if len(parts) == 2 && parts[1] == "attempts" && r.Method == http.MethodPost {
		var request interactionLeaseRequest
		if err := decodeLifecycleBody(w, r, &request); err != nil {
			writeLifecycleError(w, r, http.StatusBadRequest, "operation_required", err.Error())
			return
		}
		operation, receipt, err := h.service.StartOperationAttempt(r.Context(), sessionsvc.StartAttemptCommand{
			Owner: owner, OperationID: parts[0],
			LeaseToken: request.LeaseToken, LeaseEpoch: request.LeaseEpoch,
		})
		h.writeLifecycleResult(w, r, operationResult{Operation: operation, Receipt: receipt}, err, http.StatusCreated)
		return
	}
	if len(parts) != 3 || parts[1] != "attempts" || r.Method != http.MethodPost {
		writeLifecycleError(w, r, http.StatusNotFound, "operation_required", "operation route was not found")
		return
	}
	attemptText, action, found := strings.Cut(parts[2], ":")
	if !found {
		writeLifecycleError(w, r, http.StatusNotFound, "operation_required", "attempt action was not found")
		return
	}
	attempt, err := strconv.ParseUint(attemptText, 10, 32)
	if err != nil {
		writeLifecycleError(w, r, http.StatusBadRequest, "operation_required", "attempt must be a positive integer")
		return
	}
	var request finishAttemptRequest
	if err := decodeLifecycleBody(w, r, &request); err != nil {
		writeLifecycleError(w, r, http.StatusBadRequest, "operation_required", err.Error())
		return
	}
	command := sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: parts[0], Attempt: uint32(attempt),
		ReceiptID: request.ReceiptID, PayloadHash: request.PayloadHash,
		EvidenceDurability: request.EvidenceDurability, Retryable: request.Retryable,
		RequestID: request.RequestID, TraceID: request.TraceID,
		ObservedEvidenceRefs: request.ObservedEvidenceRefs,
		BusinessRefs:         request.BusinessRefs,
		ArtifactRefs:         request.ArtifactRefs,
		PartialReasons:       request.PartialReasons,
	}
	var operation sessionvo.Operation
	var receipt sessionvo.Receipt
	if action == "complete" {
		operation, receipt, err = h.service.CompleteOperationAttempt(r.Context(), command)
	} else if action == "fail" {
		operation, receipt, err = h.service.FailOperationAttempt(r.Context(), command)
	} else {
		writeLifecycleError(w, r, http.StatusNotFound, "operation_required", "attempt action was not found")
		return
	}
	h.writeLifecycleResult(w, r, operationResult{Operation: operation, Receipt: receipt}, err, http.StatusOK)
}

func (h *SessionHandler) handleReceiptSubresource(w http.ResponseWriter, r *http.Request) {
	owner, ok := trustedOwnerFromRequest(r)
	if !ok {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "trusted lifecycle identity is required")
		return
	}
	path := pathAfterResource(r.URL.Path, "receipts")
	parts := splitLifecyclePath(path)
	if len(parts) != 1 || r.Method != http.MethodGet {
		writeLifecycleError(w, r, http.StatusNotFound, "operation_required", "receipt route was not found")
		return
	}
	value, err := h.service.GetReceipt(r.Context(), owner, parts[0])
	h.writeLifecycleResult(w, r, value, err, http.StatusOK)
}

func (h *SessionHandler) writeLifecycleResult(w http.ResponseWriter, r *http.Request, value any, err error, status int) {
	if err != nil {
		writeSessionDomainError(w, r, err)
		return
	}
	writeJSON(w, status, value)
}

func splitLifecyclePath(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func pathAfterResource(path, resource string) string {
	marker := "/" + resource + "/"
	index := strings.Index(path, marker)
	if index < 0 {
		return ""
	}
	return path[index+len(marker):]
}

func terminalStatusForAction(action string) (sessionvo.InteractionStatus, bool) {
	switch action {
	case "complete":
		return sessionvo.InteractionCompleted, true
	case "fail":
		return sessionvo.InteractionFailed, true
	case "cancel":
		return sessionvo.InteractionCanceled, true
	case "handoff":
		return sessionvo.InteractionHandedOff, true
	default:
		return "", false
	}
}

func trustedOwnerFromRequest(r *http.Request) (sessionvo.Owner, bool) {
	owner := sessionvo.Owner{
		TenantID:               strings.TrimSpace(r.Header.Get("X-BKN-Tenant-ID")),
		BusinessDomainID:       strings.TrimSpace(r.Header.Get("X-Business-Domain-ID")),
		ApplicationPrincipalID: strings.TrimSpace(r.Header.Get("X-BKN-Application-Principal-ID")),
		EffectiveSubjectType:   sessionvo.SubjectType(strings.TrimSpace(r.Header.Get("X-BKN-Effective-Subject-Type"))),
		EffectiveSubjectID:     strings.TrimSpace(r.Header.Get("X-BKN-Effective-Subject-ID")),
		DelegationID:           strings.TrimSpace(r.Header.Get("X-BKN-Delegation-ID")),
	}
	valid := owner.TenantID != "" && owner.BusinessDomainID != "" &&
		owner.ApplicationPrincipalID != "" && owner.EffectiveSubjectID != "" &&
		(owner.EffectiveSubjectType == sessionvo.SubjectUser || owner.EffectiveSubjectType == sessionvo.SubjectService)
	return owner, valid
}

func decodeLifecycleBody(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLifecycleBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("request body does not match the lifecycle contract")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeSessionDomainError(w http.ResponseWriter, r *http.Request, err error) {
	var domainErr *sessionsvc.DomainError
	if !errors.As(err, &domainErr) {
		writeLifecycleError(w, r, http.StatusInternalServerError, "internal_error", "lifecycle request failed")
		return
	}
	status, action, retryable := lifecycleErrorContract(domainErr.Code)
	writeJSON(w, status, lifecycleErrorEnvelope{Error: lifecycleError{
		Code: string(domainErr.Code), Message: domainErr.Message,
		CurrentStatus: domainErr.CurrentStatus, Retryable: retryable,
		RequiredAction: action, RequestID: requestIDFromRequest(r),
	}})
}

func writeLifecycleError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	_, action, retryable := lifecycleErrorContract(sessionsvc.ErrorCode(code))
	writeJSON(w, status, lifecycleErrorEnvelope{Error: lifecycleError{
		Code: code, Message: message, Retryable: retryable,
		RequiredAction: action, RequestID: requestIDFromRequest(r),
	}})
}

func lifecycleErrorContract(code sessionsvc.ErrorCode) (int, string, bool) {
	switch code {
	case sessionsvc.CodeConversationRequired:
		return http.StatusBadRequest, "create_conversation", false
	case sessionsvc.CodeConversationNotFound:
		return http.StatusNotFound, "create_or_resume_conversation", false
	case sessionsvc.CodeConversationClosed:
		return http.StatusConflict, "create_conversation", false
	case sessionsvc.CodeConversationExpired:
		return http.StatusConflict, "create_or_resume_conversation", false
	case sessionsvc.CodeConversationOwnerMismatch:
		return http.StatusForbidden, "use_authorized_conversation", false
	case sessionsvc.CodeInteractionRequired:
		return http.StatusBadRequest, "start_interaction", false
	case sessionsvc.CodeInteractionInProgress:
		return http.StatusConflict, "complete_or_cancel_interaction", false
	case sessionsvc.CodeInteractionTerminal:
		return http.StatusConflict, "start_interaction", false
	case sessionsvc.CodeOperationRequired:
		return http.StatusBadRequest, "ensure_operation", false
	case sessionsvc.CodeIdempotencyConflict:
		return http.StatusConflict, "use_new_idempotency_key", false
	case sessionsvc.CodeReceiptPending:
		return http.StatusConflict, "poll_receipt", true
	case sessionsvc.CodeTerminalConflict:
		return http.StatusConflict, "get_interaction", false
	case sessionsvc.CodeClosureManifestInvalid:
		return http.StatusUnprocessableEntity, "fix_closure_manifest", false
	case sessionsvc.CodeResourceNotDisclosed:
		return http.StatusNotFound, "verify_scope_or_identifier", false
	case "permission_denied":
		return http.StatusForbidden, "request_authorization", false
	default:
		return http.StatusInternalServerError, "", false
	}
}

func requestIDFromRequest(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Request-ID")); value != "" {
		return value
	}
	return strings.TrimSpace(r.Header.Get("x-bkn-request-id"))
}
