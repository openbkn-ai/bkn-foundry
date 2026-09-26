// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

var _ icapturepolicy.Store = (*Store)(nil)

// EnsureControlState creates the singleton policy and its first immutable
// revision exactly once. It is intentionally a runtime bootstrap operation,
// not a migration seed: existing installations keep their authoritative
// state and configuration changes never overwrite it.
func (s *Store) EnsureControlState(ctx context.Context, enabled bool, now time.Time) error {
	if now.IsZero() {
		return icapturepolicy.ErrInvalidControlState
	}
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		var revision uint64
		err := tx.QueryRowContext(ctx, `
			SELECT current_revision
			FROM bkn_trace_capture_control_state
			WHERE singleton_id = 1
			FOR UPDATE`).Scan(&revision)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		state := icapturepolicy.StateDisabled
		if enabled {
			state = icapturepolicy.StateEnabled
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bkn_trace_capture_control_state
				(singleton_id, current_revision, desired_state, effective_state,
				 last_stable_revision, coverage_gap, coverage_gap_updated_at, updated_at)
			VALUES (?, ?, ?, ?, ?, FALSE, ?, ?)`,
			uint8(1), uint64(1), state, state, uint64(1), now.UTC(), now.UTC()); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO bkn_trace_capture_policy_revisions
				(revision, admission_enabled, recorded_at)
			VALUES (?, ?, ?)`, uint64(1), enabled, now.UTC())
		return err
	})
}

func (s *Store) withSerializableTransaction(ctx context.Context, fn func(*sql.Tx) error) error {
	var lastErr error
	for attempt := 0; attempt < transactionRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			if !retryableTransactionError(err) {
				return err
			}
			lastErr = err
			if waitErr := waitForTransactionRetry(ctx, attempt); waitErr != nil {
				return waitErr
			}
			continue
		}
		callbackErr := fn(tx)
		if callbackErr != nil {
			_ = tx.Rollback()
			if !retryableTransactionError(callbackErr) {
				return callbackErr
			}
			lastErr = callbackErr
			if waitErr := waitForTransactionRetry(ctx, attempt); waitErr != nil {
				return waitErr
			}
			continue
		}
		if err := tx.Commit(); err != nil {
			if !retryableTransactionError(err) {
				return err
			}
			lastErr = err
			if waitErr := waitForTransactionRetry(ctx, attempt); waitErr != nil {
				return waitErr
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("transaction retry budget exhausted: %w", lastErr)
}

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
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		return s.startOperationTx(ctx, tx, operation, expected)
	})
}

func (s *Store) startOperationTx(ctx context.Context, tx *sql.Tx, operation icapturepolicy.Operation, expected []icapturepolicy.ExpectedAcknowledgement) error {
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
	} else if currentRevision != operation.ExpectedRevision {
		return icapturepolicy.ErrRevisionConflict
	} else if activeID.Valid && activeID.String != "" {
		return icapturepolicy.ErrOperationInProgress
	}
	var maxRevision uint64
	if err := tx.QueryRowContext(ctx, `
		SELECT revision
		FROM bkn_trace_capture_policy_revisions
		ORDER BY revision DESC LIMIT 1 FOR UPDATE`).Scan(&maxRevision); errors.Is(err, sql.ErrNoRows) {
		maxRevision = 0
	} else if err != nil {
		return err
	}
	if operation.PolicyRevision != maxRevision+1 {
		return icapturepolicy.ErrRevisionConflict
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_policy_revisions (revision, admission_enabled, recorded_at)
		VALUES (?, ?, ?)`, operation.PolicyRevision, operation.RequestedState == icapturepolicy.StateEnabled, operation.CreatedAt.UTC()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_operations
			(operation_id, policy_revision, requested_state, expected_revision, phase,
			 compensation_revision, restored_state, lease_owner, lease_token, lease_expires_at, convergence_deadline,
			 created_at, updated_at, terminal_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, operation.ID, operation.PolicyRevision,
		operation.RequestedState, operation.ExpectedRevision, operation.Phase,
		nil, nil,
		nullString(operation.LeaseOwner), operation.LeaseToken, nullableTime(operation.LeaseExpiresAt),
		operation.ConvergenceDeadline.UTC(), operation.CreatedAt.UTC(), operation.UpdatedAt.UTC(), nullableTime(operation.TerminalAt)); err != nil {
		return err
	}
	for _, acknowledgement := range expected {
		if err := insertExpectedAcknowledgement(ctx, tx, acknowledgement); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE bkn_trace_capture_control_state
		SET current_revision = ?, desired_state = ?, active_operation_id = ?,
		    last_operation_id = ?, updated_at = ?
		WHERE singleton_id = 1 AND current_revision = ? AND (active_operation_id IS NULL OR active_operation_id = '')`,
		operation.PolicyRevision, operation.RequestedState, operation.ID, operation.ID, operation.UpdatedAt.UTC(), operation.ExpectedRevision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return icapturepolicy.ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_operation_events
			(operation_id, phase, event_type, lease_token, error_code, gap_reason, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, operation.ID, operation.Phase, icapturepolicy.EventOperationCreated, operation.LeaseToken, nil, nil, operation.CreatedAt.UTC()); err != nil {
		return err
	}
	return nil
}

