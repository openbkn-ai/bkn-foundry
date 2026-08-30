// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRecordPersistsOneBoundedManagementFact(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	defer db.Close()
	store := NewStore(db, "MARIADB")
	eventTime := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_operation_audit")).
		WithArgs(
			"evt-1", eventTime, eventTime, "tenant-a", "kn-a",
			"user-a", "Alice", "user", "oauth", "", "req-a", "api", "POST",
			"create", "object_type", "material", "物料", "success", "", "",
			`{"changed_fields":["name"]}`, "1.0",
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.Record(context.Background(), Entry{
		EventID: "evt-1", EventTime: eventTime, RecordedAt: eventTime,
		TenantID: "tenant-a", KnowledgeNetworkID: "kn-a",
		ActorID: "user-a", ActorName: "Alice", ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-a", SourceChannel: "api", Method: "POST", Action: "create",
		TargetType: "object_type", TargetID: "material", TargetName: "物料", Outcome: "success",
		ChangeSummary: map[string]any{"changed_fields": []string{"name"}},
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordRejectsUnboundedOrUnregisteredFactsBeforeSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	defer db.Close()
	store := NewStore(db, "MARIADB")

	for _, entry := range []Entry{
		{EventID: "evt-a", TenantID: "tenant-a", ActorID: "user-a", ActorName: "Alice", RequestID: "req-a", Action: "preview", TargetType: "object_type", TargetID: "material", TargetName: "物料", Outcome: "success"},
		{EventID: "evt-b", TenantID: "tenant-a", ActorID: "user-a", ActorName: "Alice", RequestID: string(make([]byte, 129)), Action: "create", TargetType: "object_type", TargetID: "material", TargetName: "物料", Outcome: "success"},
		{EventID: "evt-c", TenantID: "tenant-a", ActorID: "user-a", ActorName: "Alice", RequestID: "req-c", Action: "create", TargetType: "object_type", TargetID: "material", TargetName: "物料", Outcome: "success", ChangeSummary: map[string]any{"password": "secret"}},
	} {
		if err := store.Record(context.Background(), entry); err == nil {
			t.Fatalf("Record() accepted invalid entry: %+v", entry)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListPushesTenantNetworkAndKeysetScopeIntoSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	defer db.Close()
	store := NewStore(db, "MARIADB")
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(7 * 24 * time.Hour)
	before := to.Add(-time.Hour)

	queryPattern := `SELECT .* FROM t_operation_audit WHERE tenant_id = \? AND knowledge_network_id IN \(\?,\?\) AND event_time >= \? AND event_time < \? AND \(event_time < \? OR \(event_time = \? AND event_id > \?\)\) ORDER BY event_time DESC, event_id ASC LIMIT \?`
	mock.ExpectQuery(queryPattern).
		WithArgs("tenant-a", "kn-a", "kn-b", from, to, before, before, "evt-cursor", 21).
		WillReturnRows(sqlmock.NewRows(auditColumns()).AddRow(auditRow("evt-1", before.Add(-time.Second))...))

	page, err := store.List(context.Background(), Filter{
		TenantID: "tenant-a", KnowledgeNetworkIDs: []string{"kn-b", "kn-a"},
		From: from, To: to, BeforeTime: before, BeforeEventID: "evt-cursor", Limit: 20,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].EventID != "evt-1" || page.HasMore {
		t.Fatalf("List() = %+v", page)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetRequiresTenantAndAuthorizedNetwork(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	defer db.Close()
	store := NewStore(db, "MARIADB")
	eventTime := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT .* FROM t_operation_audit WHERE event_id = \? AND tenant_id = \? AND knowledge_network_id IN \(\?\)`).
		WithArgs("evt-1", "tenant-a", "kn-a").
		WillReturnRows(sqlmock.NewRows(auditColumns()).AddRow(auditRow("evt-1", eventTime)...))
	entry, found, err := store.Get(context.Background(), "evt-1", Scope{TenantID: "tenant-a", KnowledgeNetworkIDs: []string{"kn-a"}})
	if err != nil || !found || entry.EventID != "evt-1" {
		t.Fatalf("Get() = %+v, %v, %v", entry, found, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func auditColumns() []string {
	return []string{
		"event_id", "event_time", "recorded_at", "tenant_id", "knowledge_network_id",
		"actor_id", "actor_name", "actor_type", "auth_method", "credential_id", "request_id", "source_channel",
		"method", "action", "target_type", "target_id", "target_name", "outcome", "failure_code", "failure_message",
		"change_summary", "schema_version",
	}
}

func auditRow(eventID string, eventTime time.Time) []driver.Value {
	return []driver.Value{
		eventID, eventTime, eventTime, "tenant-a", "kn-a", "user-a", "Alice", "user", "oauth", "",
		"req-a", "api", "POST", "create", "object_type", "material", "物料", "success", "", "", []byte(`{"changed_fields":["name"]}`), "1.0",
	}
}

func TestRecordFallsBackToLegacyBusinessDomainColumn(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	defer db.Close()
	store := NewStore(db, "MARIADB")
	eventTime := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	tenantOnlyArgs := []driver.Value{
		"evt-1", eventTime, eventTime, "tenant-a", "kn-a",
		"user-a", "Alice", "user", "oauth", "", "req-a", "api", "POST",
		"create", "object_type", "material", "物料", "success", "", "",
		`{"changed_fields":["name"]}`, "1.0",
	}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_operation_audit")).
		WithArgs(tenantOnlyArgs...).
		WillReturnError(errors.New("Error 1364 (HY000): Field 'business_domain_id' doesn't have a default value"))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_operation_audit")).
		WithArgs(append(tenantOnlyArgs, "")...).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.Record(context.Background(), Entry{
		EventID: "evt-1", EventTime: eventTime, RecordedAt: eventTime,
		TenantID: "tenant-a", KnowledgeNetworkID: "kn-a",
		ActorID: "user-a", ActorName: "Alice", ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-a", SourceChannel: "api", Method: "POST", Action: "create",
		TargetType: "object_type", TargetID: "material", TargetName: "物料", Outcome: "success",
		ChangeSummary: map[string]any{"changed_fields": []string{"name"}},
		SchemaVersion: "1.0",
	})
	if err != nil {
		t.Fatalf("record with legacy schema: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
