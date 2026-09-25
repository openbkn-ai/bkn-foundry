// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencemigration"
)

func (s *Store) CloseManifest(ctx context.Context, manifestID, actor string) (string, error) {
	if manifestID == "" || actor == "" || validatePrintableASCII(manifestID) != nil || validatePrintableASCII(actor) != nil {
		return "", errors.New("invalid Evidence migration close input")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return "", fmt.Errorf("begin Evidence migration close transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var state, contractSHA, entriesDigestValue, entryCountText, storedManifestID string
	var activatedAtText, sourceSnapshotAtText, existingClosureDigest sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT state,contract_sha,entries_digest,CAST(entry_count AS CHAR),manifest_id,
		DATE_FORMAT(activated_at,'%Y-%m-%dT%H:%i:%s.%fZ'),DATE_FORMAT(source_snapshot_at,'%Y-%m-%dT%H:%i:%s.%fZ'),closure_digest
		FROM bkn_trace_evidence_migration_manifests WHERE manifest_id=? FOR UPDATE`, manifestID).
		Scan(&state, &contractSHA, &entriesDigestValue, &entryCountText, &storedManifestID, &activatedAtText, &sourceSnapshotAtText, &existingClosureDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("evidence migration manifest not found")
	}
	if err != nil {
		return "", fmt.Errorf("read Evidence migration close header: %w", err)
	}
	if state == string(ievidencemigration.ManifestClosed) {
		if !existingClosureDigest.Valid || existingClosureDigest.String == "" {
			return "", errors.New("closed Evidence migration manifest has no closure digest")
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit Evidence migration close receipt: %w", err)
		}
		return existingClosureDigest.String, nil
	}
	if state != string(ievidencemigration.ManifestActive) {
		return "", errors.New("evidence migration manifest is not active")
	}
	entryCount, err := canonicalUint(entryCountText)
	if err != nil {
		return "", err
	}
	var persistedEntryCount, conflictCount int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_evidence_migration_entries WHERE manifest_id=?", manifestID).Scan(&persistedEntryCount); err != nil {
		return "", err
	}
	if persistedEntryCount < 0 || uint64(persistedEntryCount) != entryCount {
		return "", errors.New("evidence migration entry count changed after activation")
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_evidence_migration_result_conflicts WHERE manifest_id=?", manifestID).Scan(&conflictCount); err != nil {
		return "", err
	}
	if conflictCount != 0 {
		return "", errors.New("Evidence migration result conflicts block close")
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.adjudication,r.entry_id,r.first_observation,
		DATE_FORMAT(r.first_observed_at,'%Y-%m-%dT%H:%i:%s.%fZ'),CAST(r.offset_id AS CHAR),CAST(r.partition_id AS CHAR),r.topic,
		r.last_observation,DATE_FORMAT(r.last_observed_at,'%Y-%m-%dT%H:%i:%s.%fZ'),CAST(r.ledger_ingest_sequence AS CHAR),r.manifest_id,r.reason_code
		FROM bkn_trace_evidence_migration_results r
		WHERE r.manifest_id=? ORDER BY r.entry_id`, manifestID)
	if err != nil {
		return "", fmt.Errorf("read Evidence migration terminal results: %w", err)
	}
	defer func() { _ = rows.Close() }()
	results := make([]closureResult, 0, persistedEntryCount)
	counts := closureCounts{Conflict: "0", CoverageGap: "0", LedgerCommitted: "0", Rejected: "0", VerifiedDelivered: "0"}
	for rows.Next() {
		var result closureResult
		var firstObservedAt, lastObservedAt string
		var offset, partition, topic, ingestSequence, reason sql.NullString
		if err := rows.Scan(&result.Adjudication, &result.EntryID, &result.FirstObservation, &firstObservedAt, &offset, &partition, &topic, &result.LastObservation, &lastObservedAt, &ingestSequence, &result.ManifestID, &reason); err != nil {
			return "", fmt.Errorf("scan Evidence migration terminal result: %w", err)
		}
		result.FirstObservedAt, err = normalizeClosureDBTimestamp(firstObservedAt)
		if err != nil {
			return "", err
		}
		result.LastObservedAt, err = normalizeClosureDBTimestamp(lastObservedAt)
		if err != nil {
			return "", err
		}
		result.KafkaOffset = nullableSQLString(offset)
		result.KafkaPartition = nullableSQLString(partition)
		result.KafkaTopic = nullableSQLString(topic)
		result.LedgerIngestSequence = nullableSQLString(ingestSequence)
		result.ReasonCode = nullableSQLString(reason)
		results = append(results, result)
		switch result.Adjudication {
		case "ledger_committed":
			counts.LedgerCommitted = incrementDecimal(counts.LedgerCommitted)
		case "conflict":
			counts.Conflict = incrementDecimal(counts.Conflict)
		case "rejected":
			counts.Rejected = incrementDecimal(counts.Rejected)
		case "verified_delivered":
			counts.VerifiedDelivered = incrementDecimal(counts.VerifiedDelivered)
		case "coverage_gap":
			counts.CoverageGap = incrementDecimal(counts.CoverageGap)
		default:
			return "", errors.New("Evidence migration has a nonterminal result")
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate Evidence migration terminal results: %w", err)
	}
	if uint64(len(results)) != entryCount {
		return "", errors.New("Evidence migration close requires one terminal result per entry")
	}
	activatedAt, err := normalizeClosureDBTimestamp(activatedAtText.String)
	if err != nil || !activatedAtText.Valid {
		return "", errors.New("invalid Evidence migration activation timestamp")
	}
	sourceSnapshotAt, err := normalizeClosureDBTimestamp(sourceSnapshotAtText.String)
	if err != nil || !sourceSnapshotAtText.Valid {
		return "", errors.New("invalid Evidence migration source snapshot timestamp")
	}
	closedAt := time.Now().UTC().Truncate(time.Millisecond)
	closedAtISO := closedAt.Format("2006-01-02T15:04:05.000Z")
	closedAtSQL := closedAt.Format("2006-01-02 15:04:05.000")
	header := closureHeader{
		ActivatedAt: activatedAt, ClosedAt: closedAtISO, ContractSHA: contractSHA, EntriesDigest: entriesDigestValue,
		EntryCount: entryCountText, ManifestID: storedManifestID, SourceSnapshotAt: sourceSnapshotAt, TerminalCounts: counts,
	}
	digest, err := closureDigest(header, results)
	if err != nil {
		return "", err
	}
	countJSON := terminalCountsJSON(counts)
	update, err := tx.ExecContext(ctx, `UPDATE bkn_trace_evidence_migration_manifests SET state='closed',closed_at=?,closed_by=?,closure_digest=?,terminal_counts_json=?,row_version=row_version+1
		WHERE manifest_id=? AND state='active'`, closedAtSQL, actor, digest, countJSON, manifestID)
	if err != nil {
		return "", fmt.Errorf("close Evidence migration manifest: %w", err)
	}
	if changed, err := update.RowsAffected(); err != nil || changed != 1 {
		return "", errors.New("Evidence migration close CAS failed")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO bkn_trace_evidence_migration_manifest_audit (manifest_id,action,actor,before_digest,after_digest,occurred_at)
		VALUES (?,'close',?,?,?,UTC_TIMESTAMP(3))`, manifestID, actor, entriesDigestValue, digest); err != nil {
		return "", fmt.Errorf("audit Evidence migration close: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit Evidence migration close: %w", err)
	}
	return digest, nil
}

func nullableSQLString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func normalizeClosureDBTimestamp(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Nanosecond()%int(time.Millisecond) != 0 {
		return "", errors.New("Evidence migration database timestamp is not millisecond UTC")
	}
	return parsed.UTC().Format("2006-01-02T15:04:05.000Z"), nil
}

func incrementDecimal(value string) string {
	count, _ := canonicalUint(value)
	return fmt.Sprintf("%d", count+1)
}

func terminalCountsJSON(counts closureCounts) string {
	return fmt.Sprintf(`{"conflict":"%s","coverage_gap":"%s","ledger_committed":"%s","rejected":"%s","verified_delivered":"%s"}`,
		counts.Conflict, counts.CoverageGap, counts.LedgerCommitted, counts.Rejected, counts.VerifiedDelivered)
}