func (s *Store) AdvanceOperation(ctx context.Context, operationID string, leaseToken uint64, phase, errorCode, gapReason string, now time.Time) error {
	if operationID == "" || leaseToken == 0 || phase == "" || now.IsZero() {
		return icapturepolicy.ErrInvalidOperation
	}
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		return s.advanceOperationTx(ctx, tx, operationID, leaseToken, phase, errorCode, gapReason, now)
	})
}

func (s *Store) advanceOperationTx(ctx context.Context, tx *sql.Tx, operationID string, leaseToken uint64, phase, errorCode, gapReason string, now time.Time) error {
	var storedToken uint64
	var currentPhase string
	var leaseExpires sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT phase, lease_token, lease_expires_at
		FROM bkn_trace_capture_operations
		WHERE operation_id = ? FOR UPDATE`, operationID).Scan(&currentPhase, &storedToken, &leaseExpires)
	if errors.Is(err, sql.ErrNoRows) {
		return icapturepolicy.ErrOperationNotFound
	}
	if err != nil {
		return err
	}
	if storedToken != leaseToken || (leaseExpires.Valid && !leaseExpires.Time.After(now)) {
		return icapturepolicy.ErrLeaseConflict
	}
	if !icapturepolicy.CanTransition(currentPhase, phase) || phase == icapturepolicy.PhaseSucceeded || phase == icapturepolicy.PhaseFailed || phase == icapturepolicy.PhaseRollbackCompleted || phase == icapturepolicy.PhaseRollbackFailed {
		if currentPhase == phase && currentPhase != icapturepolicy.PhasePending {
			return nil
		}
		return icapturepolicy.ErrInvalidTransition
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE bkn_trace_capture_operations
		SET phase = ?, error_code = ?, updated_at = ?
		WHERE operation_id = ? AND lease_token = ?`, phase, nullString(errorCode), now.UTC(), operationID, leaseToken); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_operation_events
			(operation_id, phase, event_type, lease_token, error_code, gap_reason, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, operationID, phase, icapturepolicy.EventPhaseChanged, leaseToken, nullString(errorCode), nullString(gapReason), now.UTC()); err != nil {
		return err
	}
	return nil
}

func (s *Store) BeginRollback(ctx context.Context, operationID string, leaseToken, compensationRevision uint64, restoredState string, expected []icapturepolicy.ExpectedAcknowledgement, now time.Time) error {
	if operationID == "" || leaseToken == 0 || compensationRevision == 0 || (restoredState != icapturepolicy.StateEnabled && restoredState != icapturepolicy.StateDisabled) || now.IsZero() {
		return icapturepolicy.ErrInvalidOperation
	}
	for _, acknowledgement := range expected {
		if err := acknowledgement.Validate(); err != nil || acknowledgement.OperationID != operationID || acknowledgement.PolicyRevision != compensationRevision {
			return icapturepolicy.ErrExpectedSetConflict
		}
	}
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		return s.beginRollbackTx(ctx, tx, operationID, leaseToken, compensationRevision, restoredState, expected, now)
	})
}

