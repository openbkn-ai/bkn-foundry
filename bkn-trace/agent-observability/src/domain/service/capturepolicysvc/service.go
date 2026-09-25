// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package capturepolicysvc exposes the read-side contract for the unified
// Trace/Evidence admission policy. Persistence and command handling are
// intentionally supplied by the shared control-plane integration; this
// package does not implement a second database or an outbox.
package capturepolicysvc

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrRevisionConflict     = errors.New("capture policy revision conflict")
	ErrOperationInProgress  = errors.New("capture policy operation is already in progress")
	ErrInvalidChangeRequest = errors.New("invalid capture policy change request")
	ErrOperationNotFound    = errors.New("capture policy operation was not found")
)

type State string

const (
	StateEnabled     State = "enabled"
	StateDisabled    State = "disabled"
	StateEnabling    State = "enabling"
	StateDisabling   State = "disabling"
	StateRollingBack State = "rolling_back"
)

type Phase string

const (
	PhasePending          Phase = "pending"
	PhaseEnabling         Phase = "enabling"
	PhaseDisabling        Phase = "disabling"
	PhaseRollingBack      Phase = "rolling_back"
	PhaseSucceeded        Phase = "succeeded"
	PhaseFailed           Phase = "failed"
	PhaseRollbackComplete Phase = "rollback_completed"
	PhaseRollbackFailed   Phase = "rollback_failed"
)

type AckState string

const (
	AckReady    AckState = "ready"
	AckDraining AckState = "draining"
	AckDisabled AckState = "disabled"
	AckGap      AckState = "gap"
)

// QueueDisposition is populated by the endpoint acknowledgement contract.
// Unaccounted is a pointer because a gap is explicitly different from zero.
type QueueDisposition struct {
	Exported    int  `json:"exported"`
	Dropped     int  `json:"dropped"`
	Unaccounted *int `json:"unaccounted"`
}

type EndpointAcknowledgement struct {
	InstanceID string           `json:"instance_id"`
	Generation string           `json:"generation"`
	Ready      bool             `json:"ready"`
	State      AckState         `json:"state"`
	Queue      QueueDisposition `json:"queue_disposition"`
	Revision   uint64           `json:"revision"`
}

type Operation struct {
	ID               string `json:"id"`
	Phase            Phase  `json:"phase"`
	RequestedState   State  `json:"requested_state"`
	ExpectedRevision uint64 `json:"expected_revision"`
	ErrorCode        string `json:"error_code,omitempty"`
}

type Snapshot struct {
	Revision           uint64                    `json:"revision"`
	DesiredState       State                     `json:"desired_state"`
	EffectiveState     State                     `json:"effective_state"`
	LastStableRevision uint64                    `json:"last_stable_revision"`
	CoverageGap        bool                      `json:"coverage_gap"`
	Operation          Operation                 `json:"operation"`
	Acknowledgements   []EndpointAcknowledgement `json:"acknowledgements"`
}

// AdmissionMeasurement and AdmissionBudget are the frozen configuration_get
// read-model contract. The control plane must not fabricate a healthy budget;
// callers without a current provider must fail closed at the HTTP boundary.
type AdmissionMeasurement struct {
	Metric     string    `json:"metric"`
	Source     string    `json:"source"`
	SampleTime time.Time `json:"sample_time"`
	Value      float64   `json:"value"`
	Threshold  float64   `json:"threshold"`
	Fresh      bool      `json:"fresh"`
}

type AdmissionBudget struct {
	ContractVersion string                 `json:"contract_version"`
	Profile         string                 `json:"profile"`
	SampledAt       time.Time              `json:"sampled_at"`
	FreshUntil      time.Time              `json:"fresh_until"`
	Measurements    []AdmissionMeasurement `json:"measurements"`
}

type ConfigurationGetResponse struct {
	Kind                  string          `json:"kind"`
	DesiredState          State           `json:"desired_state"`
	EffectiveState        State           `json:"effective_state"`
	PolicyRevision        uint64          `json:"policy_revision"`
	LastStableRevision    uint64          `json:"last_stable_revision"`
	ActiveOperationID     *string         `json:"active_operation_id,omitempty"`
	HeartbeatIntervalSecs int             `json:"heartbeat_interval_seconds"`
	LeaseTTLSeconds       int             `json:"lease_ttl_seconds"`
	AdmissionBudget       AdmissionBudget `json:"admission_budget"`
}

type ChangeRequest struct {
	DesiredState     State  `json:"desired_state"`
	ExpectedRevision uint64 `json:"expected_revision"`
}

type Commander interface {
	Request(context.Context, ChangeRequest) (Snapshot, error)
}

type CommanderFunc func(context.Context, ChangeRequest) (Snapshot, error)

func (f CommanderFunc) Request(ctx context.Context, request ChangeRequest) (Snapshot, error) {
	return f(ctx, request)
}

func (s Snapshot) Validate() error {
	if s.Revision == 0 {
		return errors.New("capture policy revision must be positive")
	}
	if !validState(s.DesiredState) {
		return fmt.Errorf("invalid desired state %q", s.DesiredState)
	}
	if !validState(s.EffectiveState) {
		return fmt.Errorf("invalid effective state %q", s.EffectiveState)
	}
	if s.Operation.ID == "" {
		return errors.New("capture policy operation id is required")
	}
	if !validPhase(s.Operation.Phase) {
		return fmt.Errorf("invalid operation phase %q", s.Operation.Phase)
	}
	return nil
}

func validState(state State) bool {
	switch state {
	case StateEnabled, StateDisabled, StateEnabling, StateDisabling, StateRollingBack:
		return true
	default:
		return false
	}
}

func validPhase(phase Phase) bool {
	switch phase {
	case PhasePending, PhaseEnabling, PhaseDisabling, PhaseRollingBack, PhaseSucceeded, PhaseFailed, PhaseRollbackComplete, PhaseRollbackFailed:
		return true
	default:
		return false
	}
}

type Reader interface {
	Read(context.Context) (Snapshot, error)
}

type OperationReader interface {
	ReadOperation(context.Context, string) (Operation, error)
}

type ReaderFunc func(context.Context) (Snapshot, error)

func (f ReaderFunc) Read(ctx context.Context) (Snapshot, error) { return f(ctx) }

type Service struct{ reader Reader }

func New(reader Reader) *Service { return &Service{reader: reader} }

func (s *Service) Read(ctx context.Context) (Snapshot, error) {
	if s == nil || s.reader == nil {
		return Snapshot{}, errors.New("capture policy reader is not configured")
	}
	snapshot, err := s.reader.Read(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Service) ReadOperation(ctx context.Context, operationID string) (Operation, error) {
	if s == nil || s.reader == nil || operationID == "" {
		return Operation{}, ErrOperationNotFound
	}
	reader, ok := s.reader.(OperationReader)
	if !ok {
		return Operation{}, ErrOperationNotFound
	}
	return reader.ReadOperation(ctx, operationID)
}
