// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestTraceArchiveConcurrentSQLWritesMariaDB(t *testing.T) {
	for _, change := range []string{"late_receipt", "stored_integrity"} {
		t.Run(change, func(t *testing.T) {
			db := archiveConcurrencyDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			store := New(db)
			now := time.Now().UTC()
			old := now.AddDate(0, 0, -31)
			seed := func(id string) {
				t.Helper()
				if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
					tx.SaveConversation(sessionvo.Conversation{ID: id, Owner: sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "owner"}, ExternalConversationKey: id, Generation: 1, Status: sessionvo.ConversationClosed, RowVersion: 1, CreatedAt: old, UpdatedAt: old})
					tx.SaveInteraction(sessionvo.Interaction{ID: id, ConversationID: id, Ordinal: 1, ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete, StartIdempotencyKey: id, LeaseToken: id, LeaseExpiresAt: old.Add(time.Hour), RowVersion: 1, CreatedAt: old, UpdatedAt: old, TerminalAt: &old})
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			seed("unchanged")
			changed := "changed"
			var service *sessionsvc.Service
			var owner sessionvo.Owner
			var operation sessionvo.Operation
			var receipt sessionvo.Receipt
			if change == "late_receipt" {
				owner = sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "owner"}
				service = sessionsvc.New(store, sessionsvc.Options{})
				conv, err := service.EnsureCurrentConversation(ctx, sessionsvc.EnsureConversationCommand{Owner: owner, ExternalConversationKey: "late", IdempotencyKey: "late-conv"})
				if err != nil {
					t.Fatal(err)
				}
				interaction, err := service.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conv.ID, IdempotencyKey: "late-start"})
				if err != nil {
					t.Fatal(err)
				}
				input, err := sessionvo.InlineJSONPayload(json.RawMessage(`{"test_input":"late"}`))
				if err != nil {
					t.Fatal(err)
				}
				operation, receipt, err = service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{Owner: owner, ConversationID: conv.ID, InteractionID: interaction.ID, OperationKey: "late", ToolName: "query", Input: input, Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch})
				if err != nil {
					t.Fatal(err)
				}
				deadline := now.Add(-time.Hour)
				terminal, err := service.TerminateInteraction(ctx, sessionsvc.TerminateInteractionCommand{Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted, TerminalIdempotencyKey: "late-terminal", LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch, Manifest: sessionvo.ClosureManifest{Version: "1", CompletionReason: "answer_returned", AssemblerDeadline: &deadline, ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: true}}, ExpectedReceipts: []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: true}}}})
				if err != nil || terminal.EvidenceStatus != sessionvo.EvidencePartial {
					t.Fatalf("terminal fixture: evidence=%s err=%v", terminal.EvidenceStatus, err)
				}
				changed = interaction.ID
				// Keep archive eligibility historical without changing the actual producer flow.
				if _, err = db.ExecContext(ctx, "UPDATE bkn_trace_interactions SET terminal_at=? WHERE interaction_id=?", old, changed); err != nil {
					t.Fatal(err)
				}
			} else {
				seed(changed)
			}
			source := NewTraceArchiveSource(store)
			batch, err := source.FreezeBatch(ctx, observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{To: now})
			if err != nil || len(batch.Candidates) != 2 {
				t.Fatalf("freeze entries=%d err=%v", len(batch.Candidates), err)
			}
			byID := map[string]archivesvc.Candidate{}
			for _, candidate := range batch.Candidates {
				byID[candidate.ID] = candidate
			}
			ordered := []archivesvc.Candidate{byID["unchanged"], byID[changed]}
			if ordered[0].ID == "" || ordered[1].ID == "" {
				t.Fatal("missing frozen candidate")
			}
			var before uint64
			if err = db.QueryRowContext(ctx, "SELECT record_integrity_version FROM bkn_trace_interactions WHERE interaction_id=?", changed).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if change == "late_receipt" {
				output, err := sessionvo.InlineJSONPayload(json.RawMessage(`{"result":"late-result"}`))
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = service.CompleteOperationAttempt(ctx, sessionsvc.FinishAttemptCommand{Owner: owner, OperationID: operation.ID, Attempt: receipt.Attempt, ReceiptID: receipt.ID, Output: output, EvidenceDurability: sessionvo.DurabilityDurable, RequestID: "req-late", TraceID: "4b3d59daeff5bfbb23d46c47a5051ec9", ObservedEvidenceRefs: []string{"evt-late-evidence"}})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				saved, err := store.SaveStoredRecordIntegrity(ctx, changed, sessionvo.StoredRecordIntegrity{SourceVersion: before, Applicable: false})
				if err != nil || !saved {
					t.Fatalf("save integrity=%v err=%v", saved, err)
				}
			}
			var after uint64
			if err = db.QueryRowContext(ctx, "SELECT record_integrity_version FROM bkn_trace_interactions WHERE interaction_id=?", changed).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if change == "late_receipt" && after <= before || change == "stored_integrity" && after != before {
				t.Fatalf("wrong producer version transition %d -> %d", before, after)
			}
			purgeErr := source.Purge(ctx, observabilityvo.ArchiveKindTrace, ordered)
			var retained int
			if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_interactions WHERE interaction_id IN (?,?)", "unchanged", changed).Scan(&retained); err != nil {
				t.Fatal(err)
			}
			if purgeErr == nil || retained != 2 {
				t.Fatalf("unsafe cleanup: change=%s version=%d->%d purgeErr=%v retained=%d/2 (first candidate must roll back too)", change, before, after, purgeErr, retained)
			}
			if change == "late_receipt" {
				var status, output string
				var revisions int
				if err = db.QueryRowContext(ctx, "SELECT receipt_status FROM bkn_trace_receipts WHERE receipt_id=?", receipt.ID).Scan(&status); err != nil {
					t.Fatal(err)
				}
				if err = db.QueryRowContext(ctx, "SELECT output_payload FROM bkn_trace_operation_call_facts WHERE operation_id=? AND attempt_no=?", operation.ID, receipt.Attempt).Scan(&output); err != nil {
					t.Fatal(err)
				}
				if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_assembly_revisions WHERE interaction_id=?", changed).Scan(&revisions); err != nil {
					t.Fatal(err)
				}
				if status != "completed" || !strings.Contains(output, "late-result") || revisions != 2 {
					t.Fatalf("late source lost: status=%s revisions=%d output=%s", status, revisions, output)
				}
			} else {
				var raw string
				if err = db.QueryRowContext(ctx, "SELECT record_integrity_json FROM bkn_trace_interactions WHERE interaction_id=?", changed).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var verdict sessionvo.StoredRecordIntegrity
				if err = json.Unmarshal([]byte(raw), &verdict); err != nil || verdict.SourceVersion != before {
					t.Fatalf("verdict lost: %q err=%v", raw, err)
				}
			}
			// The original unchanged payload must still clean normally after the
			// failed batch, and repeating that exact cleanup must be idempotent.
			for attempt := 0; attempt < 2; attempt++ {
				if err := source.Purge(ctx, observabilityvo.ArchiveKindTrace, ordered[:1]); err != nil {
					t.Fatalf("unchanged cleanup attempt %d: %v", attempt, err)
				}
			}
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_interactions WHERE interaction_id IN (?,?)", "unchanged", changed).Scan(&retained); err != nil || retained != 1 {
				t.Fatalf("unchanged control did not clean independently: retained=%d err=%v", retained, err)
			}
			t.Logf("%s rejected stale frozen cleanup; new SQL evidence retained; unchanged control cleaned idempotently", change)
		})
	}
}

func archiveConcurrencyDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("BKN_TRACE_ARCHIVE_BATCH_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit isolated archive DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated archive DSN")
	}
	dedicated := cfg.Addr == "127.0.0.1:13341" || os.Getenv("CI") == "true" && cfg.Addr == "127.0.0.1:3306"
	if cfg.Net != "tcp" || !dedicated || cfg.DBName != "" {
		t.Fatal("dedicated archive endpoint without existing database required")
	}
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("bkn_trace_archive_concurrency_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE `"+name+"`"); err != nil {
			t.Error(err)
		}
	})
	cfg.DBName = name
	cfg.ParseTime = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = New(db).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}
