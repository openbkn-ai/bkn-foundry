// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

// RequestCapturePolicyChange persists only the desired transition. Endpoint
// convergence is deliberately asynchronous and is completed by the control
// worker through the v033 operation lease.
func (s *Store) RequestCapturePolicyChange(ctx context.Context, request capturepolicysvc.ChangeRequest) (capturepolicysvc.Snapshot, error) {
	return s.requestCapturePolicyChange(ctx, request, nil)
}

// RequestCapturePolicyChangeWithExpected persists a transition together with
// the fixed endpoint set the controller is responsible for converging.  The
// expected set is part of the same transaction as the policy revision; a
// controller can therefore never observe a command without its ACK set.
func (s *Store) RequestCapturePolicyChangeWithExpected(ctx context.Context, request capturepolicysvc.ChangeRequest, expected []icapturepolicy.ExpectedAcknowledgement) (capturepolicysvc.Snapshot, error) {
	return s.requestCapturePolicyChange(ctx, request, expected)
}

func (s *Store) requestCapturePolicyChange(ctx context.Context, request capturepolicysvc.ChangeRequest, expected []icapturepolicy.ExpectedAcknowledgement) (capturepolicysvc.Snapshot, error) {
	if request.ExpectedRevision == 0 || (request.DesiredState != capturepolicysvc.StateEnabled && request.DesiredState != capturepolicysvc.StateDisabled) {
		return capturepolicysvc.Snapshot{}, capturepolicysvc.ErrInvalidChangeRequest
	}
	now := time.Now().UTC()
	state, err := s.ReadControlState(ctx)
	if err != nil {
		return capturepolicysvc.Snapshot{}, err
	}
	if state.CurrentRevision != request.ExpectedRevision {
		return capturepolicysvc.Snapshot{}, capturepolicysvc.ErrRevisionConflict
	}
	if state.ActiveOperationID != "" {
		return capturepolicysvc.Snapshot{}, capturepolicysvc.ErrOperationInProgress
	}
	if state.DesiredState == string(request.DesiredState) {
		return s.ReadCapturePolicySnapshot(ctx)
	}
	var latestRevision uint64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0) FROM bkn_trace_capture_policy_revisions`).Scan(&latestRevision); err != nil {
		return capturepolicysvc.Snapshot{}, err
	}
	operationID, err := newCapturePolicyOperationID()
	if err != nil {
		return capturepolicysvc.Snapshot{}, err
	}
	operation := icapturepolicy.Operation{
		ID: operationID, PolicyRevision: latestRevision + 1,
		RequestedState: string(request.DesiredState), ExpectedRevision: state.CurrentRevision,
		Phase: icapturepolicy.PhasePending, ConvergenceDeadline: now.Add(10 * time.Minute),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.StartOperation(ctx, state, operation, expected); err != nil {
		switch {
		case errors.Is(err, icapturepolicy.ErrRevisionConflict):
			return capturepolicysvc.Snapshot{}, capturepolicysvc.ErrRevisionConflict
		case errors.Is(err, icapturepolicy.ErrOperationInProgress):
			return capturepolicysvc.Snapshot{}, capturepolicysvc.ErrOperationInProgress
		default:
			return capturepolicysvc.Snapshot{}, err
		}
	}
	return s.ReadCapturePolicySnapshot(ctx)
}

func newCapturePolicyOperationID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "trace-op-" + hex.EncodeToString(raw[:]), nil
}

var _ interface {
	RequestCapturePolicyChange(context.Context, capturepolicysvc.ChangeRequest) (capturepolicysvc.Snapshot, error)
} = (*Store)(nil)
