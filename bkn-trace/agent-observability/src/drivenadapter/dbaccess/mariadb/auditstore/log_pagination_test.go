// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package auditstore

import (
	"context"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"math"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestLogPageQueriesDeepOffsetWithoutReplayingPages(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	reader, _ := NewReader(db)
	from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	query := observabilityvo.LogQuery{TimeFrom: &from, TimeTo: &to, AuthorizedCategories: []string{"audit.admin"}, Limit: 20, Page: 181}
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*audit_event_202609").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5000))
	mock.ExpectQuery("SELECT event_id.*audit_event_202609.*ORDER BY occurred_at DESC, event_id DESC LIMIT \\? OFFSET \\?").WithArgs(from, to, "audit.admin", 21, int64(3600)).WillReturnRows(sqlmock.NewRows([]string{"event_id", "source_id", "payload", "occurred_at", "broker_received_at", "recorded_at"}))
	_, count, err := reader.QueryLogPage(context.Background(), query, evidencevo.AccessProfile{AccountActive: true, Roles: []string{"admin"}})
	if err != nil || count != 5000 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLogPageCountsMonthsBeforeChoosingOffset(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	reader, _ := NewReader(db)
	from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	q := observabilityvo.LogQuery{TimeFrom: &from, TimeTo: &to, AuthorizedCategories: []string{"audit.admin"}, Limit: 20, Page: 181}
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*audit_event_202610").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1000))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*audit_event_202609").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4000))
	mock.ExpectQuery("SELECT event_id.*audit_event_202609.*LIMIT \\? OFFSET \\?").WithArgs(from, to, "audit.admin", 21, int64(2600)).WillReturnRows(sqlmock.NewRows([]string{"event_id", "source_id", "payload", "occurred_at", "broker_received_at", "recorded_at"}))
	_, count, err := reader.QueryLogPage(context.Background(), q, evidencevo.AccessProfile{AccountActive: true, Roles: []string{"admin"}})
	if err != nil || count != 5000 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLogPageBeyondTotalDoesNotExecuteDeepOffset(t *testing.T) {
	for _, page := range []int{1000000, math.MaxInt} {
		t.Run("deep", func(t *testing.T) {
			db, mock, _ := sqlmock.New()
			defer func() { _ = db.Close() }()
			reader, _ := NewReader(db)
			from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
			to := from.Add(time.Hour)
			mock.ExpectQuery("SELECT COUNT\\(\\*\\)").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5000))
			result, count, err := reader.QueryLogPage(context.Background(), observabilityvo.LogQuery{TimeFrom: &from, TimeTo: &to, AuthorizedCategories: []string{"audit.admin"}, Limit: 20, Page: page}, evidencevo.AccessProfile{AccountActive: true, Roles: []string{"admin"}})
			if err != nil || len(result.Records) != 0 || count != 5000 {
				t.Fatalf("result=%+v count=%d err=%v", result, count, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func logPageRows(month int, count int, offsets ...int) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"event_id", "source_id", "payload", "occurred_at", "broker_received_at", "recorded_at"})
	day := 28
	if month == 10 {
		day = 20
	}
	start := 0
	if len(offsets) > 0 {
		start = offsets[0]
	}
	base := time.Date(2026, time.Month(month), day, 12, 0, 0, 0, time.UTC)
	for i := start; i < start+count; i++ {
		at := base.Add(-time.Duration(i) * time.Minute)
		id := fmt.Sprintf("evt-%02d-%02d", month, i)
		payload := []byte(fmt.Sprintf(`{"event_id":%q,"source_id":"execution-factory","category":"audit.admin","event_name":"execution_factory.operation.observed","occurred_at":%q,"actor":{"id":"user-1","display_name_snapshot":"User One","auth_method":"oauth"},"target":{"type":"toolbox","id":"box-1","name":"Toolbox One"},"scope":{"business_module":"execution_factory","environment":"test"},"facts":{"action":"execute"},"request_context":{"source_channel":"studio"},"outcome":"success","summary":"Executed toolbox"}`, id, at.Format(time.RFC3339Nano)))
		rows.AddRow(id, "execution-factory", payload, at, at.Add(time.Second), at.Add(2*time.Second))
	}
	return rows
}

func TestLogPageSpansMonthsAndCursorContinuesInOlderMonth(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	reader, _ := NewReader(db)
	from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	q := observabilityvo.LogQuery{TimeFrom: &from, TimeTo: &to, AuthorizedCategories: []string{"audit.admin"}, Limit: 20, Page: 2}
	profile := evidencevo.AccessProfile{AccountActive: true, Roles: []string{"admin"}}
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*audit_event_202610").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(35))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*audit_event_202609").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(15))
	mock.ExpectQuery("SELECT event_id.*audit_event_202610").WithArgs(from, to, "audit.admin", 21, int64(20)).WillReturnRows(logPageRows(10, 15, 20))
	mock.ExpectQuery("SELECT event_id.*audit_event_202609").WithArgs(from, to, "audit.admin", 6, int64(0)).WillReturnRows(logPageRows(9, 6))
	page, count, err := reader.QueryLogPage(context.Background(), q, profile)
	if err != nil || count != 50 || len(page.Records) != 20 || page.Next == nil || page.Next.EventID != "evt-09-04" {
		t.Fatalf("page=%+v count=%d err=%v", page, count, err)
	}
	q.Page = 181 // A supplied cursor takes precedence over the numbered offset.
	q.PageBefore = &observabilityvo.SourcePosition{EventTimestamp: page.Next.OccurredAt, LogID: page.Next.EventID}
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*audit_event_202610").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*audit_event_202609").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))
	mock.ExpectQuery("SELECT event_id.*audit_event_202609").WithArgs(from, to, "audit.admin", page.Next.OccurredAt, page.Next.OccurredAt, page.Next.EventID, 21, int64(0)).WillReturnRows(logPageRows(9, 10, 5))
	next, _, err := reader.QueryLogPage(context.Background(), q, profile)
	if err != nil || len(next.Records) != 10 || next.Next != nil || next.Records[0].EventID != "evt-09-05" {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLogPageFiltersActorSearchAndNetworkScopeBeforeCountAndOffset(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	reader, _ := NewReader(db)
	from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	q := observabilityvo.LogQuery{TimeFrom: &from, TimeTo: &to, AuthorizedCategories: []string{"audit.admin"}, Limit: 20, Page: 181, ActorQuery: "Alice", Query: "box%_"}
	profile := evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "builder-1", Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn-1"}}
	predicate := ".*" + regexp.QuoteMeta("LOCATE(LOWER(?), LOWER(JSON_UNQUOTE(JSON_EXTRACT(payload, '$.summary')))) > 0") + ".*" + regexp.QuoteMeta("$.actor.display_name_snapshot") + ".*" + regexp.QuoteMeta("JSON_CONTAINS(JSON_EXTRACT(payload, '$.scope.knowledge_network_ids'), JSON_QUOTE(?))")
	// Both COUNT and SELECT must carry actor matching and trusted network scope.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\)"+predicate).WithArgs(from, to, "audit.admin", "box%_", "Alice", "Alice", "builder-1", "builder-1", "kn-1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5000))
	mock.ExpectQuery("SELECT event_id"+predicate+".*LIMIT \\? OFFSET \\?").WithArgs(from, to, "audit.admin", "box%_", "Alice", "Alice", "builder-1", "builder-1", "kn-1", 21, int64(3600)).WillReturnRows(logPageRows(9, 0))
	_, count, err := reader.QueryLogPage(context.Background(), q, profile)
	if err != nil || count != 5000 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLogPageSkipsMissingMonthlyLedger(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	reader, _ := NewReader(db)
	from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*audit_event_202610").WillReturnError(&mysql.MySQLError{Number: 1146})
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*audit_event_202609").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	_, count, err := reader.QueryLogPage(context.Background(), observabilityvo.LogQuery{TimeFrom: &from, TimeTo: &to, AuthorizedCategories: []string{"audit.admin"}, Limit: 20, Page: 1000000}, evidencevo.AccessProfile{Roles: []string{"admin"}})
	if err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
