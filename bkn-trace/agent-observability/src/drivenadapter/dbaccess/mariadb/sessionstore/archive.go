// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// TraceArchiveSource packages terminal Interaction facts from the Trace Core
// authority. It intentionally keeps a Conversation row as a light index after
// its completed Interaction package is removed from hot storage.
type TraceArchiveSource struct {
	store          *Store
	revisionInputs bool
}

func NewTraceArchiveSource(store *Store) *TraceArchiveSource {
	return &TraceArchiveSource{store: store}
}

// NewTraceArchiveSourceWithRevisionInputs requires the revision-input schema.
// It is opt-in until migration and producer/consumer boot assembly are approved.
func NewTraceArchiveSourceWithRevisionInputs(store *Store) *TraceArchiveSource {
	return &TraceArchiveSource{store: store, revisionInputs: true}
}

func (source *TraceArchiveSource) Freeze(ctx context.Context, kind observabilityvo.ArchiveKind, archiveRange observabilityvo.ArchiveRange) ([]archivesvc.Candidate, error) {
	batch, err := source.FreezeBatch(ctx, kind, archiveRange)
	if err != nil {
		return nil, err
	}
	if len(batch.Candidates) != batch.EligibleCount {
		return nil, archivesvc.ErrIncompleteCandidates
	}
	return batch.Candidates, nil
}

// FreezeBatch bounds both selected identities and the JSONL bytes handed to storage.
func (source *TraceArchiveSource) FreezeBatch(ctx context.Context, kind observabilityvo.ArchiveKind, archiveRange observabilityvo.ArchiveRange) (archivesvc.Batch, error) {
	batch := archivesvc.Batch{Batched: true}
	if kind != observabilityvo.ArchiveKindTrace {
		return batch, nil
	}
	count, err := source.Count(ctx, kind, archiveRange)
	if err != nil {
		return batch, err
	}
	batch.EligibleCount = count
	if count == 0 {
		return batch, nil
	}
	rows, err := source.store.db.QueryContext(ctx, `SELECT interaction_id FROM bkn_trace_interactions WHERE execution_status<>? AND terminal_at<? ORDER BY terminal_at ASC, interaction_id ASC LIMIT ?`, sessionvo.InteractionActive, archiveRange.To.UTC(), archivesvc.MaxTraceArchiveCandidates)
	if err != nil {
		return batch, err
	}
	ids := make([]string, 0, archivesvc.MaxTraceArchiveCandidates)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return archivesvc.Batch{}, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return archivesvc.Batch{}, err
	}
	if closeErr != nil {
		return archivesvc.Batch{}, closeErr
	}
	// Release the identity result before opening a package transaction, including
	// with a one-connection pool.
	bytes := 0
	for _, id := range ids {
		payload, occurredAt, err := source.packageInteraction(ctx, id)
		if err != nil {
			return archivesvc.Batch{}, err
		}
		if len(payload) > archivesvc.MaxTraceArchiveCandidateBytes {
			return archivesvc.Batch{}, archivesvc.ErrArchivePayloadTooLarge
		}
		if bytes+len(payload)+1 > archivesvc.MaxTraceArchiveBatchBytes {
			break
		}
		bytes += len(payload) + 1
		batch.Candidates = append(batch.Candidates, archivesvc.Candidate{ID: id, OccurredAt: occurredAt, Payload: payload})
	}
	return batch, nil
}

