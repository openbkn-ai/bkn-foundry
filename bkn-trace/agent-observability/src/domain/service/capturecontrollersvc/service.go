// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

// Package capturecontrollersvc owns the small, pull-based convergence worker
// for the unified Trace/Evidence policy. It does not talk to Kubernetes or
// Helm. Endpoints pull the signed policy snapshot and publish ACK facts; this
// worker only creates the fixed expected set, leases an operation, and closes
// it when every endpoint has reported a valid disposition.
package capturecontrollersvc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

var (
	ErrNoOperation = errors.New("no capture policy operation is available")
	ErrTargetSet   = errors.New("capture controller target set is invalid")
)

type Target struct {
	EndpointKind     string
	InstanceID       string
	WorkloadIdentity string
	ProcessBootID    string
}

func (t Target) Validate() error {
	lease := icapturepolicy.EndpointLease{
		EndpointKind: t.EndpointKind, InstanceID: t.InstanceID,
		WorkloadIdentity: t.WorkloadIdentity, ProcessBootID: t.ProcessBootID,
		HeartbeatAt: time.Unix(1, 0), LeaseExpiresAt: time.Unix(2, 0), UpdatedAt: time.Unix(1, 0),
	}
	if err := lease.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrTargetSet, err)
	}
	return nil
}

type Store interface {
	ReadControlState(context.Context) (icapturepolicy.ControlState, error)
	NextCapturePolicyRevision(context.Context) (uint64, error)
	ListReadyEndpointLeases(context.Context, time.Time) ([]icapturepolicy.EndpointLease, error)
	StartOperation(context.Context, icapturepolicy.ControlState, icapturepolicy.Operation, []icapturepolicy.ExpectedAcknowledgement) error
	ClaimCapturePolicyOperation(context.Context, string, time.Duration, time.Time) (icapturepolicy.Operation, bool, error)
	ReadCapturePolicyAcknowledgements(context.Context, string, uint64) ([]icapturepolicy.ExpectedAcknowledgement, error)
	BeginRollback(context.Context, string, uint64, uint64, string, []icapturepolicy.ExpectedAcknowledgement, time.Time) error
	CompleteSucceeded(context.Context, string, uint64, time.Time) error
	CompleteFailed(context.Context, string, uint64, time.Time) error
	CompleteRollback(context.Context, string, uint64, time.Time) error
	CompleteRollbackFailed(context.Context, string, uint64, time.Time) error
}

type Controller struct {
	store                 Store
	targets               []Target
	workerID              string
	lease                 time.Duration
	convergence           time.Duration
	now                   func() time.Time
	id                    func() (string, error)
	onTerminal            func(context.Context, TerminalEvent)
	onState               func(icapturepolicy.ControlState)
	requiredEndpointKinds map[string]struct{}
}

// TerminalEvent is emitted only after the durable terminal transition commits.
type TerminalEvent struct {
	OperationID    string
	PolicyRevision uint64
	DesiredState   string
	EffectiveState string
	Phase          string
	FailureCode    string
	At             time.Time
}

type Options struct {
	Store                 Store
	Targets               []Target
	WorkerID              string
	Lease                 time.Duration
	Convergence           time.Duration
	Now                   func() time.Time
	OperationID           func() (string, error)
	OnTerminal            func(context.Context, TerminalEvent)
	OnState               func(icapturepolicy.ControlState)
	RequiredEndpointKinds []string
}