func (s *Store) beginRollbackTx(ctx context.Context, tx *sql.Tx, operationID string, leaseToken, compensationRevision uint64, restoredState string, expected []icapturepolicy.ExpectedAcknowledgement, now time.Time) error {
	var policyRevision, storedToken uint64
	var currentPhase string
	var operationRestoredState sql.NullString
	var storedCompensationRevision sql.NullInt64
	var leaseExpires sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT policy_revision, phase, lease_token, lease_expires_at, compensation_revision, restored_state
		FROM bkn_trace_capture_operations
		WHERE operation_id = ? FOR UPDATE`, operationID).Scan(&policyRevision, &currentPhase, &storedToken, &leaseExpires, &storedCompensationRevision, &operationRestoredState)
	if errors.Is(err, sql.ErrNoRows) {
		return icapturepolicy.ErrOperationNotFound
	}
	if err != nil {
		return err
	}
	var currentRevision uint64
	var currentDesired, currentEffective, activeOperationID string
	if err := tx.QueryRowContext(ctx, `
		SELECT current_revision, desired_state, effective_state, COALESCE(active_operation_id, '')
		FROM bkn_trace_capture_control_state
		WHERE singleton_id = 1 FOR UPDATE`).Scan(&currentRevision, &currentDesired, &currentEffective, &activeOperationID); err != nil {
		return err
	}
	_ = currentDesired
	if storedToken != leaseToken || (leaseExpires.Valid && !leaseExpires.Time.After(now)) {
		return icapturepolicy.ErrLeaseConflict
	}
	if currentPhase == icapturepolicy.PhaseRollingBack {
		if !storedCompensationRevision.Valid || uint64(storedCompensationRevision.Int64) != compensationRevision || !operationRestoredState.Valid || operationRestoredState.String != restoredState || currentRevision != compensationRevision || currentDesired != restoredState || activeOperationID != operationID {
			return icapturepolicy.ErrRevisionConflict
		}
		if err := verifyFrozenAcknowledgements(ctx, tx, operationID, compensationRevision, expected); err != nil {
			return err
		}
		return nil
	}
	if !icapturepolicy.CanTransition(currentPhase, icapturepolicy.PhaseRollingBack) || activeOperationID != operationID || restoredState != currentEffective || compensationRevision <= policyRevision {
		return icapturepolicy.ErrInvalidTransition
	}
	var maxRevision uint64
	if err := tx.QueryRowContext(ctx, `
		SELECT revision
		FROM bkn_trace_capture_policy_revisions
		ORDER BY revision DESC LIMIT 1 FOR UPDATE`).Scan(&maxRevision); errors.Is(err, sql.ErrNoRows) {
		maxRevision = 0
	} else if err != nil {
		return err
	}
	if compensationRevision != maxRevision+1 {
		return icapturepolicy.ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_policy_revisions (revision, admission_enabled, recorded_at)
		VALUES (?, ?, ?)`, compensationRevision, restoredState == icapturepolicy.StateEnabled, now.UTC()); err != nil {
		return err
	}
	for _, acknowledgement := range expected {
		if err := insertExpectedAcknowledgement(ctx, tx, acknowledgement); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE bkn_trace_capture_operations
		SET phase = ?, compensation_revision = ?, restored_state = ?, convergence_deadline = ?, updated_at = ?
		WHERE operation_id = ? AND lease_token = ?`, icapturepolicy.PhaseRollingBack, compensationRevision, restoredState, now.Add(10*time.Minute).UTC(), now.UTC(), operationID, leaseToken); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE bkn_trace_capture_control_state
		SET current_revision = ?, desired_state = ?, updated_at = ?
		WHERE singleton_id = 1 AND current_revision = ? AND active_operation_id = ?`, compensationRevision, restoredState, now.UTC(), currentRevision, operationID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return icapturepolicy.ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_operation_events
			(operation_id, phase, event_type, lease_token, error_code, gap_reason, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, operationID, icapturepolicy.PhaseRollingBack, icapturepolicy.EventRollbackStarted, leaseToken, nil, nil, now.UTC()); err != nil {
		return err
	}
	return nil
}

