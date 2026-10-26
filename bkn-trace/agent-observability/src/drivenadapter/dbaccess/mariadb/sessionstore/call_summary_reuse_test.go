// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

func TestCallSummaryReadIsReusedAndReturnsIndependentValues(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT operation_id, attempt_no, conversation_id,").WithArgs("int").WillReturnRows(sqlmock.NewRows([]string{"op", "attempt", "conv", "int", "receipt", "tool", "protocol", "module", "parent", "req", "trace", "span", "start", "finish", "status", "retryable"}).AddRow("op", 1, "conv", "int", "receipt", "tool", "internal", "registered", "", "req", "trace", "span", now, now, "completed", false))
	mock.ExpectRollback()
	sqltx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	tx := &transaction{ctx: context.Background(), tx: sqltx}
	first := tx.ListOperationCallSummaries("int")
	if len(first) != 1 {
		t.Fatalf("summaries=%v err=%v", first, tx.err)
	}
	first[0].SourceModule = "changed"
	*first[0].FinishedAt = now.Add(time.Hour)
	second := tx.ListOperationCallSummaries("int")
	if len(second) != 1 || second[0].SourceModule != "registered" || !second[0].FinishedAt.Equal(now) || second[0].Status != sessionvo.AttemptCompleted {
		t.Fatalf("shared mutable summary: %+v", second)
	}
	_ = sqltx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
