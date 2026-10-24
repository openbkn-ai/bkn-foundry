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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestTraceFailureMetadataReadsOnlySelectedFirstFailurePayloads(t *testing.T) {
	matcher := sqlmock.QueryMatcherFunc(func(_, actual string) error {
		for _, forbidden := range []string{"input_payload", "output_payload", "capability_profile", "SELECT *"} {
			if strings.Contains(actual, forbidden) {
				return fmt.Errorf("unrelated payload selected: %s", forbidden)
			}
		}
		for _, required := range []string{"PARTITION BY interaction_id,trace_id,conversation_id", "ORDER BY started_at,operation_id,attempt_no", "WHERE status=? AND (trace_id,conversation_id,interaction_id) IN ((?,?,?),(?,?,?))", "first_failure.failure_rank=1"} {
			if !strings.Contains(actual, required) {
				return fmt.Errorf("missing failure selection boundary: %s", required)
			}
		}
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		if err := db.Close(); err != nil {
			t.Errorf("close mock database: %v", err)
		}
	})
	mock.ExpectBegin()
	dbtx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	rows := sqlmock.NewRows([]string{"trace", "conversation", "interaction", "operation", "attempt", "started", "status", "error"}).
		AddRow("trace-a", "conv", "int", "op", 1, now, "failed", `{"mode":"inline","media_type":"application/json","byte_length":7,"inline":"first"}`).
		AddRow("trace-b", "conv", "int", "other", 2, now, "failed", "")
	mock.ExpectQuery("failure").WithArgs(sessionvo.AttemptFailed, "trace-a", "conv", "int", "trace-b", "conv", "int").WillReturnRows(rows).RowsWillBeClosed()
	tx := &transaction{tx: dbtx, ctx: context.Background()}
	facts := tx.ListFirstTraceFailureMetadata([]isessionstore.TraceMetadataScope{{}, {TraceID: "trace-a", ConversationID: "conv", InteractionID: "int"}, {TraceID: "trace-b", ConversationID: "conv", InteractionID: "int"}, {TraceID: "trace-a", ConversationID: "conv", InteractionID: "int"}})
	if tx.err != nil || len(facts) != 2 {
		t.Fatalf("facts=%+v err=%v", facts, tx.err)
	}
	if string(facts[0].Error.Inline) != `"first"` || facts[1].Error != nil || facts[0].Output != nil || facts[0].Input.Inline != nil || facts[0].CapabilityProfile != nil {
		t.Fatalf("metadata widened or error envelope lost: %+v", facts)
	}
	mock.ExpectRollback()
	if err := dbtx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceFailureMetadataDoesNotHideStorageOrDecodeFailure(t *testing.T) {
	storageErr := errors.New("failure read unavailable")
	for _, kind := range []string{"query", "scan", "iteration", "decode", "close"} {
		t.Run(kind, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				mock.ExpectClose()
				if err := db.Close(); err != nil {
					t.Errorf("close mock database: %v", err)
				}
			})
			mock.ExpectBegin()
			dbtx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			expect := mock.ExpectQuery("SELECT f.trace_id").WithArgs(sessionvo.AttemptFailed, "trace", "c", "i")
			rows := sqlmock.NewRows([]string{"trace", "conversation", "interaction", "operation", "attempt", "started", "status", "error"})
			switch kind {
			case "query":
				expect.WillReturnError(storageErr)
			case "scan":
				expect.WillReturnRows(rows.AddRow("trace", "c", "i", "o", 1, "invalid date", "failed", ""))
			case "iteration":
				expect.WillReturnRows(rows.AddRow("trace", "c", "i", "o", 1, time.Now(), "failed", "").RowError(0, storageErr))
			case "decode":
				expect.WillReturnRows(rows.AddRow("trace", "c", "i", "o", 1, time.Now(), "failed", "{"))
			case "close":
				expect.WillReturnRows(rows.CloseError(storageErr))
			}
			tx := &transaction{tx: dbtx, ctx: context.Background()}
			if got := tx.ListFirstTraceFailureMetadata([]isessionstore.TraceMetadataScope{{TraceID: "trace", ConversationID: "c", InteractionID: "i"}}); got != nil || tx.err == nil {
				t.Fatalf("successful partial result=%+v err=%v", got, tx.err)
			}
			mock.ExpectRollback()
			if err := dbtx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
	if got := (&transaction{}).ListFirstTraceFailureMetadata([]isessionstore.TraceMetadataScope{{}, {}}); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestTraceSourceMetadataKeepsScopeTuplesWithoutPayloads(t *testing.T) {
	matcher := sqlmock.QueryMatcherFunc(func(_, actual string) error {
		for _, forbidden := range []string{"input_payload", "output_payload", "error_payload", "capability_profile", "SELECT *"} {
			if strings.Contains(actual, forbidden) {
				return fmt.Errorf("unrelated payload selected: %s", forbidden)
			}
		}
		for _, required := range []string{"PARTITION BY interaction_id,trace_id,conversation_id", "ORDER BY started_at,operation_id,attempt_no", "WHERE source_module<>'' AND (trace_id,conversation_id,interaction_id) IN ((?,?,?),(?,?,?))", "first_source.source_rank=1"} {
			if !strings.Contains(actual, required) {
				return fmt.Errorf("missing source boundary %s", required)
			}
		}
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		if err := db.Close(); err != nil {
			t.Errorf("close mock database: %v", err)
		}
	})
	mock.ExpectBegin()
	dbtx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	rows := sqlmock.NewRows([]string{"trace", "conversation", "interaction", "operation", "attempt", "started", "source"}).
		AddRow("trace", "conv", "authorized", "op1", 1, now, "visible").
		AddRow("trace", "", "", "legacy-op", 2, now.Add(-time.Hour), "legacy")
	mock.ExpectQuery("source").WithArgs("trace", "conv", "authorized", "trace", "", "").WillReturnRows(rows).RowsWillBeClosed()
	tx := &transaction{tx: dbtx, ctx: context.Background()}
	got := tx.ListFirstTraceSourceMetadata([]isessionstore.TraceMetadataScope{{}, {TraceID: "trace", ConversationID: "conv", InteractionID: "authorized"}, {TraceID: "trace", ConversationID: "conv", InteractionID: "authorized"}, {TraceID: "trace"}})
	if tx.err != nil || len(got) != 2 || got[0].InteractionID != "authorized" || got[1].SourceModule != "legacy" || got[0].Error != nil || got[0].Input.Inline != nil || got[0].Status != "" {
		t.Fatalf("source metadata=%+v err=%v", got, tx.err)
	}
	mock.ExpectRollback()
	if err := dbtx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceSourceMetadataPropagatesRowFailures(t *testing.T) {
	storageErr := errors.New("source read failed")
	for _, kind := range []string{"query", "scan", "iteration", "close"} {
		t.Run(kind, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				mock.ExpectClose()
				if err := db.Close(); err != nil {
					t.Errorf("close mock database: %v", err)
				}
			})
			mock.ExpectBegin()
			dbtx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			expect := mock.ExpectQuery("SELECT f.trace_id").WithArgs("trace", "conv", "int")
			rows := sqlmock.NewRows([]string{"trace", "conversation", "interaction", "operation", "attempt", "started", "source"})
			switch kind {
			case "query":
				expect.WillReturnError(storageErr)
			case "scan":
				expect.WillReturnRows(rows.AddRow("trace", "conv", "int", "op", 1, "invalid date", "module"))
			case "iteration":
				expect.WillReturnRows(rows.AddRow("trace", "conv", "int", "op", 1, time.Now(), "module").RowError(0, storageErr))
			case "close":
				expect.WillReturnRows(rows.CloseError(storageErr))
			}
			tx := &transaction{tx: dbtx, ctx: context.Background()}
			if got := tx.ListFirstTraceSourceMetadata([]isessionstore.TraceMetadataScope{{TraceID: "trace", ConversationID: "conv", InteractionID: "int"}}); got != nil || tx.err == nil {
				t.Fatalf("successful partial source=%+v err=%v", got, tx.err)
			}
			mock.ExpectRollback()
			if err := dbtx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
	if got := (&transaction{}).ListFirstTraceSourceMetadata(nil); len(got) != 0 {
		t.Fatal(got)
	}
}