func (s *Store) CompleteSucceeded(ctx context.Context, operationID string, leaseToken uint64, now time.Time) error {
	return s.completeOperation(ctx, operationID, leaseToken, icapturepolicy.PhaseSucceeded, now)
}

func (s *Store) CompleteFailed(ctx context.Context, operationID string, leaseToken uint64, now time.Time) error {
	return s.completeOperation(ctx, operationID, leaseToken, icapturepolicy.PhaseFailed, now)
}

func (s *Store) CompleteRollback(ctx context.Context, operationID string, leaseToken uint64, now time.Time) error {
	return s.completeOperation(ctx, operationID, leaseToken, icapturepolicy.PhaseRollbackCompleted, now)
}

func (s *Store) CompleteRollbackFailed(ctx context.Context, operationID string, leaseToken uint64, now time.Time) error {
	return s.completeOperation(ctx, operationID, leaseToken, icapturepolicy.PhaseRollbackFailed, now)
}

func (s *Store) completeOperation(ctx context.Context, operationID string, leaseToken uint64, outcome string, now time.Time) error {
	if operationID == "" || leaseToken == 0 || now.IsZero() {
		return icapturepolicy.ErrInvalidOperation
	}
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		return s.completeOperationTx(ctx, tx, operationID, leaseToken, outcome, now)
	})
}

