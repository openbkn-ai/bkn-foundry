// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"strings"
	"testing"
	"time"
)

var dlqPreviewColumns = []string{"dlq_id", "source_kind", "source_id", "failure_class", "last_error_code", "failed_at", "replayed_at", "outbox_id", "status", "aggregate_type", "aggregate_id", "aggregate_version", "event_type", "attempts"}

func TestProjectionDLQPreviewIsBoundedMetadataOnly(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, query string) error {
		if strings.Contains(query, "payload") || !strings.Contains(query, "WHERE d.dlq_id=? LIMIT 2") {
			return errors.New("unbounded preview or payload read")
		}
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	rows := sqlmock.NewRows(dlqPreviewColumns).AddRow(7, "projection", "event", "retry_window_expired", "retry_window_expired", now, nil, 1, "dead", "receipt", "r", 3, "receipt.completed", 10)
	mock.ExpectQuery("preview").WithArgs(uint64(7)).WillReturnRows(rows).RowsWillBeClosed()
	entry, err := New(db).InspectProjectionDLQ(context.Background(), 7)
	if err != nil || entry.OutboxID != 1 || entry.ReplayedAt != nil || entry.Ambiguous {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestProjectionDLQPreviewDetectsAmbiguityAndReadFailures(t *testing.T) {
	for _, kind := range []string{"ambiguous", "missing", "query", "scan", "iterate", "close"} {
		t.Run(kind, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			expected := mock.ExpectQuery("SELECT d.dlq_id")
			failure := errors.New("read failed")
			now := time.Now().UTC()
			rows := sqlmock.NewRows(dlqPreviewColumns)
			if kind == "query" {
				expected.WillReturnError(failure)
			} else {
				if kind != "missing" {
					rows.AddRow(7, "projection", "event", "failed", "failed", now, nil, 1, "dead", "receipt", "r", 1, "receipt.completed", 10)
				}
				switch kind {
				case "ambiguous":
					rows.AddRow(7, "projection", "event", "failed", "failed", now, nil, 2, "dead", "receipt", "r", 2, "receipt.started", 10)
				case "scan":
					rows = sqlmock.NewRows(dlqPreviewColumns).AddRow("invalid", "projection", "event", "failed", "failed", now, nil, 1, "dead", "receipt", "r", 1, "receipt.completed", 10)
				case "iterate":
					rows.RowError(0, failure)
				case "close":
					rows.CloseError(failure)
				}
				expected.WillReturnRows(rows).RowsWillBeClosed()
			}
			entry, err := New(db).InspectProjectionDLQ(context.Background(), 7)
			if kind == "ambiguous" {
				if err != nil || !entry.Ambiguous {
					t.Fatal("ambiguity lost")
				}
			} else if err == nil {
				t.Fatal("read failure reported success")
			}
			if kind == "missing" && !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("not-found classification lost")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
