// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencemigration"
)

// ReconcileManifest records only the terminal classes owned by the one-time
// reconciler. Publish entries remain exclusively owned by the Kafka Consumer.
// A delivered source row is terminal only when the immutable Ledger identity
// (event_id + payload_hash) matches exactly.
func (s *Store) ReconcileManifest(ctx context.Context, manifestID string) error {
	if manifestID == "" {
		return errors.New("Evidence migration manifest ID is required")
	}
	var state string
	err := s.db.QueryRowContext(ctx, "SELECT state FROM bkn_trace_evidence_migration_manifests WHERE manifest_id=?", manifestID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("Evidence migration manifest not found")
	}
	if err != nil {
		return fmt.Errorf("read Evidence migration reconciliation state: %w", err)
	}
	if state == string(ievidencemigration.ManifestClosed) {
		return nil
	}
	if state != string(ievidencemigration.ManifestActive) {
		return errors.New("Evidence migration reconciliation requires an active manifest")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.entry_id,e.classification,e.classification_reason,e.event_id,e.payload_hash
		FROM bkn_trace_evidence_migration_entries e WHERE e.manifest_id=?
		ORDER BY e.source_service,e.source_table,e.source_primary_key`, manifestID)
	if err != nil {
		return fmt.Errorf("read Evidence migration reconciliation entries: %w", err)
	}
	type entry struct {
		id, classification, reason string
		eventID, payloadHash       sql.NullString
	}
	entries := make([]entry, 0)
	for rows.Next() {
		var value entry
		if err := rows.Scan(&value.id, &value.classification, &value.reason, &value.eventID, &value.payloadHash); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan Evidence migration reconciliation entry: %w", err)
		}
		entries = append(entries, value)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate Evidence migration reconciliation entries: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close Evidence migration reconciliation entries: %w", err)
	}
	for _, value := range entries {
		var result ievidencemigration.ReconcilerResult
		switch value.classification {
		case "publish":
			continue
		case "coverage_gap":
			result = ievidencemigration.ReconcilerResult{ManifestID: manifestID, EntryID: value.id, Adjudication: ievidencemigration.AdjudicationCoverageGap, ReasonCode: value.reason}
		case "verify_delivered":
			if !value.eventID.Valid || !value.payloadHash.Valid {
				return errors.New("delivered Evidence migration entry has incomplete immutable identity")
			}
			var ledgerPayloadHash sql.NullString
			var hasRejection, hasConflict bool
			err := s.db.QueryRowContext(ctx, `SELECT l.payload_hash,
				EXISTS(SELECT 1 FROM bkn_trace_evidence_ingest_rejections r WHERE r.topic='openbkn.evidence.v1' AND r.migration_id=? AND r.event_id=?),
				EXISTS(SELECT 1 FROM bkn_trace_event_conflicts c WHERE c.event_id=?)
				FROM (SELECT ? AS event_id) expected
				LEFT JOIN bkn_trace_evidence_event_ledger l ON l.event_id=expected.event_id`,
				manifestID, value.eventID.String, value.eventID.String, value.eventID.String).
				Scan(&ledgerPayloadHash, &hasRejection, &hasConflict)
			if err != nil {
				return fmt.Errorf("verify delivered Evidence migration Ledger identity: %w", err)
			}
			if !ledgerPayloadHash.Valid || ledgerPayloadHash.String != value.payloadHash.String || hasRejection || hasConflict {
				return fmt.Errorf("delivered Evidence migration entry %s has a missing or conflicting Ledger identity", value.id)
			}
			result = ievidencemigration.ReconcilerResult{ManifestID: manifestID, EntryID: value.id, Adjudication: ievidencemigration.AdjudicationVerifiedDelivered, ReasonCode: value.reason}
		default:
			return fmt.Errorf("unknown immutable Evidence migration classification %q", value.classification)
		}
		if err := s.RecordReconcilerResult(ctx, result); err != nil {
			return fmt.Errorf("persist Evidence migration reconciliation result for %s: %w", value.id, err)
		}
	}
	return nil
}
