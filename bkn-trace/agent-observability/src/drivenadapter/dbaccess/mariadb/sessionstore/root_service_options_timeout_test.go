// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestRootServiceOptionsServerBudget(t *testing.T) {
	for _, phase := range []string{"query", "rows", "other", "success"} {
		t.Run(phase, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			original := &mysql.MySQLError{Number: 1969, Message: "statement timeout"}
			if phase == "other" {
				original.Number = 1146
			}
			expectation := mock.ExpectQuery("^SET STATEMENT max_statement_time=3 FOR SELECT DISTINCT CAST").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "needle", 2)
			switch phase {
			case "rows":
				expectation.WillReturnRows(sqlmock.NewRows([]string{"root_service"}).AddRow("partial").RowError(0, original))
			case "success":
				expectation.WillReturnRows(sqlmock.NewRows([]string{"root_service"}).AddRow("one").AddRow("two"))
			default:
				expectation.WillReturnError(original)
			}
			got, more, err := New(db).ListTraceRootServiceOptions(context.Background(), isessionstore.SummaryPageQuery{Limit: 1}, " needle ")
			if phase == "success" {
				if err != nil || len(got) != 1 || got[0] != "one" || !more {
					t.Fatalf("unexpected result: %v %v %v", got, more, err)
				}
			} else {
				if !errors.Is(err, original) || got != nil || more {
					t.Fatalf("lost failure: %v", err)
				}
				if errors.Is(err, context.DeadlineExceeded) != (phase != "other") {
					t.Fatalf("wrong classification: %v", err)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
