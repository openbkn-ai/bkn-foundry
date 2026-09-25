// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the repository root for the full text.

package capturepolicysvc

import (
	"context"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

// CommandService is the domain validation boundary for durable control facts.
// Public API translation and worker orchestration remain outside this service.
type CommandService struct {
	facts icapturepolicy.FactWriter
	store icapturepolicy.Store
}

func NewCommandService(facts icapturepolicy.FactWriter, store icapturepolicy.Store) *CommandService {
	return &CommandService{facts: facts, store: store}
}

func (s *CommandService) PersistPolicyRevision(ctx context.Context, revision icapturepolicy.PolicyRevision) error {
	if s == nil || s.facts == nil {
		return icapturepolicy.ErrInvalidPolicyRevision
	}
	if err := revision.Validate(); err != nil {
		return err
	}
	return s.facts.PersistPolicyRevision(ctx, revision)
}

func (s *CommandService) RegisterProducer(ctx context.Context, registration icapturepolicy.ProducerRegistration) error {
	if s == nil || s.facts == nil {
		return icapturepolicy.ErrInvalidProducerRegistration
	}
	if err := registration.Validate(); err != nil {
		return err
	}
	return s.facts.RegisterProducer(ctx, registration)
}

func (s *CommandService) PersistClosureWatermark(ctx context.Context, watermark icapturepolicy.ClosureWatermark) error {
	if s == nil || s.facts == nil {
		return icapturepolicy.ErrInvalidClosureWatermark
	}
	if err := watermark.Validate(); err != nil {
		return err
	}
	return s.facts.PersistClosureWatermark(ctx, watermark)
}

func (s *CommandService) StartOperation(ctx context.Context, state icapturepolicy.ControlState, operation icapturepolicy.Operation, expected []icapturepolicy.ExpectedAcknowledgement) error {
	if s == nil || s.store == nil {
		return icapturepolicy.ErrControlStateNotInitialized
	}
	if err := state.Validate(); err != nil {
		return err
	}
	if err := operation.Validate(); err != nil {
		return err
	}
	if operation.ExpectedRevision != state.CurrentRevision || operation.PolicyRevision <= state.CurrentRevision {
		return icapturepolicy.ErrRevisionConflict
	}
	if state.ActiveOperationID != "" {
		return icapturepolicy.ErrOperationInProgress
	}
	for _, acknowledgement := range expected {
		if err := acknowledgement.Validate(); err != nil {
			return err
		}
		if acknowledgement.OperationID != operation.ID || acknowledgement.PolicyRevision != operation.PolicyRevision {
			return icapturepolicy.ErrExpectedSetConflict
		}
	}
	return s.store.StartOperation(ctx, state, operation, expected)
}

func (s *CommandService) UpsertEndpointLease(ctx context.Context, lease icapturepolicy.EndpointLease) error {
	if s == nil || s.store == nil {
		return icapturepolicy.ErrInvalidEndpointLease
	}
	if err := lease.Validate(); err != nil {
		return err
	}
	return s.store.UpsertEndpointLease(ctx, lease)
}

func (s *CommandService) RecordAcknowledgement(ctx context.Context, acknowledgement icapturepolicy.ExpectedAcknowledgement) error {
	if s == nil || s.store == nil {
		return icapturepolicy.ErrInvalidAcknowledgement
	}
	if err := acknowledgement.Validate(); err != nil {
		return err
	}
	return s.store.RecordAcknowledgement(ctx, acknowledgement)
}

func (s *CommandService) AdvanceOperation(ctx context.Context, operationID string, leaseToken uint64, phase, errorCode, gapReason string, now time.Time) error {
	if s == nil || s.store == nil || operationID == "" || leaseToken == 0 || phase == "" || now.IsZero() {
		return icapturepolicy.ErrInvalidOperation
	}
	return s.store.AdvanceOperation(ctx, operationID, leaseToken, phase, errorCode, gapReason, now)
}

func (s *CommandService) BeginRollback(ctx context.Context, operationID string, leaseToken, compensationRevision uint64, restoredState string, now time.Time) error {
	if s == nil || s.store == nil || operationID == "" || leaseToken == 0 || compensationRevision == 0 || !validStableState(restoredState) || now.IsZero() {
		return icapturepolicy.ErrInvalidOperation
	}
	return s.store.BeginRollback(ctx, operationID, leaseToken, compensationRevision, restoredState, now)
}

func (s *CommandService) CompleteSucceeded(ctx context.Context, operationID string, leaseToken uint64, now time.Time) error {
	return s.complete(ctx, operationID, leaseToken, now, func() error { return s.store.CompleteSucceeded(ctx, operationID, leaseToken, now) })
}

func (s *CommandService) CompleteFailed(ctx context.Context, operationID string, leaseToken uint64, now time.Time) error {
	return s.complete(ctx, operationID, leaseToken, now, func() error { return s.store.CompleteFailed(ctx, operationID, leaseToken, now) })
}

func (s *CommandService) CompleteRollback(ctx context.Context, operationID string, leaseToken uint64, now time.Time) error {
	return s.complete(ctx, operationID, leaseToken, now, func() error { return s.store.CompleteRollback(ctx, operationID, leaseToken, now) })
}

func (s *CommandService) CompleteRollbackFailed(ctx context.Context, operationID string, leaseToken uint64, now time.Time) error {
	return s.complete(ctx, operationID, leaseToken, now, func() error { return s.store.CompleteRollbackFailed(ctx, operationID, leaseToken, now) })
}

func (s *CommandService) complete(_ context.Context, operationID string, leaseToken uint64, now time.Time, fn func() error) error {
	if s == nil || s.store == nil || operationID == "" || leaseToken == 0 || now.IsZero() {
		return icapturepolicy.ErrInvalidOperation
	}
	return fn()
}

func validStableState(state string) bool {
	return state == icapturepolicy.StateEnabled || state == icapturepolicy.StateDisabled
}
