// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package auditstore

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
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
	brokerReceivedAt := occurredAt.Add(2 * time.Second)
	recordedAt := occurredAt.Add(3 * time.Second)
	payload := []byte(`{"event_id":"evt-1","source_id":"execution-factory","category":"audit.admin","event_name":"execution_factory.operation.observed","occurred_at":"2026-09-25T09:30:00Z","actor":{"id":"user-1","effective_subject":"user-1","display_name_snapshot":"User One","type":"user","auth_method":"oauth"},"target":{"type":"toolbox","id":"box-1","name":"Toolbox One"},"scope":{"business_module":"execution_factory","environment":"test","application_id":"app-1","knowledge_network_ids":["kn-1"]},"request_context":{"source_channel":"studio","transport":"http","method":"POST","client_ip":"192.0.2.1"},"correlation":{"request_id":"req-1","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"},"facts":{"action":"execute","operation_id":"op-1"},"outcome":"failure","failure_code":"SAFE_FAILURE","http_status":503,"summary":"toolbox execution failed"}`)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event_id, source_id, payload, occurred_at, broker_received_at, recorded_at FROM bkn_audit.audit_event_202609")).
		WithArgs(occurredAt.Add(-time.Hour), occurredAt.Add(time.Hour), "audit.admin", 201).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "source_id", "payload", "occurred_at", "broker_received_at", "recorded_at"}).AddRow("evt-1", "execution-factory", payload, occurredAt, brokerReceivedAt, recordedAt))

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
	if record.EventID != "evt-1" || record.SourceID != "execution-factory" || record.Category != "audit.admin" || record.EventName != "execution_factory.operation.observed" || record.BusinessModule != "execution_factory" || record.ActorID != "user-1" || record.TargetType != "toolbox" || record.TargetID != "box-1" || record.TargetNameSnapshot != "Toolbox One" || record.Action != "execute" || record.Outcome != "failure" || !record.OccurredAt.Equal(occurredAt) {
		t.Fatalf("record=%#v", record)
	}
	if !record.BrokerReceivedAt.Equal(brokerReceivedAt) || !record.RecordedAt.Equal(recordedAt) || record.ApplicationID != "app-1" || len(record.KnowledgeNetworkIDs) != 1 || record.HTTPStatus != 503 || record.Method != "POST" || record.RequestID != "req-1" || record.TraceID == "" || record.OperationID != "op-1" {
		t.Fatalf("extended record=%#v", record)
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
	watermark := from.Add(7 * time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event_id, source_id, payload, occurred_at, broker_received_at, recorded_at FROM bkn_audit.audit_event_202609")).
		WithArgs(from, from.Add(8*time.Hour), "audit.admin", watermark, cursor.OccurredAt, cursor.OccurredAt, cursor.EventID, 2).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "source_id", "payload", "occurred_at", "broker_received_at", "recorded_at"}))

	if _, err := reader.Query(context.Background(), auditsvc.Query{
		Categories: []string{"audit.admin"}, From: from, To: from.Add(8 * time.Hour), ObservedBefore: watermark, Cursor: &cursor, Limit: 1,
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
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event_id, source_id, payload, occurred_at, broker_received_at, recorded_at FROM bkn_audit.audit_event_202609")).
		WithArgs(from, from.Add(time.Hour), "audit.admin", 2).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "source_id", "payload", "occurred_at", "broker_received_at", "recorded_at"}).AddRow("evt-1", "execution-factory", []byte(`{"event_id":"other"}`), from, from, from))
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

func TestAuditSelectPushesPublicFiltersBeforeLimit(t *testing.T) {
	from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	statement, args := auditSelect("audit_event_202609", auditsvc.Query{
		Categories: []string{"audit.admin"}, From: from, To: from.Add(time.Hour),
		SourceID: "execution-factory", BusinessModule: "execution_factory", ActorID: "user-1",
		TargetType: "toolbox", TargetID: "box-1", Action: "execute", Outcomes: []string{"success", "failure"}, Limit: 50,
	})
	for _, fragment := range []string{
		"source_id = ?", "$.scope.business_module", "$.actor.id", "$.target.type", "$.target.id", "$.facts.action", "$.outcome",
	} {
		if !strings.Contains(statement, fragment) {
			t.Fatalf("missing %q in %s", fragment, statement)
		}
	}
	for _, forbidden := range []string{"$.scope.application_id", "$.correlation.request_id", "$.correlation.trace_id", "$.facts.operation_id", "LIKE ?"} {
		if strings.Contains(statement, forbidden) {
			t.Fatalf("unfrozen audit filter %q in %s", forbidden, statement)
		}
	}
	if got := args[len(args)-1]; got != 51 {
		t.Fatalf("limit arg=%#v, all args=%#v", got, args)
	}
}

func TestReaderGetUsesValidatedDedupTargetTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	reader, err := NewReader(db)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 9, 25, 9, 30, 0, 0, time.UTC)
	payload := []byte(`{"event_id":"evt-1","source_id":"execution-factory","category":"audit.admin","event_name":"execution_factory.operation.observed","occurred_at":"2026-09-25T09:30:00Z","actor":{"id":"user-1"},"target":{"type":"toolbox","id":"box-1"},"scope":{"business_module":"execution_factory"},"facts":{"action":"execute"},"outcome":"success"}`)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT target_table FROM bkn_audit.audit_event_dedup WHERE event_id=?")).
		WithArgs("evt-1").WillReturnRows(sqlmock.NewRows([]string{"target_table"}).AddRow("audit_event_202609"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT source_id, payload, occurred_at, broker_received_at, recorded_at FROM bkn_audit.audit_event_202609 WHERE event_id=?")).
		WithArgs("evt-1").WillReturnRows(sqlmock.NewRows([]string{"source_id", "payload", "occurred_at", "broker_received_at", "recorded_at"}).AddRow("execution-factory", payload, when, when, when))

	record, found, err := reader.Get(context.Background(), "evt-1")
	if err != nil || !found || record.EventID != "evt-1" {
		t.Fatalf("record=%#v found=%t err=%v", record, found, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReaderGetRejectsInvalidDedupTargetTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	reader, err := NewReader(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT target_table FROM bkn_audit.audit_event_dedup WHERE event_id=?")).
		WithArgs("evt-1").WillReturnRows(sqlmock.NewRows([]string{"target_table"}).AddRow("audit_event_202609;DROP TABLE x"))

	if _, _, err := reader.Get(context.Background(), "evt-1"); !errors.Is(err, ErrInvalidMonth) {
		t.Fatalf("err=%v", err)
	}
}

func TestReaderGetTreatsMissingMonthlyRowAsNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	reader, err := NewReader(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT target_table FROM bkn_audit.audit_event_dedup WHERE event_id=?")).
		WithArgs("evt-1").WillReturnRows(sqlmock.NewRows([]string{"target_table"}).AddRow("audit_event_202609"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT source_id, payload, occurred_at, broker_received_at, recorded_at FROM bkn_audit.audit_event_202609 WHERE event_id=?")).
		WithArgs("evt-1").WillReturnRows(sqlmock.NewRows([]string{"source_id", "payload", "occurred_at", "broker_received_at", "recorded_at"}))

	_, found, err := reader.Get(context.Background(), "evt-1")
	if err != nil || found {
		t.Fatalf("found=%t err=%v", found, err)
	}
}

var _ = sql.ErrNoRows
