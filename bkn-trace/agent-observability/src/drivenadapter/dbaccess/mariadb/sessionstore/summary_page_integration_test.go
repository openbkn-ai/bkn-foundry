// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// Uses the ordinary CI DSN, not the opt-in capacity workload or a latency gate.
func TestServiceTraceIdentityWindowPreservesTotalAcrossPages(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	store := New(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("window-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		for _, table := range []string{"bkn_trace_receipts", "bkn_trace_operation_call_facts", "bkn_trace_operations", "bkn_trace_interactions", "bkn_trace_conversations"} {
			if _, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE conversation_id=?", prefix); err != nil {
				t.Errorf("clean fixture %s: %v", table, err)
			}
		}
	})
	owner := sessionvo.Owner{EffectiveSubjectType: sessionvo.SubjectService, EffectiveSubjectID: prefix}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: prefix, Owner: owner, ExternalConversationKey: prefix, Generation: 1, Status: sessionvo.ConversationClosed, RowVersion: 1, CreatedAt: now, UpdatedAt: now})
		for i := 0; i < 5; i++ {
			id := fmt.Sprintf("%s-%d", prefix, i)
			tx.SaveInteraction(sessionvo.Interaction{ID: id, ConversationID: prefix, Ordinal: uint64(i + 1), ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete, StartIdempotencyKey: id, LeaseToken: id, LeaseExpiresAt: now.Add(time.Hour), RowVersion: 1, CreatedAt: now, UpdatedAt: now})
			module := "context-loader"
			if i == 3 {
				module = "other"
			}
			for j, source := range []string{"", module, "descendant"} {
				op := fmt.Sprintf("%s-op-%d", id, j)
				tx.SaveOperation(sessionvo.Operation{ID: op, ConversationID: prefix, InteractionID: id, OperationKey: op, ToolName: "run_sql", AttemptStatus: sessionvo.AttemptCompleted, RowVersion: 1, CreatedAt: now, UpdatedAt: now})
				tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: op, Attempt: 1, ConversationID: prefix, InteractionID: id, ToolName: "run_sql", Protocol: sessionvo.ProtocolMCP, SourceModule: source, TraceID: id, RequestID: id, StartedAt: now, Status: sessionvo.AttemptCompleted})
			}
			rowOwner := owner
			if i == 4 {
				rowOwner.EffectiveSubjectID = prefix + "-foreign"
			}
			// Duplicate receipts must still contribute one identity to the window total.
			attempts := 1
			if i == 0 {
				attempts = 2
			}
			for attempt := 0; attempt < attempts; attempt++ {
				tx.SaveReceipt(sessionvo.Receipt{ID: fmt.Sprintf("%s-r-%d", id, attempt), SchemaVersion: "1", Attempt: uint32(attempt + 1), Owner: rowOwner, ConversationID: prefix, InteractionID: id, OperationID: id + "-op-1", OperationKey: id + "-op-1", ToolName: "run_sql", Status: sessionvo.ReceiptCompleted, EvidenceDurability: sessionvo.DurabilityDurable, RequestID: id, TraceID: id, RowVersion: 1, IssuedAt: now})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{AccountID: prefix, AccountType: "service"}, Service: "context-loader", Limit: 1}
	compare := func(q isessionstore.SummaryPageQuery) isessionstore.SummaryIdentityPage {
		t.Helper()
		want, err := capacityLegacyTraceIdentities(ctx, store, q)
		if err != nil {
			t.Fatal(err)
		}
		got, err := store.ListTraceSummaryIdentities(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("query=%+v got=%+v legacy=%+v", q, got, want)
		}
		return got
	}
	q := base
	for i := 0; i < 3; i++ {
		page := compare(q)
		if page.Total != 3 || len(page.Entries) != 1 || page.HasMore != (i < 2) {
			t.Fatalf("page%d: %+v", i, page)
		}
		if page.Entries[0].ID != fmt.Sprintf("%s-%d", prefix, i) {
			t.Fatalf("tied timestamp ordering: %+v", page)
		}
		q.AfterStartedAt = page.Entries[0].StartedAt
		q.AfterID = page.Entries[0].ID
	}
	// An exhausted cursor and offsets must preserve the full pre-pagination total.
	emptyQueries := []isessionstore.SummaryPageQuery{q}
	for _, offset := range []int{3, 100} {
		query := base
		query.Offset = offset
		emptyQueries = append(emptyQueries, query)
	}
	for _, query := range emptyQueries {
		page := compare(query)
		if page.Total != 3 || len(page.Entries) != 0 || page.HasMore {
			t.Fatalf("empty page: %+v", page)
		}
	}
	q = base
	q.Service = "missing"
	if page := compare(q); page.Total != 0 || len(page.Entries) != 0 || page.HasMore {
		t.Fatalf("no matches: %+v", page)
	}
}