func (s *Store) completeOperationTx(ctx context.Context, tx *sql.Tx, operationID string, leaseToken uint64, outcome string, now time.Time) error {
	var policyRevision, expectedRevision, storedToken uint64
	var compensationRevision sql.NullInt64
	var requestedState, currentPhase string
	var restoredState sql.NullString
	var leaseExpires sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT policy_revision, expected_revision, requested_state, phase, lease_token,
		       lease_expires_at, compensation_revision, restored_state
		FROM bkn_trace_capture_operations
		WHERE operation_id = ? FOR UPDATE`, operationID).Scan(&policyRevision, &expectedRevision, &requestedState, &currentPhase, &storedToken, &leaseExpires, &compensationRevision, &restoredState)
	if errors.Is(err, sql.ErrNoRows) {
		return icapturepolicy.ErrOperationNotFound
	}
	if err != nil {
		return err
	}
	if storedToken != leaseToken || (leaseExpires.Valid && !leaseExpires.Time.After(now)) {
		return icapturepolicy.ErrLeaseConflict
	}
	if currentPhase == outcome {
		return nil
	}
	if !icapturepolicy.CanTransition(currentPhase, outcome) {
		if currentPhase == icapturepolicy.PhaseSucceeded || currentPhase == icapturepolicy.PhaseFailed || currentPhase == icapturepolicy.PhaseRollbackCompleted || currentPhase == icapturepolicy.PhaseRollbackFailed {
			return icapturepolicy.ErrTerminalOperation
		}
		return icapturepolicy.ErrInvalidTransition
	}
	if outcome == icapturepolicy.PhaseRollbackCompleted && (!compensationRevision.Valid || compensationRevision.Int64 <= 0 || !restoredState.Valid || restoredState.String == "") {
		return icapturepolicy.ErrInvalidOperation
	}
	operationResult, err := tx.ExecContext(ctx, `
		UPDATE bkn_trace_capture_operations
		SET phase = ?, updated_at = ?, terminal_at = ?
		WHERE operation_id = ? AND lease_token = ?`, outcome, now.UTC(), now.UTC(), operationID, leaseToken)
	if err != nil {
		return err
	}
	operationRows, err := operationResult.RowsAffected()
	if err != nil || operationRows != 1 {
		return icapturepolicy.ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_capture_operation_events
			(operation_id, phase, event_type, lease_token, error_code, gap_reason, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, operationID, outcome, icapturepolicy.EventPhaseChanged, leaseToken, nil, nil, now.UTC()); err != nil {
		return err
	}
	var controlResult sql.Result
	switch outcome {
	case icapturepolicy.PhaseSucceeded:
		controlResult, err = tx.ExecContext(ctx, `
			UPDATE bkn_trace_capture_control_state
			SET active_operation_id = NULL, last_operation_id = ?, effective_state = ?,
			    last_stable_revision = ?, updated_at = ?
			WHERE singleton_id = 1 AND active_operation_id = ?`, operationID, requestedState, policyRevision, now.UTC(), operationID)
	case icapturepolicy.PhaseFailed:
		controlResult, err = tx.ExecContext(ctx, `
			UPDATE bkn_trace_capture_control_state
			SET current_revision = ?, desired_state = effective_state, active_operation_id = NULL,
			    last_operation_id = ?, updated_at = ?
			WHERE singleton_id = 1 AND active_operation_id = ?`, expectedRevision, operationID, now.UTC(), operationID)
	case icapturepolicy.PhaseRollbackCompleted:
		controlResult, err = tx.ExecContext(ctx, `
			UPDATE bkn_trace_capture_control_state
			SET current_revision = ?, desired_state = ?, effective_state = ?,
			last_stable_revision = ?, active_operation_id = NULL, last_operation_id = ?, updated_at = ?
			WHERE singleton_id = 1 AND active_operation_id = ?`, uint64(compensationRevision.Int64), restoredState.String, restoredState.String, uint64(compensationRevision.Int64), operationID, now.UTC(), operationID)
	case icapturepolicy.PhaseRollbackFailed:
		controlResult, err = tx.ExecContext(ctx, `
			UPDATE bkn_trace_capture_control_state
			SET active_operation_id = NULL, last_operation_id = ?, updated_at = ?
			WHERE singleton_id = 1 AND active_operation_id = ?`, operationID, now.UTC(), operationID)
	}
	if err != nil {
		return err
	}
	rows, err := controlResult.RowsAffected()
	if err != nil || rows != 1 {
		return icapturepolicy.ErrRevisionConflict
	}
	return nil
}

func (s *Store) AppendOperationEvent(ctx context.Context, event icapturepolicy.OperationEvent) error {
	if event.OperationID == "" || !icapturepolicy.ValidPhase(event.Phase) || !icapturepolicy.ValidEventType(event.EventType) || event.RecordedAt.IsZero() {
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
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		return s.upsertEndpointLeaseTx(ctx, tx, lease)
	})
}

func (s *Store) upsertEndpointLeaseTx(ctx context.Context, tx *sql.Tx, lease icapturepolicy.EndpointLease) error {
	var workloadIdentity, processBootID string
	err := tx.QueryRowContext(ctx, `
		SELECT workload_identity, process_boot_id
		FROM bkn_trace_capture_endpoint_leases
		WHERE endpoint_kind = ? AND instance_id = ? AND process_boot_id = ? FOR UPDATE`, lease.EndpointKind, lease.InstanceID, lease.ProcessBootID).Scan(&workloadIdentity, &processBootID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO bkn_trace_capture_endpoint_leases
				(endpoint_kind, instance_id, workload_identity, process_boot_id, observed_revision,
				 ready, heartbeat_at, lease_expires_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			lease.EndpointKind, lease.InstanceID, lease.WorkloadIdentity, lease.ProcessBootID, lease.ObservedRevision,
			lease.Ready, lease.HeartbeatAt.UTC(), lease.LeaseExpiresAt.UTC(), lease.UpdatedAt.UTC())
		if err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if workloadIdentity != lease.WorkloadIdentity || processBootID != lease.ProcessBootID {
		return icapturepolicy.ErrExpectedSetConflict
	}
	_, err = tx.ExecContext(ctx, `
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
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) RecordAcknowledgement(ctx context.Context, acknowledgement icapturepolicy.ExpectedAcknowledgement) error {
	if err := acknowledgement.Validate(); err != nil {
		return err
	}
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		return s.recordAcknowledgementTx(ctx, tx, acknowledgement)
	})
}

