// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencemigration"
)

func TestLookupAdmissionSeparatesMissingFromActiveAndClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		if closeErr := db.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	query := regexp.QuoteMeta("SELECT m.manifest_id, m.state, e.entry_id, e.event_id, e.payload_hash,\n\t\t\te.classification, e.classification_reason\n\t\tFROM bkn_trace_evidence_migration_manifests m\n\t\tJOIN bkn_trace_evidence_migration_entries e ON e.manifest_id=m.manifest_id\n\t\tWHERE m.manifest_id=? AND e.event_id=?")
	mock.ExpectQuery(query).WithArgs("m-1", "evt-1").WillReturnRows(sqlmock.NewRows([]string{"manifest_id", "state", "entry_id", "event_id", "payload_hash", "classification", "classification_reason"}).AddRow("m-1", "active", "entry-1", "evt-1", "hash-1", "publish", "pending"))
	got, found, err := store.LookupAdmission(context.Background(), "m-1", "evt-1")
	if err != nil || !found || got.State != ievidencemigration.ManifestActive || got.EntryID != "entry-1" {
		t.Fatalf("unexpected admission: %+v found=%v err=%v", got, found, err)
	}
	mock.ExpectQuery(query).WithArgs("m-1", "missing").WillReturnRows(sqlmock.NewRows([]string{"manifest_id", "state", "entry_id", "event_id", "payload_hash", "classification", "classification_reason"}))
	_, found, err = store.LookupAdmission(context.Background(), "m-1", "missing")
	if err != nil || found {
		t.Fatalf("missing entry must be terminal-not-found: found=%v err=%v", found, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateDraftAndActivatePersistsFrozenManifest(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	entry := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "1", SourceService: "bkn-backend", SourceStatus: "dlq", SourceTable: "bkn_backend_trace_outbox"}
	digest, err := entriesDigest([]frozenEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT contract_sha").WithArgs("mig-1").WillReturnRows(sqlmock.NewRows([]string{"contract_sha"}))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifests").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_entries").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifest_audit").WillReturnResult(sqlmock.NewResult(1, 1))
	expectPersistedManifestEntries(mock, entry)
	mock.ExpectExec("UPDATE bkn_trace_evidence_migration_manifests").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifest_audit").WithArgs("mig-1", "migration-admin", digest, digest).WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()
	err = store.CreateDraftAndActivate(context.Background(), manifestAdminInput{ManifestID: "mig-1", ContractSHA: "0016ad359b11d162e04bb11a78784c33fad0ec8d", SourceSnapshotAt: "2026-09-22T08:00:00.000Z", EntriesDigest: digest, Actor: "migration-admin", Entries: []frozenEntry{entry}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectPersistedManifestEntries(mock sqlmock.Sqlmock, entries ...frozenEntry) {
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM bkn_trace_evidence_migration_entries").WithArgs("mig-1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(len(entries))))
	rows := sqlmock.NewRows([]string{"classification", "classification_reason", "event_id", "manifest_id", "payload_hash", "producer_epoch", "producer_id", "producer_sequence", "producer_stream_id", "source_primary_key", "source_service", "source_status", "source_table"})
	for _, entry := range entries {
		rows.AddRow(entry.Classification, entry.ClassificationReason, nullable(entry.EventID), entry.ManifestID, nullable(entry.PayloadHash), nullable(entry.ProducerEpoch), nullable(entry.ProducerID), nullable(entry.ProducerSequence), nullable(entry.ProducerStreamID), entry.SourcePrimaryKey, entry.SourceService, entry.SourceStatus, entry.SourceTable)
	}
	mock.ExpectQuery("SELECT classification,classification_reason").WithArgs("mig-1").WillReturnRows(rows)
}

func TestCreateDraftAndActivateRejectsPersistedEntryDigestDrift(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	input := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "1", SourceService: "bkn-backend", SourceStatus: "dlq", SourceTable: "bkn_backend_trace_outbox"}
	digest, err := entriesDigest([]frozenEntry{input})
	if err != nil {
		t.Fatal(err)
	}
	stored := input
	stored.SourceStatus = "pending"
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT contract_sha").WithArgs("mig-1").WillReturnRows(sqlmock.NewRows([]string{"contract_sha", "state", "source_snapshot_at", "entry_count", "entries_digest"}))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifests").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_entries").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifest_audit").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM bkn_trace_evidence_migration_entries").WithArgs("mig-1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	rows := sqlmock.NewRows([]string{"classification", "classification_reason", "event_id", "manifest_id", "payload_hash", "producer_epoch", "producer_id", "producer_sequence", "producer_stream_id", "source_primary_key", "source_service", "source_status", "source_table"}).AddRow(stored.Classification, stored.ClassificationReason, nil, stored.ManifestID, nil, nil, nil, nil, nil, stored.SourcePrimaryKey, stored.SourceService, stored.SourceStatus, stored.SourceTable)
	mock.ExpectQuery("SELECT classification,classification_reason").WithArgs("mig-1").WillReturnRows(rows)
	mock.ExpectRollback()
	err = store.CreateDraftAndActivate(context.Background(), manifestAdminInput{ManifestID: "mig-1", ContractSHA: "0016ad359b11d162e04bb11a78784c33fad0ec8d", SourceSnapshotAt: "2026-09-22T08:00:00.000Z", EntriesDigest: digest, Actor: "migration-admin", Entries: []frozenEntry{input}})
	if err == nil {
		t.Fatal("expected persisted digest mismatch")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateDraftAndActivateRejectsEntryFromDifferentManifest(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	entry := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "other-manifest", SourcePrimaryKey: "1", SourceService: "bkn-backend", SourceStatus: "dlq", SourceTable: "bkn_backend_trace_outbox"}
	digest, err := entriesDigest([]frozenEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	err = store.CreateDraftAndActivate(context.Background(), manifestAdminInput{ManifestID: "mig-1", ContractSHA: "0016ad359b11d162e04bb11a78784c33fad0ec8d", SourceSnapshotAt: "2026-09-22T08:00:00.000Z", EntriesDigest: digest, Actor: "migration-admin", Entries: []frozenEntry{entry}})
	if err == nil {
		t.Fatal("expected entry/header manifest mismatch")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateDraftAndActivateUsesStableEntryIDsAndCursorOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	entry2 := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "2", SourceService: "bkn-backend", SourceStatus: "dlq", SourceTable: "bkn_backend_trace_outbox"}
	entry1 := entry2
	entry1.SourcePrimaryKey = "1"
	entries := []frozenEntry{entry2, entry1}
	digest, err := entriesDigest(entries)
	if err != nil {
		t.Fatal(err)
	}
	entryID := func(primaryKey string) string {
		sum := sha256.Sum256([]byte("mig-1\x00bkn_backend_trace_outbox\x00" + primaryKey))
		return "mig:" + hex.EncodeToString(sum[:])
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT contract_sha").WithArgs("mig-1").WillReturnRows(sqlmock.NewRows([]string{"contract_sha", "state", "source_snapshot_at", "entry_count", "entries_digest"}))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifests").WillReturnResult(sqlmock.NewResult(1, 1))
	for _, item := range []struct{ id, pk string }{{entryID("1"), "1"}, {entryID("2"), "2"}} {
		mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_entries").WithArgs(item.id, "mig-1", "bkn-backend", "bkn_backend_trace_outbox", item.pk, "dlq", "coverage_gap", "bad_payload", nil, nil, nil, nil, nil, nil).WillReturnResult(sqlmock.NewResult(1, 1))
	}
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifest_audit").WillReturnResult(sqlmock.NewResult(1, 1))
	expectPersistedManifestEntries(mock, entry1, entry2)
	mock.ExpectExec("UPDATE bkn_trace_evidence_migration_manifests").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifest_audit").WithArgs("mig-1", "migration-admin", digest, digest).WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()
	err = store.CreateDraftAndActivate(context.Background(), manifestAdminInput{ManifestID: "mig-1", ContractSHA: "0016ad359b11d162e04bb11a78784c33fad0ec8d", SourceSnapshotAt: "2026-09-22T08:00:00.000Z", EntriesDigest: digest, Actor: "migration-admin", Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateDraftAndActivateReturnsIdempotentlyOnlyForExactActiveHeader(t *testing.T) {
	const contractSHA = "0016ad359b11d162e04bb11a78784c33fad0ec8d"
	const inputSnapshot = "2026-09-22T16:00:00.000+08:00"
	const storedSnapshot = "2026-09-22 08:00:00.000"
	entry := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "1", SourceService: "bkn-backend", SourceStatus: "dlq", SourceTable: "bkn_backend_trace_outbox"}
	digest, err := entriesDigest([]frozenEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, contract, state, snapshot, digest string
		count                                   int64
		wantError                               bool
	}{
		{name: "exact active header is idempotent", contract: contractSHA, state: "active", snapshot: storedSnapshot, digest: digest, count: 1},
		{name: "contract mismatch conflicts", contract: "different", state: "active", snapshot: storedSnapshot, digest: digest, count: 1, wantError: true},
		{name: "non-active state conflicts", contract: contractSHA, state: "draft", snapshot: storedSnapshot, digest: digest, count: 1, wantError: true},
		{name: "snapshot mismatch conflicts", contract: contractSHA, state: "active", snapshot: "2026-09-22 08:01:00.000", digest: digest, count: 1, wantError: true},
		{name: "count mismatch conflicts", contract: contractSHA, state: "active", snapshot: storedSnapshot, digest: digest, count: 2, wantError: true},
		{name: "digest mismatch conflicts", contract: contractSHA, state: "active", snapshot: storedSnapshot, digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", count: 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
			store, err := New(db)
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT contract_sha").WithArgs("mig-1").WillReturnRows(sqlmock.NewRows([]string{"contract_sha", "state", "source_snapshot_at", "entry_count", "entries_digest"}).AddRow(tc.contract, tc.state, tc.snapshot, tc.count, tc.digest))
			if tc.wantError {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			err = store.CreateDraftAndActivate(context.Background(), manifestAdminInput{ManifestID: "mig-1", ContractSHA: contractSHA, SourceSnapshotAt: inputSnapshot, EntriesDigest: digest, Actor: "migration-admin", Entries: []frozenEntry{entry}})
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, wantError %v", err, tc.wantError)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCreateDraftAndActivateNormalizesStoredSnapshotTime(t *testing.T) {
	const contractSHA = "0016ad359b11d162e04bb11a78784c33fad0ec8d"
	const inputSnapshot = "2026-09-22T08:00:00.000Z"
	entry := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "1", SourceService: "bkn-backend", SourceStatus: "dlq", SourceTable: "bkn_backend_trace_outbox"}
	digest, err := entriesDigest([]frozenEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT contract_sha").WithArgs("mig-1").WillReturnRows(sqlmock.NewRows([]string{"contract_sha", "state", "source_snapshot_at", "entry_count", "entries_digest"}).AddRow(contractSHA, "active", "2026-09-22 08:00:00.000", int64(1), digest))
	mock.ExpectCommit()
	err = store.CreateDraftAndActivate(context.Background(), manifestAdminInput{ManifestID: "mig-1", ContractSHA: contractSHA, SourceSnapshotAt: inputSnapshot, EntriesDigest: digest, Actor: "migration-admin", Entries: []frozenEntry{entry}})
	if err != nil {
		t.Fatalf("equivalent UTC DATETIME snapshot should be idempotent: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeSnapshotTimeRejectsSubMillisecondPrecision(t *testing.T) {
	if _, err := normalizeSnapshotTime("2026-09-22T08:00:00.000001Z"); err == nil {
		t.Fatal("DATETIME(3) cannot preserve sub-millisecond source snapshot precision")
	}
}

func TestCreateDraftAndActivateRollsBackWhenActivationCASChangesNoRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	entry := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "1", SourceService: "bkn-backend", SourceStatus: "dlq", SourceTable: "bkn_backend_trace_outbox"}
	digest, err := entriesDigest([]frozenEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT contract_sha").WithArgs("mig-1").WillReturnRows(sqlmock.NewRows([]string{"contract_sha", "state", "source_snapshot_at", "entry_count", "entries_digest"}))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifests").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_entries").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifest_audit").WillReturnResult(sqlmock.NewResult(1, 1))
	expectPersistedManifestEntries(mock, entry)
	mock.ExpectExec("UPDATE bkn_trace_evidence_migration_manifests").WillReturnResult(sqlmock.NewResult(1, 0))
	mock.ExpectRollback()
	err = store.CreateDraftAndActivate(context.Background(), manifestAdminInput{ManifestID: "mig-1", ContractSHA: "0016ad359b11d162e04bb11a78784c33fad0ec8d", SourceSnapshotAt: "2026-09-22T08:00:00.000Z", EntriesDigest: digest, Actor: "migration-admin", Entries: []frozenEntry{entry}})
	if err == nil {
		t.Fatal("expected activation CAS failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateDraftAndActivateRollsBackWhenActivationAuditFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	entry := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "1", SourceService: "bkn-backend", SourceStatus: "dlq", SourceTable: "bkn_backend_trace_outbox"}
	digest, err := entriesDigest([]frozenEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT contract_sha").WithArgs("mig-1").WillReturnRows(sqlmock.NewRows([]string{"contract_sha", "state", "source_snapshot_at", "entry_count", "entries_digest"}))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifests").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_entries").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifest_audit").WillReturnResult(sqlmock.NewResult(1, 1))
	expectPersistedManifestEntries(mock, entry)
	mock.ExpectExec("UPDATE bkn_trace_evidence_migration_manifests").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifest_audit").WithArgs("mig-1", "migration-admin", digest, digest).WillReturnError(errors.New("audit unavailable"))
	mock.ExpectRollback()
	err = store.CreateDraftAndActivate(context.Background(), manifestAdminInput{ManifestID: "mig-1", ContractSHA: "0016ad359b11d162e04bb11a78784c33fad0ec8d", SourceSnapshotAt: "2026-09-22T08:00:00.000Z", EntriesDigest: digest, Actor: "migration-admin", Entries: []frozenEntry{entry}})
	if err == nil {
		t.Fatal("expected activation audit failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordConsumerResultUsesIdempotentTerminalUpsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		if closeErr := db.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT adjudication").WithArgs("m-1", "entry-1").WillReturnRows(sqlmock.NewRows([]string{"adjudication", "reason_code"}))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_results").WithArgs("m-1", "entry-1", ievidencemigration.AdjudicationLedgerCommitted, "deduplicated", "deduplicated", "", "openbkn.evidence.v1", 2, int64(9), uint64(42)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	err = store.RecordConsumerResult(context.Background(), ievidencemigration.ConsumerResult{ManifestID: "m-1", EntryID: "entry-1", Adjudication: ievidencemigration.AdjudicationLedgerCommitted, Observation: "deduplicated", Topic: "openbkn.evidence.v1", Partition: 2, Offset: 9, IngestSequence: 42})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordConsumerResultReturnsPersistenceFailureAndRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		if closeErr := db.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	persistErr := errors.New("migration result insert unavailable")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT adjudication").WithArgs("m-1", "entry-1").WillReturnRows(sqlmock.NewRows([]string{"adjudication", "reason_code"}))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_results").WillReturnError(persistErr)
	mock.ExpectRollback()

	err = store.RecordConsumerResult(context.Background(), ievidencemigration.ConsumerResult{
		ManifestID: "m-1", EntryID: "entry-1", Adjudication: ievidencemigration.AdjudicationLedgerCommitted,
		Observation: "accepted", Topic: "openbkn.evidence.v1", Partition: 0, Offset: 9, IngestSequence: 42,
	})
	if !errors.Is(err, persistErr) {
		t.Fatalf("RecordConsumerResult() error = %v, want wrapped persistence error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordConsumerResultPersistsIncompatibleTerminalAsConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		if closeErr := db.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT adjudication").WithArgs("m-1", "entry-1").WillReturnRows(sqlmock.NewRows([]string{"adjudication", "reason_code", "ledger_ingest_sequence"}).AddRow("ledger_committed", "", ""))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_result_conflicts").WithArgs("m-1", "entry-1", "ledger_committed", ievidencemigration.AdjudicationConflict, "", "event_payload_conflict").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	err = store.RecordConsumerResult(context.Background(), ievidencemigration.ConsumerResult{ManifestID: "m-1", EntryID: "entry-1", Adjudication: ievidencemigration.AdjudicationConflict, Observation: "conflict", ReasonCode: "event_payload_conflict", Topic: "openbkn.evidence.v1", Partition: 2, Offset: 9})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordConsumerResultPersistsLedgerIngestIdentityMismatchAsConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT adjudication").WithArgs("m-1", "entry-1").WillReturnRows(sqlmock.NewRows([]string{"adjudication", "reason_code", "ledger_ingest_sequence"}).AddRow("ledger_committed", "", "9001"))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_result_conflicts").WithArgs("m-1", "entry-1", "ledger_committed", ievidencemigration.AdjudicationConflict, "", "ledger_ingest_identity_mismatch").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	err = store.RecordConsumerResult(context.Background(), ievidencemigration.ConsumerResult{ManifestID: "m-1", EntryID: "entry-1", Adjudication: ievidencemigration.AdjudicationLedgerCommitted, Observation: "deduplicated", Topic: "openbkn.evidence.v1", Partition: 2, Offset: 10, IngestSequence: 9002})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordReconcilerResultPersistsOnlyMatchingActiveClassification(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT m.state,e.classification").WithArgs("m-1", "entry-1").WillReturnRows(sqlmock.NewRows([]string{"state", "classification"}).AddRow("active", "coverage_gap"))
	mock.ExpectQuery("SELECT adjudication").WithArgs("m-1", "entry-1").WillReturnRows(sqlmock.NewRows([]string{"adjudication", "reason_code", "ledger_ingest_sequence"}))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_results").WithArgs("m-1", "entry-1", ievidencemigration.AdjudicationCoverageGap, "coverage_gap", "coverage_gap", "bad_payload").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	err = store.RecordReconcilerResult(context.Background(), ievidencemigration.ReconcilerResult{ManifestID: "m-1", EntryID: "entry-1", Adjudication: ievidencemigration.AdjudicationCoverageGap, ReasonCode: "bad_payload"})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
