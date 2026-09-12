// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func seedSnapshot(t *testing.T) *sessionstore.Store {
	t.Helper()
	s := sessionstore.New()
	err := s.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveInteraction(sessionvo.Interaction{ID: "int-a", ConversationID: "conv", LeaseToken: "must-not-escape",
			ExecutionStatus: sessionvo.InteractionAbandoned, EvidenceStatus: sessionvo.EvidenceComplete,
			ClosureManifest: &sessionvo.ClosureManifest{ExpectedReceipts: []sessionvo.ExpectedReceipt{{ReceiptID: "receipt-1"}}}})
		tx.SaveOperation(sessionvo.Operation{ID: "op-a", InteractionID: "int-a", ConversationID: "conv",
			Attempt: 2, AttemptStatus: sessionvo.AttemptReady, CausationEventIDs: []string{"event-1"}})
		for i, id := range []string{"receipt-1", "receipt-2"} {
			tx.SaveReceipt(sessionvo.Receipt{ID: id, InteractionID: "int-a", ConversationID: "conv", OperationID: "op-a",
				Attempt: uint32(i + 1), ArtifactRefs: []string{"artifact:original"}})
		}
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op-a", InteractionID: "int-a", ConversationID: "conv",
			Attempt: 1, ReceiptID: "receipt-1", Status: sessionvo.AttemptFailed,
			Input: sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`{ "id": 9007199254740993, "decimal": 1.2300 }`)},
			Error: &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`{"message":"failed"}`)}})
		tx.SaveAssemblyRevision(sessionvo.AssemblyRevision{ID: "rev", InteractionID: "int-a", IncludedReceiptIDs: []string{"receipt-1"}})
		tx.SaveInteraction(sessionvo.Interaction{ID: "int-b", ConversationID: "conv"})
		tx.SaveOperation(sessionvo.Operation{ID: "op-b", InteractionID: "int-b"})
		tx.SaveReceipt(sessionvo.Receipt{ID: "receipt-b", InteractionID: "int-b"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReadEvidenceSnapshotIncludesIndependentReceipts(t *testing.T) {
	s := seedSnapshot(t)
	got, found, err := s.ReadEvidenceSnapshot(context.Background(), "int-a")
	if err != nil || !found {
		t.Fatalf("read: found=%v err=%v", found, err)
	}
	if len(got.Operations) != 1 || len(got.Receipts) != 2 || len(got.CallFacts) != 1 || len(got.Revisions) != 1 {
		t.Fatalf("lost records or crossed scope: operations=%d receipts=%d facts=%d revisions=%d", len(got.Operations), len(got.Receipts), len(got.CallFacts), len(got.Revisions))
	}
	if got.Ledger != nil {
		t.Fatal("session-only memory store must not claim the separate ledger was read")
	}
	if got.Operations[0].Attempt != 2 || got.CallFacts[0].Attempt != 1 || got.CallFacts[0].Output != nil {
		t.Fatal("ready retry must not manufacture or overwrite a call fact")
	}
	if got.Interaction.ExecutionStatus != sessionvo.InteractionAbandoned || got.Interaction.EvidenceStatus != sessionvo.EvidenceComplete {
		t.Fatal("execution and evidence state must remain independent")
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("lease")) || bytes.Contains(raw, []byte("must-not-escape")) {
		t.Fatal("snapshot exposed lifecycle credentials")
	}
}

func TestReadEvidenceSnapshotDetachesMutableSourcesWithoutRenormalizingPayload(t *testing.T) {
	s := seedSnapshot(t)
	got, _, err := s.ReadEvidenceSnapshot(context.Background(), "int-a")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{ "id": 9007199254740993, "decimal": 1.2300 }`)
	if !bytes.Equal(got.CallFacts[0].Input.Inline, want) {
		t.Fatal("stored bytes were renormalized")
	}
	got.CallFacts[0].Input.Inline[0] = '!'
	got.CallFacts[0].Error.Inline[0] = '!'
	got.Operations[0].CausationEventIDs[0] = "changed"
	got.Receipts[0].ArtifactRefs[0] = "changed"
	got.Revisions[0].IncludedReceiptIDs[0] = "changed"
	got.Interaction.ClosureManifest.ExpectedReceipts[0].ReceiptID = "changed"
	again, _, err := s.ReadEvidenceSnapshot(context.Background(), "int-a")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again.CallFacts[0].Input.Inline, want) || again.CallFacts[0].Error.Inline[0] != '{' ||
		again.Operations[0].CausationEventIDs[0] != "event-1" || again.Receipts[0].ArtifactRefs[0] != "artifact:original" ||
		again.Revisions[0].IncludedReceiptIDs[0] != "receipt-1" || again.Interaction.ClosureManifest.ExpectedReceipts[0].ReceiptID != "receipt-1" {
		t.Fatal("capture mutation reached stored data")
	}
	if err := s.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		r, _ := tx.FindReceipt("receipt-2")
		r.ArtifactRefs[0] = "artifact:late"
		tx.SaveReceipt(r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if again.Receipts[1].ArtifactRefs[0] != "artifact:original" {
		t.Fatal("late receipt changed earlier capture")
	}
}

func TestReadEvidenceSnapshotPreservesUnknownAttempt(t *testing.T) {
	s := seedSnapshot(t)
	if err := s.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "legacy", InteractionID: "int-b"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.ReadEvidenceSnapshot(context.Background(), "int-b")
	if err != nil || !found || len(got.CallFacts) != 1 || got.CallFacts[0].Attempt != 0 {
		t.Fatal("unknown attempt synthesized or lost")
	}
}

func TestReadEvidenceSnapshotMissingAndCanceled(t *testing.T) {
	s := seedSnapshot(t)
	got, found, err := s.ReadEvidenceSnapshot(context.Background(), "absent")
	if err != nil || found || got.Interaction.ID != "" {
		t.Fatal("missing record synthesized")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, found, err = s.ReadEvidenceSnapshot(ctx, "int-a")
	if found || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: found=%v err=%v", found, err)
	}
}

func TestReadEvidenceSnapshotKeepsUnavailablePayloadBoundaries(t *testing.T) {
	s := seedSnapshot(t)
	if err := s.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "large", InteractionID: "int-b", Attempt: 3,
			Input:  sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadOmitted, OmittedReason: sessionvo.PayloadOmittedReasonTooLarge, ByteLength: 2000000},
			Output: &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:external", ByteLength: 3000000}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.ReadEvidenceSnapshot(context.Background(), "int-b")
	if err != nil || !found {
		t.Fatalf("read: found=%v err=%v", found, err)
	}
	fact := got.CallFacts[0]
	if fact.Input.Mode != sessionvo.PayloadOmitted || fact.Input.Inline != nil || fact.Input.ByteLength != 2000000 ||
		fact.Output.Mode != sessionvo.PayloadReferenced || fact.Output.Ref != "artifact:external" || fact.Output.Inline != nil {
		t.Fatal("missing payload was reconstructed or an artifact reference was silently resolved")
	}
}