// RecordEvidencePublisherAcknowledgement persists a disabled publisher ACK
// and its closure watermark atomically. The ACK already carries the frozen
// Session 1 disposition (including the last accepted sequence); no second
// wire contract or asynchronous follow-up is needed. closedAt is captured by
// Agent Observability when it receives the ACK, so the Kafka broker timestamp
// comparison does not depend on the publisher clock or millisecond truncation.
func (s *Store) RecordEvidencePublisherAcknowledgement(ctx context.Context, acknowledgement icapturepolicy.ExpectedAcknowledgement, closedAt time.Time) error {
	if err := acknowledgement.Validate(); err != nil {
		return err
	}
	if acknowledgement.EndpointKind != icapturepolicy.EndpointEvidencePublisher || acknowledgement.AckState != icapturepolicy.AckDisabled || acknowledgement.AcknowledgedAt == nil || acknowledgement.LastAcceptedSequence == nil || acknowledgement.EvidenceDisposition != icapturepolicy.DispositionComplete || acknowledgement.QueueEmpty == nil || !*acknowledgement.QueueEmpty || closedAt.IsZero() {
		return icapturepolicy.ErrInvalidAcknowledgement
	}
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		registrationRevision, err := s.registeredProducerRevisionTx(ctx, tx, acknowledgement)
		if err != nil {
			return err
		}
		if err := s.recordAcknowledgementTx(ctx, tx, acknowledgement); err != nil {
			return err
		}
		watermark := icapturepolicy.ClosureWatermark{
			// The operation ACK uses the newly-created disabled revision, but
			// queued Kafka records carry the last enabled registration revision.
			// Store the watermark against that record revision so the consumer's
			// historical lookup can reject late records deterministically.
			InstanceID: acknowledgement.InstanceID, PolicyRevision: registrationRevision,
			LastAcceptedSequence: *acknowledgement.LastAcceptedSequence,
			ClosedAt:             closedAt.UTC(), AcknowledgedAt: closedAt.UTC(),
		}
		return s.persistClosureWatermarkTx(ctx, tx, watermark)
	})
}

func (s *Store) registeredProducerRevisionTx(ctx context.Context, tx *sql.Tx, acknowledgement icapturepolicy.ExpectedAcknowledgement) (uint64, error) {
	var registrationRevision uint64
	var processBootID, registrationState string
	err := tx.QueryRowContext(ctx, `
		SELECT policy_revision, process_boot_id, registration_state
		FROM bkn_trace_producer_instance_registrations
		WHERE producer_instance_id = ? AND policy_revision < ?
		  AND registration_state = 'registered' AND revoked_at IS NULL
		ORDER BY policy_revision DESC LIMIT 1 FOR UPDATE`,
		acknowledgement.InstanceID, acknowledgement.PolicyRevision).Scan(&registrationRevision, &processBootID, &registrationState)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, icapturepolicy.ErrExpectedSetConflict
	}
	if err != nil {
		return 0, err
	}
	if processBootID != acknowledgement.ProcessBootID || registrationState != "registered" {
		return 0, icapturepolicy.ErrExpectedSetConflict
	}
	return registrationRevision, nil
}

