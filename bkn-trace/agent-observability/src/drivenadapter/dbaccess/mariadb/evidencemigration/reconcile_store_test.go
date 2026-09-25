// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencemigration"
)

func TestReconcileManifestPersistsOnlyVerifiedAndCoverageGapResults(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT state FROM bkn_trace_evidence_migration_manifests").WithArgs("m-1").WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("active"))
	mock.ExpectQuery("SELECT e.entry_id,e.classification").WithArgs("m-1").WillReturnRows(sqlmock.NewRows([]string{"entry_id", "classification", "classification_reason", "event_id", "payload_hash"}).
		AddRow("entry-delivered", "verify_delivered", "delivered", "evt-1", "hash-1").
		AddRow("entry-gap", "coverage_gap", "abandoned", nil, nil))
	mock.ExpectQuery("SELECT l.payload_hash").WithArgs("m-1", "evt-1", "evt-1", "evt-1").WillReturnRows(sqlmock.NewRows([]string{"payload_hash", "has_rejection", "has_conflict"}).AddRow("hash-1", false, false))
	expectReconcilerInsert(mock, "entry-delivered", ievidencemigration.AdjudicationVerifiedDelivered, "delivered")
	expectReconcilerInsert(mock, "entry-gap", ievidencemigration.AdjudicationCoverageGap, "abandoned")
	if err := store.ReconcileManifest(context.Background(), "m-1"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileManifestIsIdempotentAfterManifestClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT state FROM bkn_trace_evidence_migration_manifests").WithArgs("m-1").WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("closed"))
	if err := store.ReconcileManifest(context.Background(), "m-1"); err != nil {
		t.Fatalf("closed manifest reconciliation should be a no-op: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileManifestRejectsDeliveredEntryWithoutExactLedgerIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.ExpectClose(); _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT state FROM bkn_trace_evidence_migration_manifests").WithArgs("m-1").WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("active"))
	mock.ExpectQuery("SELECT e.entry_id,e.classification").WithArgs("m-1").WillReturnRows(sqlmock.NewRows([]string{"entry_id", "classification", "classification_reason", "event_id", "payload_hash"}).
		AddRow("entry-delivered", "verify_delivered", "delivered", "evt-1", "expected-hash"))
	mock.ExpectQuery("SELECT l.payload_hash").WithArgs("m-1", "evt-1", "evt-1", "evt-1").WillReturnRows(sqlmock.NewRows([]string{"payload_hash", "has_rejection", "has_conflict"}).AddRow("different-hash", false, false))
	if err := store.ReconcileManifest(context.Background(), "m-1"); err == nil {
		t.Fatal("expected exact Ledger identity mismatch to block reconciliation")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileManifestRejectsLedgerIdentityWithMigrationRejectionOrConflict(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rejection bool
		conflict  bool
	}{
		{name: "rejection", rejection: true},
		{name: "event conflict", conflict: true},
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
			mock.ExpectQuery("SELECT state FROM bkn_trace_evidence_migration_manifests").WithArgs("m-1").WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("active"))
			mock.ExpectQuery("SELECT e.entry_id,e.classification").WithArgs("m-1").WillReturnRows(sqlmock.NewRows([]string{"entry_id", "classification", "classification_reason", "event_id", "payload_hash"}).
				AddRow("entry-delivered", "verify_delivered", "delivered", "evt-1", "expected-hash"))
			mock.ExpectQuery("SELECT l.payload_hash").WithArgs("m-1", "evt-1", "evt-1", "evt-1").WillReturnRows(sqlmock.NewRows([]string{"payload_hash", "has_rejection", "has_conflict"}).AddRow("expected-hash", tc.rejection, tc.conflict))
			if err := store.ReconcileManifest(context.Background(), "m-1"); err == nil {
				t.Fatal("expected migration rejection/conflict to block delivered reconciliation")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func expectReconcilerInsert(mock sqlmock.Sqlmock, entryID string, adjudication ievidencemigration.Adjudication, reason string) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT m.state,e.classification").WithArgs("m-1", entryID).WillReturnRows(sqlmock.NewRows([]string{"state", "classification"}).AddRow("active", map[ievidencemigration.Adjudication]string{
		ievidencemigration.AdjudicationVerifiedDelivered: "verify_delivered",
		ievidencemigration.AdjudicationCoverageGap:       "coverage_gap",
	}[adjudication]))
	mock.ExpectQuery("SELECT adjudication").WithArgs("m-1", entryID).WillReturnRows(sqlmock.NewRows([]string{"adjudication", "reason_code", "ledger_ingest_sequence"}))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_migration_results").WithArgs("m-1", entryID, adjudication, string(adjudication), string(adjudication), reason).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
}
