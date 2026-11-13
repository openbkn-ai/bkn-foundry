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
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/archivestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestTraceArchiveBoundedMariaDB(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_ARCHIVE_BATCH_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit isolated MariaDB DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid dedicated archive test DSN")
	}
	dedicatedEndpoint := cfg.Addr == "127.0.0.1:13341" || (os.Getenv("CI") == "true" && cfg.Addr == "127.0.0.1:3306")
	if cfg.Net != "tcp" || !dedicatedEndpoint || cfg.DBName != "" {
		t.Fatal("dedicated local archive test endpoint without existing database required")
	}
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	name := fmt.Sprintf("bkn_trace_archive_batch_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE DATABASE `"+name+"`"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(clean, "DROP DATABASE `"+name+"`"); err != nil {
			t.Error(err)
		}
	}()
	cfg.DBName = name
	cfg.ParseTime = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	store := New(db)
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := now.AddDate(0, 0, -31)
	if err = store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "archive-c", Owner: sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "owner"}, ExternalConversationKey: "archive-c", Generation: 1, Status: sessionvo.ConversationClosed, RowVersion: 1, CreatedAt: old, UpdatedAt: old})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seed := func(i int) {
		t.Helper()
		if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			id := fmt.Sprintf("archive-i-%03d", i)
			tx.SaveInteraction(sessionvo.Interaction{ID: id, ConversationID: "archive-c", Ordinal: uint64(i + 1), ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete, StartIdempotencyKey: id, LeaseToken: id, LeaseExpiresAt: old.Add(time.Hour), RowVersion: 1, CreatedAt: old, UpdatedAt: old, TerminalAt: &old})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 101; i++ {
		seed(i)
	}
	source := NewTraceArchiveSource(store)
	window := observabilityvo.ArchiveRange{To: now}
	for batchNo, want := range []int{100, 1} {
		batch, err := source.FreezeBatch(ctx, observabilityvo.ArchiveKindTrace, window)
		wantEligible := 101
		if batchNo == 1 {
			wantEligible = 1
		}
		if err != nil || !batch.Batched || len(batch.Candidates) != want || batch.EligibleCount != wantEligible {
			t.Fatalf("batch %d entries=%d eligible=%d err=%v", batchNo, len(batch.Candidates), batch.EligibleCount, err)
		}
		if err = source.Purge(ctx, observabilityvo.ArchiveKindTrace, batch.Candidates); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := source.Count(ctx, observabilityvo.ArchiveKindTrace, window); err != nil || count != 0 {
		t.Fatalf("remaining=%d err=%v", count, err)
	}
	seed(101)
	// Invalid JSON makes success impossible if a decoder ever receives this row:
	// the aggregate budget must reject it before loading closure_manifest.
	if _, err = db.ExecContext(ctx, `UPDATE bkn_trace_interactions SET closure_manifest=REPEAT('x',?) WHERE interaction_id=?`, archivesvc.MaxTraceArchiveCandidateBytes+1, "archive-i-101"); err != nil {
		t.Fatal(err)
	}
	batch, err := source.FreezeBatch(ctx, observabilityvo.ArchiveKindTrace, window)
	if !errors.Is(err, archivesvc.ErrArchivePayloadTooLarge) || len(batch.Candidates) != 0 {
		t.Fatalf("oversized closure entries=%d err=%v", len(batch.Candidates), err)
	}
	if count, err := source.Count(ctx, observabilityvo.ArchiveKindTrace, window); err != nil || count != 1 {
		t.Fatalf("oversized interaction lost: remaining=%d err=%v", count, err)
	}
	// Reuse only this disposable database. Three sorted candidates: small,
	// 37 sub-1MB outputs accumulating beyond 8MiB, then another small candidate.
	if _, err = db.ExecContext(ctx, `UPDATE bkn_trace_interactions SET closure_manifest=NULL WHERE interaction_id=?`, "archive-i-101"); err != nil {
		t.Fatal(err)
	}
	seed(102)
	seed(103)
	output := sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`"` + strings.Repeat("x", 400000) + `"`)}
	if err = store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		for i := 0; i < 37; i++ {
			op := fmt.Sprintf("large-op-%02d", i)
			receipt := fmt.Sprintf("large-receipt-%02d", i)
			tx.SaveOperation(sessionvo.Operation{ID: op, ConversationID: "archive-c", InteractionID: "archive-i-102", OperationKey: op, ToolName: "run_cypher", Attempt: 1, AttemptStatus: sessionvo.AttemptCompleted, RowVersion: 1, CreatedAt: old, UpdatedAt: old})
			tx.SaveReceipt(sessionvo.Receipt{ID: receipt, SchemaVersion: "3.0.0", Owner: sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "owner"}, ConversationID: "archive-c", InteractionID: "archive-i-102", OperationID: op, Attempt: 1, OperationKey: op, ToolName: "run_cypher", Status: sessionvo.ReceiptCompleted, EvidenceDurability: sessionvo.DurabilityDurable, RequestID: op, RowVersion: 1, IssuedAt: old, TerminalAt: &old})
			tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: op, ReceiptID: receipt, Attempt: 1, ConversationID: "archive-c", InteractionID: "archive-i-102", ToolName: "run_cypher", Protocol: sessionvo.ProtocolMCP, SourceModule: "context-loader", Input: sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`{}`)}, Output: &output, StartedAt: old, FinishedAt: &old, Status: sessionvo.AttemptCompleted})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The full service holds a kind lock on its own connection.
	db.SetMaxOpenConns(3)
	jobs := archivestore.New(db)
	objects := &packetArchiveObjects{}
	svc := archivesvc.New(jobs, archivesvc.TraceBundleSource{Core: source}, objects, archivesvc.Options{Now: func() time.Time { return now }, Location: time.UTC})
	job, err := svc.Create(ctx, observabilityvo.ArchiveKindTrace)
	var blocked *archivesvc.PayloadBudgetError
	if !errors.As(err, &blocked) || blocked.InteractionID != "archive-i-102" || blocked.Stage != "sql_payload" || blocked.Actual <= blocked.Limit {
		t.Fatalf("missing accumulated-output diagnosis: %v", err)
	}
	if len(objects.counts) != 1 || objects.counts[0] != 1 {
		t.Fatalf("prefix not committed alone: %v", objects.counts)
	}
	saved, ok := jobs.Get(job.ID)
	if !ok || saved.BatchProgress.Blocked == nil || saved.BatchProgress.RunComplete || saved.BatchProgress.CleanedInRun != 1 || saved.BatchProgress.RemainingAtSelection != 2 || saved.CandidateCount != 0 || saved.ManifestRef != "" {
		t.Fatalf("non-durable blockage: %+v", saved)
	}
	if count, err := source.Count(ctx, observabilityvo.ArchiveKindTrace, window); err != nil || count != 2 {
		t.Fatalf("lost blocked candidate or suffix: %d %v", count, err)
	}
	var facts int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bkn_trace_operation_call_facts WHERE interaction_id=?`, blocked.InteractionID).Scan(&facts); err != nil || facts != 37 {
		t.Fatalf("blocked facts changed: %d %v", facts, err)
	}
	restarted := archivesvc.New(archivestore.New(db), archivesvc.TraceBundleSource{Core: source}, objects, archivesvc.Options{Now: func() time.Time { return now }, Location: time.UTC})
	if _, err = restarted.RetryCleanup(ctx, job.ID); err == nil {
		t.Fatal("blocked job accepted cleanup")
	}
	retried, retryErr := restarted.Create(ctx, observabilityvo.ArchiveKindTrace)
	if retried.ID != job.ID {
		t.Fatal("real MariaDB retry did not reuse durable blocker")
	}
	if err = retryErr; !errors.Is(err, archivesvc.ErrArchivePayloadTooLarge) || len(objects.counts) != 1 {
		t.Fatalf("restart skipped blocked candidate: %v", err)
	}
	t.Logf("#2948: 37 outputs x400000 bytes; SQL aggregate=%d; safe prefix=1; blocked+suffix=2; persisted diagnosis survives restart; all 37 facts retained", blocked.Actual)

	t.Log("real MariaDB: 101 interactions archived as 100+1, empty eligible count, oversized closure rejected before decoding and retained hot")
}
