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

// ClaimCapturePolicyOperation leases the active operation for one controller
// worker. Claiming and the pending -> enabling/disabling transition are one
// transaction, so a crashed worker can be replaced without a second queue or
// a Kubernetes write permission.
func (s *Store) ClaimCapturePolicyOperation(ctx context.Context, workerID string, lease time.Duration, now time.Time) (icapturepolicy.Operation, bool, error) {
	if workerID == "" || lease <= 0 || now.IsZero() {
		return icapturepolicy.Operation{}, false, icapturepolicy.ErrInvalidOperation
	}
	var operation icapturepolicy.Operation
	claimed := false
	err := s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		var operationID sql.NullString
		if err := tx.QueryRowContext(ctx, `
			SELECT active_operation_id
			FROM bkn_trace_capture_control_state
			WHERE singleton_id = 1 FOR UPDATE`).Scan(&operationID); errors.Is(err, sql.ErrNoRows) {
			return icapturepolicy.ErrControlStateNotInitialized
		} else if err != nil {
			return err
		}
		if !operationID.Valid || operationID.String == "" {
			return nil
		}
		var phase, requestedState, errorCode sql.NullString
		var expectedRevision, policyRevision, leaseToken uint64
		var leaseOwner sql.NullString
		var leaseExpires, convergenceDeadline, createdAt, updatedAt, terminalAt sql.NullTime
		var compensationRevision sql.NullInt64
		var restoredState sql.NullString
		err := tx.QueryRowContext(ctx, `
			SELECT policy_revision, requested_state, expected_revision, phase, error_code,
			       lease_owner, lease_token, lease_expires_at, convergence_deadline,
			       created_at, updated_at, terminal_at, compensation_revision, restored_state
			FROM bkn_trace_capture_operations
			WHERE operation_id = ? FOR UPDATE`, operationID.String).Scan(
			&policyRevision, &requestedState, &expectedRevision, &phase, &errorCode,
			&leaseOwner, &leaseToken, &leaseExpires, &convergenceDeadline,
			&createdAt, &updatedAt, &terminalAt, &compensationRevision, &restoredState,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return icapturepolicy.ErrOperationNotFound
		}
		if err != nil {
			return err
		}
		if phase.String == icapturepolicy.PhaseSucceeded || phase.String == icapturepolicy.PhaseFailed || phase.String == icapturepolicy.PhaseRollbackCompleted || phase.String == icapturepolicy.PhaseRollbackFailed {
			return nil
		}
		if leaseExpires.Valid && leaseExpires.Time.After(now) && leaseOwner.Valid && leaseOwner.String != workerID {
			return nil
		}
		if phase.String == icapturepolicy.PhasePending {
			if requestedState.String == icapturepolicy.StateEnabled {
				phase.String = icapturepolicy.PhaseEnabling
			} else {
				phase.String = icapturepolicy.PhaseDisabling
			}
		}
		leaseToken++
		expires := now.Add(lease)
		result, err := tx.ExecContext(ctx, `
			UPDATE bkn_trace_capture_operations
			SET phase = ?, lease_owner = ?, lease_token = ?, lease_expires_at = ?, updated_at = ?
			WHERE operation_id = ?`, phase.String, workerID, leaseToken, expires.UTC(), now.UTC(), operationID.String)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return icapturepolicy.ErrLeaseConflict
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bkn_trace_capture_operation_events
				(operation_id, phase, event_type, lease_token, error_code, gap_reason, recorded_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, operationID.String, phase.String, icapturepolicy.EventPhaseChanged, leaseToken, nullString(errorCode.String), nil, now.UTC()); err != nil {
			return err
		}
		operation = icapturepolicy.Operation{
			ID: operationID.String, PolicyRevision: policyRevision, RequestedState: requestedState.String,
			ExpectedRevision: expectedRevision, Phase: phase.String, ErrorCode: errorCode.String,
			LeaseOwner: workerID, LeaseToken: leaseToken, LeaseExpiresAt: &expires,
			ConvergenceDeadline: convergenceDeadline.Time.UTC(), CreatedAt: createdAt.Time.UTC(), UpdatedAt: now.UTC(),
		}
		if terminalAt.Valid {
			value := terminalAt.Time.UTC()
			operation.TerminalAt = &value
		}
		if compensationRevision.Valid {
			operation.CompensationRevision = uint64(compensationRevision.Int64)
		}
		if restoredState.Valid {
			operation.RestoredState = restoredState.String
		}
		claimed = true
		return nil
	})
	return operation, claimed, err
}