func New(options Options) (*Controller, error) {
	if options.Store == nil || options.WorkerID == "" || options.Lease <= 0 || options.Convergence <= 0 {
		return nil, ErrTargetSet
	}
	seen := make(map[string]struct{}, len(options.Targets))
	for _, target := range options.Targets {
		if err := target.Validate(); err != nil {
			return nil, err
		}
		key := target.EndpointKind + "\x00" + target.InstanceID + "\x00" + target.ProcessBootID
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%w: duplicate target %s", ErrTargetSet, key)
		}
		seen[key] = struct{}{}
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.OperationID == nil {
		options.OperationID = newOperationID
	}
	required := make(map[string]struct{}, len(options.RequiredEndpointKinds))
	for _, kind := range options.RequiredEndpointKinds {
		if kind != "" {
			required[kind] = struct{}{}
		}
	}
	return &Controller{store: options.Store, targets: append([]Target(nil), options.Targets...), workerID: options.WorkerID, lease: options.Lease, convergence: options.Convergence, now: options.Now, id: options.OperationID, onTerminal: options.OnTerminal, onState: options.OnState, requiredEndpointKinds: required}, nil
}

// Request is the Commander used by the API. The revision and expected ACK
// rows are created atomically by the durable store.
func (c *Controller) Request(ctx context.Context, request capturepolicysvc.ChangeRequest) (capturepolicysvc.Snapshot, error) {
	if c == nil || c.store == nil {
		return capturepolicysvc.Snapshot{}, ErrTargetSet
	}
	if request.ExpectedRevision == 0 || (request.DesiredState != capturepolicysvc.StateEnabled && request.DesiredState != capturepolicysvc.StateDisabled) {
		return capturepolicysvc.Snapshot{}, capturepolicysvc.ErrInvalidChangeRequest
	}
	now := c.now().UTC()
	targets := append([]Target(nil), c.targets...)
	if len(targets) == 0 {
		leases, err := c.store.ListReadyEndpointLeases(ctx, now)
		if err != nil {
			return capturepolicysvc.Snapshot{}, err
		}
		for _, lease := range leases {
			targets = append(targets, Target{EndpointKind: lease.EndpointKind, InstanceID: lease.InstanceID, WorkloadIdentity: lease.WorkloadIdentity, ProcessBootID: lease.ProcessBootID})
		}
	}
	if len(targets) == 0 {
		return capturepolicysvc.Snapshot{}, ErrTargetSet
	}
	if len(c.requiredEndpointKinds) > 0 {
		seen := make(map[string]struct{}, len(targets))
		for _, target := range targets {
			seen[target.EndpointKind] = struct{}{}
		}
		for kind := range c.requiredEndpointKinds {
			if _, ok := seen[kind]; !ok {
				return capturepolicysvc.Snapshot{}, ErrTargetSet
			}
		}
	}
	state, err := c.store.ReadControlState(ctx)
	if err != nil {
		return capturepolicysvc.Snapshot{}, err
	}
	if c.onState != nil {
		c.onState(state)
	}
	if state.CurrentRevision != request.ExpectedRevision {
		return capturepolicysvc.Snapshot{}, capturepolicysvc.ErrRevisionConflict
	}
	if state.ActiveOperationID != "" {
		return capturepolicysvc.Snapshot{}, capturepolicysvc.ErrOperationInProgress
	}
	if state.DesiredState == string(request.DesiredState) {
		return capturepolicysvc.Snapshot{Revision: state.CurrentRevision, DesiredState: capturepolicysvc.State(state.DesiredState), EffectiveState: capturepolicysvc.State(state.EffectiveState), LastStableRevision: state.LastStableRevision, Operation: capturepolicysvc.Operation{ID: "noop", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.State(state.EffectiveState), ExpectedRevision: state.CurrentRevision}}, nil
	}
	operationID, err := c.id()
	if err != nil {
		return capturepolicysvc.Snapshot{}, err
	}
	nextRevision, err := c.store.NextCapturePolicyRevision(ctx)
	if err != nil {
		return capturepolicysvc.Snapshot{}, err
	}
	operation := icapturepolicy.Operation{ID: operationID, PolicyRevision: nextRevision, RequestedState: string(request.DesiredState), ExpectedRevision: state.CurrentRevision, Phase: icapturepolicy.PhasePending, ConvergenceDeadline: now.Add(c.convergence), CreatedAt: now, UpdatedAt: now}
	expected := make([]icapturepolicy.ExpectedAcknowledgement, 0, len(targets))
	for _, target := range targets {
		expected = append(expected, icapturepolicy.ExpectedAcknowledgement{OperationID: operationID, EndpointKind: target.EndpointKind, InstanceID: target.InstanceID, WorkloadIdentity: target.WorkloadIdentity, ProcessBootID: target.ProcessBootID, PolicyRevision: operation.PolicyRevision, AckState: icapturepolicy.AckPending})
	}
	if err := c.store.StartOperation(ctx, state, operation, expected); err != nil {
		if errors.Is(err, icapturepolicy.ErrRevisionConflict) {
			return capturepolicysvc.Snapshot{}, capturepolicysvc.ErrRevisionConflict
		}
		if errors.Is(err, icapturepolicy.ErrOperationInProgress) {
			return capturepolicysvc.Snapshot{}, capturepolicysvc.ErrOperationInProgress
		}
		return capturepolicysvc.Snapshot{}, err
	}
	return capturepolicysvc.Snapshot{Revision: operation.PolicyRevision, DesiredState: request.DesiredState, EffectiveState: capturepolicysvc.State(state.EffectiveState), LastStableRevision: state.LastStableRevision, Operation: capturepolicysvc.Operation{ID: operationID, Phase: capturepolicysvc.PhasePending, RequestedState: request.DesiredState, ExpectedRevision: state.CurrentRevision}}, nil
}

