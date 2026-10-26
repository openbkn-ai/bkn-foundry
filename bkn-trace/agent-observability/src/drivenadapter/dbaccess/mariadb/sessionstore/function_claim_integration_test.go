// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/assemblysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

// The fixture carries an unchanged sample result through the same generic
// producer protocol. Core checks bindings without interpreting business content.
func TestFunctionResultClaimLifecycleMariaDB(t *testing.T) {
	for _, mode := range []string{"ready", "late", "lost", "failed-then-success"} {
		t.Run(mode, func(t *testing.T) {
			db := durableAnswerDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			store := New(db)
			service, owner, interaction, finish, _ := durableAnswerSession(t, ctx, db)
			finish.AnswerArtifact = nil
			finish.Manifest.AnswerArtifactRef = "artifact:answer-fixture"
			raw, err := os.ReadFile("testdata/sample-function-result.json")
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Input   sessionvo.PayloadEnvelope    `json:"input"`
				Output  sessionvo.PayloadEnvelope    `json:"output"`
				Profile *sessionvo.CapabilityProfile `json:"profile"`
				Source  ledgervo.Event               `json:"source"`
			}
			if err := json.Unmarshal(raw, &fixture); err != nil {
				t.Fatal(err)
			}
			fixture.Profile.EvidenceContract = "managed_function_execution/v1"
			if mode == "failed-then-success" {
				failedOp, failedReceipt, err := service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{Owner: owner, ConversationID: interaction.ConversationID, InteractionID: interaction.ID, OperationKey: "failed-bom", ToolName: "bom_shared_list", Protocol: sessionvo.ProtocolInternal, SourceModule: "context-loader-function-result/v1", Input: fixture.Input, CapabilityProfile: fixture.Profile, Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch})
				if err != nil {
					t.Fatal(err)
				}
				errorPayload, _ := sessionvo.InlineJSONPayload(json.RawMessage(`{"code":"bom_not_found"}`))
				_, _, err = service.FailOperationAttempt(ctx, sessionsvc.FinishAttemptCommand{Owner: owner, OperationID: failedOp.ID, Attempt: failedOp.Attempt, ReceiptID: failedReceipt.ID, Error: errorPayload, RequestID: "req-failure", TraceID: fixture.Source.TraceID, SpanID: fixture.Source.SpanID, BusinessRefs: fixture.Source.BusinessRefs, EvidenceDurability: sessionvo.DurabilityDurable, EvidenceExpectation: &sessionvo.EvidenceExpectation{Version: 1, Closed: true}})
				if err != nil {
					t.Fatal(err)
				}
			}
			operation, receipt, err := service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{Owner: owner, ConversationID: interaction.ConversationID, InteractionID: interaction.ID, OperationKey: "bom", ToolName: "bom_shared_list", Protocol: sessionvo.ProtocolInternal, SourceModule: "context-loader-function-result/v1", Input: fixture.Input, CapabilityProfile: fixture.Profile, Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch})
			if err != nil {
				t.Fatal(err)
			}
			source := fixture.Source
			source.EventID = "evt-bom-source"
			source.Owner = owner
			source.ConversationID = interaction.ConversationID
			source.InteractionID = interaction.ID
			source.OperationID = operation.ID
			source.Attempt = operation.Attempt
			source.ProducerStreamID = "bom-stream"
			source.ProducerSequence = 1
			source.CausationEventIDs = nil
			for i := range source.OperationBusinessEdges {
				source.OperationBusinessEdges[i].OperationID = operation.ID
			}
			var envelope map[string]any
			if err := json.Unmarshal(source.Envelope, &envelope); err != nil {
				t.Fatal(err)
			}
			envelope["event_id"] = source.EventID
			envelope["conversation_id"] = source.ConversationID
			envelope["interaction_id"] = source.InteractionID
			envelope["operation_id"] = source.OperationID
			envelope["owner"] = owner
			source.Envelope, _ = json.Marshal(envelope)
			source.PayloadHash = ledgervo.CanonicalPayloadHash(source.Envelope)
			ledger := ledgersvc.New(store)
			ledger.SetDurableObserver(service.ReconcileEvidence)
			if mode != "late" && mode != "lost" {
				if _, err := ledger.Ingest(ctx, source); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err = service.CompleteOperationAttempt(ctx, sessionsvc.FinishAttemptCommand{Owner: owner, OperationID: operation.ID, Attempt: operation.Attempt, ReceiptID: receipt.ID, Output: fixture.Output, BusinessRefs: source.BusinessRefs, EvidenceDurability: sessionvo.DurabilityPending, RequestID: source.RequestID, TraceID: source.TraceID, SpanID: source.SpanID, EvidenceExpectation: &sessionvo.EvidenceExpectation{Version: 1, Closed: true, Events: []sessionvo.ExpectedEvidenceEvent{{EventID: source.EventID, EventType: source.EventType, PayloadHash: source.PayloadHash, ProducerID: source.ProducerID, PublishDisposition: "accepted"}}}})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "lost" {
				deadline := time.Now().UTC().Add(-time.Second)
				finish.Manifest.AssemblerDeadline = &deadline
			}
			terminal, err := service.TerminateInteraction(ctx, finish)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "lost" {
				if terminal.ExecutionStatus != sessionvo.InteractionCompleted || terminal.EvidenceStatus != sessionvo.EvidencePartial || len(terminal.ClosureManifest.Claims) != 0 {
					t.Fatalf("lost declared event was hidden: %+v", terminal)
				}
				return
			}
			if mode == "late" {
				if terminal.ExecutionStatus != sessionvo.InteractionCompleted || terminal.EvidenceStatus != sessionvo.EvidenceAssembling {
					t.Fatalf("business/evidence not separated: %+v", terminal)
				}
				if _, err := ledger.Ingest(ctx, source); err != nil {
					t.Fatal(err)
				}
			}
			query := assemblysvc.NewQueryService(New(db), New(db))
			view, err := query.GetInteraction(ctx, owner, interaction.ID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Interaction.EvidenceStatus != sessionvo.EvidenceComplete || view.Assembly.Completeness != sessionvo.EvidenceComplete || len(view.Assembly.Claims) != 1 || len(view.Assembly.EvidenceRefs) != 1 || view.Revision == nil {

				t.Fatalf("formal chain missing: evidence=%s assembly=%s claims=%d refs=%d revision=%+v", view.Interaction.EvidenceStatus, view.Assembly.Completeness, len(view.Assembly.Claims), len(view.Assembly.EvidenceRefs), view.Revision)
			}
			claim := view.Assembly.Claims[0].Claim
			if claim.Supports[0].SourceRevisionID != view.Revision.ID || claim.Supports[0].SourceOperationID != operation.ID {
				t.Fatalf("imprecise support %+v", claim)
			}
			before := durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_evidence_event_ledger WHERE producer_id='agent-observability-core'")
			if _, err := service.TerminateInteraction(ctx, finish); err != nil {
				t.Fatalf("finish replay: %v", err)
			}
			if _, err := ledger.Ingest(ctx, source); err != nil {
				t.Fatalf("event replay: %v", err)
			}
			after := durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_evidence_event_ledger WHERE producer_id='agent-observability-core'")
			if before != 1 || after != before {
				t.Fatalf("duplicated Claim %d -> %d", before, after)
			}
			snapshot, found, err := New(db).ReadEvidenceSnapshot(ctx, interaction.ID)
			if err != nil || !found || snapshot.Ledger == nil {
				t.Fatalf("snapshot: %v %v", found, err)
			}
			formal := 0
			for _, record := range snapshot.Ledger.Events {
				var event ledgervo.Event
				if json.Unmarshal(record.Envelope, &event) != nil {
					t.Fatal("bad frozen Ledger JSON")
				}
				if event.ProducerID == "agent-observability-core" {
					formal++
					if len(event.Claims) != 1 || event.Claims[0].ID != claim.ID {
						t.Fatal("snapshot lost Claim")
					}
				}
			}
			if formal != 1 {
				t.Fatalf("snapshot claims=%d", formal)
			}
			other := owner
			other.EffectiveSubjectID = "other-owner"
			if _, err := query.GetInteraction(ctx, other, interaction.ID); err == nil {
				t.Fatal("Claim crossed owner boundary")
			}
		})
	}
}