func (s *Store) recordAcknowledgementTx(ctx context.Context, tx *sql.Tx, acknowledgement icapturepolicy.ExpectedAcknowledgement) error {
	var expectedRevision uint64
	var workloadIdentity, processBootID string
	err := tx.QueryRowContext(ctx, `
		SELECT policy_revision, workload_identity, process_boot_id
		FROM bkn_trace_capture_operation_acknowledgements
		WHERE operation_id = ? AND policy_revision = ? AND endpoint_kind = ? AND instance_id = ?
		  AND workload_identity = ? AND process_boot_id = ? FOR UPDATE`,
		acknowledgement.OperationID, acknowledgement.PolicyRevision, acknowledgement.EndpointKind, acknowledgement.InstanceID,
		acknowledgement.WorkloadIdentity, acknowledgement.ProcessBootID).Scan(&expectedRevision, &workloadIdentity, &processBootID)
	if errors.Is(err, sql.ErrNoRows) {
		return icapturepolicy.ErrExpectedSetConflict
	}
	if err != nil {
		return err
	}
	if expectedRevision != acknowledgement.PolicyRevision || workloadIdentity != acknowledgement.WorkloadIdentity || processBootID != acknowledgement.ProcessBootID {
		return icapturepolicy.ErrExpectedSetConflict
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE bkn_trace_capture_operation_acknowledgements
		SET ready = ?, ack_state = ?, acknowledged_at = ?, exported_count = ?, dropped_count = ?,
		    unaccounted_count = ?, trace_disposition = ?, last_accepted_sequence = ?,
		    published_count = ?, queue_empty = ?, evidence_disposition = ?, gap_reason = ?
		WHERE operation_id = ? AND endpoint_kind = ? AND instance_id = ?
		  AND workload_identity = ? AND process_boot_id = ? AND policy_revision = ?`,
		acknowledgement.Ready, acknowledgement.AckState, nullableTime(acknowledgement.AcknowledgedAt),
		uint64Ptr(acknowledgement.ExportedCount), uint64Ptr(acknowledgement.DroppedCount), uint64Ptr(acknowledgement.UnaccountedCount),
		nullString(acknowledgement.TraceDisposition), uint64Ptr(acknowledgement.LastAcceptedSequence),
		uint64Ptr(acknowledgement.PublishedCount), boolPtr(acknowledgement.QueueEmpty), nullString(acknowledgement.EvidenceDisposition),
		nullString(acknowledgement.GapReason), acknowledgement.OperationID, acknowledgement.EndpointKind, acknowledgement.InstanceID,
		acknowledgement.WorkloadIdentity, acknowledgement.ProcessBootID, acknowledgement.PolicyRevision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 0 && rows != 1 {
		return icapturepolicy.ErrExpectedSetConflict
	}
	return nil
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

type acknowledgementIdentity struct {
	EndpointKind     string
	InstanceID       string
	WorkloadIdentity string
	ProcessBootID    string
}

func verifyFrozenAcknowledgements(ctx context.Context, tx *sql.Tx, operationID string, policyRevision uint64, expected []icapturepolicy.ExpectedAcknowledgement) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT endpoint_kind, instance_id, workload_identity, process_boot_id
		FROM bkn_trace_capture_operation_acknowledgements
		WHERE operation_id = ? AND policy_revision = ?
		ORDER BY endpoint_kind, instance_id, workload_identity, process_boot_id`, operationID, policyRevision)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	frozen := make(map[acknowledgementIdentity]struct{}, len(expected))
	for rows.Next() {
		var endpointKind, instanceID, workloadIdentity, processBootID string
		if err := rows.Scan(&endpointKind, &instanceID, &workloadIdentity, &processBootID); err != nil {
			return err
		}
		identity := acknowledgementIdentity{EndpointKind: endpointKind, InstanceID: instanceID, WorkloadIdentity: workloadIdentity, ProcessBootID: processBootID}
		if _, exists := frozen[identity]; exists {
			return icapturepolicy.ErrExpectedSetConflict
		}
		frozen[identity] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(frozen) != len(expected) {
		return icapturepolicy.ErrExpectedSetConflict
	}
	seen := make(map[acknowledgementIdentity]struct{}, len(expected))
	for _, acknowledgement := range expected {
		if err := acknowledgement.Validate(); err != nil || acknowledgement.OperationID != operationID || acknowledgement.PolicyRevision != policyRevision {
			return icapturepolicy.ErrExpectedSetConflict
		}
		identity := acknowledgementIdentity{EndpointKind: acknowledgement.EndpointKind, InstanceID: acknowledgement.InstanceID, WorkloadIdentity: acknowledgement.WorkloadIdentity, ProcessBootID: acknowledgement.ProcessBootID}
		if _, duplicate := seen[identity]; duplicate {
			return icapturepolicy.ErrExpectedSetConflict
		}
		seen[identity] = struct{}{}
		if _, exists := frozen[identity]; !exists {
			return icapturepolicy.ErrExpectedSetConflict
		}
	}
	return nil
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
