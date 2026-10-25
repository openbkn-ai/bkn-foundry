// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
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
	t.Log("real MariaDB: 101 interactions archived as 100+1, empty eligible count, oversized closure rejected before decoding and retained hot")
}
