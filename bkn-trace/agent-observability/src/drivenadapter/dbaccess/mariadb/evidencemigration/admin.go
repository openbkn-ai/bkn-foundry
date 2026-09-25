package evidencemigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"time"
)

type manifestAdminInput struct {
	ManifestID, ContractSHA, SourceSnapshotAt, EntriesDigest, Actor string
	Entries                                                         []frozenEntry
}

func (s *Store) CreateDraftAndActivate(ctx context.Context, in manifestAdminInput) error {
	if in.ManifestID == "" || in.Actor == "" || in.ContractSHA == "" {
		return errors.New("invalid migration admin input")
	}
	snapshotAt, err := normalizeSnapshotTime(in.SourceSnapshotAt)
	if err != nil {
		return err
	}
	digest, err := entriesDigest(in.Entries)
	if err != nil || digest != in.EntriesDigest {
		return errors.New("migration entry digest mismatch")
	}
	for _, entry := range in.Entries {
		if entry.ManifestID != in.ManifestID {
			return errors.New("migration entry manifest ID mismatch")
		}
	}
	entries := append([]frozenEntry(nil), in.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].SourceService != entries[j].SourceService {
			return entries[i].SourceService < entries[j].SourceService
		}
		if entries[i].SourceTable != entries[j].SourceTable {
			return entries[i].SourceTable < entries[j].SourceTable
		}
		return entries[i].SourcePrimaryKey < entries[j].SourcePrimaryKey
	})
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existingContract, existingState, existingSnapshot, existingDigest string
	var existingCount int
	err = tx.QueryRowContext(ctx, "SELECT contract_sha,state,CAST(source_snapshot_at AS CHAR),entry_count,entries_digest FROM bkn_trace_evidence_migration_manifests WHERE manifest_id=? FOR UPDATE", in.ManifestID).Scan(&existingContract, &existingState, &existingSnapshot, &existingCount, &existingDigest)
	if err == nil {
		if existingContract != in.ContractSHA || existingSnapshot != snapshotAt || existingCount != len(in.Entries) || existingDigest != in.EntriesDigest || existingState != "active" {
			return errors.New("migration manifest conflict")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO bkn_trace_evidence_migration_manifests (manifest_id,contract_sha,state,source_snapshot_at,entry_count,entries_digest,created_at,created_by) VALUES (?,?,'draft',?,?,?,UTC_TIMESTAMP(6),?)", in.ManifestID, in.ContractSHA, snapshotAt, len(in.Entries), in.EntriesDigest, in.Actor); err != nil {
		return err
	}
	for _, e := range entries {
		sum := sha256.Sum256([]byte(in.ManifestID + "\x00" + e.SourceTable + "\x00" + e.SourcePrimaryKey))
		id := "mig:" + hex.EncodeToString(sum[:])
		if _, err = tx.ExecContext(ctx, "INSERT INTO bkn_trace_evidence_migration_entries (entry_id,manifest_id,source_service,source_table,source_primary_key,source_status,classification,classification_reason,event_id,payload_hash,producer_id,producer_stream_id,producer_epoch,producer_sequence) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)", id, in.ManifestID, e.SourceService, e.SourceTable, e.SourcePrimaryKey, e.SourceStatus, e.Classification, e.ClassificationReason, nullable(e.EventID), nullable(e.PayloadHash), nullable(e.ProducerID), nullable(e.ProducerStreamID), nullable(e.ProducerEpoch), nullable(e.ProducerSequence)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO bkn_trace_evidence_migration_manifest_audit (manifest_id,action,actor,before_digest,after_digest,occurred_at) VALUES (?,'draft',?,NULL,?,UTC_TIMESTAMP(6))", in.ManifestID, in.Actor, in.EntriesDigest); err != nil {
		return err
	}
	if err = verifyPersistedEntries(ctx, tx, in.ManifestID, len(in.Entries), in.EntriesDigest); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE bkn_trace_evidence_migration_manifests SET state='active',activated_at=UTC_TIMESTAMP(6),activated_by=? WHERE manifest_id=? AND state='draft'", in.Actor, in.ManifestID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return errors.New("migration manifest activation CAS failed")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO bkn_trace_evidence_migration_manifest_audit (manifest_id,action,actor,before_digest,after_digest,occurred_at) VALUES (?,'activate',?,?,?,UTC_TIMESTAMP(6))", in.ManifestID, in.Actor, in.EntriesDigest, in.EntriesDigest); err != nil {
		return err
	}
	return tx.Commit()
}

func normalizeSnapshotTime(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", errors.New("invalid migration source snapshot timestamp")
	}
	parsed = parsed.UTC()
	if parsed.Nanosecond()%int(time.Millisecond) != 0 {
		return "", errors.New("migration source snapshot timestamp exceeds DATETIME(3) precision")
	}
	return parsed.Format("2006-01-02 15:04:05.000"), nil
}

func verifyPersistedEntries(ctx context.Context, tx *sql.Tx, manifestID string, expectedCount int, expectedDigest string) error {
	var count int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_evidence_migration_entries WHERE manifest_id=?", manifestID).Scan(&count); err != nil {
		return err
	}
	if count != int64(expectedCount) {
		return errors.New("persisted migration entry count mismatch")
	}
	rows, err := tx.QueryContext(ctx, "SELECT classification,classification_reason,event_id,manifest_id,payload_hash,producer_epoch,producer_id,producer_sequence,producer_stream_id,source_primary_key,source_service,source_status,source_table FROM bkn_trace_evidence_migration_entries WHERE manifest_id=? ORDER BY source_service,source_table,source_primary_key", manifestID)
	if err != nil {
		return err
	}
	defer rows.Close()
	entries := make([]frozenEntry, 0, expectedCount)
	for rows.Next() {
		var entry frozenEntry
		var eventID, payloadHash, producerEpoch, producerID, producerSequence, producerStreamID sql.NullString
		if err := rows.Scan(&entry.Classification, &entry.ClassificationReason, &eventID, &entry.ManifestID, &payloadHash, &producerEpoch, &producerID, &producerSequence, &producerStreamID, &entry.SourcePrimaryKey, &entry.SourceService, &entry.SourceStatus, &entry.SourceTable); err != nil {
			return err
		}
		entry.EventID = nullStringValue(eventID)
		entry.PayloadHash = nullStringValue(payloadHash)
		entry.ProducerEpoch = nullStringValue(producerEpoch)
		entry.ProducerID = nullStringValue(producerID)
		entry.ProducerSequence = nullStringValue(producerSequence)
		entry.ProducerStreamID = nullStringValue(producerStreamID)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(entries) != expectedCount {
		return errors.New("persisted migration entry count mismatch")
	}
	digest, err := entriesDigest(entries)
	if err != nil {
		return err
	}
	if digest != expectedDigest {
		return errors.New("persisted migration entry digest mismatch")
	}
	return nil
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
