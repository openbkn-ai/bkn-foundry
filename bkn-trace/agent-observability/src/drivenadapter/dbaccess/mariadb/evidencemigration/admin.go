package evidencemigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
)

type manifestAdminInput struct {
	ManifestID, ContractSHA, SourceSnapshotAt, EntriesDigest, Actor string
	Entries                                                         []frozenEntry
}

func (s *Store) CreateDraftAndActivate(ctx context.Context, in manifestAdminInput) error {
	if in.ManifestID == "" || in.Actor == "" || in.ContractSHA == "" {
		return errors.New("invalid migration admin input")
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
	err = tx.QueryRowContext(ctx, "SELECT contract_sha,state,source_snapshot_at,entry_count,entries_digest FROM bkn_trace_evidence_migration_manifests WHERE manifest_id=? FOR UPDATE", in.ManifestID).Scan(&existingContract, &existingState, &existingSnapshot, &existingCount, &existingDigest)
	if err == nil {
		if existingContract != in.ContractSHA || existingSnapshot != in.SourceSnapshotAt || existingCount != len(in.Entries) || existingDigest != in.EntriesDigest || existingState != "active" {
			return errors.New("migration manifest conflict")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO bkn_trace_evidence_migration_manifests (manifest_id,contract_sha,state,source_snapshot_at,entry_count,entries_digest,created_at,created_by) VALUES (?,?,'draft',?,?,?,UTC_TIMESTAMP(6),?)", in.ManifestID, in.ContractSHA, in.SourceSnapshotAt, len(in.Entries), in.EntriesDigest, in.Actor); err != nil {
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
	result, err := tx.ExecContext(ctx, "UPDATE bkn_trace_evidence_migration_manifests SET state='active',activated_at=UTC_TIMESTAMP(6),activated_by=? WHERE manifest_id=? AND state='draft'", in.Actor, in.ManifestID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return errors.New("migration manifest activation CAS failed")
	}
	return tx.Commit()
}
