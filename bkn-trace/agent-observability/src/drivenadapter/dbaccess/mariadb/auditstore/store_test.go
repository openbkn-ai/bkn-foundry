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
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	monthlyaudit "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/audit"
)

func testEvent() Event {
	return Event{EventID: "evt-1", ContentHash: "sha256:abc", SourceID: "bkn-backend", Payload: []byte(`{"event_id":"evt-1"}`), OccurredAt: time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC), BrokerReceivedAt: time.Date(2026, 9, 22, 8, 0, 1, 0, time.UTC), Kafka: KafkaCoordinate{Topic: "openbkn.audit.v1", Partition: 4, Offset: 12}}
}

func expectMonthlySchemaV032(mock sqlmock.Sqlmock, table string) {
	expectMonthlySchemaV032Variant(mock, table, "varchar(128)", true)
}

func expectMonthlySchemaV032Variant(mock sqlmock.Sqlmock, table, eventIDType string, includeAllIndexes bool) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(DISTINCT column_name) FROM information_schema.columns")).
		WithArgs("bkn_audit", table, "event_id", "source_id", "content_hash", "payload", "occurred_at", "broker_received_at", "recorded_at", "topic", "partition_id", "offset_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ',')")).
		WithArgs("bkn_audit", table).
		WillReturnRows(sqlmock.NewRows([]string{"columns"}).AddRow("topic,partition_id,offset_id"))
	expectMonthlyColumnAndIndexDetails(mock, table, eventIDType, includeAllIndexes)
}

func expectMonthlyColumnAndIndexDetails(mock sqlmock.Sqlmock, table, eventIDType string, includeAllIndexes bool) {
	expectMonthlyColumns(mock, table, eventIDType)
	expectMonthlyIndexes(mock, table, includeAllIndexes)
}

func expectMonthlyColumns(mock sqlmock.Sqlmock, table, eventIDType string) {
	columns := sqlmock.NewRows([]string{"column_name", "data_type", "column_type", "is_nullable"}).
		AddRow("event_id", "varchar", eventIDType, "NO").AddRow("source_id", "varchar", "varchar(128)", "NO").
		AddRow("content_hash", "char", "char(71)", "NO").AddRow("payload", "json", "json", "NO").
		AddRow("occurred_at", "datetime", "datetime(6)", "NO").AddRow("broker_received_at", "datetime", "datetime(6)", "NO").
		AddRow("recorded_at", "datetime", "datetime(6)", "NO").AddRow("topic", "varchar", "varchar(249)", "NO").
		AddRow("partition_id", "int", "int(11)", "NO").AddRow("offset_id", "bigint", "bigint(20)", "NO")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT column_name, LOWER(data_type), LOWER(column_type), is_nullable")).
		WithArgs("bkn_audit", table).WillReturnRows(columns)
}

func expectMonthlyIndexes(mock sqlmock.Sqlmock, table string, includeAllIndexes bool) {
	indexes := sqlmock.NewRows([]string{"index_name", "columns", "non_unique"}).
		AddRow("PRIMARY", "event_id", 0).AddRow("uq_audit_kafka_coordinate", "topic,partition_id,offset_id", 0).
		AddRow("idx_audit_event_occurred", "occurred_at,event_id", 1)
	if includeAllIndexes {
		indexes.AddRow("idx_audit_event_source", "source_id,occurred_at,event_id", 1)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT index_name, GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ','), non_unique")).
		WithArgs("bkn_audit", table).WillReturnRows(indexes)
}