// Reconcile performs one bounded worker tick. It is safe for multiple
// replicas: only the worker holding the v033 lease can complete the operation.
func (c *Controller) Reconcile(ctx context.Context) (bool, error) {
	if c == nil {
		return false, ErrTargetSet
	}
	now := c.now().UTC()
	op, claimed, err := c.store.ClaimCapturePolicyOperation(ctx, c.workerID, c.lease, now)
	if err != nil {
		return claimed, err
	}
	if c.onState != nil && op.CurrentRevision != 0 && op.EffectiveState != "" {
		c.onState(icapturepolicy.ControlState{CurrentRevision: op.CurrentRevision, DesiredState: op.DesiredState, EffectiveState: op.EffectiveState})
	}
	if !claimed {
		return false, nil
	}
	ackRevision := op.PolicyRevision
	if op.Phase == icapturepolicy.PhaseRollingBack && op.CompensationRevision > 0 {
		ackRevision = op.CompensationRevision
	}
	acks, err := c.store.ReadCapturePolicyAcknowledgements(ctx, op.ID, ackRevision)
	if err != nil {
		return true, err
	}
	if now.After(op.ConvergenceDeadline) {
		if op.Phase == icapturepolicy.PhaseRollingBack {
			err := c.store.CompleteRollbackFailed(ctx, op.ID, op.LeaseToken, now)
			if err == nil {
				state, readErr := c.store.ReadControlState(ctx)
				if readErr == nil {
					c.observeTerminal(ctx, op, icapturepolicy.PhaseRollbackFailed, state.EffectiveState, now)
				} else if c.onTerminal != nil {
					log.Printf("audit coverage_gap: rollback failure state unavailable: %v", readErr)
				}
			}
			return true, err
		}
		state, stateErr := c.store.ReadControlState(ctx)
		if stateErr != nil {
			return true, stateErr
		}
		compensationRevision, revisionErr := c.store.NextCapturePolicyRevision(ctx)
		if revisionErr != nil {
			return true, revisionErr
		}
		expected := make([]icapturepolicy.ExpectedAcknowledgement, 0, len(acks))
		for _, ack := range acks {
			expected = append(expected, icapturepolicy.ExpectedAcknowledgement{
				OperationID: op.ID, EndpointKind: ack.EndpointKind, InstanceID: ack.InstanceID,
				WorkloadIdentity: ack.WorkloadIdentity, ProcessBootID: ack.ProcessBootID,
				PolicyRevision: compensationRevision, AckState: icapturepolicy.AckPending,
			})
		}
		if len(expected) == 0 {
			err := c.store.CompleteFailed(ctx, op.ID, op.LeaseToken, now)
			if err == nil {
				c.observeTerminal(ctx, op, icapturepolicy.PhaseFailed, state.EffectiveState, now)
			}
			return true, err
		}
		return true, c.store.BeginRollback(ctx, op.ID, op.LeaseToken, compensationRevision, state.EffectiveState, expected, now)
	}
	desiredState := op.RequestedState
	if op.Phase == icapturepolicy.PhaseRollingBack {
		desiredState = op.RestoredState
	}
	if !converged(op.ID, ackRevision, desiredState, acks) {
		return true, nil
	}
	if op.Phase == icapturepolicy.PhaseRollingBack {
		err := c.store.CompleteRollback(ctx, op.ID, op.LeaseToken, now)
		if err == nil {
			c.observeTerminal(ctx, op, icapturepolicy.PhaseRollbackCompleted, op.RestoredState, now)
		}
		return true, err
	}
	err = c.store.CompleteSucceeded(ctx, op.ID, op.LeaseToken, now)
	if err == nil {
		c.observeTerminal(ctx, op, icapturepolicy.PhaseSucceeded, op.RequestedState, now)
	}
	return true, err
}

