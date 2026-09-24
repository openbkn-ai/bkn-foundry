// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

func TestCapturePolicyFactsPersistPolicyRevisionIdempotently(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}).AddRow(true))
	mock.ExpectCommit()
	if err := store.PersistPolicyRevision(context.Background(), icapturepolicy.PolicyRevision{Revision: 7, AdmissionEnabled: true, RecordedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsRejectPolicyRevisionModeConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}).AddRow(false))
	mock.ExpectRollback()
	if err := store.PersistPolicyRevision(context.Background(), icapturepolicy.PolicyRevision{Revision: 7, AdmissionEnabled: true, RecordedAt: now}); err == nil {
		t.Fatal("PersistPolicyRevision() accepted a conflicting immutable revision")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsAppendsOnlyTheNextPolicyRevision(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(8)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}))
	mock.ExpectQuery("SELECT revision FROM bkn_trace_capture_policy_revisions").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(uint64(7)))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_policy_revisions").WithArgs(uint64(8), false, now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := store.PersistPolicyRevision(context.Background(), icapturepolicy.PolicyRevision{Revision: 8, AdmissionEnabled: false, RecordedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsRegisterProducerAndClosure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT process_boot_id, registration_state, revoked_at FROM bkn_trace_producer_instance_registrations").WithArgs("backend#boot-1", uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"process_boot_id", "registration_state", "revoked_at"}))
	mock.ExpectExec("INSERT INTO bkn_trace_producer_instance_registrations").WithArgs("backend#boot-1", uint64(7), "boot-1", "registered", now, nil).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := store.RegisterProducer(context.Background(), icapturepolicy.ProducerRegistration{InstanceID: "backend#boot-1", PolicyRevision: 7, ProcessBootID: "boot-1", RegistrationState: "registered", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}

	closed := now.Add(time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT last_accepted_sequence, closed_at, acknowledged_at FROM bkn_trace_producer_closure_watermarks").WithArgs("backend#boot-1", uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"last_accepted_sequence", "closed_at", "acknowledged_at"}))
	mock.ExpectExec("INSERT INTO bkn_trace_producer_closure_watermarks").WithArgs("backend#boot-1", uint64(7), uint64(12), closed, closed.Add(time.Second)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := store.PersistClosureWatermark(context.Background(), icapturepolicy.ClosureWatermark{InstanceID: "backend#boot-1", PolicyRevision: 7, LastAcceptedSequence: 12, ClosedAt: closed, AcknowledgedAt: closed.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
