// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/assemblysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

// Domain-neutral SQL cases use the same production path as the supply fixture.
// These are deterministic result fixtures, not live financial/customer systems.
func TestFunctionResultExamplesMariaDB(t *testing.T) {
	for _, test := range []struct {
		name    string
		results []string
	}{
		{"analytical rows", []string{`{"rows":[{"id":"A","score":0.75},{"id":"B","score":0.95}],"count":2}`}},
		{"exact monetary strings", []string{`{"currency":"CNY","total":"30.30","items":2}`}},
		{"empty result", []string{`[]`}},
		{"independent tools in one interaction", []string{`{"available":0,"items":[]}`, `{"measurement":"passed","count":2}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := durableAnswerDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			store := New(db)
			service, owner, interaction, finish, _ := durableAnswerSession(t, ctx, db)
			finish.AnswerArtifact = nil
			finish.Manifest.AnswerArtifactRef = "artifact:answer-fixture"
			ledger := ledgersvc.New(store)
			ledger.SetDurableObserver(service.ReconcileEvidence)
			producer := map[string]any{"knowledge_network_id": "examples", "toolbox_id": "read-only", "tool_id": "example", "function_version": "v1", "code_sha256": "sha256:" + strings.Repeat("a", 64)}
			profile := &sessionvo.CapabilityProfile{ManifestID: "openbkn.context-loader.managed-function", ManifestVersion: "1.0.0", CanonicalToolName: "example", InputSchemaDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("managed-function-input/v1"))), OutputSchemaDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("managed-function-output/v1"))), ExecutionRole: "business_function", ChildEvidencePolicy: "managed_descendants", FailurePolicy: "preserve_execution_and_downgrade", MinimumTraceSchema: "3.0.0", Resolution: "matched", ToolVersion: "v1", EvidenceContract: "function_result_evidence/v1", RequiredTraceFields: []string{"business_refs", "result_completeness"}}
			operations := map[string]struct{ artifact, hash string }{}
			for i, result := range test.results {
				tool := fmt.Sprintf("example_%d", i)
				producer["tool_id"] = tool
				inputRaw, _ := json.Marshal(map[string]any{"kn_id": "examples", "toolbox_id": "read-only", "tool_id": tool, "function_version": "v1"})
				input, _ := sessionvo.InlineJSONPayload(inputRaw)
				callProfile := *profile
				callProfile.CanonicalToolName = tool
				op, receipt, err := service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{Owner: owner, ConversationID: interaction.ConversationID, InteractionID: interaction.ID, OperationKey: fmt.Sprintf("example-%d", i), ToolName: tool, Protocol: sessionvo.ProtocolInternal, SourceModule: "context-loader-function-result/v1", Input: input, CapabilityProfile: &callProfile, Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch})
				if err != nil {
					t.Fatal(err)
				}
				outputRaw, _ := json.Marshal(map[string]any{"status_code": 200, "function_execution": map[string]any{"toolbox_id": "read-only", "tool_id": tool, "function_version": "v1", "code_sha256": producer["code_sha256"]}, "body": map[string]any{"result": json.RawMessage(result)}})
				output, _ := sessionvo.InlineJSONPayload(outputRaw)
				artifact := fmt.Sprintf("artifact:example-result-%d", i)
				operations[op.ID] = struct{ artifact, hash string }{artifact, "sha256:" + ledgervo.CanonicalPayloadHash([]byte(result))}
				envelope, _ := json.Marshal(map[string]any{"payload": map[string]any{"result_contract": "function_result_evidence/v1", "complete": true, "producer": producer, "result_artifact_ref": artifact, "content_hash": "sha256:" + ledgervo.CanonicalPayloadHash([]byte(result)), "output_hash": ledgervo.CanonicalPayloadHash(output.Inline)}})
				now := time.Now().UTC()
				source := ledgervo.Event{EventID: fmt.Sprintf("example-source-%d", i), EventType: "logic.execution.observed", SchemaVersion: "3.0.0", Owner: owner, ConversationID: interaction.ConversationID, InteractionID: interaction.ID, OperationID: op.ID, Attempt: op.Attempt, RequestID: "req-example", TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16), ProducerID: "agent-retrieval", ProducerStreamID: "example-stream", ProducerEpoch: 1, ProducerSequence: uint64(i + 1), StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: envelope, PayloadHash: ledgervo.CanonicalPayloadHash(envelope), ArtifactRefs: []string{artifact}, BusinessRefs: []sessionvo.BusinessRef{{RefType: sessionvo.BusinessRefKnowledgeNetwork, RefID: "kn:examples", Version: "unversioned"}, {RefType: sessionvo.BusinessRefFunction, RefID: "function:examples:" + tool, Version: "v1"}}}
				if _, err := ledger.Ingest(ctx, source); err != nil {
					t.Fatal(err)
				}
				_, _, err = service.CompleteOperationAttempt(ctx, sessionsvc.FinishAttemptCommand{Owner: owner, OperationID: op.ID, Attempt: op.Attempt, ReceiptID: receipt.ID, Output: output, BusinessRefs: source.BusinessRefs, EvidenceDurability: sessionvo.DurabilityPending, RequestID: source.RequestID, TraceID: source.TraceID, SpanID: source.SpanID, EvidenceExpectation: &sessionvo.EvidenceExpectation{Version: 1, Closed: true, Events: []sessionvo.ExpectedEvidenceEvent{{EventID: source.EventID, EventType: source.EventType, PayloadHash: source.PayloadHash, ProducerID: source.ProducerID, PublishDisposition: "accepted"}}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.TerminateInteraction(ctx, finish); err != nil {
				t.Fatal(err)
			}
			view, err := assemblysvc.NewQueryService(New(db), New(db)).GetInteraction(ctx, owner, interaction.ID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Assembly.Completeness != sessionvo.EvidenceComplete || len(view.Assembly.Claims) != len(test.results) || len(view.Assembly.EvidenceRefs) != len(test.results) || view.Revision == nil {
				t.Fatalf("incomplete example chain: %+v", view)
			}
			seen := map[string]bool{}
			for _, assembled := range view.Assembly.Claims {
				c := assembled.Claim
				if len(c.Supports) != 1 {
					t.Fatalf("unexpected supports: %+v", c)
				}
				support := c.Supports[0]
				expected, registered := operations[support.SourceOperationID]
				if c.Type != "function.result/v1" || support.SourceRevisionID != view.Revision.ID || !registered || seen[support.SourceOperationID] || c.ContentArtifactRef != expected.artifact || support.TargetRef != expected.artifact || support.ContentHash != expected.hash {
					t.Fatalf("cross-wired example result: %+v", c)
				}
				seen[support.SourceOperationID] = true
			}
			if _, err := service.TerminateInteraction(ctx, finish); err != nil {
				t.Fatalf("replay: %v", err)
			}
			if n := durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_evidence_event_ledger WHERE producer_id='agent-observability-core'"); n != len(test.results) {
				t.Fatalf("duplicate result Claims: %d", n)
			}
		})
	}
}
