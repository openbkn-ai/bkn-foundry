// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func integrityMemoryPort(t *testing.T, store *Store) isessionstore.RecordIntegrityStore {
	t.Helper()
	port, ok := any(store).(isessionstore.RecordIntegrityStore)
	if !ok {
		t.Fatal("memory store lacks record integrity capability")
	}
	return port
}

func memoryIntegrityReport(version uint64) sessionvo.StoredRecordIntegrity {
	return sessionvo.StoredRecordIntegrity{SourceVersion: version, Applicable: true, Report: &sessionvo.RecordIntegrity{Status: "complete", CheckedAt: time.Now().UTC(), Scope: "registered_call_records", Missing: []sessionvo.MissingRecord{}}}
}

func TestMemoryRecordIntegrityCASAndDetachedReads(t *testing.T) {
	store := New()
	port := integrityMemoryPort(t, store)
	store.interactions["int"] = sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, RowVersion: 8, IntegritySourceVersion: 3}
	if ok, err := port.SaveStoredRecordIntegrity(context.Background(), "int", memoryIntegrityReport(2)); err != nil || ok {
		t.Fatalf("stale CAS accepted: %v %v", ok, err)
	}
	report := memoryIntegrityReport(3)
	report.Report.Missing = []sessionvo.MissingRecord{{Reason: "original"}}
	if ok, err := port.SaveStoredRecordIntegrity(context.Background(), "int", report); err != nil || !ok {
		t.Fatalf("current CAS failed: %v %v", ok, err)
	}
	report.Report.Missing[0].Reason = "caller mutation"
	if ok, err := port.SaveStoredRecordIntegrity(context.Background(), "int", memoryIntegrityReport(3)); err != nil || ok {
		t.Fatalf("same-version second writer accepted: %v %v", ok, err)
	}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		value, _ := tx.PeekInteraction("int")
		if value.RowVersion != 8 || value.IntegritySourceVersion != 3 || value.StoredRecordIntegrity.Report.Missing[0].Reason != "original" {
			t.Fatalf("CAS changed source/lifecycle or shares input: %#v", value)
		}
		value.StoredRecordIntegrity.Report.Missing[0].Reason = "read mutation"
		value, _ = tx.FindInteraction("int")
		if value.StoredRecordIntegrity.Report.Missing[0].Reason != "original" {
			t.Fatal("read aliases stored report")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryRecordIntegrityTransactionMergesDirtyIDs(t *testing.T) {
	store := New()
	store.interactions["int"] = sessionvo.Interaction{ID: "int", ExecutionStatus: sessionvo.InteractionCompleted, RowVersion: 8, IntegritySourceVersion: 3, StoredRecordIntegrity: ptrIntegrity(memoryIntegrityReport(3))}
	store.interactions["active"] = sessionvo.Interaction{ID: "active", ExecutionStatus: sessionvo.InteractionActive}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		// A stale ordinary lifecycle object must not reset independent metadata.
		tx.SaveInteraction(sessionvo.Interaction{ID: "int", ExecutionStatus: sessionvo.InteractionCompleted, RowVersion: 9})
		inside, _ := tx.PeekInteraction("int")
		if inside.IntegritySourceVersion != 3 || inside.StoredRecordIntegrity == nil {
			t.Fatal("ordinary save erased internal metadata before commit")
		}
		tx.SaveReceipt(sessionvo.Receipt{ID: "r", InteractionID: "int"})
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op", Attempt: 1, InteractionID: "int"})
		tx.SaveReceipt(sessionvo.Receipt{ID: "a", InteractionID: "active"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	value := store.interactions["int"]
	if value.IntegritySourceVersion != 4 || value.StoredRecordIntegrity != nil || value.RowVersion != 9 {
		t.Fatalf("dirty IDs not merged or business version changed: %#v", value)
	}
	if store.interactions["active"].IntegritySourceVersion != 0 {
		t.Fatal("active row was needlessly invalidated")
	}
}

func TestMemoryRecordIntegrityReceiptAndCallChangesInvalidate(t *testing.T) {
	for _, mutate := range []func(isessionstore.Transaction){
		func(tx isessionstore.Transaction) { tx.SaveReceipt(sessionvo.Receipt{ID: "r", InteractionID: "int"}) },
		func(tx isessionstore.Transaction) {
			tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op", Attempt: 1, InteractionID: "int"})
		},
	} {
		store := New()
		store.interactions["int"] = sessionvo.Interaction{ID: "int", ExecutionStatus: sessionvo.InteractionCompleted, RowVersion: 8, IntegritySourceVersion: 3, StoredRecordIntegrity: ptrIntegrity(memoryIntegrityReport(3))}
		if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error { mutate(tx); return nil }); err != nil {
			t.Fatal(err)
		}
		value := store.interactions["int"]
		if value.IntegritySourceVersion != 4 || value.StoredRecordIntegrity != nil || value.RowVersion != 8 {
			t.Fatalf("receipt/call mutation retained stale integrity: %#v", value)
		}
	}
}

func TestMemoryRecordIntegrityOwnerScopedInvalidation(t *testing.T) {
	store := New()
	port := integrityMemoryPort(t, store)
	owner := sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "user", DelegationID: "delegation"}
	store.conversations["conv"] = sessionvo.Conversation{ID: "conv", Owner: owner}
	store.interactions["int"] = sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, RowVersion: 8, IntegritySourceVersion: 3, StoredRecordIntegrity: ptrIntegrity(memoryIntegrityReport(3))}
	for _, wrong := range []sessionvo.Owner{
		{ApplicationPrincipalID: "other", EffectiveSubjectType: owner.EffectiveSubjectType, EffectiveSubjectID: owner.EffectiveSubjectID, DelegationID: owner.DelegationID},
		{ApplicationPrincipalID: owner.ApplicationPrincipalID, EffectiveSubjectType: "other", EffectiveSubjectID: owner.EffectiveSubjectID, DelegationID: owner.DelegationID},
		{ApplicationPrincipalID: owner.ApplicationPrincipalID, EffectiveSubjectType: owner.EffectiveSubjectType, EffectiveSubjectID: "other", DelegationID: owner.DelegationID},
		{ApplicationPrincipalID: owner.ApplicationPrincipalID, EffectiveSubjectType: owner.EffectiveSubjectType, EffectiveSubjectID: owner.EffectiveSubjectID, DelegationID: "other"},
	} {
		if err := port.InvalidateRecordIntegrity(context.Background(), "int", wrong); err != nil {
			t.Fatal(err)
		}
		if store.interactions["int"].IntegritySourceVersion != 3 {
			t.Fatal("foreign owner invalidated summary")
		}
	}
	if err := port.InvalidateRecordIntegrity(context.Background(), "int", owner); err != nil {
		t.Fatal(err)
	}
	if value := store.interactions["int"]; value.IntegritySourceVersion != 4 || value.StoredRecordIntegrity != nil || value.RowVersion != 8 {
		t.Fatalf("authorized invalidation failed: %#v", value)
	}
}

