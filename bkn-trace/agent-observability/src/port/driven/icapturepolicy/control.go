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
)

var (
	ErrControlStateNotInitialized = errors.New("capture control state is not initialized")
	ErrRevisionConflict           = errors.New("capture policy revision conflict")
	ErrOperationInProgress        = errors.New("capture policy operation is already in progress")
	ErrOperationNotFound          = errors.New("capture policy operation was not found")
	ErrLeaseConflict              = errors.New("capture operation lease is stale or held")
	ErrExpectedSetConflict        = errors.New("capture operation expected instance set conflicts")
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
	ID                  string
	PolicyRevision      uint64
	RequestedState      string
	ExpectedRevision    uint64
	Phase               string
	ErrorCode           string
	LeaseOwner          string
	LeaseToken          uint64
	LeaseExpiresAt      *time.Time
	ConvergenceDeadline time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	TerminalAt          *time.Time
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
	AppendOperationEvent(context.Context, OperationEvent) error
	UpsertEndpointLease(context.Context, EndpointLease) error
	RecordAcknowledgement(context.Context, ExpectedAcknowledgement) error
}

func (s ControlState) Validate() error {
	if s.CurrentRevision == 0 || s.LastStableRevision == 0 || s.DesiredState == "" || s.EffectiveState == "" || s.CoverageGapUpdatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return ErrInvalidControlState
	}
	return nil
}

func (o Operation) Validate() error {
	if o.ID == "" || o.PolicyRevision == 0 || o.ExpectedRevision == 0 || o.RequestedState == "" || o.Phase == "" || o.ConvergenceDeadline.IsZero() || o.CreatedAt.IsZero() || o.UpdatedAt.IsZero() {
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
	if a.OperationID == "" || (a.EndpointKind != EndpointTraceGateway && a.EndpointKind != EndpointEvidencePublisher) || a.InstanceID == "" || a.WorkloadIdentity == "" || a.ProcessBootID == "" || a.PolicyRevision == 0 || a.AckState == "" {
		return ErrInvalidAcknowledgement
	}
	if a.EndpointKind == EndpointTraceGateway && a.LastAcceptedSequence != nil {
		return ErrInvalidAcknowledgement
	}
	if a.EndpointKind == EndpointEvidencePublisher && (a.ExportedCount != nil || a.UnaccountedCount != nil || a.TraceDisposition != "") {
		return ErrInvalidAcknowledgement
	}
	return nil
}

var (
	ErrInvalidControlState    = errors.New("invalid capture control state")
	ErrInvalidOperation       = errors.New("invalid capture operation")
	ErrInvalidEndpointLease   = errors.New("invalid capture endpoint lease")
	ErrInvalidAcknowledgement = errors.New("invalid capture acknowledgement")
)
