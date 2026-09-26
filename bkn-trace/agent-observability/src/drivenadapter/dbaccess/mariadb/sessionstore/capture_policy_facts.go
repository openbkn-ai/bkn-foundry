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

var _ icapturepolicy.FactWriter = (*Store)(nil)

func (s *Store) PersistPolicyRevision(ctx context.Context, revision icapturepolicy.PolicyRevision) error {
	if err := revision.Validate(); err != nil {
		return err
	}
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		return s.persistPolicyRevisionTx(ctx, tx, revision)
	})
}

func (s *Store) persistPolicyRevisionTx(ctx context.Context, tx *sql.Tx, revision icapturepolicy.PolicyRevision) error {
	var enabled bool
	err := tx.QueryRowContext(ctx, `
		SELECT admission_enabled
		FROM bkn_trace_capture_policy_revisions
		WHERE revision = ? FOR UPDATE`, revision.Revision).Scan(&enabled)
	switch {
	case err == nil:
		if enabled != revision.AdmissionEnabled {
			return fmt.Errorf("policy revision %d: %w", revision.Revision, icapturepolicy.ErrCaptureFactConflict)
		}
	case errors.Is(err, sql.ErrNoRows):
		var maxRevision uint64
		if err := tx.QueryRowContext(ctx, `
			SELECT revision
			FROM bkn_trace_capture_policy_revisions
			ORDER BY revision DESC LIMIT 1 FOR UPDATE`).Scan(&maxRevision); errors.Is(err, sql.ErrNoRows) {
			maxRevision = 0
		} else if err != nil {
			return err
		}
		if revision.Revision <= maxRevision {
			return fmt.Errorf("policy revision %d is not greater than %d: %w", revision.Revision, maxRevision, icapturepolicy.ErrCaptureFactConflict)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bkn_trace_capture_policy_revisions (revision, admission_enabled, recorded_at)
			VALUES (?, ?, ?)`, revision.Revision, revision.AdmissionEnabled, revision.RecordedAt.UTC()); err != nil {
			return err
		}
	default:
		return err
	}
	return nil
}

func (s *Store) RegisterProducer(ctx context.Context, registration icapturepolicy.ProducerRegistration) error {
	if err := registration.Validate(); err != nil {
		return err
	}
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		return s.registerProducerTx(ctx, tx, registration)
	})
}

// RegisterEvidencePublisherHeartbeat atomically refreshes a verified Publisher
// lease and registers that exact process boot for the current enabled policy
// revision. The control-state row serializes this write with policy changes, so
// a stale process cannot register against an earlier revision after a switch.
func (s *Store) RegisterEvidencePublisherHeartbeat(ctx context.Context, lease icapturepolicy.EndpointLease) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	if lease.EndpointKind != icapturepolicy.EndpointEvidencePublisher || !lease.Ready || lease.InstanceID != lease.WorkloadIdentity+"#"+lease.ProcessBootID {
		return icapturepolicy.ErrInvalidProducerRegistration
	}
	registration := icapturepolicy.ProducerRegistration{
		InstanceID: lease.InstanceID, PolicyRevision: lease.ObservedRevision,
		ProcessBootID: lease.ProcessBootID, RegistrationState: "registered", RegisteredAt: lease.HeartbeatAt,
	}
	if err := registration.Validate(); err != nil {
		return err
	}
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		var currentRevision uint64
		err := tx.QueryRowContext(ctx, `
			SELECT current_revision
			FROM bkn_trace_capture_control_state
			WHERE singleton_id = 1 FOR UPDATE`).Scan(&currentRevision)
		if errors.Is(err, sql.ErrNoRows) {
			return icapturepolicy.ErrControlStateNotInitialized
		}
		if err != nil {
			return err
		}
		if currentRevision != lease.ObservedRevision {
			return fmt.Errorf("observed policy revision %d is not current %d: %w", lease.ObservedRevision, currentRevision, icapturepolicy.ErrCaptureFactConflict)
		}
		var admissionEnabled bool
		err = tx.QueryRowContext(ctx, `
			SELECT admission_enabled
			FROM bkn_trace_capture_policy_revisions
			WHERE revision = ? FOR UPDATE`, lease.ObservedRevision).Scan(&admissionEnabled)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !admissionEnabled) {
			return icapturepolicy.ErrCaptureFactConflict
		}
		if err != nil {
			return err
		}
		if err := s.upsertEndpointLeaseTx(ctx, tx, lease); err != nil {
			return err
		}
		return s.registerProducerTx(ctx, tx, registration)
	})
}

func (s *Store) registerProducerTx(ctx context.Context, tx *sql.Tx, registration icapturepolicy.ProducerRegistration) error {
	var bootID, state string
	var revokedAt sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT process_boot_id, registration_state, revoked_at
		FROM bkn_trace_producer_instance_registrations
		WHERE producer_instance_id = ? AND policy_revision = ? FOR UPDATE`, registration.InstanceID, registration.PolicyRevision).Scan(&bootID, &state, &revokedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bkn_trace_producer_instance_registrations
				(producer_instance_id, policy_revision, process_boot_id, registration_state, registered_at, revoked_at)
			VALUES (?, ?, ?, ?, ?, ?)`, registration.InstanceID, registration.PolicyRevision,
			registration.ProcessBootID, registration.RegistrationState, registration.RegisteredAt.UTC(), nullableTime(registration.RevokedAt)); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if bootID != registration.ProcessBootID || state != registration.RegistrationState || !sameNullableTime(revokedAt, registration.RevokedAt) {
			return fmt.Errorf("producer instance %q revision %d: %w", registration.InstanceID, registration.PolicyRevision, icapturepolicy.ErrCaptureFactConflict)
		}
	}
	return nil
}

func (s *Store) PersistClosureWatermark(ctx context.Context, watermark icapturepolicy.ClosureWatermark) error {
	if err := watermark.Validate(); err != nil {
		return err
	}
	return s.withSerializableTransaction(ctx, func(tx *sql.Tx) error {
		return s.persistClosureWatermarkTx(ctx, tx, watermark)
	})
}

func (s *Store) persistClosureWatermarkTx(ctx context.Context, tx *sql.Tx, watermark icapturepolicy.ClosureWatermark) error {
	var sequence uint64
	var closedAt, acknowledgedAt sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT last_accepted_sequence, closed_at, acknowledged_at
		FROM bkn_trace_producer_closure_watermarks
		WHERE producer_instance_id = ? AND policy_revision = ? FOR UPDATE`, watermark.InstanceID, watermark.PolicyRevision).Scan(&sequence, &closedAt, &acknowledgedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bkn_trace_producer_closure_watermarks
				(producer_instance_id, policy_revision, last_accepted_sequence, closed_at, acknowledged_at)
			VALUES (?, ?, ?, ?, ?)`, watermark.InstanceID, watermark.PolicyRevision, watermark.LastAcceptedSequence,
			watermark.ClosedAt.UTC(), watermark.AcknowledgedAt.UTC()); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if sequence != watermark.LastAcceptedSequence || !sameNullableTime(closedAt, &watermark.ClosedAt) || !sameNullableTime(acknowledgedAt, &watermark.AcknowledgedAt) {
			return fmt.Errorf("closure watermark %q revision %d: %w", watermark.InstanceID, watermark.PolicyRevision, icapturepolicy.ErrCaptureFactConflict)
		}
	}
	return nil
}

func sameNullableTime(value sql.NullTime, expected *time.Time) bool {
	if expected == nil {
		return !value.Valid
	}
	return value.Valid && value.Time.UTC().Equal(expected.UTC())
}
