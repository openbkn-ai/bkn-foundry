// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
)

func TestEvidenceAdmissionLoadsPersistedHistoricalFactsOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	closedAt := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions WHERE revision = \\?").
		WithArgs(uint64(41)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}).AddRow(true))
	mock.ExpectQuery("SELECT process_boot_id FROM bkn_trace_producer_instance_registrations").
		WithArgs("bkn-backend#boot-a", uint64(41)).WillReturnRows(sqlmock.NewRows([]string{"process_boot_id"}).AddRow("boot-a"))
	mock.ExpectQuery("SELECT last_accepted_sequence, closed_at FROM bkn_trace_producer_closure_watermarks").
		WithArgs("bkn-backend#boot-a", uint64(41)).WillReturnRows(sqlmock.NewRows([]string{"last_accepted_sequence", "closed_at"}).AddRow(uint64(7), closedAt))

	snapshot, err := store.Lookup(context.Background(), 41, "bkn-backend#boot-a")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 41 || !snapshot.Enabled || snapshot.InstanceID != "bkn-backend#boot-a" || snapshot.RegisteredRevision != 41 || snapshot.ProcessBootID != "boot-a" {
		t.Fatalf("historical policy/instance was not read: %+v", snapshot)
	}
	if snapshot.Closure.InstanceID != "bkn-backend#boot-a" || snapshot.Closure.LastAcceptedSequence != 7 || snapshot.Closure.ClosedAt != closedAt.Format(time.RFC3339Nano) {
		t.Fatalf("closure watermark was not read: %+v", snapshot.Closure)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceAdmissionUnknownHistoryIsAClosedDecisionAndStorageErrorsPropagate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions WHERE revision = \\?").
		WithArgs(uint64(999)).WillReturnError(sql.ErrNoRows)
	snapshot, err := store.Lookup(context.Background(), 999, "unknown#boot")
	if err != nil || snapshot.Revision != 0 {
		t.Fatalf("unknown persisted revision should produce an empty admission snapshot: %+v %v", snapshot, err)
	}
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions WHERE revision = \\?").
		WithArgs(uint64(41)).WillReturnError(errors.New("db offline"))
	if _, err := store.Lookup(context.Background(), 41, "instance#boot"); err == nil {
		t.Fatal("temporary authoritative-state read failure must propagate")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
