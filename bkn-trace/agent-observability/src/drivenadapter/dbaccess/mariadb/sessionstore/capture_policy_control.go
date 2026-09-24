// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

var _ icapturepolicy.Store = (*Store)(nil)

func (s *Store) ReadControlState(ctx context.Context) (icapturepolicy.ControlState, error) {
	var state icapturepolicy.ControlState
	var activeID, lastID, reason sql.NullString
	var gapStarted sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT current_revision, desired_state, effective_state, last_stable_revision,
		       active_operation_id, last_operation_id, coverage_gap, coverage_gap_reason,
		       coverage_gap_started_at, coverage_gap_updated_at, updated_at
		FROM bkn_trace_capture_control_state
		WHERE singleton_id = 1`,
	).Scan(&state.CurrentRevision, &state.DesiredState, &state.EffectiveState, &state.LastStableRevision,
		&activeID, &lastID, &state.CoverageGap, &reason, &gapStarted, &state.CoverageGapUpdatedAt, &state.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return state, icapturepolicy.ErrControlStateNotInitialized
	}
	if err != nil {
		return state, err
	}
	if activeID.Valid {
		state.ActiveOperationID = activeID.String
	}
	if lastID.Valid {
		state.LastOperationID = lastID.String
	}
	if reason.Valid {
		state.CoverageGapReason = reason.String
	}
	if gapStarted.Valid {
		value := gapStarted.Time.UTC()
		state.CoverageGapStartedAt = &value
	}
	state.CoverageGapUpdatedAt = state.CoverageGapUpdatedAt.UTC()
	state.UpdatedAt = state.UpdatedAt.UTC()
	return state, nil
}

func (s *Store) StartOperation(ctx context.Context, expectedState icapturepolicy.ControlState, operation icapturepolicy.Operation, expected []icapturepolicy.ExpectedAcknowledgement) error {
	if err := expectedState.Validate(); err != nil {
		return err
	}
	if err := operation.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var currentRevision uint64
	var activeID sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT current_revision, active_operation_id
		FROM bkn_trace_capture_control_state
		WHERE singleton_id = 1 FOR UPDATE`,
	).Scan(&currentRevision, &activeID); errors.Is(err, sql.ErrNoRows) {
		return icapturepolicy.ErrControlStateNotInitialized
	} else if err != nil {
		return err
	} else if currentRevision != operation.ExpectedRevision || operation.PolicyRevision != currentRevision+1 {
		return icapturepolicy.ErrRevisionConflict
	} else if activeID.Valid && activeID.String != "" {
		return icapturepolicy.ErrOperationInProgress
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_policy_revisions (revision, admission_enabled, recorded_at)
		VALUES (?, ?, ?)`, operation.PolicyRevision, operation.RequestedState == icapturepolicy.StateEnabled, operation.CreatedAt.UTC()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_operations
			(operation_id, policy_revision, requested_state, expected_revision, phase,
			 lease_owner, lease_token, lease_expires_at, convergence_deadline,
			 created_at, updated_at, terminal_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, operation.ID, operation.PolicyRevision,
		operation.RequestedState, operation.ExpectedRevision, operation.Phase,
		nullString(operation.LeaseOwner), operation.LeaseToken, nullableTime(operation.LeaseExpiresAt),
		operation.ConvergenceDeadline.UTC(), operation.CreatedAt.UTC(), operation.UpdatedAt.UTC(), nullableTime(operation.TerminalAt)); err != nil {
		return err
	}
	for _, acknowledgement := range expected {
		if err := insertExpectedAcknowledgement(ctx, tx, acknowledgement); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE bkn_trace_capture_control_state
		SET current_revision = ?, desired_state = ?, active_operation_id = ?,
		    last_operation_id = ?, updated_at = ?
		WHERE singleton_id = 1 AND current_revision = ? AND (active_operation_id IS NULL OR active_operation_id = '')`,
		operation.PolicyRevision, operation.RequestedState, operation.ID, operation.ID, operation.UpdatedAt.UTC(), operation.ExpectedRevision); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_operation_events
			(operation_id, phase, event_type, lease_token, error_code, gap_reason, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, operation.ID, operation.Phase, "operation_created", operation.LeaseToken, nil, nil, operation.CreatedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AdvanceOperation(ctx context.Context, operationID string, leaseToken uint64, phase, errorCode, gapReason string, now time.Time) error {
	if operationID == "" || leaseToken == 0 || phase == "" || now.IsZero() {
		return icapturepolicy.ErrInvalidOperation
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var storedToken, policyRevision uint64
	var requestedState, currentPhase string
	var leaseExpires sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT policy_revision, requested_state, phase, lease_token, lease_expires_at
		FROM bkn_trace_capture_operations
		WHERE operation_id = ? FOR UPDATE`, operationID).Scan(&policyRevision, &requestedState, &currentPhase, &storedToken, &leaseExpires)
	if errors.Is(err, sql.ErrNoRows) {
		return icapturepolicy.ErrOperationNotFound
	}
	if err != nil {
		return err
	}
	if storedToken != leaseToken || (leaseExpires.Valid && !leaseExpires.Time.After(now)) {
		return icapturepolicy.ErrLeaseConflict
	}
	if currentPhase == "succeeded" || currentPhase == "failed" || currentPhase == "rollback_completed" || currentPhase == "rollback_failed" {
		return icapturepolicy.ErrOperationInProgress
	}
	var terminalAt any
	if phase == "succeeded" || phase == "failed" || phase == "rollback_completed" || phase == "rollback_failed" {
		terminalAt = now.UTC()
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE bkn_trace_capture_operations
		SET phase = ?, error_code = ?, updated_at = ?, terminal_at = ?
		WHERE operation_id = ? AND lease_token = ?`, phase, nullString(errorCode), now.UTC(), terminalAt, operationID, leaseToken); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_operation_events
			(operation_id, phase, event_type, lease_token, error_code, gap_reason, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, operationID, phase, "operation_phase_changed", leaseToken, nullString(errorCode), nullString(gapReason), now.UTC()); err != nil {
		return err
	}
	if terminalAt != nil {
		if phase == "succeeded" || phase == "rollback_completed" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE bkn_trace_capture_control_state
				SET active_operation_id = NULL, last_operation_id = ?, effective_state = ?,
				    last_stable_revision = ?, updated_at = ?
				WHERE singleton_id = 1 AND active_operation_id = ?`, operationID, requestedState, policyRevision, now.UTC(), operationID); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
				UPDATE bkn_trace_capture_control_state
				SET active_operation_id = NULL, last_operation_id = ?, updated_at = ?
				WHERE singleton_id = 1 AND active_operation_id = ?`, operationID, now.UTC(), operationID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) AppendOperationEvent(ctx context.Context, event icapturepolicy.OperationEvent) error {
	if event.OperationID == "" || event.Phase == "" || event.EventType == "" || event.RecordedAt.IsZero() {
		return icapturepolicy.ErrInvalidOperation
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_operation_events
			(operation_id, phase, event_type, lease_token, error_code, gap_reason, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, event.OperationID, event.Phase, event.EventType, event.LeaseToken,
		nullString(event.ErrorCode), nullString(event.GapReason), event.RecordedAt.UTC())
	return err
}

func (s *Store) UpsertEndpointLease(ctx context.Context, lease icapturepolicy.EndpointLease) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_endpoint_leases
			(endpoint_kind, instance_id, workload_identity, process_boot_id, observed_revision,
			 ready, heartbeat_at, lease_expires_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE workload_identity = VALUES(workload_identity),
			process_boot_id = VALUES(process_boot_id), observed_revision = VALUES(observed_revision),
			ready = VALUES(ready), heartbeat_at = VALUES(heartbeat_at),
			lease_expires_at = VALUES(lease_expires_at), updated_at = VALUES(updated_at)`,
		lease.EndpointKind, lease.InstanceID, lease.WorkloadIdentity, lease.ProcessBootID, lease.ObservedRevision,
		lease.Ready, lease.HeartbeatAt.UTC(), lease.LeaseExpiresAt.UTC(), lease.UpdatedAt.UTC())
	return err
}

func (s *Store) RecordAcknowledgement(ctx context.Context, acknowledgement icapturepolicy.ExpectedAcknowledgement) error {
	if err := acknowledgement.Validate(); err != nil {
		return err
	}
	var expectedRevision uint64
	err := s.db.QueryRowContext(ctx, `
		SELECT policy_revision
		FROM bkn_trace_capture_operation_acknowledgements
		WHERE operation_id = ? AND endpoint_kind = ? AND instance_id = ?`,
		acknowledgement.OperationID, acknowledgement.EndpointKind, acknowledgement.InstanceID).Scan(&expectedRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return icapturepolicy.ErrExpectedSetConflict
	}
	if err != nil {
		return err
	}
	if expectedRevision != acknowledgement.PolicyRevision {
		return icapturepolicy.ErrExpectedSetConflict
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE bkn_trace_capture_operation_acknowledgements
		SET ready = ?, ack_state = ?, acknowledged_at = ?, exported_count = ?, dropped_count = ?,
		    unaccounted_count = ?, trace_disposition = ?, last_accepted_sequence = ?,
		    published_count = ?, queue_empty = ?, evidence_disposition = ?, gap_reason = ?
		WHERE operation_id = ? AND endpoint_kind = ? AND instance_id = ?`,
		acknowledgement.Ready, acknowledgement.AckState, nullableTime(acknowledgement.AcknowledgedAt),
		uint64Ptr(acknowledgement.ExportedCount), uint64Ptr(acknowledgement.DroppedCount), uint64Ptr(acknowledgement.UnaccountedCount),
		nullString(acknowledgement.TraceDisposition), uint64Ptr(acknowledgement.LastAcceptedSequence),
		uint64Ptr(acknowledgement.PublishedCount), boolPtr(acknowledgement.QueueEmpty), nullString(acknowledgement.EvidenceDisposition),
		nullString(acknowledgement.GapReason), acknowledgement.OperationID, acknowledgement.EndpointKind, acknowledgement.InstanceID)
	return err
}

func insertExpectedAcknowledgement(ctx context.Context, tx *sql.Tx, a icapturepolicy.ExpectedAcknowledgement) error {
	if err := a.Validate(); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_operation_acknowledgements
			(operation_id, endpoint_kind, instance_id, workload_identity, process_boot_id, policy_revision,
			 ready, ack_state, acknowledged_at, exported_count, dropped_count, unaccounted_count,
			 trace_disposition, last_accepted_sequence, published_count, queue_empty, evidence_disposition, gap_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.OperationID, a.EndpointKind, a.InstanceID, a.WorkloadIdentity, a.ProcessBootID, a.PolicyRevision,
		a.Ready, a.AckState, nullableTime(a.AcknowledgedAt), uint64Ptr(a.ExportedCount), uint64Ptr(a.DroppedCount), uint64Ptr(a.UnaccountedCount),
		nullString(a.TraceDisposition), uint64Ptr(a.LastAcceptedSequence), uint64Ptr(a.PublishedCount), boolPtr(a.QueueEmpty),
		nullString(a.EvidenceDisposition), nullString(a.GapReason))
	return err
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func uint64Ptr(value *uint64) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolPtr(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}
