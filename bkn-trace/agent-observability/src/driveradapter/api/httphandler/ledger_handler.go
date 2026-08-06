package httphandler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

type LedgerSecurityConfig struct {
	IngestToken                string
	AllowUnauthenticatedIngest bool
}

type LedgerHandler struct {
	service                    *ledgersvc.Service
	ingestToken                string
	allowUnauthenticatedIngest bool
}

func NewLedgerHandler(service *ledgersvc.Service, security LedgerSecurityConfig) *LedgerHandler {
	return &LedgerHandler{
		service: service, ingestToken: strings.TrimSpace(security.IngestToken),
		allowUnauthenticatedIngest: security.AllowUnauthenticatedIngest,
	}
}

func NewConfiguredLedgerHandler(service *ledgersvc.Service) *LedgerHandler {
	return NewLedgerHandler(service, LedgerSecurityConfig{
		IngestToken: os.Getenv(evidenceIngestTokenEnv),
		AllowUnauthenticatedIngest: strings.EqualFold(
			strings.TrimSpace(os.Getenv(evidenceAllowUnauthenticatedIngestEnv)), "true",
		),
	})
}

type evidenceEventRequest struct {
	EventID                string                            `json:"event_id" binding:"required"`
	EventType              string                            `json:"event_type" binding:"required"`
	SchemaVersion          string                            `json:"bkn.trace.schema.version" binding:"required"`
	PayloadHash            string                            `json:"payload_hash" binding:"required"`
	ConversationID         string                            `json:"conversation_id" binding:"required"`
	InteractionID          string                            `json:"interaction_id" binding:"required"`
	OperationID            string                            `json:"operation_id,omitempty"`
	Attempt                uint32                            `json:"attempt,omitempty"`
	RequestID              string                            `json:"request_id,omitempty"`
	TraceID                string                            `json:"trace_id,omitempty"`
	SpanID                 string                            `json:"span_id,omitempty"`
	ProducerID             string                            `json:"producer_id" binding:"required"`
	ProducerStreamID       string                            `json:"producer_stream_id" binding:"required"`
	ProducerEpoch          uint64                            `json:"producer_epoch" binding:"required"`
	ProducerSequence       uint64                            `json:"producer_sequence" binding:"required"`
	CausationEventIDs      []string                          `json:"causation_event_ids,omitempty"`
	StartedAt              time.Time                         `json:"started_at" binding:"required"`
	ObservedAt             time.Time                         `json:"observed_at" binding:"required"`
	EmittedAt              time.Time                         `json:"emitted_at" binding:"required"`
	Envelope               json.RawMessage                   `json:"envelope" binding:"required"`
	BusinessRefs           []sessionvo.BusinessRef           `json:"business_refs,omitempty"`
	ArtifactRefs           []string                          `json:"artifact_refs,omitempty"`
	EvidenceRefs           []sessionvo.EvidenceRef           `json:"evidence_refs,omitempty"`
	Claims                 []sessionvo.Claim                 `json:"claims,omitempty"`
	OperationBusinessEdges []sessionvo.OperationBusinessEdge `json:"operation_business_edges,omitempty"`
}

// Ingest accepts one immutable BKN Trace 3.0 evidence event.
// @Summary Durably ingest one BKN Trace 3.0 evidence event
// @Description Commits the immutable Evidence Ledger record and Projection Outbox in one transaction before returning durable_ack.
// @Tags evidence
// @Accept json
// @Produce json
// @Param request body evidenceEventRequest true "BKN Trace 3.0 evidence event"
// @Success 202 {object} ledgervo.DurableAck
// @Failure 400,401,403,409,500 {object} lifecycleErrorEnvelope
// @Router /evidence/events [post]
func (h *LedgerHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeLifecycleError(w, r, http.StatusMethodNotAllowed, "permission_denied", "only POST is supported")
		return
	}
	if !h.authorize(w, r) {
		return
	}
	owner, ok := trustedOwnerFromRequest(r)
	if !ok {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "trusted evidence identity is required")
		return
	}
	var request evidenceEventRequest
	if err := decodeLifecycleBody(w, r, &request); err != nil {
		writeLifecycleError(w, r, http.StatusBadRequest, "invalid_evidence_event", err.Error())
		return
	}
	ack, err := h.service.Ingest(r.Context(), ledgervo.Event{
		EventID: request.EventID, EventType: request.EventType, SchemaVersion: request.SchemaVersion,
		PayloadHash: request.PayloadHash, Owner: owner, ConversationID: request.ConversationID,
		InteractionID: request.InteractionID, OperationID: request.OperationID, Attempt: request.Attempt,
		RequestID: request.RequestID, TraceID: request.TraceID, SpanID: request.SpanID,
		ProducerID: request.ProducerID, ProducerStreamID: request.ProducerStreamID,
		ProducerEpoch: request.ProducerEpoch, ProducerSequence: request.ProducerSequence,
		CausationEventIDs: request.CausationEventIDs, StartedAt: request.StartedAt,
		ObservedAt: request.ObservedAt, EmittedAt: request.EmittedAt, Envelope: request.Envelope,
		BusinessRefs: request.BusinessRefs, ArtifactRefs: request.ArtifactRefs,
		EvidenceRefs: request.EvidenceRefs, Claims: request.Claims,
		OperationBusinessEdges: request.OperationBusinessEdges,
	})
	if err != nil {
		var domainErr *ledgersvc.DomainError
		if errors.As(err, &domainErr) {
			status := http.StatusBadRequest
			action := "fix_evidence_event"
			if domainErr.Code == ledgersvc.CodeEventPayloadConflict || domainErr.Code == ledgersvc.CodeEventSequenceConflict {
				status = http.StatusConflict
				action = "inspect_event_conflict"
			}
			writeJSON(w, status, lifecycleErrorEnvelope{Error: lifecycleError{
				Code: string(domainErr.Code), Message: domainErr.Message,
				RequiredAction: action, RequestID: requestIDFromRequest(r),
			}})
			return
		}
		writeLifecycleError(w, r, http.StatusInternalServerError, "internal_error", "evidence event was not durably committed")
		return
	}
	writeJSON(w, http.StatusAccepted, ack)
}

func (h *LedgerHandler) authorize(w http.ResponseWriter, r *http.Request) bool {
	if h.allowUnauthenticatedIngest {
		return true
	}
	provided := strings.TrimSpace(r.Header.Get(evidenceIngestTokenHeader))
	if h.ingestToken == "" || provided == "" ||
		subtle.ConstantTimeCompare([]byte(provided), []byte(h.ingestToken)) != 1 {
		writeLifecycleError(w, r, http.StatusUnauthorized, "permission_denied", "evidence ingest authorization is required")
		return false
	}
	return true
}
