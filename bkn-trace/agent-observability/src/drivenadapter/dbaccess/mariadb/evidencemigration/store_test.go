// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import (
	"context"
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
	defer db.Close()
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

func TestRecordConsumerResultUsesIdempotentTerminalUpsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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

func TestRecordConsumerResultPersistsIncompatibleTerminalAsConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT adjudication").WithArgs("m-1", "entry-1").WillReturnRows(sqlmock.NewRows([]string{"adjudication", "reason_code"}).AddRow("ledger_committed", ""))
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
