// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func integritySQLPort(t *testing.T, value any) isessionstore.RecordIntegrityStore {
	t.Helper()
	port, ok := value.(isessionstore.RecordIntegrityStore)
	if !ok {
		t.Fatal("MariaDB store lacks record integrity capability")
	}
	return port
}

func sqlIntegrityReport(version uint64) sessionvo.StoredRecordIntegrity {
	return sessionvo.StoredRecordIntegrity{SourceVersion: version, Applicable: true, Report: &sessionvo.RecordIntegrity{Status: "complete", CheckedAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), Scope: "registered_call_records", Missing: []sessionvo.MissingRecord{}}}
}

func TestMariaDBRecordIntegrityCASOnlyFirstMatchingVersion(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	port := integritySQLPort(t, store)
	stored := sqlIntegrityReport(3)
	raw, _ := json.Marshal(stored)
	query := `UPDATE bkn_trace_interactions SET record_integrity_json=\? WHERE interaction_id=\? AND record_integrity_version=\? AND record_integrity_json IS NULL AND execution_status<>\?`
	for _, affected := range []int64{1, 0} {
		mock.ExpectExec(query).WithArgs(string(raw), "int", uint64(3), sessionvo.InteractionActive).WillReturnResult(sqlmock.NewResult(0, affected))
		ok, err := port.SaveStoredRecordIntegrity(context.Background(), "int", stored)
		if err != nil || ok != (affected == 1) {
			t.Fatalf("CAS changed: %v %v", ok, err)
		}
	}
}

func TestMariaDBRecordIntegrityCandidatesReturnLightweightKeyset(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	port := integritySQLPort(t, store)
	mock.ExpectQuery(`SELECT interaction_id, record_integrity_version FROM bkn_trace_interactions WHERE record_integrity_pending=1 AND interaction_id>\? ORDER BY interaction_id ASC LIMIT \?`).WithArgs("a", 2).WillReturnRows(sqlmock.NewRows([]string{"interaction_id", "record_integrity_version"}).AddRow("b", 3).AddRow("c", 0))
	entries, err := port.ListRecordIntegrityCandidates(context.Background(), "a", 2)
	if err != nil || len(entries) != 2 || entries[0].ID != "b" || entries[0].IntegritySourceVersion != 3 || entries[0].ConversationID != "" {
		t.Fatalf("wrong candidate page: %#v %v", entries, err)
	}
}

func TestMariaDBRecordIntegrityInvalidatesByFullOwner(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	port := integritySQLPort(t, store)
	owner := sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "user", DelegationID: "delegation"}
	mock.ExpectExec(`UPDATE bkn_trace_interactions i JOIN bkn_trace_conversations c ON c.conversation_id=i.conversation_id SET i.record_integrity_version=i.record_integrity_version\+1, i.record_integrity_json=NULL WHERE i.interaction_id=\? AND i.execution_status<>\? AND c.application_principal_id=\? AND c.effective_subject_type=\? AND c.effective_subject_id=\? AND c.delegation_id=\?`).WithArgs("int", sessionvo.InteractionActive, owner.ApplicationPrincipalID, owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := port.InvalidateRecordIntegrity(context.Background(), "int", owner); err != nil {
		t.Fatal(err)
	}
}

