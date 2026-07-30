package ledgersvc

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
)

type ErrorCode string

const (
	CodeEventPayloadConflict  ErrorCode = "event_payload_conflict"
	CodeEventSequenceConflict ErrorCode = "producer_sequence_conflict"
	CodeInvalidEvent          ErrorCode = "invalid_evidence_event"
)

type DomainError struct {
	Code    ErrorCode
	Message string
}

func (e *DomainError) Error() string { return e.Message }

func IsCode(err error, code ErrorCode) bool {
	var domainErr *DomainError
	return errors.As(err, &domainErr) && domainErr.Code == code
}

type Service struct {
	store   ievidenceledger.Store
	metrics icoremetrics.Recorder
}

func New(store ievidenceledger.Store) *Service {
	return NewWithMetrics(store, icoremetrics.Noop{})
}

func NewWithMetrics(store ievidenceledger.Store, metrics icoremetrics.Recorder) *Service {
	if metrics == nil {
		metrics = icoremetrics.Noop{}
	}
	return &Service{store: store, metrics: metrics}
}

func (s *Service) Ingest(ctx context.Context, event ledgervo.Event) (ledgervo.DurableAck, error) {
	if err := validateEvent(event); err != nil {
		return ledgervo.DurableAck{}, err
	}
	ack, err := s.store.Commit(ctx, event)
	switch {
	case errors.Is(err, ievidenceledger.ErrPayloadConflict):
		s.metrics.Increment(icoremetrics.EvidenceHashConflictsTotal)
		return ledgervo.DurableAck{}, &DomainError{
			Code: CodeEventPayloadConflict, Message: "event ID already exists with a different payload hash",
		}
	case errors.Is(err, ievidenceledger.ErrSequenceConflict):
		return ledgervo.DurableAck{}, &DomainError{
			Code: CodeEventSequenceConflict, Message: "producer stream sequence is already occupied",
		}
	case errors.Is(err, ievidenceledger.ErrCausalityConflict):
		return ledgervo.DurableAck{}, &DomainError{
			Code: CodeInvalidEvent, Message: "event causality is cyclic or crosses its trusted interaction scope",
		}
	case err != nil:
		return ledgervo.DurableAck{}, err
	default:
		s.metrics.Increment(icoremetrics.EvidenceIngestTotal)
		return ack, nil
	}
}

func validateEvent(event ledgervo.Event) error {
	if event.SchemaVersion != "3.0.0" {
		return &DomainError{Code: CodeInvalidEvent, Message: "schema_version must be 3.0.0"}
	}
	if event.EventID == "" || event.EventType == "" || event.ConversationID == "" ||
		event.InteractionID == "" || event.ProducerID == "" || event.ProducerStreamID == "" ||
		event.ProducerEpoch == 0 || event.ProducerSequence == 0 ||
		event.Owner.TenantID == "" || event.Owner.BusinessDomainID == "" {
		return &DomainError{Code: CodeInvalidEvent, Message: "required evidence event fields are missing"}
	}
	if event.PayloadHash != ledgervo.CanonicalPayloadHash(event.Envelope) {
		return &DomainError{Code: CodeInvalidEvent, Message: "payload_hash does not match canonical envelope"}
	}
	for _, cause := range event.CausationEventIDs {
		if cause == event.EventID {
			return &DomainError{Code: CodeInvalidEvent, Message: "event cannot cause itself"}
		}
	}
	if event.StartedAt.IsZero() || event.ObservedAt.IsZero() || event.EmittedAt.IsZero() {
		return &DomainError{Code: CodeInvalidEvent, Message: "started_at, observed_at and emitted_at are required"}
	}
	if !json.Valid(event.Envelope) {
		return &DomainError{Code: CodeInvalidEvent, Message: "event envelope is not valid JSON"}
	}
	return nil
}
