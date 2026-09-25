// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
)

// ReadCapturePolicySnapshot is the durable read adapter for the public
// control-plane model. It reads only v033 control facts and never derives
// effective state from a worker or Helm setting.
func (s *Store) ReadCapturePolicySnapshot(ctx context.Context) (capturepolicysvc.Snapshot, error) {
	state, err := s.ReadControlState(ctx)
	if err != nil {
		return capturepolicysvc.Snapshot{}, err
	}
	operation := capturepolicysvc.Operation{
		ID:               state.LastOperationID,
		Phase:            capturepolicysvc.PhaseSucceeded,
		RequestedState:   capturepolicysvc.State(state.EffectiveState),
		ExpectedRevision: state.CurrentRevision,
	}
	if operation.ID == "" {
		operation.ID = "bootstrap"
	}
	if state.ActiveOperationID != "" {
		operation.ID = state.ActiveOperationID
		if err := s.readCapturePolicyOperation(ctx, &operation); err != nil {
			return capturepolicysvc.Snapshot{}, err
		}
	}
	if operation.ID != "bootstrap" && state.ActiveOperationID == "" {
		if err := s.readCapturePolicyOperation(ctx, &operation); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return capturepolicysvc.Snapshot{}, err
		}
	}
	acknowledgements, err := s.readCapturePolicyAcknowledgements(ctx, operation.ID, state.CurrentRevision)
	if err != nil {
		return capturepolicysvc.Snapshot{}, err
	}
	snapshot := capturepolicysvc.Snapshot{
		Revision:           state.CurrentRevision,
		DesiredState:       capturepolicysvc.State(state.DesiredState),
		EffectiveState:     capturepolicysvc.State(state.EffectiveState),
		LastStableRevision: state.LastStableRevision,
		CoverageGap:        state.CoverageGap,
		Operation:          operation,
		Acknowledgements:   acknowledgements,
	}
	return snapshot, snapshot.Validate()
}

func (s *Store) readCapturePolicyOperation(ctx context.Context, operation *capturepolicysvc.Operation) error {
	var requestedState, phase, errorCode sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT requested_state, phase, expected_revision, error_code
		FROM bkn_trace_capture_operations
		WHERE operation_id = ?`, operation.ID).Scan(&requestedState, &phase, &operation.ExpectedRevision, &errorCode)
	if err != nil {
		return err
	}
	if requestedState.Valid {
		operation.RequestedState = capturepolicysvc.State(requestedState.String)
	}
	if phase.Valid {
		operation.Phase = capturepolicysvc.Phase(phase.String)
	}
	if errorCode.Valid {
		operation.ErrorCode = errorCode.String
	}
	return nil
}

func (s *Store) ReadOperation(ctx context.Context, operationID string) (capturepolicysvc.Operation, error) {
	if operationID == "" {
		return capturepolicysvc.Operation{}, capturepolicysvc.ErrOperationNotFound
	}
	operation := capturepolicysvc.Operation{ID: operationID}
	if err := s.readCapturePolicyOperation(ctx, &operation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return capturepolicysvc.Operation{}, capturepolicysvc.ErrOperationNotFound
		}
		return capturepolicysvc.Operation{}, err
	}
	return operation, nil
}

func (s *Store) readCapturePolicyAcknowledgements(ctx context.Context, operationID string, revision uint64) ([]capturepolicysvc.EndpointAcknowledgement, error) {
	if operationID == "bootstrap" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT instance_id, process_boot_id, ready, ack_state,
		       exported_count, dropped_count, unaccounted_count, policy_revision
		FROM bkn_trace_capture_operation_acknowledgements
		WHERE operation_id = ? AND policy_revision = ?
		ORDER BY endpoint_kind, instance_id, process_boot_id`, operationID, revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]capturepolicysvc.EndpointAcknowledgement, 0)
	for rows.Next() {
		var instanceID, generation, ackState string
		var ready bool
		var exported, dropped, unaccounted sql.NullInt64
		var policyRevision uint64
		if err := rows.Scan(&instanceID, &generation, &ready, &ackState, &exported, &dropped, &unaccounted, &policyRevision); err != nil {
			return nil, err
		}
		queue := capturepolicysvc.QueueDisposition{}
		if exported.Valid {
			queue.Exported = int(exported.Int64)
		}
		if dropped.Valid {
			queue.Dropped = int(dropped.Int64)
		}
		if unaccounted.Valid {
			value := int(unaccounted.Int64)
			queue.Unaccounted = &value
		}
		result = append(result, capturepolicysvc.EndpointAcknowledgement{
			InstanceID: instanceID, Generation: generation, Ready: ready,
			State: capturepolicysvc.AckState(ackState), Queue: queue, Revision: policyRevision,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

var _ interface {
	ReadCapturePolicySnapshot(context.Context) (capturepolicysvc.Snapshot, error)
	ReadOperation(context.Context, string) (capturepolicysvc.Operation, error)
} = (*Store)(nil)
