// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"strings"
)

// EncodeRevisionInput reuses the fixed capture encoding for a caller-owned
// transaction snapshot. It does not open a transaction or certify that inputs
// were observed at revision creation. No lifecycle credentials exist in the
// EvidenceInteraction type. Ledger and external bodies belong to later capture.
func EncodeRevisionInput(s sessionvo.EvidenceSnapshot, maxRecords, maxBytes int) ([]byte, string, error) {
	if err := validateRevisionInput(s, maxRecords); err != nil {
		return nil, "", err
	}
	return EncodeCapture(CaptureManifest{Snapshot: s}, maxBytes)
}

// DecodeRevisionInput requires independently trusted identity and hash. Missing
// declared members remain missing; this codec does not recompute completeness.
func DecodeRevisionInput(raw []byte, hash, interactionID, revisionID string, maxRecords, maxBytes int) (sessionvo.EvidenceSnapshot, error) {
	m, err := DecodeCapture(raw, hash, maxBytes)
	if err != nil {
		return sessionvo.EvidenceSnapshot{}, err
	}
	if len(m.Artifacts) != 0 || len(m.PartialReasons) != 0 || m.ReadAttempts != 0 || m.ReadBytes != 0 || m.CapturedBytes != 0 {
		return sessionvo.EvidenceSnapshot{}, errors.New("supplementary capture is not revision input")
	}
	if err = validateRevisionInput(m.Snapshot, maxRecords); err != nil {
		return sessionvo.EvidenceSnapshot{}, err
	}
	if m.Snapshot.Interaction.ID != interactionID || m.Snapshot.Revisions[0].ID != revisionID {
		return sessionvo.EvidenceSnapshot{}, errors.New("revision input identity mismatch")
	}
	return m.Snapshot, nil
}

func validateRevisionInput(s sessionvo.EvidenceSnapshot, maxRecords int) error {
	if maxRecords <= 0 || strings.TrimSpace(s.Interaction.ID) == "" || strings.TrimSpace(s.Interaction.ConversationID) == "" || len(s.Revisions) != 1 || s.Ledger != nil {
		return errors.New("invalid revision input scope")
	}
	r := s.Revisions[0]
	if strings.TrimSpace(r.ID) == "" || r.RevisionNo == 0 || r.InteractionID != s.Interaction.ID || r.ParentRevisionID == r.ID {
		return errors.New("invalid revision identity")
	}
	// Include reference lists in the budget, not just top-level records.
	remaining := maxRecords
	take := func(n int) bool {
		if n > remaining {
			return false
		}
		remaining -= n
		return true
	}
	if !take(2) || !take(len(s.Operations)) || !take(len(s.Receipts)) || !take(len(s.CallFacts)) || !take(len(r.IncludedReceiptIDs)) || !take(len(r.IncludedEventIDs)) {
		return errors.New("revision input record budget")
	}
	if m := s.Interaction.ClosureManifest; m != nil {
		if !take(len(m.Claims)) || !take(len(m.ExpectedOperations)) || !take(len(m.ExpectedReceipts)) {
			return errors.New("revision input reference budget")
		}
	}
	scope := func(i, c string) bool { return i == s.Interaction.ID && c == s.Interaction.ConversationID }
	ops := map[string]bool{}
	receipts := map[string]bool{}
	type attemptKey struct {
		id      string
		attempt uint32
	}
	receiptAttempts := map[attemptKey]bool{}
	facts := map[attemptKey]bool{}
	for _, o := range s.Operations {
		if !scope(o.InteractionID, o.ConversationID) || strings.TrimSpace(o.ID) == "" || ops[o.ID] || o.Attempt == 0 {
			return errors.New("invalid operation identity")
		}
		ops[o.ID] = true
	}
	for _, r := range s.Receipts {
		key := attemptKey{r.OperationID, r.Attempt}
		if !scope(r.InteractionID, r.ConversationID) || strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.OperationID) == "" || r.Attempt == 0 || receipts[r.ID] || receiptAttempts[key] {
			return errors.New("invalid receipt identity")
		}
		receipts[r.ID] = true
		receiptAttempts[key] = true
	}
	for _, f := range s.CallFacts {
		key := attemptKey{f.OperationID, f.Attempt}
		if !scope(f.InteractionID, f.ConversationID) || strings.TrimSpace(f.OperationID) == "" || f.Attempt == 0 || facts[key] {
			return errors.New("invalid call fact identity")
		}
		facts[key] = true
	}
	return nil
}
