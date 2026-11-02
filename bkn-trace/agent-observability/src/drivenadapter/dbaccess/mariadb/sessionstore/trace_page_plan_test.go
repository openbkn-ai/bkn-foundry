// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestTracePageIndexHintIsLimitedToUnboundedGlobalPages(t *testing.T) {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	profile := func(active bool, role string) evidencevo.QueryScope {
		return evidencevo.QueryScope{AccessProfile: &evidencevo.AccessProfile{
			AccountActive: active, EffectiveSubjectID: "owner", Roles: []string{role},
		}}
	}
	global := profile(true, "super_admin")
	for _, tc := range []struct {
		name  string
		query isessionstore.SummaryPageQuery
		hint  bool
	}{
		{"super admin", isessionstore.SummaryPageQuery{Scope: global}, true},
		{"admin", isessionstore.SummaryPageQuery{Scope: profile(true, "admin")}, true},
		{"offset", isessionstore.SummaryPageQuery{Scope: global, Offset: 40}, true},
		{"cursor", isessionstore.SummaryPageQuery{Scope: global, AfterStartedAt: at.Format(time.RFC3339Nano), AfterID: "trace-previous"}, true},
		{"from", isessionstore.SummaryPageQuery{Scope: global, From: at}, false},
		{"to", isessionstore.SummaryPageQuery{Scope: global, To: at}, false},
		{"bounded", isessionstore.SummaryPageQuery{Scope: global, From: at, To: at.Add(time.Hour)}, false},
		{"owner", isessionstore.SummaryPageQuery{Scope: profile(true, "user")}, false},
		{"inactive admin", isessionstore.SummaryPageQuery{Scope: profile(false, "super_admin")}, false},
		{"auditor", isessionstore.SummaryPageQuery{Scope: profile(true, "audit_admin")}, false},
		{"legacy owner", isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "service"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(kind, query string) error {
				hasHint := strings.Contains(query, "IGNORE INDEX (idx_bkn_trace_receipt_trace_root)")
				if kind == "count" {
					if hasHint || !strings.Contains(query, "COUNT(DISTINCT r.trace_id)") {
						return fmt.Errorf("COUNT must retain its independent covering scan: %s", query)
					}
					return nil
				}
				if hasHint != tc.hint {
					return fmt.Errorf("page hint=%v, want %v: %s", hasHint, tc.hint, query)
				}
				for _, required := range []string{"r.request_id IS NOT NULL", "r.request_id<>''", "GROUP BY r.trace_id", "ORDER BY MIN(r.issued_at) DESC, r.trace_id ASC LIMIT ? OFFSET ?"} {
					if !strings.Contains(query, required) {
						return fmt.Errorf("page lost %q: %s", required, query)
					}
				}
				return nil
			})))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			store := New(db)
			// Repeated reads must still query current totals and page identities.
			for _, total := range []int{21, 22} {
				mock.ExpectQuery("count").WillReturnRows(sqlmock.NewRows([]string{"total"}).AddRow(total))
				mock.ExpectQuery("page").WillReturnRows(sqlmock.NewRows([]string{"trace_id", "started_at"}).AddRow("trace-current", at))
				page, err := store.ListTraceSummaryIdentities(context.Background(), tc.query)
				if err != nil || page.Total != total || len(page.Entries) != 1 || page.Entries[0].ID != "trace-current" {
					t.Fatalf("page=%+v, error=%v", page, err)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRootFilteredTracePagesKeepOptimizerIndexChoices(t *testing.T) {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, filter := range []string{"service", "tool", "both"} {
		t.Run(filter, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, query string) error {
				if strings.Contains(query, "IGNORE INDEX") {
					return fmt.Errorf("root-filtered page must retain optimizer choices: %s", query)
				}
				if !strings.Contains(query, "COUNT(*) OVER()") {
					return fmt.Errorf("root-filtered page lost its existing count/page statement: %s", query)
				}
				return nil
			})))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			q := isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{AccessProfile: &evidencevo.AccessProfile{
				AccountActive: true, Roles: []string{"super_admin"},
			}}}
			if filter != "tool" {
				q.Service = "context-loader"
			}
			if filter != "service" {
				q.Tool = "run_sql"
			}
			mock.ExpectQuery("window page").WillReturnRows(sqlmock.NewRows([]string{"trace_id", "started_at", "total"}).AddRow("trace", at, 1))
			page, err := New(db).ListTraceSummaryIdentities(context.Background(), q)
			if err != nil || page.Total != 1 || len(page.Entries) != 1 {
				t.Fatalf("page=%+v, error=%v", page, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