func TestMariaDBRecordIntegrityDirtyWritesMergeBeforeCommit(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(now))
	mock.ExpectQuery("SELECT 1 FROM bkn_trace_interactions").WithArgs("int").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
	mock.ExpectExec("UPDATE bkn_trace_interactions SET execution_status=").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT 1 FROM bkn_trace_receipts").WithArgs("r").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
	mock.ExpectExec("UPDATE bkn_trace_receipts SET receipt_status=").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT 1 FROM bkn_trace_operation_call_facts").WithArgs("op", uint32(1)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
	mock.ExpectExec("UPDATE bkn_trace_operation_call_facts SET receipt_id=").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE bkn_trace_interactions SET record_integrity_version=record_integrity_version\+1, record_integrity_json=NULL WHERE execution_status<>\? AND interaction_id IN \(\?\)`).WithArgs(sessionvo.InteractionActive, "int").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveInteraction(sessionvo.Interaction{ID: "int", ExecutionStatus: sessionvo.InteractionCompleted, RowVersion: 9})
		tx.SaveReceipt(sessionvo.Receipt{ID: "r", InteractionID: "int"})
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op", Attempt: 1, InteractionID: "int"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMariaDBRecordIntegrityDirtyUpdateErrorRollsBack(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(time.Now()))
	mock.ExpectQuery("SELECT 1 FROM bkn_trace_receipts").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
	mock.ExpectExec("UPDATE bkn_trace_receipts SET receipt_status=").WillReturnResult(sqlmock.NewResult(0, 1))
	failure := errors.New("invalidation write failed")
	mock.ExpectExec("UPDATE bkn_trace_interactions SET record_integrity_version=").WillReturnError(failure)
	mock.ExpectRollback()
	err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveReceipt(sessionvo.Receipt{ID: "r", InteractionID: "int"})
		return nil
	})
	if !errors.Is(err, failure) {
		t.Fatalf("dirty source update failure lost: %v", err)
	}
}

func TestMariaDBRecordIntegrityCapacityRejectsBeforeSQL(t *testing.T) {
	store, _ := newTransactionErrorStore(t)
	port := integritySQLPort(t, store)
	stored := sqlIntegrityReport(3)
	stored.Report.Scope = strings.Repeat("x", isessionstore.MaxStoredRecordIntegrityBytes)
	if _, err := port.SaveStoredRecordIntegrity(context.Background(), "int", stored); !errors.Is(err, isessionstore.ErrRecordIntegrityLimit) {
		t.Fatal(err)
	}
}

func TestMariaDBInteractionSelectCarriesIntegrityWithoutWireLeak(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	stored := sqlIntegrityReport(7)
	raw, _ := json.Marshal(stored)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(time.Now()))
	mock.ExpectQuery("FROM bkn_trace_interactions").WithArgs("int").WillReturnRows(sqlIntegrityInteractionRows(string(raw), 7))
	mock.ExpectCommit()
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		value, found := tx.PeekInteraction("int")
		if !found || value.IntegritySourceVersion != 7 || value.StoredRecordIntegrity == nil || value.StoredRecordIntegrity.Report.Status != "complete" {
			t.Fatalf("list projection discarded stored integrity: %#v", value)
		}
		wire, _ := json.Marshal(value)
		if strings.Contains(string(wire), "integrity") {
			t.Fatal("internal metadata leaked on lifecycle wire")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMariaDBInteractionSelectRejectsCorruptIntegrityJSON(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(time.Now()))
	mock.ExpectQuery("FROM bkn_trace_interactions").WithArgs("int").WillReturnRows(sqlIntegrityInteractionRows("{", 7))
	mock.ExpectRollback()
	err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error { tx.PeekInteraction("int"); return nil })
	if !errors.Is(err, isessionstore.ErrInvalidEvidenceJSON) {
		t.Fatalf("corrupt cache became absent: %v", err)
	}
}

func sqlIntegrityInteractionRows(raw any, version uint64) *sqlmock.Rows {
	now := time.Now().UTC()
	columns := []string{"interaction_id", "conversation_id", "ordinal", "execution", "evidence", "start", "terminal_key", "terminal_hash", "manifest", "lease", "epoch", "lease_version", "expiry", "row_version", "created", "updated", "terminal", "record_integrity_version", "record_integrity_json"}
	return sqlmock.NewRows(columns).AddRow("int", "conv", 1, "completed", "complete", "start", "", "", "", "lease", 1, 1, now, 9, now, now, now, version, raw)
}

func TestMariaDBNewLedgerCommitInvalidatesIntegrityAtomically(t *testing.T) {
	for _, failInvalidation := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[failInvalidation], func(t *testing.T) {
			store, mock := newTransactionErrorStore(t)
			event := ledgervo.Event{EventID: "event", ConversationID: "conv", InteractionID: "int", Owner: transactionErrorOwner(), ProducerStreamID: "stream", ProducerEpoch: 1, ProducerSequence: 1, PayloadHash: "hash"}
			expectIntegrityLedgerStart(mock, event, false)
			mock.ExpectQuery("SELECT envelope FROM bkn_trace_evidence_event_ledger").WithArgs("int").WillReturnRows(sqlmock.NewRows([]string{"envelope"}))
			mock.ExpectQuery("SELECT event_id, payload_hash FROM bkn_trace_evidence_event_ledger").WithArgs("stream", uint64(1), uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"id", "hash"}))
			mock.ExpectQuery("SELECT MAX\\(producer_epoch\\)").WithArgs("stream").WillReturnRows(sqlmock.NewRows([]string{"epoch"}).AddRow(nil))
			mock.ExpectQuery("SELECT MAX\\(producer_sequence\\)").WithArgs("stream", uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"seq"}).AddRow(nil))
			mock.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(time.Now()))
			mock.ExpectExec("INSERT INTO bkn_trace_evidence_event_ledger").WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectExec("INSERT INTO bkn_trace_projection_outbox").WillReturnResult(sqlmock.NewResult(1, 1))
			invalidation := mock.ExpectExec(`UPDATE bkn_trace_interactions SET record_integrity_version=record_integrity_version\+1, record_integrity_json=NULL WHERE interaction_id=\? AND execution_status<>\?`).WithArgs("int", sessionvo.InteractionActive)
			failure := errors.New("ledger integrity invalidation failed")
			if failInvalidation {
				invalidation.WillReturnError(failure)
				mock.ExpectRollback()
			} else {
				invalidation.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			ack, err := store.Commit(context.Background(), event)
			if failInvalidation {
				if !errors.Is(err, failure) || ack.Durable {
					t.Fatalf("partial ledger commit: %#v %v", ack, err)
				}
			} else if err != nil || !ack.Durable {
				t.Fatalf("ledger did not commit with invalidation: %#v %v", ack, err)
			}
		})
	}
}

func TestMariaDBLedgerReplayPreservesStoredIntegrity(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	event := ledgervo.Event{EventID: "event", ConversationID: "conv", InteractionID: "int", Owner: transactionErrorOwner()}
	expectIntegrityLedgerStart(mock, event, true)
	mock.ExpectCommit()
	ack, err := store.Commit(context.Background(), event)
	if err != nil || !ack.Replayed {
		t.Fatalf("replay changed: %#v %v", ack, err)
	}
}

func expectIntegrityLedgerStart(mock sqlmock.Sqlmock, event ledgervo.Event, replay bool) {
	mock.ExpectBegin()
	owner := event.Owner
	mock.ExpectQuery("SELECT c.application_principal_id").WithArgs(event.ConversationID, event.InteractionID).WillReturnRows(sqlmock.NewRows([]string{"app", "type", "id", "delegation"}).AddRow(owner.ApplicationPrincipalID, owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID))
	rows := sqlmock.NewRows([]string{"immutable_record_hash", "ingest_sequence", "ingested_at"})
	if replay {
		rows.AddRow(ledgervo.ImmutableRecordHash(event), 1, time.Now())
	}
	mock.ExpectQuery("SELECT immutable_record_hash, ingest_sequence, ingested_at").WithArgs(event.EventID).WillReturnRows(rows)
}

func TestMariaDBTraceArchivePreservesInternalIntegrityMetadata(t *testing.T) {
	store, mock := newTransactionErrorStore(t)
	stored := sqlIntegrityReport(7)
	raw, _ := json.Marshal(stored)
	mock.ExpectQuery("SELECT interaction_id FROM bkn_trace_interactions").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("int"))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(time.Now()))
	mock.ExpectQuery("FROM bkn_trace_interactions").WithArgs("int").WillReturnRows(sqlIntegrityInteractionRows(string(raw), 7))
	mock.ExpectQuery("FROM bkn_trace_conversations").WithArgs("conv").WillReturnRows(transactionErrorConversationRows(ptrTransactionErrorOwner()))
	mock.ExpectQuery("FROM bkn_trace_operations").WithArgs("int").WillReturnRows(sqlmock.NewRows([]string{"empty"}))
	mock.ExpectQuery("FROM bkn_trace_receipts").WithArgs("int").WillReturnRows(sqlmock.NewRows([]string{"empty"}))
	mock.ExpectQuery("FROM bkn_trace_operation_call_facts").WithArgs("int").WillReturnRows(sqlmock.NewRows([]string{"empty"}))
	mock.ExpectCommit()
	entries, err := sessionstore.NewTraceArchiveSource(store).Freeze(context.Background(), observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{To: time.Now().Add(time.Hour)})
	if err != nil || len(entries) != 1 {
		t.Fatalf("archive failed: %#v %v", entries, err)
	}
	var archive struct {
		Interaction struct {
			Version uint64                           `json:"record_integrity_version"`
			Stored  *sessionvo.StoredRecordIntegrity `json:"stored_record_integrity"`
		} `json:"interaction"`
	}
	if err := json.Unmarshal(entries[0].Payload, &archive); err != nil {
		t.Fatal(err)
	}
	if archive.Interaction.Version != 7 || archive.Interaction.Stored == nil || archive.Interaction.Stored.SourceVersion != 7 {
		t.Fatalf("archive dropped internal metadata: %s", entries[0].Payload)
	}
}

func ptrTransactionErrorOwner() *sessionvo.Owner { owner := transactionErrorOwner(); return &owner }
