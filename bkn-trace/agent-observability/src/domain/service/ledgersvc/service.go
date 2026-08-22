// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ledgersvc

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
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
		return &DomainError{Code: CodeInvalidEvent, Message: "bkn.trace.schema.version must be 3.0.0"}
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
	if event.ObservedAt.Before(event.StartedAt) || event.EmittedAt.Before(event.ObservedAt) {
		return &DomainError{Code: CodeInvalidEvent, Message: "event timestamps must satisfy started_at <= observed_at <= emitted_at"}
	}
	if !json.Valid(event.Envelope) {
		return &DomainError{Code: CodeInvalidEvent, Message: "event envelope is not valid JSON"}
	}
	if err := validateSemanticEvidence(event); err != nil {
		return err
	}
	return nil
}

func validateSemanticEvidence(event ledgervo.Event) error {
	for _, ref := range event.ArtifactRefs {
		if ref == "" {
			return &DomainError{Code: CodeInvalidEvent, Message: "artifact_refs contains an empty artifact reference"}
		}
	}
	for _, ref := range event.BusinessRefs {
		if !ref.IsCanonicalForBusinessDomain(event.Owner.BusinessDomainID) {
			return &DomainError{Code: CodeInvalidEvent, Message: "business_refs contains an invalid typed business reference"}
		}
	}
	for _, ref := range event.EvidenceRefs {
		if ref.Ref == "" || ref.SourceInteractionID == "" || ref.SourceRevisionID == "" ||
			ref.Version == "" || ref.ContentHash == "" || !validEvidenceRefType(ref.RefType) {
			return &DomainError{Code: CodeInvalidEvent, Message: "evidence_refs contains an imprecise evidence reference"}
		}
	}
	for _, claim := range event.Claims {
		if claim.ID == "" || claim.Type == "" || claim.ContentArtifactRef == "" ||
			!validClaimMateriality(claim.Materiality) || !validClaimStatus(claim.Status) {
			return &DomainError{Code: CodeInvalidEvent, Message: "claims contains an invalid claim"}
		}
		for _, support := range claim.Supports {
			if support.TargetRef == "" || support.SourceInteractionID == "" || support.SourceRevisionID == "" ||
				support.Version == "" || support.ContentHash == "" || support.Role == "" ||
				!validSupportTargetType(support.TargetType) || !validSupportStatus(support.Status) ||
				(support.Status == sessionvo.SupportRejected && support.Reason == "") {
				return &DomainError{Code: CodeInvalidEvent, Message: "claim supports contains an imprecise or unexplained support"}
			}
		}
	}
	for _, edge := range event.OperationBusinessEdges {
		if edge.OperationID == "" || edge.OperationID != event.OperationID || edge.ObservedAt.IsZero() ||
			edge.ObservedAt.Before(event.StartedAt) || edge.ObservedAt.After(event.EmittedAt) || !validOperationRole(edge.Role) ||
			!edge.BusinessRef.IsCanonicalForBusinessDomain(event.Owner.BusinessDomainID) {
			return &DomainError{Code: CodeInvalidEvent, Message: "operation_business_edges contains an invalid typed edge"}
		}
	}
	return nil
}

func validEvidenceRefType(value sessionvo.EvidenceRefType) bool {
	switch value {
	case sessionvo.EvidenceRefEvent, sessionvo.EvidenceRefArtifact,
		sessionvo.EvidenceRefArtifactFragment, sessionvo.EvidenceRefOperationOutput,
		sessionvo.EvidenceRefClaim:
		return true
	default:
		return false
	}
}

func validClaimMateriality(value sessionvo.ClaimMateriality) bool {
	return value == sessionvo.ClaimMaterial || value == sessionvo.ClaimSupporting
}

func validClaimStatus(value sessionvo.ClaimStatus) bool {
	return value == sessionvo.ClaimAsserted || value == sessionvo.ClaimWithdrawn
}

func validSupportTargetType(value sessionvo.SupportTargetType) bool {
	switch value {
	case sessionvo.SupportEvidence, sessionvo.SupportClaim,
		sessionvo.SupportArtifactFragment, sessionvo.SupportOperationOutput:
		return true
	default:
		return false
	}
}

func validSupportStatus(value sessionvo.SupportStatus) bool {
	return value == sessionvo.SupportAdopted || value == sessionvo.SupportRejected
}

func validOperationRole(value sessionvo.OperationBusinessRole) bool {
	switch value {
	case sessionvo.OperationRoleRead, sessionvo.OperationRoleFilter, sessionvo.OperationRoleGroup,
		sessionvo.OperationRoleAggregate, sessionvo.OperationRoleInput, sessionvo.OperationRoleOutput,
		sessionvo.OperationRoleModify, sessionvo.OperationRoleRecommend, sessionvo.OperationRoleExecute:
		return true
	default:
		return false
	}
}