func TestMemoryRecordIntegrityCandidatesUseKeysetAndSaveNonApplicable(t *testing.T) {
	store := New()
	port := integrityMemoryPort(t, store)
	for _, id := range []string{"a", "b", "c"} {
		store.interactions[id] = sessionvo.Interaction{ID: id, ExecutionStatus: sessionvo.InteractionCompleted, IntegritySourceVersion: 3}
	}
	store.interactions["d"] = sessionvo.Interaction{ID: "d", ExecutionStatus: sessionvo.InteractionActive}
	first, err := port.ListRecordIntegrityCandidates(context.Background(), "", 2)
	if err != nil || len(first) != 2 || first[0].ID != "a" || first[1].ID != "b" {
		t.Fatalf("unstable first page: %#v %v", first, err)
	}
	for _, entry := range first {
		if ok, err := port.SaveStoredRecordIntegrity(context.Background(), entry.ID, sessionvo.StoredRecordIntegrity{SourceVersion: entry.IntegritySourceVersion, Applicable: false}); err != nil || !ok {
			t.Fatalf("non-applicable CAS failed: %v %v", ok, err)
		}
	}
	second, err := port.ListRecordIntegrityCandidates(context.Background(), first[1].ID, 2)
	if err != nil || len(second) != 1 || second[0].ID != "c" {
		t.Fatalf("shrinking candidates skipped entries: %#v %v", second, err)
	}
}

func TestMemoryRecordIntegrityLimitCancellationAndMissingRows(t *testing.T) {
	store := New()
	port := integrityMemoryPort(t, store)
	large := memoryIntegrityReport(0)
	large.Report.Scope = strings.Repeat("x", isessionstore.MaxStoredRecordIntegrityBytes)
	if _, err := port.SaveStoredRecordIntegrity(context.Background(), "none", large); !errors.Is(err, isessionstore.ErrRecordIntegrityLimit) {
		t.Fatalf("unbounded summary accepted: %v", err)
	}
	if ok, err := port.SaveStoredRecordIntegrity(context.Background(), "none", memoryIntegrityReport(0)); err != nil || ok {
		t.Fatalf("missing row upserted: %v %v", ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := port.SaveStoredRecordIntegrity(ctx, "none", memoryIntegrityReport(0)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func ptrIntegrity(value sessionvo.StoredRecordIntegrity) *sessionvo.StoredRecordIntegrity {
	return &value
}
