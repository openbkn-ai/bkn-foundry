// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceadmission"
)

var _ ievidenceadmission.ReadOnlySource = (*Store)(nil)

// Lookup reads only the requested historical revision and per-instance facts.
// Missing rows are returned as an empty/partial snapshot for protocol-level
// permanent admission rejection; database failures remain retryable errors.
func (s *Store) Lookup(ctx context.Context, revision uint64, instanceID string) (ievidenceadmission.Snapshot, error) {
	var snapshot ievidenceadmission.Snapshot
	var enabled bool
	err := s.db.QueryRowContext(ctx, `
		SELECT admission_enabled
		FROM bkn_trace_capture_policy_revisions
		WHERE revision = ?`, revision).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	snapshot.Revision, snapshot.Enabled = revision, enabled
	if instanceID == "" {
		return snapshot, nil
	}
	var bootID string
	err = s.db.QueryRowContext(ctx, `
		SELECT process_boot_id
		FROM bkn_trace_producer_instance_registrations
		WHERE producer_instance_id = ? AND policy_revision = ?
		  AND registration_state = 'registered' AND revoked_at IS NULL`,
		instanceID, revision,
	).Scan(&bootID)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	if bootID == "" {
		return snapshot, errors.New("persisted producer instance has empty process boot ID")
	}
	snapshot.InstanceID, snapshot.RegisteredRevision, snapshot.ProcessBootID = instanceID, revision, bootID
	var sequence uint64
	var closedAt time.Time
	err = s.db.QueryRowContext(ctx, `
		SELECT last_accepted_sequence, closed_at
		FROM bkn_trace_producer_closure_watermarks
		WHERE producer_instance_id = ? AND policy_revision = ?`, instanceID, revision,
	).Scan(&sequence, &closedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	snapshot.Closure = ievidenceadmission.ClosureWatermark{
		InstanceID: instanceID, Revision: revision, LastAcceptedSequence: sequence,
		ClosedAt: closedAt.UTC().Format(time.RFC3339Nano),
	}
	return snapshot, nil
}