func expectMonthlyTableCount(mock sqlmock.Sqlmock, table string, count int) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM information_schema.tables")).
		WithArgs("bkn_audit", table).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
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
	expectMonthlySchemaV032(mock, "audit_event_202609")
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT content_hash, target_table")).WithArgs(event.EventID).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO bkn_audit.audit_event_dedup")).WithArgs(event.EventID, event.ContentHash, sqlmock.AnyArg(), "audit_event_202609").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO bkn_audit.audit_event_202609")).WithArgs(event.EventID, event.SourceID, event.ContentHash, event.Payload, event.OccurredAt, event.BrokerReceivedAt, sqlmock.AnyArg(), event.Kafka.Topic, event.Kafka.Partition, event.Kafka.Offset).WillReturnResult(sqlmock.NewResult(1, 1))
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
	expectMonthlySchemaV032(mock, "audit_event_202609")
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
	expectMonthlySchemaV032(mock, "audit_event_202609")
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
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(DISTINCT column_name) FROM information_schema.columns")).
		WithArgs("bkn_audit", "audit_event_202609", "event_id", "source_id", "content_hash", "payload", "occurred_at", "broker_received_at", "recorded_at", "topic", "partition_id", "offset_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	if _, err := store.Append(context.Background(), event); err == nil {
		t.Fatal("missing monthly table was accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateMonthlyWindowUpgradesV031AndCreatesV032TablesIdempotently(t *testing.T) {
	db, mock, store := testDB(t)
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	oldTable, newTable, existingV032 := "audit_event_202609", "audit_event_202610", "audit_event_202611"

	for _, table := range []string{oldTable, newTable, existingV032} {
		count := 1
		if table == newTable {
			count = 0
		}
		expectMonthlyTableCount(mock, table, count)
		if count == 1 {
			mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(DISTINCT column_name) FROM information_schema.columns WHERE table_schema=? AND table_name=? AND column_name IN")).
				WithArgs("bkn_audit", table, "event_id", "source_id", "content_hash", "payload", "occurred_at", "broker_received_at", "recorded_at").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
			alter := `ALTER TABLE bkn_audit.` + table + `
			ADD COLUMN IF NOT EXISTS topic VARCHAR(249) NULL,
			ADD COLUMN IF NOT EXISTS partition_id INT NULL,
			ADD COLUMN IF NOT EXISTS offset_id BIGINT NULL`
			mock.ExpectExec(regexp.QuoteMeta(alter)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(regexp.QuoteMeta("CREATE UNIQUE INDEX IF NOT EXISTS uq_audit_kafka_coordinate ON bkn_audit." + table + " (topic, partition_id, offset_id)")).
				WillReturnResult(sqlmock.NewResult(0, 0))
		} else {
			create := strings.ReplaceAll(monthlyaudit.TemplateSQL(), "YYYYMM", "202610")
			mock.ExpectExec(regexp.QuoteMeta(create)).WillReturnResult(sqlmock.NewResult(0, 0))
		}
		expectMonthlySchemaV032(mock, table)
	}
	if err := store.MigrateMonthlyWindow(context.Background(), now); err != nil {
		t.Fatalf("migrate monthly window: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMonthlyWindowUsesCurrentUTCMonthAndNextTwo(t *testing.T) {
	db, mock, store := testDB(t)
	defer func() { _ = db.Close() }()
	now := time.Date(2027, 1, 1, 0, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	for _, table := range []string{"audit_event_202612", "audit_event_202701", "audit_event_202702"} {
		expectMonthlySchemaV032(mock, table)
	}
	if err := store.ValidateMonthlyWindow(context.Background(), now); err != nil {
		t.Fatalf("validate monthly window: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMonthlyWindowFailsClosedWhenAnyRequiredTableIsMissingSchema(t *testing.T) {
	db, mock, store := testDB(t)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(DISTINCT column_name) FROM information_schema.columns")).
		WithArgs("bkn_audit", "audit_event_202609", "event_id", "source_id", "content_hash", "payload", "occurred_at", "broker_received_at", "recorded_at", "topic", "partition_id", "offset_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
	if err := store.ValidateMonthlyWindow(context.Background(), time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)); !errors.Is(err, ErrMonthlySchemaUnavailable) {
		t.Fatalf("missing coordinate columns must fail closed, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAppendFailsClosedOnMismatchedBaseColumnType(t *testing.T) {
	db, mock, store := testDB(t)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(DISTINCT column_name) FROM information_schema.columns")).
		WithArgs("bkn_audit", "audit_event_202609", "event_id", "source_id", "content_hash", "payload", "occurred_at", "broker_received_at", "recorded_at", "topic", "partition_id", "offset_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ',')")).
		WithArgs("bkn_audit", "audit_event_202609").WillReturnRows(sqlmock.NewRows([]string{"columns"}).AddRow("topic,partition_id,offset_id"))
	expectMonthlyColumns(mock, "audit_event_202609", "varchar(255)")
	if _, err := store.Append(context.Background(), testEvent()); !errors.Is(err, ErrMonthlySchemaUnavailable) {
		t.Fatalf("Append with mismatched base column type error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAppendFailsClosedWhenV032SecondaryIndexIsMissing(t *testing.T) {
	db, mock, store := testDB(t)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(DISTINCT column_name) FROM information_schema.columns")).
		WithArgs("bkn_audit", "audit_event_202609", "event_id", "source_id", "content_hash", "payload", "occurred_at", "broker_received_at", "recorded_at", "topic", "partition_id", "offset_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ',')")).
		WithArgs("bkn_audit", "audit_event_202609").WillReturnRows(sqlmock.NewRows([]string{"columns"}).AddRow("topic,partition_id,offset_id"))
	expectMonthlyColumnAndIndexDetails(mock, "audit_event_202609", "varchar(128)", false)
	if _, err := store.Append(context.Background(), testEvent()); !errors.Is(err, ErrMonthlySchemaUnavailable) {
		t.Fatalf("Append without v032 secondary index error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAppendRejectsMissingKafkaCoordinateBeforeDatabaseAccess(t *testing.T) {
	db, mock, store := testDB(t)
	defer func() { _ = db.Close() }()
	event := testEvent()
	event.Kafka = KafkaCoordinate{}
	if _, err := store.Append(context.Background(), event); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("Append without Kafka coordinate error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("invalid event unexpectedly touched database: %v", err)
	}
}
