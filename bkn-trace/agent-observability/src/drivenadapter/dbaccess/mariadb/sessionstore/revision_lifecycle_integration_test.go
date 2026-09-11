// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"os"
	"testing"
	"time"
)

func TestRevisionLifecycleSealAndRead(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_REVISION_STORAGE_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit isolated writable database required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := New(db)
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, revisionInputTestSchema); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"late", "canceled", "budget", "outbox_failure", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "outbox_failure" {
				if _, e := db.ExecContext(ctx, `CREATE TRIGGER revision_notice_failure BEFORE INSERT ON bkn_trace_projection_outbox FOR EACH ROW BEGIN IF NEW.event_type = 'revision.input.sealed' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'injected notice failure'; END IF; END`); e != nil {
					t.Fatal(e)
				}
				defer func() {
					if _, e := db.ExecContext(context.Background(), `DROP TRIGGER revision_notice_failure`); e != nil {
						t.Error(e)
					}
				}()
			}
			options := sessionsvc.Options{}
			if mode != "disabled" {
				options.RevisionSealer = func(tx isessionstore.Transaction, i sessionvo.Interaction, r sessionvo.AssemblyRevision) error {
					limit := 1000000
					if mode == "budget" {
						limit = 1
					}
					return evidencesvc.SealRevisionInput(tx, i, r, 1000, limit)
				}
			}
			service := sessionsvc.New(store, options)
			owner := sessionvo.Owner{ApplicationPrincipalID: "seal-test", EffectiveSubjectType: sessionvo.SubjectService, EffectiveSubjectID: "seal-test"}
			conv, err := service.EnsureCurrentConversation(ctx, sessionsvc.EnsureConversationCommand{Owner: owner, ExternalConversationKey: fmt.Sprintf("seal-%s-%d", mode, time.Now().UnixNano()), IdempotencyKey: "ensure"})
			if err != nil {
				t.Fatal(err)
			}
			i, err := service.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conv.ID, IdempotencyKey: "start"})
			if err != nil {
				t.Fatal(err)
			}
			op, receipt, err := service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{Owner: owner, ConversationID: conv.ID, InteractionID: i.ID, OperationKey: "query", ToolName: "query", Input: sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, MediaType: "application/json", Inline: []byte(`{"n":9007199254740993}`)}, Required: true, LeaseToken: i.LeaseToken, LeaseEpoch: i.LeaseEpoch})
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(-time.Second)
			status := sessionvo.InteractionCompleted
			if mode == "canceled" {
				status = sessionvo.InteractionCanceled
			}
			command := sessionsvc.TerminateInteractionCommand{Owner: owner, InteractionID: i.ID, Status: status, TerminalIdempotencyKey: "terminal", LeaseToken: i.LeaseToken, LeaseEpoch: i.LeaseEpoch, Manifest: sessionvo.ClosureManifest{Version: "test", CompletionReason: "test", AssemblerDeadline: &deadline, ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: op.ID, Required: true}}, ExpectedReceipts: []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: true}}}}
			_, err = service.TerminateInteraction(ctx, command)
			if mode == "budget" || mode == "outbox_failure" {
				if err == nil {
					t.Fatal("budget failure not propagated")
				}
				current, e := service.GetInteraction(ctx, owner, i.ID)
				if e != nil || current.ExecutionStatus != sessionvo.InteractionActive {
					t.Fatal("termination not rolled back", e)
				}
				revisions, e := service.ListAssemblyRevisions(ctx, owner, i.ID)
				if e != nil || len(revisions) != 0 {
					t.Fatal("orphan revision", e)
				}
				var count int
				if e = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bkn_trace_revision_inputs WHERE interaction_id=?`, i.ID).Scan(&count); e != nil || count != 0 {
					t.Fatal("orphan seal", e)
				}
				if e = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE event_type=? AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.interaction_id'))=?`, sessionvo.RevisionInputSealedEvent, i.ID).Scan(&count); e != nil || count != 0 {
					t.Fatal("orphan notice", e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.TerminateInteraction(ctx, command); err != nil {
				t.Fatal("terminal retry", err)
			}
			revisions, err := service.ListAssemblyRevisions(ctx, owner, i.ID)
			if err != nil || len(revisions) != 1 {
				t.Fatal("duplicate revision", err)
			}
			if mode == "disabled" {
				if _, found, e := store.ReadRevisionInput(ctx, i.ID, revisions[0].ID, 1000000); e != nil || found {
					t.Fatal("default behavior changed", e)
				}
				return
			}
			read := func(r sessionvo.AssemblyRevision) sessionvo.EvidenceSnapshot {
				var payload []byte
				if err := db.QueryRowContext(ctx, `SELECT payload FROM bkn_trace_projection_outbox WHERE event_type=? AND aggregate_id=?`, sessionvo.RevisionInputSealedEvent, r.ID).Scan(&payload); err != nil {
					t.Fatal(err)
				}
				var n sessionvo.RevisionInputNotice
				if err := json.Unmarshal(payload, &n); err != nil {
					t.Fatal(err)
				}
				snapshot, err := evidencesvc.ReadSealedRevision(ctx, store, n, 1000, 1000000)
				if err != nil {
					t.Fatal(err)
				}
				again, err := evidencesvc.ReadSealedRevision(ctx, store, n, 1000, 1000000)
				if err != nil || !bytes.Equal(snapshot.CallFacts[0].Input.Inline, again.CallFacts[0].Input.Inline) {
					t.Fatal("retry changed source", err)
				}
				n.Hash = "wrong"
				if _, err = evidencesvc.ReadSealedRevision(ctx, store, n, 1000, 1000000); err == nil {
					t.Fatal("forged notice accepted")
				}
				return snapshot
			}
			source := NewTraceArchiveSourceWithRevisionInputs(store)
			frozenBeforeLate, occurred, e := source.packageInteraction(ctx, i.ID)
			if e != nil {
				t.Fatal(e)
			}
			old := read(revisions[0])
			if old.Interaction.ExecutionStatus != status || old.Ledger != nil || old.CallFacts[0].Output != nil {
				t.Fatal("initial state changed")
			}
			if mode == "canceled" {
				return
			}
			_, _, err = service.CompleteOperationAttempt(ctx, sessionsvc.FinishAttemptCommand{Owner: owner, OperationID: op.ID, Attempt: receipt.Attempt, ReceiptID: receipt.ID, Output: sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, MediaType: "application/json", Inline: []byte(`{"result":42}`)}, EvidenceDurability: sessionvo.DurabilityDurable, RequestID: "req-late", TraceID: "0123456789abcdef0123456789abcdef", ObservedEvidenceRefs: []string{"event-late"}})
			if err != nil {
				t.Fatal(err)
			}
			revisions, err = service.ListAssemblyRevisions(ctx, owner, i.ID)
			if err != nil || len(revisions) != 2 || revisions[1].ParentRevisionID != revisions[0].ID {
				t.Fatal("late revision missing", err)
			}
			if output := os.Getenv("BKN_REVISION_PIPELINE_OUTPUT"); output != "" {
				values := []sessionvo.SealedRevisionInput{}
				for _, r := range revisions {
					v, found, e := store.ReadRevisionInput(ctx, i.ID, r.ID, 1000000)
					if e != nil || !found {
						t.Fatal(e)
					}
					values = append(values, v)
				}
				encoded, e := json.Marshal(values)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(output, encoded, 0600); e != nil {
					t.Fatal(e)
				}
			}
			latest := read(revisions[1])
			oldAgain := read(revisions[0])
			if latest.CallFacts[0].Output == nil || oldAgain.CallFacts[0].Output != nil || oldAgain.Revisions[0].Completeness != sessionvo.EvidencePartial || latest.Revisions[0].Completeness != sessionvo.EvidenceComplete {
				t.Fatal("historical state mixed")
			}
			if !bytes.Equal(old.CallFacts[0].Input.Inline, latest.CallFacts[0].Input.Inline) {
				t.Fatal("wide integer input changed")
			}
			kind := observabilityvo.ArchiveKindTrace
			if e := source.Purge(ctx, kind, []archivesvc.Candidate{{ID: i.ID, OccurredAt: occurred, Payload: frozenBeforeLate}}); e == nil {
				t.Fatal("stale archive purged new revision")
			}
			payload, at, e := source.packageInteraction(ctx, i.ID)
			if e != nil {
				t.Fatal(e)
			}
			candidate := []archivesvc.Candidate{{ID: i.ID, OccurredAt: at, Payload: payload}}
			if e := source.Purge(ctx, kind, candidate); e == nil {
				t.Fatal("pending notices purged")
			}
			applied := map[string]string{}
			failOnce := true
			consumer := &evidencesvc.RevisionInputConsumer{Reader: store, MaxRecords: 1000, MaxBytes: 1000000, Apply: func(_ context.Context, snapshot sessionvo.EvidenceSnapshot, hash string) error {
				if failOnce {
					failOnce = false
					return fmt.Errorf("injected projection failure")
				}
				rid := snapshot.Revisions[0].ID
				if prior, ok := applied[rid]; ok && prior != hash {
					return fmt.Errorf("projection identity conflict")
				}
				applied[rid] = hash
				return nil
			}}
			worker := projectorsvc.NewWorker(store, revisionTestSink{}, projectorsvc.WorkerOptions{RevisionInputHandler: consumer, FullJitter: func(time.Duration) time.Duration { return 0 }})
			first, e := worker.RunOnce(ctx)
			if e != nil || first.Retried != 1 {
				t.Fatal("projection failure not retried", e, first)
			}
			if e := source.Purge(ctx, kind, candidate); e == nil {
				t.Fatal("retrying notice purged")
			}
			if _, e = worker.Drain(ctx); e != nil {
				t.Fatal(e)
			}
			if len(applied) != 2 {
				t.Fatal("revision consumer did not apply both versions")
			}
			// Add a second terminal candidate, then pause purge immediately before
			// locking it. A concurrent update must be visible after that lock is taken.
			second, e := service.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conv.ID, IdempotencyKey: "archive-second"})
			if e != nil {
				t.Fatal(e)
			}
			_, e = service.TerminateInteraction(ctx, sessionsvc.TerminateInteractionCommand{Owner: owner, InteractionID: second.ID, Status: sessionvo.InteractionCompleted, TerminalIdempotencyKey: "second-done", LeaseToken: second.LeaseToken, LeaseEpoch: second.LeaseEpoch, Manifest: sessionvo.ClosureManifest{Version: "test", CompletionReason: "test"}})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = worker.Drain(ctx); e != nil {
				t.Fatal(e)
			}
			secondPayload, secondAt, e := source.packageInteraction(ctx, second.ID)
			if e != nil {
				t.Fatal(e)
			}
			gated, entered, resume := revisionPurgeGateDB(t, dsn, second.ID)
			done := make(chan error, 1)
			go func() {
				done <- NewTraceArchiveSourceWithRevisionInputs(New(gated)).Purge(ctx, kind, append(append([]archivesvc.Candidate{}, candidate...), archivesvc.Candidate{ID: second.ID, OccurredAt: secondAt, Payload: secondPayload}))
			}()
			select {
			case <-entered:
			case e := <-done:
				t.Fatal("purge did not reach second candidate", e)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if _, e = db.ExecContext(ctx, `UPDATE bkn_trace_interactions SET row_version=row_version+1 WHERE interaction_id=?`, second.ID); e != nil {
				close(resume)
				t.Fatal(e)
			}
			close(resume)
			if e = <-done; e == nil {
				t.Fatal("stale second candidate was purged")
			}
			if _, found, e := store.ReadRevisionInput(ctx, i.ID, revisions[0].ID, 1000000); e != nil || !found {
				t.Fatal("first candidate was not rolled back", e)
			}
			if e = source.Purge(ctx, kind, candidate); e != nil {
				t.Fatal("verified archive purge", e)
			}
			var remain int
			if e = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bkn_trace_revision_inputs WHERE interaction_id=?`, i.ID).Scan(&remain); e != nil || remain != 0 {
				t.Fatal("orphan sealed body", e)
			}
			var archived struct {
				Evidence revisionArchiveContents `json:"revision_evidence"`
			}
			if e = json.Unmarshal(payload, &archived); e != nil || len(archived.Evidence.Inputs) != 2 {
				t.Fatal("archive omitted seals", e)
			}
			for _, v := range archived.Evidence.Inputs {
				if _, e = evidencesvc.DecodeRevisionInput(v.Package, v.Hash, v.InteractionID, v.RevisionID, 1000, 1000000); e != nil {
					t.Fatal("archived seal unreadable", e)
				}
			}

		})
	}
}

type revisionTestSink struct{}

func (revisionTestSink) Project(context.Context, iprojectionoutbox.Item) error { return nil }
