// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ledgerstore_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
)

func TestListInteractionEventsReturnsOnlyAuthorizedInteractionInIngestOrder(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	owner := sessionvo.Owner{ApplicationPrincipalID: "app-1"}
	for _, event := range []ledgervo.Event{
		ledgerEvent("evt-2", owner, "int-1", 2),
		ledgerEvent("evt-other", owner, "int-2", 1),
		ledgerEvent("evt-1", owner, "int-1", 1),
	} {
		if _, err := store.Commit(context.Background(), event); err != nil {
			t.Fatalf("commit event %s: %v", event.EventID, err)
		}
	}

	events, err := store.ListInteractionEvents(context.Background(), owner, "int-1")
	if err != nil {
		t.Fatalf("list interaction events: %v", err)
	}
	if len(events) != 2 || events[0].EventID != "evt-2" || events[1].EventID != "evt-1" {
		t.Fatalf("events must follow durable ingest order without leaking another interaction: %#v", events)
	}

	otherOwner := owner
	otherOwner.ApplicationPrincipalID = "app-2"
	events, err = store.ListInteractionEvents(context.Background(), otherOwner, "int-1")
	if err != nil || len(events) != 0 {
		t.Fatalf("owner scope leaked evidence: %#v, %v", events, err)
	}
}

func ledgerEvent(id string, owner sessionvo.Owner, interactionID string, sequence uint64) ledgervo.Event {
	envelope := json.RawMessage(`{"ok":true}`)
	now := time.Date(2026, 7, 31, 8, 0, int(sequence), 0, time.UTC)
	return ledgervo.Event{
		EventID: id, EventType: "operation.output.observed", SchemaVersion: "3.0.0",
		PayloadHash: ledgervo.CanonicalPayloadHash(envelope), Owner: owner,
		ConversationID: "conv-1", InteractionID: interactionID,
		ProducerID: "test", ProducerStreamID: id, ProducerEpoch: 1,
		ProducerSequence: sequence, StartedAt: now, ObservedAt: now, EmittedAt: now,
		Envelope: envelope,
	}
}