// ReadCapturePolicyAcknowledgements returns the durable ACK facts for an
// operation. The controller uses this exact set to decide convergence; it
// never infers disposition from processor telemetry.
func (s *Store) ReadCapturePolicyAcknowledgements(ctx context.Context, operationID string, revision uint64) ([]icapturepolicy.ExpectedAcknowledgement, error) {
	if operationID == "" || revision == 0 {
		return nil, icapturepolicy.ErrInvalidAcknowledgement
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT operation_id, endpoint_kind, instance_id, workload_identity, process_boot_id,
		       policy_revision, ready, ack_state, acknowledged_at, exported_count, dropped_count,
		       unaccounted_count, trace_disposition, last_accepted_sequence, published_count,
		       queue_empty, evidence_disposition, gap_reason
		FROM bkn_trace_capture_operation_acknowledgements
		WHERE operation_id = ? AND policy_revision = ?
		ORDER BY endpoint_kind, instance_id, workload_identity, process_boot_id`, operationID, revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]icapturepolicy.ExpectedAcknowledgement, 0)
	for rows.Next() {
		var a icapturepolicy.ExpectedAcknowledgement
		var ready sql.NullBool
		var acknowledgedAt sql.NullTime
		var exported, dropped, unaccounted, accepted, published sql.NullInt64
		var queueEmpty sql.NullBool
		var ackState, traceDisposition, evidenceDisposition, gapReason sql.NullString
		if err := rows.Scan(&a.OperationID, &a.EndpointKind, &a.InstanceID, &a.WorkloadIdentity, &a.ProcessBootID,
			&a.PolicyRevision, &ready, &ackState, &acknowledgedAt, &exported, &dropped, &unaccounted,
			&traceDisposition, &accepted, &published, &queueEmpty, &evidenceDisposition, &gapReason); err != nil {
			return nil, err
		}
		a.Ready = ready.Valid && ready.Bool
		a.AckState = ackState.String
		if acknowledgedAt.Valid {
			value := acknowledgedAt.Time.UTC()
			a.AcknowledgedAt = &value
		}
		if exported.Valid {
			value := uint64(exported.Int64)
			a.ExportedCount = &value
		}
		if dropped.Valid {
			value := uint64(dropped.Int64)
			a.DroppedCount = &value
		}
		if unaccounted.Valid {
			value := uint64(unaccounted.Int64)
			a.UnaccountedCount = &value
		}
		if accepted.Valid {
			value := uint64(accepted.Int64)
			a.LastAcceptedSequence = &value
		}
		if published.Valid {
			value := uint64(published.Int64)
			a.PublishedCount = &value
		}
		if queueEmpty.Valid {
			value := queueEmpty.Bool
			a.QueueEmpty = &value
		}
		a.TraceDisposition, a.EvidenceDisposition, a.GapReason = traceDisposition.String, evidenceDisposition.String, gapReason.String
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

var _ interface {
	NextCapturePolicyRevision(context.Context) (uint64, error)
	ListReadyEndpointLeases(context.Context, time.Time) ([]icapturepolicy.EndpointLease, error)
	ClaimCapturePolicyOperation(context.Context, string, time.Duration, time.Time) (icapturepolicy.Operation, bool, error)
	ReadCapturePolicyAcknowledgements(context.Context, string, uint64) ([]icapturepolicy.ExpectedAcknowledgement, error)
} = (*Store)(nil)

func (s *Store) ListReadyEndpointLeases(ctx context.Context, now time.Time) ([]icapturepolicy.EndpointLease, error) {
	if now.IsZero() {
		return nil, icapturepolicy.ErrInvalidEndpointLease
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT endpoint_kind, instance_id, workload_identity, process_boot_id,
		       observed_revision, ready, heartbeat_at, lease_expires_at, updated_at
		FROM bkn_trace_capture_endpoint_leases
		WHERE ready = TRUE AND lease_expires_at > ?
		ORDER BY endpoint_kind, instance_id, workload_identity, process_boot_id`, now.UTC())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]icapturepolicy.EndpointLease, 0)
	for rows.Next() {
		var lease icapturepolicy.EndpointLease
		if err := rows.Scan(&lease.EndpointKind, &lease.InstanceID, &lease.WorkloadIdentity, &lease.ProcessBootID, &lease.ObservedRevision, &lease.Ready, &lease.HeartbeatAt, &lease.LeaseExpiresAt, &lease.UpdatedAt); err != nil {
			return nil, err
		}
		lease.HeartbeatAt = lease.HeartbeatAt.UTC()
		lease.LeaseExpiresAt = lease.LeaseExpiresAt.UTC()
		lease.UpdatedAt = lease.UpdatedAt.UTC()
		result = append(result, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) NextCapturePolicyRevision(ctx context.Context) (uint64, error) {
	var latest uint64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0) FROM bkn_trace_capture_policy_revisions`).Scan(&latest); err != nil {
		return 0, err
	}
	return latest + 1, nil
}
