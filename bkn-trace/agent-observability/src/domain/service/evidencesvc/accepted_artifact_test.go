// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
)

func TestAcceptedAnswerWriterPreservesTraceOwnershipAndRoleChecks(t *testing.T) {
	for _, kind := range []string{"owner", "role"} {
		t.Run(kind, func(t *testing.T) {
			store := evidencestore.New()
			requestID := "req_artifact_service"
			if kind == "owner" {
				requestID = "req_other_request"
			}
			trace := evidencevo.NormalizedTrace{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", RequestID: requestID, AccountID: "acct_demo", AccountType: "app", SchemaVersion: evidencevo.ArtifactContractVersion, Events: []evidencevo.EvidenceEvent{{EventID: "evt_question", EventType: "agent.interaction.started", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", RequestID: requestID, Payload: map[string]any{"question_artifact_ref": "artifact:artifact_role"}}}}
			if err := store.StoreEvidence(context.Background(), evidencevo.WithEvents(trace, trace.Events)); err != nil {
				t.Fatal(err)
			}
			var artifact evidencevo.EvidenceArtifact
			if err := json.Unmarshal(validArtifactBody(t, "artifact_role", map[string]any{"answer": "done"}), &artifact); err != nil {
				t.Fatal(err)
			}
			artifact.ArtifactType = evidencevo.ArtifactTypeResult
			if _, err := NewWithArtifactStore(store, store).AcceptedArtifactWriter().StoreArtifact(context.Background(), artifact); !errors.Is(err, iartifactstore.ErrInvalidAcceptedArtifact) {
				t.Fatalf("accepted work bypassed %s check: %v", kind, err)
			}
		})
	}
}
