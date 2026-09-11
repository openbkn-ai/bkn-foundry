// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestMariaDBEvidenceSnapshotRejectsCorruptRecords(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("isolated MariaDB DSN required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := sessionstore.New(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner := sessionvo.Owner{ApplicationPrincipalID: "decode-test", EffectiveSubjectType: sessionvo.SubjectService, EffectiveSubjectID: "decode-test"}
	service := sessionsvc.New(store, sessionsvc.Options{})
	conv, err := service.EnsureCurrentConversation(ctx, sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: fmt.Sprintf("decode-%d", time.Now().UnixNano()), IdempotencyKey: "ensure"})
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := service.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conv.ID, IdempotencyKey: "start"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conv.ID, InteractionID: interaction.ID, OperationKey: "query", ToolName: "query",
		Input:    sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, MediaType: "application/json", Inline: []byte(`{"value":1}`)},
		Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveAssemblyRevision(sessionvo.AssemblyRevision{ID: "rev-" + interaction.ID, InteractionID: interaction.ID,
			RevisionNo: 1, CompletionManifestVersion: "test", Completeness: sessionvo.EvidencePartial,
			Trigger: "test", CreatedAt: time.Now().UTC()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, found, err := store.ReadEvidenceSnapshot(ctx, interaction.ID)
	if err != nil || !found || len(before.Revisions) != 1 {
		t.Fatalf("fixture read found=%v err=%v", found, err)
	}
	for _, field := range []struct{ table, column string }{
		{"operations", "causation_event_ids"},
		{"receipts", "causation_event_ids"}, {"receipts", "observed_evidence_refs"},
		{"receipts", "business_refs"}, {"receipts", "artifact_refs"}, {"receipts", "partial_reasons"},
		{"assembly_revisions", "included_receipt_ids"}, {"assembly_revisions", "included_event_ids"}, {"assembly_revisions", "partial_reasons"},
	} {
		t.Run(field.table+"/"+field.column, func(t *testing.T) {
			// Identifiers come only from the static test table, never user input.
			update := "UPDATE bkn_trace_" + field.table + " SET " + field.column + "=? WHERE interaction_id=?"
			var original sql.NullString
			if err := db.QueryRowContext(ctx, "SELECT "+field.column+" FROM bkn_trace_"+field.table+" WHERE interaction_id=?", interaction.ID).Scan(&original); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := db.ExecContext(ctx, update, original, interaction.ID); err != nil {
					t.Fatal(err)
				}
			})
			for _, invalid := range []string{`["private-text"`, `{"private-text":1}`} {
				if _, err := db.ExecContext(ctx, update, invalid, interaction.ID); err != nil {
					t.Fatal(err)
				}
				got, found, err := store.ReadEvidenceSnapshot(ctx, interaction.ID)
				if found || !errors.Is(err, isessionstore.ErrInvalidEvidenceJSON) || !reflect.DeepEqual(got, sessionvo.EvidenceSnapshot{}) {
					t.Fatalf("corrupt metadata returned a partial snapshot: found=%v err=%v", found, err)
				}
				if strings.Contains(err.Error(), "private-text") || !strings.Contains(err.Error(), field.column) {
					t.Fatalf("error must identify field without source content: %v", err)
				}
				// Existing Store consumers retain their original tolerant behavior.
				if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
					tx.ListOperations(interaction.ID)
					tx.ListReceipts(interaction.ID)
					tx.ListAssemblyRevisions(interaction.ID)
					return nil
				}); err != nil {
					t.Fatalf("legacy read behavior changed: %v", err)
				}
			}
		})
	}
	after, found, err := store.ReadEvidenceSnapshot(ctx, interaction.ID)
	if err != nil || !found || !reflect.DeepEqual(before, after) {
		t.Fatalf("valid reread after rejection changed: found=%v err=%v", found, err)
	}
	t.Run("ledger", func(t *testing.T) {
		payload := json.RawMessage(`{"question_artifact_ref":"not-read"}`)
		now := time.Now().UTC()
		// A real interaction event has no operation or attempt. Capture must not
		// synthesize either merely because the interaction contains an operation.
		event := ledgervo.Event{EventID: "event-" + interaction.ID, EventType: "agent.interaction.started", SchemaVersion: "3.0.0",
			PayloadHash: ledgervo.CanonicalPayloadHash(payload), Owner: owner, ConversationID: conv.ID, InteractionID: interaction.ID,
			ProducerID: "decode-test", ProducerStreamID: "stream-" + interaction.ID, ProducerEpoch: 1, ProducerSequence: 1,
			StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: payload}
		if _, err := ledgersvc.New(store).Ingest(ctx, event); err != nil {
			t.Fatal(err)
		}
		var original []byte
		if err := db.QueryRowContext(ctx, "SELECT envelope FROM bkn_trace_evidence_event_ledger WHERE event_id=?", event.EventID).Scan(&original); err != nil {
			t.Fatal(err)
		}
		update := func(raw []byte) {
			t.Helper()
			if _, err := db.ExecContext(ctx, "UPDATE bkn_trace_evidence_event_ledger SET envelope=? WHERE event_id=?", raw, event.EventID); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() { update(original) })
		changed := func(field string, value any) []byte {
			t.Helper()
			var body map[string]json.RawMessage
			if err := json.Unmarshal(original, &body); err != nil {
				t.Fatal(err)
			}
			rawValue, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			body[field] = rawValue
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			return raw
		}
		for _, tc := range []struct {
			name string
			raw  []byte
			err  error
		}{
			{"malformed", []byte(`{"private-text"`), isessionstore.ErrInvalidEvidenceJSON},
			{"wrong_type", []byte(`[]`), isessionstore.ErrInvalidEvidenceJSON},
			{"metadata_type", changed("causation_event_ids", "private-text"), isessionstore.ErrInvalidEvidenceJSON},
			{"event_identity", changed("event_id", "private-text"), isessionstore.ErrEvidenceIdentityMismatch},
			{"interaction_identity", changed("interaction_id", "private-text"), isessionstore.ErrEvidenceIdentityMismatch},
			{"conversation_identity", changed("conversation_id", "private-text"), isessionstore.ErrEvidenceIdentityMismatch},
		} {
			t.Run(tc.name, func(t *testing.T) {
				update(tc.raw)
				got, found, err := store.ReadEvidenceSnapshot(ctx, interaction.ID)
				if found || !errors.Is(err, tc.err) || !reflect.DeepEqual(got, sessionvo.EvidenceSnapshot{}) {
					t.Fatalf("invalid event did not fail closed: found=%v err=%v", found, err)
				}
				if strings.Contains(err.Error(), "private-text") {
					t.Fatal("error exposed stored content")
				}
			})
		}
		update(changed("future_extension", json.RawMessage(`{"large":9007199254740993}`)))
		got, found, err := store.ReadEvidenceSnapshot(ctx, interaction.ID)
		if err != nil || !found || got.Ledger == nil || len(got.Ledger.Events) != 1 {
			t.Fatalf("unknown extension rejected: found=%v err=%v", found, err)
		}
		var captured ledgervo.Event
		if err := json.Unmarshal(got.Ledger.Events[0].Envelope, &captured); err != nil {
			t.Fatal(err)
		}
		if captured.Attempt != 0 || captured.OperationID != "" || !strings.Contains(string(got.Ledger.Events[0].Envelope), `"future_extension":{"large":9007199254740993}`) {
			t.Fatal("missing identity synthesized or unknown content lost")
		}
	})
}