// Count uses exactly the Freeze eligibility predicate without fetching or
// serializing Interaction packages for a read-only archive overview.
func (source *TraceArchiveSource) Count(ctx context.Context, kind observabilityvo.ArchiveKind, archiveRange observabilityvo.ArchiveRange) (int, error) {
	if kind != observabilityvo.ArchiveKindTrace {
		return 0, nil
	}
	var count int
	err := source.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bkn_trace_interactions WHERE execution_status<>? AND terminal_at<?`, sessionvo.InteractionActive, archiveRange.To.UTC()).Scan(&count)
	return count, err
}

func (source *TraceArchiveSource) packageInteraction(ctx context.Context, interactionID string) ([]byte, time.Time, error) {
	var interaction sessionvo.Interaction
	var conversation sessionvo.Conversation
	var operations []sessionvo.Operation
	var receipts []sessionvo.Receipt
	var facts []sessionvo.OperationCallFact
	var sealed revisionArchiveContents
	var rawIntegrity json.RawMessage
	read := func(tx isessionstore.Transaction) error {
		var ok bool
		query := interactionSelect + ` WHERE interaction_id=?`
		if source.revisionInputs {
			query += " FOR UPDATE"
		}
		var err error
		interaction, err = scanInteractionRowsWithRawIntegrity(tx.(*transaction).tx.QueryRowContext(ctx, query, interactionID), &rawIntegrity)
		if err != nil {
			return fmt.Errorf("read archive interaction: %w", err)
		}
		conversation, ok = tx.PeekConversation(interaction.ConversationID)
		if !ok {
			return fmt.Errorf("conversation %s not found", interaction.ConversationID)
		}
		operations, receipts, facts = tx.ListOperations(interactionID), tx.ListReceipts(interactionID), tx.ListOperationCallFacts(interactionID)
		if source.revisionInputs {
			var err error
			sealed, err = readRevisionArchive(ctx, tx.(*transaction).tx, interactionID)
			if err != nil {
				return err
			}
		}
		return nil
	}
	err := source.readBoundedPackage(ctx, interactionID, read)
	if err != nil {
		return nil, time.Time{}, err
	}
	// Lifecycle JSON omits these internal CAS fields. Archive them explicitly
	// with the interaction so the package does not lose derived source metadata.
	archivedInteraction := struct {
		sessionvo.Interaction
		IntegritySourceVersion uint64          `json:"record_integrity_version"`
		StoredRecordIntegrity  json.RawMessage `json:"stored_record_integrity,omitempty"`
	}{Interaction: interaction, IntegritySourceVersion: interaction.IntegritySourceVersion, StoredRecordIntegrity: rawIntegrity}
	value := map[string]any{"conversation": conversation, "interaction": archivedInteraction, "operations": operations, "receipts": receipts, "call_facts": facts}
	if source.revisionInputs {
		value["revision_evidence"] = sealed
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(payload) > archivesvc.MaxTraceArchiveCandidateBytes {
		return nil, time.Time{}, archivesvc.ErrArchivePayloadTooLarge
	}
	if interaction.TerminalAt == nil {
		return nil, time.Time{}, fmt.Errorf("terminal interaction %s has no terminal time", interactionID)
	}
	return payload, *interaction.TerminalAt, nil
}

// readBoundedPackage checks large SQL collections before decoding any payload.
// Repeatable read keeps the aggregate preflight and all subsequent lists on the
// same snapshot; the ordinary service transaction uses read committed instead.
func (source *TraceArchiveSource) readBoundedPackage(ctx context.Context, id string, read func(isessionstore.Transaction) error) error {
	tx, err := source.store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if source.revisionInputs {
		// Preserve the opt-in revision reader's existing parent lock before opening
		// the budget snapshot, so its later FindInteraction cannot see a larger row.
		var found string
		if err = tx.QueryRowContext(ctx, `SELECT interaction_id FROM bkn_trace_interactions WHERE interaction_id=? FOR UPDATE`, id).Scan(&found); err != nil {
			return err
		}
	}
	// Variable-length schema fields are capped independently of collection row
	// overhead. Only scalar counts/lengths cross the SQL boundary at this stage.
	query := `SELECT SUM(row_count),SUM(payload_bytes) FROM (
 SELECT COUNT(*) row_count,COALESCE(SUM(COALESCE(OCTET_LENGTH(closure_manifest),0)+COALESCE(OCTET_LENGTH(record_integrity_json),0)),0) payload_bytes FROM bkn_trace_interactions WHERE interaction_id=?
 UNION ALL SELECT COUNT(*),COALESCE(SUM(COALESCE(OCTET_LENGTH(causation_event_ids),0)),0) FROM bkn_trace_operations WHERE interaction_id=?
 UNION ALL SELECT COUNT(*),COALESCE(SUM(COALESCE(OCTET_LENGTH(causation_event_ids),0)+COALESCE(OCTET_LENGTH(observed_evidence_refs),0)+COALESCE(OCTET_LENGTH(business_refs),0)+COALESCE(OCTET_LENGTH(artifact_refs),0)+COALESCE(OCTET_LENGTH(partial_reasons),0)),0) FROM bkn_trace_receipts WHERE interaction_id=?
 UNION ALL SELECT COUNT(*),COALESCE(SUM(COALESCE(OCTET_LENGTH(capability_profile),0)+OCTET_LENGTH(input_payload)+COALESCE(OCTET_LENGTH(output_payload),0)+COALESCE(OCTET_LENGTH(error_payload),0)),0) FROM bkn_trace_operation_call_facts WHERE interaction_id=?`
	args := []any{id, id, id, id}
	if source.revisionInputs {
		query += ` UNION ALL SELECT COUNT(*),COALESCE(SUM(OCTET_LENGTH(included_receipt_ids)+OCTET_LENGTH(included_event_ids)+COALESCE(OCTET_LENGTH(partial_reasons),0)),0) FROM bkn_trace_assembly_revisions WHERE interaction_id=?
 UNION ALL SELECT COUNT(*),COALESCE(SUM(OCTET_LENGTH(input_package)),0) FROM bkn_trace_revision_inputs WHERE interaction_id=?`
		args = append(args, id, id)
	}
	query += `) archive_budget`
	var rows, bytes int64
	if err = tx.QueryRowContext(ctx, query, args...).Scan(&rows, &bytes); err != nil {
		return err
	}
	// Even empty-payload rows occupy structs, IDs and JSON metadata. The row cap
	// prevents a tiny-byte aggregate from materializing an unbounded collection.
	if rows > int64(archivesvc.MaxTraceArchiveCandidateBytes/256) || bytes > int64(archivesvc.MaxTraceArchiveCandidateBytes) {
		return archivesvc.ErrArchivePayloadTooLarge
	}
	adapter := &transaction{ctx: ctx, tx: tx}
	if err = read(adapter); err != nil {
		return err
	}
	if adapter.err != nil {
		return adapter.err
	}
	return tx.Commit()
}

func (source *TraceArchiveSource) Purge(ctx context.Context, kind observabilityvo.ArchiveKind, candidates []archivesvc.Candidate) error {
	if kind != observabilityvo.ArchiveKindTrace {
		return nil
	}
	var options *sql.TxOptions
	if source.revisionInputs {
		options = &sql.TxOptions{Isolation: sql.LevelReadCommitted}
	}
	tx, err := source.store.db.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, candidate := range candidates {
		var id string
		var version uint64
		var integritySize sql.NullInt64
		var integrity sql.NullString
		// A locking read sees the current version even after earlier candidates
		// opened a transaction snapshot. Bound metadata before it crosses SQL.
		query := fmt.Sprintf(`SELECT interaction_id,record_integrity_version,OCTET_LENGTH(record_integrity_json),CASE WHEN OCTET_LENGTH(record_integrity_json)<=%d THEN record_integrity_json ELSE NULL END FROM bkn_trace_interactions WHERE interaction_id=? AND execution_status<>? FOR UPDATE`, isessionstore.MaxStoredRecordIntegrityBytes)
		err := tx.QueryRowContext(ctx, query, candidate.ID, sessionvo.InteractionActive).Scan(&id, &version, &integritySize, &integrity)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if integritySize.Valid && (integritySize.Int64 <= 0 || integritySize.Int64 > isessionstore.MaxStoredRecordIntegrityBytes || !integrity.Valid) {
			return errArchiveSQLUnverifiable
		}
		var currentIntegrity []byte
		if integrity.Valid {
			currentIntegrity = []byte(integrity.String)
		}
		if err := verifyFrozenSQLSource(candidate, version, currentIntegrity); err != nil {
			return err
		}
		if source.revisionInputs {
			if err := verifyRevisionArchiveForPurge(ctx, tx, id, candidate.Payload); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM bkn_trace_revision_inputs WHERE interaction_id=?`, id); err != nil {
				return err
			}
		}
		for _, statement := range []string{
			`DELETE FROM bkn_trace_operation_call_facts WHERE interaction_id=?`,
			`DELETE FROM bkn_trace_receipts WHERE interaction_id=?`,
			`DELETE FROM bkn_trace_operations WHERE interaction_id=?`,
			`DELETE FROM bkn_trace_assembly_revisions WHERE interaction_id=?`,
			// Claim and Finish lock this same interaction first, so no retry
			// can be inserted after this history deletion and before commit.
			`DELETE FROM bkn_trace_record_integrity_retries WHERE interaction_id=?`,
			`DELETE FROM bkn_trace_interactions WHERE interaction_id=?`,
		} {
			if _, err := tx.ExecContext(ctx, statement, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
