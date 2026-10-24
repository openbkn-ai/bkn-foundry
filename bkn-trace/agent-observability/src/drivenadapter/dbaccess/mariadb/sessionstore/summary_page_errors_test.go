// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestServiceTraceIdentityPagePropagatesRowFailures(t *testing.T) {
	closeErr := errors.New("result close failed")
	rowErr := errors.New("result iteration failed")
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		rows        *sqlmock.Rows
		want        error
		scanFailure bool
	}{
		{name: "scan error takes precedence over close error", rows: sqlmock.NewRows([]string{"trace_id", "started_at", "total"}).AddRow("trace-1", "invalid-time", 3).CloseError(closeErr), scanFailure: true},
		{name: "iteration failure is retained", rows: sqlmock.NewRows([]string{"trace_id", "started_at", "total"}).AddRow("trace-1", now, 3).RowError(0, rowErr).CloseError(closeErr), want: rowErr},
		// database/sql closes exhausted rows automatically and exposes a driver close
		// failure through Rows.Err. It must not turn into a successful empty-page count.
		{name: "empty result close failure prevents count fallback", rows: sqlmock.NewRows([]string{"trace_id", "started_at", "total"}).CloseError(closeErr), want: closeErr},
		{name: "close failure does not return a successful partial page", rows: sqlmock.NewRows([]string{"trace_id", "started_at", "total"}).AddRow("trace-1", now, 3).CloseError(closeErr), want: closeErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			mock.ExpectQuery("SELECT trace_id,started_at,total FROM").WithArgs(2, 0).WillReturnRows(tt.rows).RowsWillBeClosed()
			_, err = New(db).listServiceTraceIdentityPage(context.Background(), isessionstore.SummaryPageQuery{Service: "context-loader", Limit: 1}, "1=1", nil)
			if tt.scanFailure {
				if err == nil || !strings.Contains(err.Error(), "scan trace summary identities") || errors.Is(err, closeErr) {
					t.Fatalf("scan failure lost: %v", err)
				}
			} else if !errors.Is(err, tt.want) {
				t.Fatalf("error=%v, want %v", err, tt.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
