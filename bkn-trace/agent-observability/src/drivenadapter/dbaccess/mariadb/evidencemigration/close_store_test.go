// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

type captureString struct{ value string }

func (capture *captureString) Match(value driver.Value) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	if capture.value == "" {
		capture.value = text
		return true
	}
	return capture.value == text
}

func TestCloseManifestPersistsFrozenClosureDigestAndAudit(t *testing.T) {
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
	mock.ExpectQuery("SELECT state,contract_sha").WithArgs("mig-openbkn-020-golden-001").WillReturnRows(sqlmock.NewRows([]string{"state", "contract_sha", "entries_digest", "entry_count", "manifest_id", "activated_at", "source_snapshot_at", "closure_digest"}).AddRow("active", "0016ad359b11d162e04bb11a78784c33fad0ec8d", "0d7573d407a7b8fec1868ac5d6a8085761c44c292e1589d80d866a9558371995", "1", "mig-openbkn-020-golden-001", "2026-09-22T08:30:00.000000Z", "2026-09-22T08:00:00.000000Z", nil))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM bkn_trace_evidence_migration_entries").WithArgs("mig-openbkn-020-golden-001").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM bkn_trace_evidence_migration_result_conflicts").WithArgs("mig-openbkn-020-golden-001").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	resultRows := sqlmock.NewRows([]string{"adjudication", "entry_id", "first_observation", "first_observed_at", "kafka_offset", "kafka_partition", "kafka_topic", "last_observation", "last_observed_at", "ledger_ingest_sequence", "manifest_id", "reason_code"}).AddRow("ledger_committed", "entry-0001", "accepted", "2026-09-22T09:00:00.000000Z", "41", "2", "openbkn.evidence.v1", "deduplicated", "2026-09-22T09:00:05.000000Z", "9001", "mig-openbkn-020-golden-001", nil)
	mock.ExpectQuery("SELECT r.adjudication").WithArgs("mig-openbkn-020-golden-001").WillReturnRows(resultRows)
	digest := &captureString{}
	countsJSON := `{"conflict":"0","coverage_gap":"0","ledger_committed":"1","rejected":"0","verified_delivered":"0"}`
	mock.ExpectExec("UPDATE bkn_trace_evidence_migration_manifests SET state='closed'").WithArgs(sqlmock.AnyArg(), "migration-admin", digest, countsJSON, "mig-openbkn-020-golden-001").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_manifest_audit").WithArgs("mig-openbkn-020-golden-001", "migration-admin", "0d7573d407a7b8fec1868ac5d6a8085761c44c292e1589d80d866a9558371995", digest).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	got, err := store.CloseManifest(context.Background(), "mig-openbkn-020-golden-001", "migration-admin")
	if err != nil {
		t.Fatal(err)
	}
	if got == "" || got != digest.value {
		t.Fatalf("closure digest = %q, audit/update digest = %q", got, digest.value)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseManifestRefusesAnyResultConflict(t *testing.T) {
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
	mock.ExpectQuery("SELECT state,contract_sha").WithArgs("m-1").WillReturnRows(sqlmock.NewRows([]string{"state", "contract_sha", "entries_digest", "entry_count", "manifest_id", "activated_at", "source_snapshot_at", "closure_digest"}).AddRow("active", "0016ad359b11d162e04bb11a78784c33fad0ec8d", "0d7573d407a7b8fec1868ac5d6a8085761c44c292e1589d80d866a9558371995", "1", "m-1", "2026-09-22T08:30:00.000000Z", "2026-09-22T08:00:00.000000Z", nil))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM bkn_trace_evidence_migration_entries").WithArgs("m-1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM bkn_trace_evidence_migration_result_conflicts").WithArgs("m-1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectRollback()
	if _, err := store.CloseManifest(context.Background(), "m-1", "migration-admin"); err == nil {
		t.Fatal("expected result conflict to block manifest close")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
