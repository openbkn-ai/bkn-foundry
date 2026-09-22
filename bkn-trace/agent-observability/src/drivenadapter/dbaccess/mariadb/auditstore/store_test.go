// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root.

package auditstore

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func testEvent() Event {
	return Event{EventID: "evt-1", ContentHash: "sha256:abc", SourceID: "bkn-backend", Payload: []byte(`{"event_id":"evt-1"}`), OccurredAt: time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC), BrokerReceivedAt: time.Date(2026, 9, 22, 8, 0, 1, 0, time.UTC)}
}

func testDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock, *Store) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return time.Date(2026, 9, 22, 8, 1, 0, 0, time.UTC) }
	return db, mock, store
}

func TestAppendInsertsDedupAndMonthlyLedgerInOneTransaction(t *testing.T) {
	db, mock, store := testDB(t)
	defer func() { _ = db.Close() }()
	event := testEvent()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT content_hash, target_table")).WithArgs(event.EventID).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO bkn_audit.audit_event_dedup")).WithArgs(event.EventID, event.ContentHash, sqlmock.AnyArg(), "audit_event_202609").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO bkn_audit.audit_event_202609")).WithArgs(event.EventID, event.SourceID, event.ContentHash, event.Payload, event.OccurredAt, event.BrokerReceivedAt, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	decision, err := store.Append(context.Background(), event)
	if err != nil || decision != DecisionInserted {
		t.Fatalf("decision=%q err=%v", decision, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAppendSameHashIsIdempotentWithoutMonthlyInsert(t *testing.T) {
	db, mock, store := testDB(t)
	defer func() { _ = db.Close() }()
	event := testEvent()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT content_hash, target_table")).WithArgs(event.EventID).WillReturnRows(sqlmock.NewRows([]string{"content_hash", "target_table"}).AddRow(event.ContentHash, "audit_event_202609"))
	mock.ExpectCommit()
	decision, err := store.Append(context.Background(), event)
	if err != nil || decision != DecisionIdempotent {
		t.Fatalf("decision=%q err=%v", decision, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAppendDifferentHashPreservesFirstFact(t *testing.T) {
	db, mock, store := testDB(t)
	defer func() { _ = db.Close() }()
	event := testEvent()
	event.ContentHash = "sha256:different"
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT content_hash, target_table")).WithArgs(event.EventID).WillReturnRows(sqlmock.NewRows([]string{"content_hash", "target_table"}).AddRow("sha256:first", "audit_event_202609"))
	mock.ExpectCommit()
	decision, err := store.Append(context.Background(), event)
	if err != nil || decision != DecisionConflict {
		t.Fatalf("decision=%q err=%v", decision, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAppendFailsClosedWhenRoutedMonthlyTableIsMissing(t *testing.T) {
	db, mock, store := testDB(t)
	defer func() { _ = db.Close() }()
	event := testEvent()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT content_hash, target_table")).WithArgs(event.EventID).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO bkn_audit.audit_event_dedup")).WithArgs(event.EventID, event.ContentHash, sqlmock.AnyArg(), "audit_event_202609").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO bkn_audit.audit_event_202609")).WillReturnError(errors.New("table does not exist"))
	mock.ExpectRollback()
	if _, err := store.Append(context.Background(), event); err == nil {
		t.Fatal("missing monthly table was accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
