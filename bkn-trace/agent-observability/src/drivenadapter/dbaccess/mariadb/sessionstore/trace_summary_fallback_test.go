// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

var fallbackColumns = []string{"trace_id", "request_id", "conversation_id", "interaction_id", "application_principal_id", "effective_subject_id", "started_at", "completed_at", "has_failure"}

func TestTraceFallbackSQLIsScopedAndReadsOnlySelectedMetadata(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, query string) error {
		for _, required := range []string{"r.effective_subject_id=", "f.effective_subject_id=", "r.trace_id IN (", "GROUP BY visible.trace_id", "ORDER BY f.issued_at,f.receipt_id LIMIT 1"} {
			if !strings.Contains(query, required) {
				return errors.New("missing scoped/bounded clause: " + required)
			}
		}
		for _, forbidden := range []string{"payload", "business_refs", "artifact_refs", "SELECT *"} {
			if strings.Contains(query, forbidden) {
				return errors.New("unexpected body read: " + forbidden)
			}
		}
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	start := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("metadata").WithArgs("service", "owner", "trace-1", "service", "owner").WillReturnRows(sqlmock.NewRows(fallbackColumns).AddRow("trace-1", "req", "conv", "int", "app", "owner", start, start.Add(2*time.Second), true)).RowsWillBeClosed()
	rows, err := New(db).LoadTraceSummaryFallback(context.Background(), []string{"trace-1", "trace-1"}, evidencevo.QueryScope{AccountID: "owner", AccountType: "service"})
	if err != nil || len(rows) != 1 || rows[0].Status != "error" || rows[0].DurationMS != 2000 || rows[0].SpanCountStatus != "unavailable" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestTraceFallbackSQLPropagatesReadFailures(t *testing.T) {
	failure := errors.New("database read failure")
	for _, kind := range []string{"query", "scan", "iterate", "close"} {
		t.Run(kind, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			expectation := mock.ExpectQuery("SELECT g.trace_id")
			if kind == "query" {
				expectation.WillReturnError(failure)
			} else {
				start := time.Now().UTC()
				rows := sqlmock.NewRows(fallbackColumns).AddRow("trace", "req", "conv", "int", "app", "owner", start, start, false)
				switch kind {
				case "scan":
					rows = sqlmock.NewRows(fallbackColumns).AddRow("trace", "req", "conv", "int", "app", "owner", "invalid", start, false)
				case "iterate":
					rows.RowError(0, failure)
				case "close":
					rows.CloseError(failure)
				}
				expectation.WillReturnRows(rows).RowsWillBeClosed()
			}
			rows, err := New(db).LoadTraceSummaryFallback(context.Background(), []string{"trace"}, evidencevo.QueryScope{AccountID: "owner", AccountType: "service"})
			if err == nil || rows != nil {
				t.Fatalf("read failure became successful data: rows=%+v err=%v", rows, err)
			}
			if kind != "scan" && !errors.Is(err, failure) {
				t.Fatalf("lost cause: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestTraceFallbackRejectsOversizedOrInvalidInputBeforeSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	oversized := make([]string, evidencevo.MaxSummaryQueryLimit+1)
	for i := range oversized {
		oversized[i] = fmt.Sprintf("trace-%d", i)
	}
	if _, err := New(db).LoadTraceSummaryFallback(context.Background(), oversized, evidencevo.QueryScope{}); err == nil || !strings.Contains(err.Error(), "page limit") {
		t.Fatalf("valid oversized IDs did not hit the page bound: %v", err)
	}
	for _, ids := range [][]string{{""}, {"非ASCII"}} {
		if _, err := New(db).LoadTraceSummaryFallback(context.Background(), ids, evidencevo.QueryScope{}); err == nil {
			t.Fatalf("invalid IDs accepted: %d", len(ids))
		}
	}
	if rows, err := New(db).LoadTraceSummaryFallback(context.Background(), nil, evidencevo.QueryScope{}); err != nil || len(rows) != 0 {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
