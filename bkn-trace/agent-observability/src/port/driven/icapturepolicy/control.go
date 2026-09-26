// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

// Package icapturepolicy defines the internal persistence boundary for the
// unified Trace/Evidence control plane. These records are storage contracts;
// public API DTOs remain in the driver adapter and are not duplicated here.
package icapturepolicy

import (
	"context"
	"errors"
	"time"
)

const (
	EndpointTraceGateway      = "trace_gateway"
	EndpointEvidencePublisher = "evidence_publisher"
	StateEnabled              = "enabled"
	StateDisabled             = "disabled"
	PhasePending              = "pending"
	PhaseEnabling             = "enabling"
	PhaseDisabling            = "disabling"
	PhaseRollingBack          = "rolling_back"
	PhaseSucceeded            = "succeeded"
	PhaseFailed               = "failed"
	PhaseRollbackCompleted    = "rollback_completed"
	PhaseRollbackFailed       = "rollback_failed"
	AckPending                = "pending"
	AckReady                  = "ready"
	AckDraining               = "draining"
	AckDisabled               = "disabled"
	AckGap                    = "gap"
	DispositionComplete       = "complete"
	DispositionGap            = "gap"
	DispositionNotApplicable  = "not_applicable"
	EventOperationCreated     = "operation_created"
	EventPhaseChanged         = "operation_phase_changed"
	EventRollbackStarted      = "rollback_started"
	EventCoverageGap          = "coverage_gap"
)

var (
	ErrControlStateNotInitialized = errors.New("capture control state is not initialized")
	ErrRevisionConflict           = errors.New("capture policy revision conflict")
	ErrOperationInProgress        = errors.New("capture policy operation is already in progress")
	ErrOperationNotFound          = errors.New("capture policy operation was not found")
	ErrLeaseConflict              = errors.New("capture operation lease is stale or held")
	ErrExpectedSetConflict        = errors.New("capture operation expected instance set conflicts")
	ErrInvalidTransition          = errors.New("invalid capture operation phase transition")
	ErrTerminalOperation          = errors.New("capture operation is terminal")
)

type ControlState struct {
	CurrentRevision      uint64
	DesiredState         string
	EffectiveState       string
	LastStableRevision   uint64
	ActiveOperationID    string
	LastOperationID      string
	CoverageGap          bool
	CoverageGapReason    string
	CoverageGapStartedAt *time.Time
	CoverageGapUpdatedAt time.Time
	UpdatedAt            time.Time
}

type Operation struct {
	ID                   string
	PolicyRevision       uint64
	RequestedState       string
	ExpectedRevision     uint64
	Phase                string
	ErrorCode            string
	LeaseOwner           string
	LeaseToken           uint64
	LeaseExpiresAt       *time.Time
	ConvergenceDeadline  time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
	TerminalAt           *time.Time
	CompensationRevision uint64
	RestoredState        string
}

type EndpointLease struct {
	EndpointKind     string
	InstanceID       string
	WorkloadIdentity string
	ProcessBootID    string
	ObservedRevision uint64
	Ready            bool
	HeartbeatAt      time.Time
	LeaseExpiresAt   time.Time
	UpdatedAt        time.Time
}

type ExpectedAcknowledgement struct {
	OperationID          string
	EndpointKind         string
	InstanceID           string
	WorkloadIdentity     string
	ProcessBootID        string
	PolicyRevision       uint64
	Ready                bool
	AckState             string
	AcknowledgedAt       *time.Time
	ExportedCount        *uint64
	DroppedCount         *uint64
	UnaccountedCount     *uint64
	TraceDisposition     string
	LastAcceptedSequence *uint64
	PublishedCount       *uint64
	QueueEmpty           *bool
	EvidenceDisposition  string
	GapReason            string
}

type OperationEvent struct {
	OperationID string
	Phase       string
	EventType   string
	LeaseToken  uint64
	ErrorCode   string
	GapReason   string
	RecordedAt  time.Time
}

type Store interface {
	ReadControlState(context.Context) (ControlState, error)
	StartOperation(context.Context, ControlState, Operation, []ExpectedAcknowledgement) error
	AdvanceOperation(context.Context, string, uint64, string, string, string, time.Time) error
	BeginRollback(context.Context, string, uint64, uint64, string, []ExpectedAcknowledgement, time.Time) error
	CompleteSucceeded(context.Context, string, uint64, time.Time) error
	CompleteFailed(context.Context, string, uint64, time.Time) error
	CompleteRollback(context.Context, string, uint64, time.Time) error
	CompleteRollbackFailed(context.Context, string, uint64, time.Time) error
	AppendOperationEvent(context.Context, OperationEvent) error
	UpsertEndpointLease(context.Context, EndpointLease) error
	RecordAcknowledgement(context.Context, ExpectedAcknowledgement) error
	RecordEvidencePublisherAcknowledgement(context.Context, ExpectedAcknowledgement, time.Time) error
}

func (s ControlState) Validate() error {
	if s.CurrentRevision == 0 || s.LastStableRevision == 0 || !validState(s.DesiredState) || !validState(s.EffectiveState) || s.CoverageGapUpdatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return ErrInvalidControlState
	}
	return nil
}

func (o Operation) Validate() error {
	if o.ID == "" || o.PolicyRevision == 0 || o.ExpectedRevision == 0 || !validState(o.RequestedState) || !validPhase(o.Phase) || o.ConvergenceDeadline.IsZero() || o.CreatedAt.IsZero() || o.UpdatedAt.IsZero() {
		return ErrInvalidOperation
	}
	return nil
}

