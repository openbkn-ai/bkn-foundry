// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestTraceArchiveUsesBoundedCandidateBatch(t *testing.T) {
	db, m, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	source, ok := any(NewTraceArchiveSource(New(db))).(archivesvc.BatchSource)
	if !ok {
		t.Fatal("Trace archive must implement bounded FreezeBatch, not load all eligible interactions")
	}
	cutoff := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM bkn_trace_interactions`).WithArgs(sessionvo.InteractionActive, cutoff).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(100000))
	m.ExpectQuery(`SELECT interaction_id FROM bkn_trace_interactions .* LIMIT \?`).WithArgs(sessionvo.InteractionActive, cutoff, 100).WillReturnRows(sqlmock.NewRows([]string{"interaction_id"})).RowsWillBeClosed()
	batch, err := source.FreezeBatch(context.Background(), observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{To: cutoff})
	if err != nil || !batch.Batched || batch.EligibleCount != 100000 || len(batch.Candidates) != 0 {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
	if err = m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceArchivePreflightRejectsBytesAndEmptyPayloadRowFloodBeforeReading(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rows, bytes int64
		fail        bool
	}{{"bytes", 1, archivesvc.MaxTraceArchiveCandidateBytes + 1, true}, {"empty row flood", archivesvc.MaxTraceArchiveCandidateBytes/256 + 1, 0, true}, {"within budget", 4, 1024, false}} {
		t.Run(tc.name, func(t *testing.T) {
			db, m, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			m.ExpectBegin()
			m.ExpectQuery(`SELECT SUM\(row_count\),SUM\(payload_bytes\)`).WithArgs("i", "i", "i", "i").WillReturnRows(sqlmock.NewRows([]string{"rows", "bytes"}).AddRow(tc.rows, tc.bytes))
			if tc.fail {
				m.ExpectRollback()
			} else {
				m.ExpectCommit()
			}
			called := false
			err = NewTraceArchiveSource(New(db)).readBoundedPackage(context.Background(), "i", func(isessionstore.Transaction) error { called = true; return nil })
			if errors.Is(err, archivesvc.ErrArchivePayloadTooLarge) != tc.fail || called == tc.fail || (!tc.fail && err != nil) {
				t.Fatalf("err=%v loaded payload=%v", err, called)
			}
			if err = m.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func archiveMockColumns(n int) []string {
	result := make([]string, n)
	for i := range result {
		result[i] = "column"
	}
	return result
}
func expectBoundedArchivePackage(m sqlmock.Sqlmock, id string, payloadSize int, now time.Time) {
	m.ExpectBegin()
	m.ExpectQuery(`SELECT SUM\(row_count\),SUM\(payload_bytes\)`).WithArgs(id, id, id, id).WillReturnRows(sqlmock.NewRows([]string{"rows", "bytes"}).AddRow(2, payloadSize))
	m.ExpectQuery(`SELECT interaction_id, conversation_id, ordinal_no`).WithArgs(id).WillReturnRows(sqlmock.NewRows(archiveMockColumns(19)).AddRow(id, "conv", 1, "completed", "complete", "start", "terminal", "", "", "token", 1, 1, now, 1, now, now, now, 0, nil))
	m.ExpectQuery(`SELECT conversation_id, application_principal_id`).WithArgs("conv").WillReturnRows(sqlmock.NewRows(archiveMockColumns(16)).AddRow("conv", "app", "agent", "actor", "method", "user", "owner", "", "external", 1, "closed", false, 1, now, now, now))
	m.ExpectQuery(`SELECT operation_id, conversation_id, interaction_id`).WithArgs(id).WillReturnRows(sqlmock.NewRows(archiveMockColumns(14)))
	m.ExpectQuery(`SELECT receipt_id, schema_version`).WithArgs(id).WillReturnRows(sqlmock.NewRows(archiveMockColumns(25)))
	input, _ := json.Marshal(sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`"` + strings.Repeat("a", payloadSize) + `"`)})
	values := []driver.Value{"op-" + id, 1, "conv", id, "receipt", "tool", "http", "source", "", "", string(input), "", "", "request", "trace", "span", now, now, "completed", false}
	m.ExpectQuery(`SELECT operation_id, attempt_no, conversation_id`).WithArgs(id).WillReturnRows(sqlmock.NewRows(archiveMockColumns(len(values))).AddRow(values...))
	m.ExpectCommit()
}
func TestTraceArchiveBatchStopsAtJSONLByteBudgetAndClosesIDRows(t *testing.T) {
	db, m, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	m.ExpectQuery(`SELECT COUNT\(\*\)`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1000))
	m.ExpectQuery(`SELECT interaction_id .*LIMIT \?`).WithArgs(sessionvo.InteractionActive, now, 100).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a").AddRow("b").AddRow("c")).RowsWillBeClosed()
	for _, id := range []string{"a", "b", "c"} {
		expectBoundedArchivePackage(m, id, 4<<20, now)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	batch, err := NewTraceArchiveSource(New(db)).FreezeBatch(ctx, observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{To: now})
	if err != nil || len(batch.Candidates) != 2 || batch.EligibleCount != 1000 || !batch.Batched {
		t.Fatalf("entries=%d eligible=%d err=%v", len(batch.Candidates), batch.EligibleCount, err)
	}
	size := 0
	for _, candidate := range batch.Candidates {
		size += len(candidate.Payload) + 1
	}
	if size > archivesvc.MaxTraceArchiveBatchBytes {
		t.Fatalf("unbounded batch=%d", size)
	}
	if err = m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestTraceArchiveLegacyFreezeRefusesTruncatedBatch(t *testing.T) {
	db, m, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	m.ExpectQuery(`SELECT COUNT\(\*\)`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(101))
	m.ExpectQuery(`SELECT interaction_id .*LIMIT \?`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	got, err := NewTraceArchiveSource(New(db)).Freeze(context.Background(), observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{})
	if !errors.Is(err, archivesvc.ErrIncompleteCandidates) || len(got) != 0 {
		t.Fatalf("partial legacy freeze=%d err=%v", len(got), err)
	}
	if err = m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceArchiveOversizedPackageFailsBatchBeforeAnyPayloadRead(t *testing.T) {
	db, m, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	m.ExpectQuery(`SELECT COUNT\(\*\)`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	m.ExpectQuery(`SELECT interaction_id .*LIMIT \?`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("large").AddRow("not-loaded")).RowsWillBeClosed()
	m.ExpectBegin()
	m.ExpectQuery(`SELECT SUM\(row_count\),SUM\(payload_bytes\)`).WithArgs("large", "large", "large", "large").WillReturnRows(sqlmock.NewRows([]string{"rows", "bytes"}).AddRow(2, archivesvc.MaxTraceArchiveCandidateBytes+1))
	m.ExpectRollback()
	batch, err := NewTraceArchiveSource(New(db)).FreezeBatch(context.Background(), observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{To: now})
	if !errors.Is(err, archivesvc.ErrArchivePayloadTooLarge) || len(batch.Candidates) != 0 {
		t.Fatalf("oversized package produced candidates: %+v %v", batch, err)
	}
	if err = m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
