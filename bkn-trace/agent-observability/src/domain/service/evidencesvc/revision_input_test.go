// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"bytes"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"testing"
)

func revisionInputFixture() sessionvo.EvidenceSnapshot {
	return sessionvo.EvidenceSnapshot{Interaction: sessionvo.EvidenceInteraction{ID: "i", ConversationID: "c"},
		CallFacts: []sessionvo.OperationCallFact{{InteractionID: "i", ConversationID: "c", OperationID: "o", Attempt: 2, Input: sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(` { "n":9007199254740993 } `)}}},
		Revisions: []sessionvo.AssemblyRevision{{ID: "r", InteractionID: "i", RevisionNo: 1, Completeness: sessionvo.EvidencePartial}}}
}
func TestRevisionInputRoundTripRetainsRawBytesAndPartial(t *testing.T) {
	s := revisionInputFixture()
	raw, hash, err := EncodeRevisionInput(s, 20, 100000)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRevisionInput(raw, hash, "i", "r", 20, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.CallFacts) != 1 || len(got.Revisions) != 1 {
		t.Fatal("missing snapshot")
	}
	if !bytes.Equal(got.CallFacts[0].Input.Inline, s.CallFacts[0].Input.Inline) || got.Revisions[0].Completeness != sessionvo.EvidencePartial || got.Ledger != nil {
		t.Fatal("state changed")
	}
	s.CallFacts[0].Input.Inline[1] = 'X'
	if bytes.Equal(s.CallFacts[0].Input.Inline, got.CallFacts[0].Input.Inline) {
		t.Fatal("aliased body")
	}
	if _, err = DecodeRevisionInput(raw, hash, "i", "other", 20, 100000); err == nil {
		t.Fatal("foreign revision accepted")
	}
	raw[len(raw)-1] = ' '
	if _, err = DecodeRevisionInput(raw, hash, "i", "r", 20, 100000); err == nil {
		t.Fatal("tampering accepted")
	}
}
func TestRevisionInputRejectsMixedScopesAndDuplicateAttempts(t *testing.T) {
	for _, change := range []func(*sessionvo.EvidenceSnapshot){
		func(s *sessionvo.EvidenceSnapshot) { s.CallFacts[0].InteractionID = "other" },
		func(s *sessionvo.EvidenceSnapshot) { s.CallFacts[0].ConversationID = "other" },
		func(s *sessionvo.EvidenceSnapshot) { s.Revisions[0].InteractionID = "other" },
		func(s *sessionvo.EvidenceSnapshot) { s.CallFacts = append(s.CallFacts, s.CallFacts[0]) },
		func(s *sessionvo.EvidenceSnapshot) { s.CallFacts[0].Attempt = 0 },
		func(s *sessionvo.EvidenceSnapshot) { s.Ledger = &sessionvo.EvidenceLedgerSnapshot{} },
		func(s *sessionvo.EvidenceSnapshot) { s.Revisions = nil },
	} {
		s := revisionInputFixture()
		change(&s)
		if raw, hash, err := EncodeRevisionInput(s, 20, 100000); err == nil || raw != nil || hash != "" {
			t.Fatal("invalid scope published")
		}
	}
}
func TestRevisionInputBudgetDoesNotSilentlyTruncate(t *testing.T) {
	s := revisionInputFixture()
	for _, b := range [][2]int{{0, 100000}, {1, 100000}, {20, 1}} {
		if raw, hash, err := EncodeRevisionInput(s, b[0], b[1]); err == nil || raw != nil || hash != "" {
			t.Fatal("budget ignored")
		}
	}
}

func TestRevisionInputKeepsMissingMembersWithoutInventingRecords(t *testing.T) {
	s := revisionInputFixture()
	s.Revisions[0].IncludedReceiptIDs = []string{"missing"}
	s.Revisions[0].IncludedEventIDs = []string{"unread"}
	raw, hash, err := EncodeRevisionInput(s, 20, 100000)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRevisionInput(raw, hash, "i", "r", 20, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Receipts) != 0 || got.Ledger != nil || got.Revisions[0].IncludedReceiptIDs[0] != "missing" {
		t.Fatal("missing evidence fabricated")
	}
	if _, err = DecodeRevisionInput(raw, hash, "i", "r", 1, 100000); err == nil {
		t.Fatal("decode budget ignored")
	}
}
func TestRevisionInputRejectsSupplementaryCaptureAndReceiptCollisions(t *testing.T) {
	s := revisionInputFixture()
	raw, hash, err := EncodeCapture(CaptureManifest{Snapshot: s, ReadAttempts: 1}, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeRevisionInput(raw, hash, "i", "r", 20, 100000); err == nil {
		t.Fatal("supplementary capture accepted")
	}
	s.Receipts = []sessionvo.Receipt{{ID: "a", OperationID: "o", Attempt: 2, InteractionID: "i", ConversationID: "c"}, {ID: "b", OperationID: "o", Attempt: 2, InteractionID: "i", ConversationID: "c"}}
	if _, _, err = EncodeRevisionInput(s, 20, 100000); err == nil {
		t.Fatal("ambiguous receipt attempt accepted")
	}
}