func (l EndpointLease) Validate() error {
	if (l.EndpointKind != EndpointTraceGateway && l.EndpointKind != EndpointEvidencePublisher) || l.InstanceID == "" || l.WorkloadIdentity == "" || l.ProcessBootID == "" || l.HeartbeatAt.IsZero() || l.LeaseExpiresAt.IsZero() || !l.LeaseExpiresAt.After(l.HeartbeatAt) || l.UpdatedAt.IsZero() {
		return ErrInvalidEndpointLease
	}
	return nil
}

func (a ExpectedAcknowledgement) Validate() error {
	if a.OperationID == "" || (a.EndpointKind != EndpointTraceGateway && a.EndpointKind != EndpointEvidencePublisher) || a.InstanceID == "" || a.WorkloadIdentity == "" || a.ProcessBootID == "" || a.PolicyRevision == 0 || !validAckState(a.AckState) {
		return ErrInvalidAcknowledgement
	}
	if a.AcknowledgedAt == nil {
		if a.AckState != AckPending || a.ExportedCount != nil || a.DroppedCount != nil || a.UnaccountedCount != nil || a.TraceDisposition != "" || a.LastAcceptedSequence != nil || a.PublishedCount != nil || a.QueueEmpty != nil || a.EvidenceDisposition != "" || a.GapReason != "" {
			return ErrInvalidAcknowledgement
		}
		return nil
	}
	if a.AckState == AckPending {
		return ErrInvalidAcknowledgement
	}
	if a.EndpointKind == EndpointTraceGateway {
		if a.LastAcceptedSequence != nil || a.PublishedCount != nil || a.QueueEmpty != nil || a.EvidenceDisposition != "" {
			return ErrInvalidAcknowledgement
		}
		if !validDisposition(a.TraceDisposition) {
			return ErrInvalidAcknowledgement
		}
		if a.TraceDisposition == DispositionNotApplicable {
			if a.ExportedCount != nil || a.DroppedCount != nil || a.UnaccountedCount != nil || a.GapReason != "" {
				return ErrInvalidAcknowledgement
			}
		} else if a.ExportedCount == nil || a.DroppedCount == nil {
			return ErrInvalidAcknowledgement
		} else if a.TraceDisposition == DispositionComplete && (a.UnaccountedCount == nil || *a.UnaccountedCount != 0) {
			return ErrInvalidAcknowledgement
		}
		if (a.TraceDisposition == DispositionComplete || a.TraceDisposition == DispositionNotApplicable) && a.GapReason != "" {
			return ErrInvalidAcknowledgement
		}
		if a.TraceDisposition == DispositionGap && a.GapReason == "" {
			return ErrInvalidAcknowledgement
		}
		return nil
	}
	if a.ExportedCount != nil || a.UnaccountedCount != nil || a.TraceDisposition != "" || a.LastAcceptedSequence == nil || a.PublishedCount == nil || a.DroppedCount == nil || a.QueueEmpty == nil || !validDisposition(a.EvidenceDisposition) {
		return ErrInvalidAcknowledgement
	}
	if a.EvidenceDisposition == DispositionComplete && !*a.QueueEmpty {
		return ErrInvalidAcknowledgement
	}
	if a.EvidenceDisposition == DispositionComplete || a.EvidenceDisposition == DispositionNotApplicable {
		if a.GapReason != "" {
			return ErrInvalidAcknowledgement
		}
	} else if a.GapReason == "" {
		return ErrInvalidAcknowledgement
	}
	return nil
}

func validState(state string) bool { return state == StateEnabled || state == StateDisabled }

func validPhase(phase string) bool {
	switch phase {
	case PhasePending, PhaseEnabling, PhaseDisabling, PhaseRollingBack, PhaseSucceeded, PhaseFailed, PhaseRollbackCompleted, PhaseRollbackFailed:
		return true
	default:
		return false
	}
}

func validAckState(state string) bool {
	switch state {
	case AckPending, AckReady, AckDraining, AckDisabled, AckGap:
		return true
	default:
		return false
	}
}

func validDisposition(disposition string) bool {
	return disposition == DispositionComplete || disposition == DispositionGap || disposition == DispositionNotApplicable
}

func CanTransition(from, to string) bool {
	if from == to && from != PhasePending {
		return true
	}
	switch from {
	case PhasePending:
		return to == PhaseEnabling || to == PhaseDisabling || to == PhaseFailed
	case PhaseEnabling, PhaseDisabling:
		return to == PhaseSucceeded || to == PhaseFailed || to == PhaseRollingBack
	case PhaseRollingBack:
		return to == PhaseRollbackCompleted || to == PhaseRollbackFailed
	default:
		return false
	}
}

func ValidPhase(phase string) bool { return validPhase(phase) }

func ValidEventType(eventType string) bool {
	switch eventType {
	case EventOperationCreated, EventPhaseChanged, EventRollbackStarted, EventCoverageGap:
		return true
	default:
		return false
	}
}

var (
	ErrInvalidControlState    = errors.New("invalid capture control state")
	ErrInvalidOperation       = errors.New("invalid capture operation")
	ErrInvalidEndpointLease   = errors.New("invalid capture endpoint lease")
	ErrInvalidAcknowledgement = errors.New("invalid capture acknowledgement")
)
