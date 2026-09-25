// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package auditstore

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditsvc"
)

func TestReaderQueriesTheUTCMonthlyLedgerAndMapsAuditPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	reader, err := NewReader(db)
	if err != nil {
		t.Fatal(err)
	}
	occurredAt := time.Date(2026, 9, 25, 9, 30, 0, 0, time.UTC)
	payload := []byte(`{"event_id":"evt-1","source_id":"execution-factory","category":"audit.admin","event_name":"execution_factory.operation.observed","occurred_at":"2026-09-25T09:30:00Z","actor":{"id":"user-1"},"target":{"type":"toolbox","id":"box-1"},"scope":{"business_module":"execution_factory"},"facts":{"action":"execute"},"outcome":"success"}`)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event_id, source_id, payload, occurred_at FROM bkn_audit.audit_event_202609")).
		WithArgs(occurredAt.Add(-time.Hour), occurredAt.Add(time.Hour), "audit.admin", 201).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "source_id", "payload", "occurred_at"}).AddRow("evt-1", "execution-factory", payload, occurredAt))

	page, err := reader.Query(context.Background(), auditsvc.Query{
		Categories: []string{"audit.admin"}, From: occurredAt.Add(-time.Hour), To: occurredAt.Add(time.Hour), Limit: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("records=%#v", page.Records)
	}
	record := page.Records[0]
	if record.EventID != "evt-1" || record.SourceID != "execution-factory" || record.Category != "audit.admin" || record.EventName != "execution_factory.operation.observed" || record.BusinessModule != "execution_factory" || record.ActorID != "user-1" || record.TargetType != "toolbox" || record.TargetID != "box-1" || record.Action != "execute" || record.Outcome != "success" || !record.OccurredAt.Equal(occurredAt) {
		t.Fatalf("record=%#v", record)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReaderAppliesKeysetCursorWithoutInterpolatingValues(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	reader, err := NewReader(db)
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	cursor := auditsvc.Position{OccurredAt: from.Add(4 * time.Hour), EventID: "evt-cursor"}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event_id, source_id, payload, occurred_at FROM bkn_audit.audit_event_202609")).
		WithArgs(from, from.Add(8*time.Hour), "audit.admin", cursor.OccurredAt, cursor.OccurredAt, cursor.EventID, 2).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "source_id", "payload", "occurred_at"}))

	if _, err := reader.Query(context.Background(), auditsvc.Query{
		Categories: []string{"audit.admin"}, From: from, To: from.Add(8 * time.Hour), Cursor: &cursor, Limit: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReaderRejectsMalformedLedgerPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	reader, err := NewReader(db)
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event_id, source_id, payload, occurred_at FROM bkn_audit.audit_event_202609")).
		WithArgs(from, from.Add(time.Hour), "audit.admin", 2).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "source_id", "payload", "occurred_at"}).AddRow("evt-1", "execution-factory", []byte(`{"event_id":"other"}`), from))
	if _, err := reader.Query(context.Background(), auditsvc.Query{Categories: []string{"audit.admin"}, From: from, To: from.Add(time.Hour), Limit: 1}); err == nil {
		t.Fatal("malformed payload was accepted")
	}
}

func TestAuditQueryTablesDoesNotIncludeExclusiveEndMonth(t *testing.T) {
	tables, err := auditQueryTables(
		time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil || len(tables) != 1 || tables[0] != "audit_event_202609" {
		t.Fatalf("tables=%v err=%v", tables, err)
	}
}

var _ = sql.ErrNoRows
