// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	memorysessionstore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestMemoryRecordIntegrityObserverInvalidatesOnlyEventOwnerAndReconciles(t *testing.T) {
	store := memorysessionstore.New()
	owner := sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectService, EffectiveSubjectID: "agent"}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner})
		tx.SaveInteraction(sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	port := any(store).(isessionstore.RecordIntegrityStore)
	if ok, err := port.SaveStoredRecordIntegrity(context.Background(), "int", sessionvo.StoredRecordIntegrity{
		SourceVersion: 1, Applicable: true,
		Report: &sessionvo.RecordIntegrity{Status: "complete", Scope: "registered_call_records", CheckedAt: time.Now().UTC()},
	}); err != nil || !ok {
		t.Fatalf("seed integrity metadata: ok=%v err=%v", ok, err)
	}

	var reconciled int
	reconcile := func(context.Context, ledgervo.Event) error { reconciled++; return nil }
	observer := memoryRecordIntegrityObserver(store, reconcile)
	event := ledgervo.Event{InteractionID: "int", Owner: owner}
	if err := observer(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if reconciled != 1 {
		t.Fatalf("reconcile calls=%d, want 1", reconciled)
	}
	var value sessionvo.Interaction
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		value, _ = tx.PeekInteraction("int")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if value.IntegritySourceVersion != 2 || value.StoredRecordIntegrity != nil {
		t.Fatalf("matching owner did not invalidate derived metadata: %+v", value)
	}

	// A foreign event owner must not invalidate the cached report, while the
	// existing durable observer still receives the event.
	if ok, err := port.SaveStoredRecordIntegrity(context.Background(), "int", sessionvo.StoredRecordIntegrity{
		SourceVersion: 2, Applicable: true,
		Report: &sessionvo.RecordIntegrity{Status: "complete", Scope: "registered_call_records", CheckedAt: time.Now().UTC()},
	}); err != nil || !ok {
		t.Fatalf("reseed integrity metadata: ok=%v err=%v", ok, err)
	}
	foreign := event
	foreign.Owner = sessionvo.Owner{ApplicationPrincipalID: "other", EffectiveSubjectType: owner.EffectiveSubjectType, EffectiveSubjectID: owner.EffectiveSubjectID}
	if err := observer(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}
	if reconciled != 2 {
		t.Fatalf("foreign event skipped reconcile: calls=%d", reconciled)
	}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		value, _ = tx.PeekInteraction("int")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if value.IntegritySourceVersion != 2 || value.StoredRecordIntegrity == nil {
		t.Fatalf("foreign owner invalidated metadata: %+v", value)
	}
}

func TestRecordIntegrityObserverLeavesNonMemoryStoreObserverUntouched(t *testing.T) {
	calls := 0
	reconcile := func(context.Context, ledgervo.Event) error { calls++; return nil }
	observer := memoryRecordIntegrityObserver(nil, reconcile)
	if err := observer(context.Background(), ledgervo.Event{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("non-memory observer calls=%d, want 1", calls)
	}
}
