// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// Use the same fresh, loopback-only random database fixture as the BOM Claim
// tests. This verifies the persisted integrity worker verdict, not just its pure
// evaluator, and deliberately keeps malformed historical declarations visible.
func TestBOMRecordIntegrityMaterializationMariaDB(t *testing.T) {
	db := durableAnswerDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := New(db)
	ledger := ledgersvc.New(store)
	service := evidencesvc.New(evidencestore.New(), evidencesvc.WithSessionStore(store), evidencesvc.WithCurrentRecordIntegrity())
	for _, test := range []struct{ id, payload, want string }{
		{"complete", `{"result_contract":"bom.structure_intersection/v1","complete":true}`, "complete"},
		{"historical_missing", `{"result_contract":"bom.structure_intersection/v1"}`, "missing"},
		{"false", `{"result_contract":"bom.structure_intersection/v1","complete":false}`, "missing"},
		{"malformed", `{"result_contract":"bom.structure_intersection/v1","complete":"true"}`, "missing"},
	} {
		t.Run(test.id, func(t *testing.T) {
			now := time.Now().UTC()
			owner := sessionvo.Owner{ApplicationPrincipalID: "bom-integrity-app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "bom-owner"}
			conv, iid, op, receiptID, eventID := "conv-"+test.id, "int-"+test.id, "op-"+test.id, "receipt-"+test.id, "event-"+test.id
			input, err := sessionvo.InlineJSONPayload(json.RawMessage(`{"kn_id":"supply","toolbox_id":"box","tool_id":"bom_shared_list"}`))
			if err != nil {
				t.Fatal(err)
			}
			output, err := sessionvo.InlineJSONPayload(json.RawMessage(`{"shared_codes":[],"shared_count":0}`))
			if err != nil {
				t.Fatal(err)
			}
			ref := sessionvo.BusinessRef{RefType: sessionvo.BusinessRefFunction, RefID: "function:supply:bom_shared_list", Version: "approved-version"}
			err = store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
				tx.SaveConversation(sessionvo.Conversation{ID: conv, Owner: owner, ExternalConversationKey: conv, Generation: 1, Status: sessionvo.ConversationActive, RowVersion: 1, CreatedAt: now, UpdatedAt: now})
				tx.SaveInteraction(sessionvo.Interaction{ID: iid, ConversationID: conv, Ordinal: 1, StartIdempotencyKey: iid, ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete, LeaseToken: iid, LeaseEpoch: 1, LeaseExpiresAt: now.Add(time.Minute), RowVersion: 1, CreatedAt: now, UpdatedAt: now, TerminalAt: &now})
				tx.SaveOperation(sessionvo.Operation{ID: op, ConversationID: conv, InteractionID: iid, OperationKey: op, ToolName: "bom_shared_list", Attempt: 1, AttemptStatus: sessionvo.AttemptCompleted, RowVersion: 1, CreatedAt: now, UpdatedAt: now})
				tx.SaveReceipt(sessionvo.Receipt{ID: receiptID, SchemaVersion: "3.0.0", Owner: owner, ConversationID: conv, InteractionID: iid, OperationID: op, Attempt: 1, OperationKey: op, ToolName: "bom_shared_list", Required: true, Status: sessionvo.ReceiptCompleted, EvidenceDurability: sessionvo.DurabilityDurable, RequestID: "req", TraceID: "trace", BusinessRefs: []sessionvo.BusinessRef{ref}, ObservedEvidenceRefs: []string{eventID}, RowVersion: 1, IssuedAt: now, TerminalAt: &now})
				tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: op, Attempt: 1, ConversationID: conv, InteractionID: iid, ReceiptID: receiptID, ToolName: "bom_shared_list", Protocol: sessionvo.ProtocolInternal, SourceModule: "context-loader-bom-intersection/v1", CapabilityProfile: &sessionvo.CapabilityProfile{Resolution: "matched", EvidenceContract: "bom.structure_intersection/v1", RequiredTraceFields: []string{"business_refs", "result_completeness"}}, Input: input, Output: &output, RequestID: "req", TraceID: "trace", StartedAt: now, FinishedAt: &now, Status: sessionvo.AttemptCompleted})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			event := ledgervo.Event{EventID: eventID, EventType: "logic.execution.observed", SchemaVersion: "3.0.0", Owner: owner, ConversationID: conv, InteractionID: iid, OperationID: op, Attempt: 1, RequestID: "req", TraceID: "trace", ProducerID: "agent-retrieval", ProducerStreamID: "bom-integrity-" + test.id, ProducerEpoch: 1, ProducerSequence: 1, StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: json.RawMessage(`{"payload":` + test.payload + `}`), BusinessRefs: []sessionvo.BusinessRef{ref}}
			event.PayloadHash = ledgervo.CanonicalPayloadHash(event.Envelope)
			if _, err := ledger.Ingest(ctx, event); err != nil {
				t.Fatal(err)
			}
			if _, err := service.PersistRecordIntegrityBatch(ctx, "", 100); err != nil {
				t.Fatalf("valid/explicitly-missing BOM declaration caused inspection failure: %v", err)
			}
			var interaction sessionvo.Interaction
			if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { interaction, _ = tx.PeekInteraction(iid); return nil }); err != nil {
				t.Fatal(err)
			}
			stored := interaction.StoredRecordIntegrity
			if stored == nil || stored.SourceVersion != interaction.IntegritySourceVersion || stored.Report == nil || stored.Report.Status != test.want {
				t.Fatalf("stored verdict=%+v interaction=%+v", stored, interaction)
			}
			retry, found, err := store.GetRecordIntegrityRetry(ctx, iid, interaction.IntegritySourceVersion)
			if err != nil || !found || retry.Status != isessionstore.RecordIntegritySucceeded || retry.LastFailureReason != "" {
				t.Fatalf("worker did not persist verdict: retry=%+v found=%v err=%v", retry, found, err)
			}
			if test.want == "missing" {
				matched := false
				for _, gap := range stored.Report.Missing {
					matched = matched || gap.Field == "evidence.result_completeness"
				}
				if !matched {
					t.Fatalf("missing declaration not explained: %+v", stored.Report)
				}
			}
		})
	}
}
