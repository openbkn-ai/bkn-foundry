// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

// Package evidencemigration implements only the Evidence bridge manifest
// tables. It is intentionally independent of sessionstore/schema.go; Session
// 0 owns registration of v030 with the shared migration manifest.
package evidencemigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencemigration"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("Evidence migration database is required")
	}
	return &Store{db: db}, nil
}

func (s *Store) LookupAdmission(ctx context.Context, manifestID, eventID string) (ievidencemigration.Admission, bool, error) {
	if manifestID == "" || eventID == "" {
		return ievidencemigration.Admission{}, false, errors.New("Evidence migration manifest and event IDs are required")
	}
	var admission ievidencemigration.Admission
	var state string
	err := s.db.QueryRowContext(ctx, `
		SELECT m.manifest_id, m.state, e.entry_id, e.event_id, e.payload_hash
		FROM bkn_trace_evidence_migration_manifests m
		JOIN bkn_trace_evidence_migration_entries e ON e.manifest_id=m.manifest_id
		WHERE m.manifest_id=? AND e.event_id=?`, manifestID, eventID,
	).Scan(&admission.ManifestID, &state, &admission.EntryID, &admission.EventID, &admission.PayloadHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ievidencemigration.Admission{}, false, nil
	}
	if err != nil {
		return ievidencemigration.Admission{}, false, fmt.Errorf("read Evidence migration admission: %w", err)
	}
	admission.State = ievidencemigration.ManifestState(state)
	return admission, true, nil
}

func (s *Store) RecordConsumerResult(ctx context.Context, result ievidencemigration.ConsumerResult) error {
	if result.ManifestID == "" || result.EntryID == "" || result.Topic != "openbkn.evidence.v1" || result.Partition < 0 || result.Offset < 0 || !validAdjudication(result.Adjudication) || !validObservation(result.Observation) {
		return errors.New("invalid Evidence migration consumer result")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin Evidence migration result transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var existingAdjudication, existingReason string
	err = tx.QueryRowContext(ctx, `SELECT adjudication, COALESCE(reason_code, '')
		FROM bkn_trace_evidence_migration_results
		WHERE manifest_id=? AND entry_id=? FOR UPDATE`, result.ManifestID, result.EntryID).
		Scan(&existingAdjudication, &existingReason)
	switch {
	case err == nil:
		if existingAdjudication != string(result.Adjudication) || !compatibleReason(existingReason, result.ReasonCode) {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO bkn_trace_evidence_migration_result_conflicts (
					manifest_id, entry_id, existing_adjudication, incoming_adjudication,
					existing_reason_code, incoming_reason_code, detected_at
				) VALUES (?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))
				ON DUPLICATE KEY UPDATE detected_at=VALUES(detected_at)`,
				result.ManifestID, result.EntryID, existingAdjudication, result.Adjudication, existingReason, result.ReasonCode)
			if err != nil {
				return fmt.Errorf("record Evidence migration result conflict: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit Evidence migration result conflict: %w", err)
			}
			return nil
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE bkn_trace_evidence_migration_results
			SET attempts=attempts+1, last_observation=?, last_observed_at=UTC_TIMESTAMP(6),
				ledger_ingest_sequence=COALESCE(ledger_ingest_sequence, NULLIF(?, 0)),
				reason_code=COALESCE(reason_code, NULLIF(?, ''))
			WHERE manifest_id=? AND entry_id=?`, result.Observation, result.IngestSequence, result.ReasonCode, result.ManifestID, result.EntryID)
		if err != nil {
			return fmt.Errorf("update Evidence migration consumer result: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_evidence_migration_results (
			manifest_id, entry_id, adjudication, first_observation, last_observation,
			reason_code, topic, partition_id, offset_id, ledger_ingest_sequence,
			first_observed_at, last_observed_at, attempts
		) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, NULLIF(?, 0), UTC_TIMESTAMP(6), UTC_TIMESTAMP(6), 1)`,
			result.ManifestID, result.EntryID, result.Adjudication, result.Observation, result.Observation,
			result.ReasonCode, result.Topic, result.Partition, result.Offset, result.IngestSequence,
		)
	default:
		return fmt.Errorf("read existing Evidence migration consumer result: %w", err)
	}
	if err != nil {
		return fmt.Errorf("upsert Evidence migration consumer result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Evidence migration consumer result: %w", err)
	}
	return nil
}

func compatibleReason(existing, incoming string) bool {
	return existing == "" || incoming == "" || existing == incoming
}

func validAdjudication(value ievidencemigration.Adjudication) bool {
	switch value {
	case ievidencemigration.AdjudicationLedgerCommitted, ievidencemigration.AdjudicationConflict, ievidencemigration.AdjudicationRejected, ievidencemigration.AdjudicationVerifiedDelivered, ievidencemigration.AdjudicationCoverageGap:
		return true
	default:
		return false
	}
}

func validObservation(value string) bool {
	switch value {
	case "accepted", "deduplicated", "conflict", "rejected", "verified_delivered", "coverage_gap":
		return true
	default:
		return false
	}
}
