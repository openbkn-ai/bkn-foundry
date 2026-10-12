// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
package main

import (
	"context"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"testing"
	"time"
)

func historicalLedgerFixture(t *testing.T) historicalLedgerRow {
	t.Helper()
	now := time.Date(2026, 9, 12, 1, 2, 3, 123456000, time.UTC)
	e := ledgervo.Event{EventID: "event", EventType: "retrieval.completed", SchemaVersion: "3.0.0", Owner: sessionvo.Owner{ApplicationPrincipalID: "agent", EffectiveSubjectType: "user", EffectiveSubjectID: "alice"}, ConversationID: "conversation", InteractionID: "interaction", RequestID: "request", TraceID: "trace", ProducerID: "producer", ProducerStreamID: "stream", ProducerEpoch: 2, ProducerSequence: 7, CausalityStatus: "complete", StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: json.RawMessage(`{ "payload": {"status":"completed"} }`)}
	e.PayloadHash = ledgervo.CanonicalPayloadHash(e.Envelope)
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	req, tr := "request", "trace"
	return historicalLedgerRow{IngestSequence: 1, EventID: e.EventID, PayloadHash: e.PayloadHash, ImmutableRecordHash: ledgervo.ImmutableRecordHash(e), SchemaVersion: e.SchemaVersion, EventType: e.EventType, ConversationID: e.ConversationID, InteractionID: e.InteractionID, RequestID: &req, TraceID: &tr, ProducerID: e.ProducerID, ProducerStreamID: e.ProducerStreamID, ProducerEpoch: e.ProducerEpoch, ProducerSequence: e.ProducerSequence, CausalityStatus: e.CausalityStatus, StartedAt: historicalLedgerTime(now), ObservedAt: historicalLedgerTime(now), EmittedAt: historicalLedgerTime(now), IngestedAt: historicalLedgerTime(now), Envelope: string(raw)}
}
func ledgerMockRow(t *testing.T, row historicalLedgerRow) *sqlmock.Rows {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return sqlmock.NewRows([]string{"record"}).AddRow(string(raw))
}
func expectLedgerOwner(mock sqlmock.Sqlmock, subject string) {
	mock.ExpectQuery("FROM bkn_trace_conversations").WithArgs("conversation", "interaction").WillReturnRows(sqlmock.NewRows([]string{"app", "type", "subject", "delegation"}).AddRow("agent", "user", subject, ""))
}

func TestHistoricalLedgerPreservesOriginalAndDatabaseSequence(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	row := historicalLedgerFixture(t)
	stored := row
	stored.IngestSequence = 500
	mock.ExpectBegin()
	expectLedgerOwner(mock, "alice")
	mock.ExpectQuery("FROM bkn_trace_evidence_event_ledger").WithArgs(row.EventID).WillReturnRows(sqlmock.NewRows([]string{"record"}))
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_event_ledger").WithArgs(
		row.EventID, row.PayloadHash, row.ImmutableRecordHash, "3.0.0", "retrieval.completed",
		"conversation", "interaction", nil, nil, "request", "trace", nil,
		"producer", "stream", uint64(2), uint64(7), "complete", nil,
		time.Time(row.StartedAt), time.Time(row.ObservedAt), time.Time(row.EmittedAt), time.Time(row.IngestedAt), row.Envelope,
	).WillReturnResult(sqlmock.NewResult(500, 1))
	mock.ExpectExec("UPDATE bkn_trace_interactions SET record_integrity_version").WithArgs(row.InteractionID, sessionvo.InteractionActive).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM bkn_trace_evidence_event_ledger").WithArgs(row.EventID).WillReturnRows(ledgerMockRow(t, stored))
	mock.ExpectCommit()
	mock.ExpectQuery("FROM bkn_trace_evidence_event_ledger").WithArgs(row.EventID).WillReturnRows(ledgerMockRow(t, stored))
	result, err := importHistoricalLedgerPlan(context.Background(), db, []historicalLedgerRow{row})
	if err != nil || !result.Verified || result.Created != 1 || result.AlreadyVerified != 0 {
		t.Fatalf("import: %+v %v", result, err)
	}
	if len(result.SequenceMappings) != 1 || result.SequenceMappings[0].TargetSequence != 500 {
		t.Fatalf("sequence collision: %+v", result)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestHistoricalLedgerRepeatIsStrictlyIdempotent(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	row := historicalLedgerFixture(t)
	stored := row
	stored.IngestSequence = 500
	mock.ExpectBegin()
	expectLedgerOwner(mock, "alice")
	mock.ExpectQuery("FROM bkn_trace_evidence_event_ledger").WithArgs(row.EventID).WillReturnRows(ledgerMockRow(t, stored))
	mock.ExpectCommit()
	mock.ExpectQuery("FROM bkn_trace_evidence_event_ledger").WithArgs(row.EventID).WillReturnRows(ledgerMockRow(t, stored))
	result, err := importHistoricalLedgerPlan(context.Background(), db, []historicalLedgerRow{row})
	if err != nil || !result.Verified || result.Created != 0 || result.AlreadyVerified != 1 {
		t.Fatalf("repeat: %+v %v", result, err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestHistoricalLedgerRejectsOwnerContentAndReadbackConflicts(t *testing.T) {
	for _, kind := range []string{"owner", "content", "readback"} {
		t.Run(kind, func(t *testing.T) {
			db, mock, _ := sqlmock.New()
			defer func() { _ = db.Close() }()
			row := historicalLedgerFixture(t)
			mock.ExpectBegin()
			if kind == "owner" {
				expectLedgerOwner(mock, "other")
			} else {
				expectLedgerOwner(mock, "alice")
				stored := row
				stored.IngestSequence = 500
				stored.Envelope += " "
				if kind == "content" {
					mock.ExpectQuery("FROM bkn_trace_evidence_event_ledger").WithArgs(row.EventID).WillReturnRows(ledgerMockRow(t, stored))
				} else {
					mock.ExpectQuery("FROM bkn_trace_evidence_event_ledger").WithArgs(row.EventID).WillReturnRows(sqlmock.NewRows([]string{"record"}))
					mock.ExpectExec("INSERT INTO bkn_trace_evidence_event_ledger").WillReturnResult(sqlmock.NewResult(500, 1))
					mock.ExpectExec("UPDATE bkn_trace_interactions SET record_integrity_version").WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectQuery("FROM bkn_trace_evidence_event_ledger").WithArgs(row.EventID).WillReturnRows(ledgerMockRow(t, stored))
				}
			}
			mock.ExpectRollback()
			result, err := importHistoricalLedgerPlan(context.Background(), db, []historicalLedgerRow{row})
			if err == nil || result.Verified {
				t.Fatalf("conflict accepted: %+v %v", result, err)
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestHistoricalLedgerPreflightRejectsHashMetadataAndDuplicate(t *testing.T) {
	for _, kind := range []string{"hash", "metadata", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			row := historicalLedgerFixture(t)
			rows := []historicalLedgerRow{row}
			switch kind {
			case "hash":
				rows[0].ImmutableRecordHash = "wrong"
			case "metadata":
				rows[0].InteractionID = "other"
			case "duplicate":
				other := row
				other.Envelope += " "
				rows = append(rows, other)
			}
			if _, err := prepareHistoricalLedger(rows); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}
