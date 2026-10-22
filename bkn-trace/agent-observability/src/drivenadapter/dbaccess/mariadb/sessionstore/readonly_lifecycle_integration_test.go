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
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// A lifecycle writer may already own the conversation row. Polling its receipt
// or reading the interaction must not join that writer's exclusive lock queue.
func TestLifecycleReadsDoNotWaitForConversationWriter(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := sessionvo.Owner{ApplicationPrincipalID: "lock-regression-app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "lock-regression-user"}
	conversation, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{Owner: owner, ExternalConversationKey: fmt.Sprintf("lock-regression-%d", time.Now().UnixNano()), IdempotencyKey: "ensure"})
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start"})
	if err != nil {
		t.Fatal(err)
	}
	operation, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID, OperationKey: "query", ToolName: "query_object_instance", Protocol: sessionvo.OperationProtocol("mcp"), SourceModule: "context-loader",
		Input: mustInlinePayload(t, json.RawMessage(`{"query":"regression"}`)), Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	var locked string
	if err := writer.QueryRow("SELECT conversation_id FROM bkn_trace_conversations WHERE conversation_id=? FOR UPDATE", conversation.ID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	for _, read := range []struct {
		name string
		call func(context.Context) error
	}{
		{"interaction", func(ctx context.Context) error {
			_, err := service.GetInteraction(ctx, owner, interaction.ID)
			return err
		}},
		{"operation", func(ctx context.Context) error { _, err := service.GetOperation(ctx, owner, operation.ID); return err }},
		{"receipt", func(ctx context.Context) error { _, err := service.GetReceipt(ctx, owner, receipt.ID); return err }},
	} {
		t.Run(read.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			if err := read.call(ctx); err != nil {
				t.Fatalf("status read waited behind conversation writer: %v", err)
			}
		})
	}
}

type pausedCallFactsStore struct {
	isessionstore.Store
	entered chan struct{}
	release chan struct{}
}

func (s *pausedCallFactsStore) WithinTransaction(ctx context.Context, fn func(isessionstore.Transaction) error) error {
	return s.Store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		return fn(pausedCallFactsTransaction{Transaction: tx, store: s})
	})
}

type pausedCallFactsTransaction struct {
	isessionstore.Transaction
	store *pausedCallFactsStore
}

func (tx pausedCallFactsTransaction) ListOperationCallFacts(id string) []sessionvo.OperationCallFact {
	close(tx.store.entered)
	<-tx.store.release
	return tx.Transaction.ListOperationCallFacts(id)
}

// Pause a read after its ownership check, as a slow detail query would. A new
// business Operation must not spend its registration deadline behind that read.
func TestSlowTraceReadDoesNotBlockOperationRegistration(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := sessionvo.Owner{ApplicationPrincipalID: "read-block-app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "read-block-user"}
	conversation, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{Owner: owner, ExternalConversationKey: fmt.Sprintf("read-block-%d", time.Now().UnixNano()), IdempotencyKey: "ensure"})
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start"})
	if err != nil {
		t.Fatal(err)
	}
	paused := &pausedCallFactsStore{Store: store, entered: make(chan struct{}), release: make(chan struct{})}
	reader := sessionsvc.New(paused, sessionsvc.Options{})
	done := make(chan error, 1)
	go func() {
		_, err := reader.ListOperationCallFacts(context.Background(), owner, interaction.ID)
		done <- err
	}()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(paused.release) }) }
	defer release()
	select {
	case <-paused.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not reach detail query")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _, err = service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID, OperationKey: "new-business-call", ToolName: "query_object_instance", Protocol: sessionvo.OperationProtocol("mcp"), SourceModule: "context-loader",
		Input: mustInlinePayload(t, json.RawMessage(`{"query":"regression"}`)), Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	release()
	if readErr := <-done; readErr != nil {
		t.Errorf("trace detail read failed: %v", readErr)
	}
	if err != nil {
		t.Fatalf("read-only provenance query blocked business registration: %v", err)
	}
}
