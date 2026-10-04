// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ledgerstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestEvidenceConfirmationBudgetCountsStoredOuterEnvelope(t *testing.T) {
	for _, delta := range []int{-1, 0, 1} {
		t.Run(map[int]string{-1: "below", 0: "exact", 1: "above"}[delta], func(t *testing.T) {
			store := ledgerstore.New()
			event := ledgerEvent("outer-budget", sessionvo.Owner{ApplicationPrincipalID: "app"}, "int", 1)
			// Size the full persisted document, including commit's causality status.
			event.CausalityStatus = "complete"
			base, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			target := sessionvo.MaxEvidenceConfirmationBytes + delta
			event.ProducerID += strings.Repeat("x", target-len(base))
			if _, err := store.Commit(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			stored, err := store.ListInteractionEvents(context.Background(), event.Owner, event.InteractionID)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(stored[0])
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) != target || len(event.Envelope) >= sessionvo.MaxEvidenceConfirmationBytes {
				t.Fatalf("fixture bytes=%d target=%d", len(raw), target)
			}
			events, err := store.ReadEvidenceEventsByIDs([]string{event.EventID})
			if delta > 0 {
				if !errors.Is(err, isessionstore.ErrEvidenceConfirmationLimit) {
					t.Fatalf("outer stored bytes bypassed budget: %v", err)
				}
			} else if err != nil || len(events) != 1 {
				t.Fatalf("within budget rejected: count=%d err=%v", len(events), err)
			}
		})
	}
}

func TestInvalidStoredEnvelopeDoesNotCommitMemoryLedger(t *testing.T) {
	store := ledgerstore.New()
	event := ledgerEvent("invalid-json", sessionvo.Owner{ApplicationPrincipalID: "app"}, "int", 1)
	event.Envelope = json.RawMessage(`{"invalid":`)
	if _, err := store.Commit(context.Background(), event); err == nil {
		t.Fatal("invalid stored JSON accepted")
	}
	stored, err := store.ListInteractionEvents(context.Background(), event.Owner, event.InteractionID)
	if err != nil || len(stored) != 0 {
		t.Fatalf("failed JSON commit wrote ledger: %#v %v", stored, err)
	}
	valid := ledgerEvent("valid-json", event.Owner, event.InteractionID, 1)
	ack, err := store.Commit(context.Background(), valid)
	if err != nil || ack.IngestSequence != 1 {
		t.Fatalf("failed JSON commit advanced sequence: %#v %v", ack, err)
	}
}