func (c *Controller) observeTerminal(ctx context.Context, op icapturepolicy.Operation, phase, effectiveState string, at time.Time) {
	if c.onTerminal != nil {
		revision, desiredState := op.PolicyRevision, op.RequestedState
		failureCode := ""
		if phase == icapturepolicy.PhaseRollbackCompleted || phase == icapturepolicy.PhaseRollbackFailed {
			revision, desiredState = op.CompensationRevision, op.RestoredState
		}
		if phase == icapturepolicy.PhaseRollbackFailed {
			failureCode = "TRACE_EVIDENCE_ROLLBACK_FAILED"
		}
		c.onTerminal(ctx, TerminalEvent{OperationID: op.ID, PolicyRevision: revision, DesiredState: desiredState, EffectiveState: effectiveState, Phase: phase, FailureCode: failureCode, At: at})
	}
}

func converged(operationID string, revision uint64, desiredState string, acks []icapturepolicy.ExpectedAcknowledgement) bool {
	if len(acks) == 0 {
		return false
	}
	for _, ack := range acks {
		if ack.OperationID != operationID || ack.PolicyRevision != revision || ack.AcknowledgedAt == nil {
			return false
		}
		if ack.Validate() != nil {
			return false
		}
		if desiredState == icapturepolicy.StateEnabled {
			if ack.AckState != icapturepolicy.AckReady || !ack.Ready {
				return false
			}
			if ack.EndpointKind == icapturepolicy.EndpointTraceGateway && ack.TraceDisposition != icapturepolicy.DispositionNotApplicable {
				return false
			}
			if ack.EndpointKind == icapturepolicy.EndpointEvidencePublisher && ack.EvidenceDisposition != icapturepolicy.DispositionNotApplicable {
				return false
			}
			continue
		}
		if ack.AckState != icapturepolicy.AckDisabled || !ack.Ready {
			return false
		}
		if ack.EndpointKind == icapturepolicy.EndpointTraceGateway && ack.TraceDisposition != icapturepolicy.DispositionComplete && ack.TraceDisposition != icapturepolicy.DispositionGap && ack.TraceDisposition != icapturepolicy.DispositionNotApplicable {
			return false
		}
		if ack.EndpointKind == icapturepolicy.EndpointEvidencePublisher && ack.EvidenceDisposition != icapturepolicy.DispositionComplete && ack.EvidenceDisposition != icapturepolicy.DispositionGap && ack.EvidenceDisposition != icapturepolicy.DispositionNotApplicable {
			return false
		}
	}
	return true
}

func newOperationID() (string, error) {
	return fmt.Sprintf("trace-op-%d", time.Now().UTC().UnixNano()), nil
}
