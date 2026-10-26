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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// Domain-neutral SQL cases use the same production path as the supply fixture.
// These are deterministic result fixtures, not live financial/customer systems.
func TestFunctionResultExamplesMariaDB(t *testing.T) {
	many := make([]string, 33)
	for i := range many {
		many[i] = `{"count":2}`
	}
	for _, test := range []struct {
		name         string
		results      []string
		skipSnapshot bool
		wantClaims   int
		lateLast     bool
	}{
		{"analytical rows", []string{`{"rows":[{"id":"A","score":0.75},{"id":"B","score":0.95}],"count":2}`}, false, 1, false},
		{"exact monetary strings", []string{`{"currency":"CNY","total":"30.30","items":2}`}, false, 1, false},
		{"empty result", []string{`[]`}, false, 1, false},
		{"independent tools in one interaction", []string{`{"available":0,"items":[]}`, `{"measurement":"passed","count":2}`}, false, 2, false},
		{"retained snapshot in later revision", []string{`{"first":1}`, `{"second":2}`}, false, 2, true},
		{"snapshot capacity", many, false, 32, false},
		{"text result", []string{`"answer"`}, true, 0, false},
		{"numeric result", []string{`42`}, true, 0, false},
		{"null result", []string{`null`}, true, 0, false},
		{"old Factory", []string{`{"count":2}`}, true, 0, false},
		{"unversioned", []string{`{"count":2}`}, true, 0, false},
		{"redacted result", []string{`{"access_token":"[redacted]"}`}, true, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := durableAnswerDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			store := New(db)
			service, owner, interaction, finish, _ := durableAnswerSession(t, ctx, db)
			finish.AnswerArtifact = nil
			finish.Manifest.AnswerArtifactRef = "artifact:answer-fixture"
			ledger := ledgersvc.New(store)
			ledger.SetDurableObserver(service.ReconcileEvidence)
			producer := map[string]any{"knowledge_network_id": "examples", "toolbox_id": "read-only", "tool_id": "example", "function_version": "v1", "code_sha256": "sha256:" + strings.Repeat("a", 64)}
			profile := &sessionvo.CapabilityProfile{ManifestID: "openbkn.context-loader.managed-function", ManifestVersion: "1.0.0", CanonicalToolName: "example", InputSchemaDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("managed-function-input/v1"))), OutputSchemaDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("managed-function-output/v1"))), ExecutionRole: "business_function", ChildEvidencePolicy: "managed_descendants", FailurePolicy: "preserve_execution_and_downgrade", MinimumTraceSchema: "3.0.0", Resolution: "matched", ToolVersion: "v1", EvidenceContract: "managed_function_execution/v1", RequiredTraceFields: []string{"business_refs", "result_completeness"}}
			operations := map[string]struct{ artifact, hash string }{}
			var deferredSource *ledgervo.Event
			for i, result := range test.results {
				tool := fmt.Sprintf("example_%d", i)
				producer["tool_id"] = tool
				inputRaw, _ := json.Marshal(map[string]any{"kn_id": "examples", "toolbox_id": "read-only", "tool_id": tool, "function_version": "v1"})
				input, _ := sessionvo.InlineJSONPayload(inputRaw)
				callProfile := *profile
				if test.name == "unversioned" {
					callProfile.ToolVersion = "unversioned"
					input, _ = sessionvo.InlineJSONPayload(json.RawMessage(fmt.Sprintf(`{"kn_id":"examples","toolbox_id":"read-only","tool_id":%q,"function_version":"unversioned"}`, tool)))
				}
				callProfile.CanonicalToolName = tool
				op, receipt, err := service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{Owner: owner, ConversationID: interaction.ConversationID, InteractionID: interaction.ID, OperationKey: fmt.Sprintf("example-%d", i), ToolName: tool, Protocol: sessionvo.ProtocolInternal, SourceModule: "context-loader-function-result/v1", Input: input, CapabilityProfile: &callProfile, Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch})
				if err != nil {
					t.Fatal(err)
				}
				outputRaw, _ := json.Marshal(map[string]any{"status_code": 200, "function_execution": map[string]any{"toolbox_id": "read-only", "tool_id": tool, "function_version": "v1", "code_sha256": producer["code_sha256"]}, "body": map[string]any{"result": json.RawMessage(result)}})
				if test.name == "old Factory" {
					outputRaw, _ = json.Marshal(map[string]any{"status_code": 200, "body": map[string]any{"result": json.RawMessage(result)}})
				}
				output, _ := sessionvo.InlineJSONPayload(outputRaw)
				artifact := fmt.Sprintf("artifact:example-result-%d", i)
				operations[op.ID] = struct{ artifact, hash string }{artifact, "sha256:" + ledgervo.CanonicalPayloadHash([]byte(result))}
				envelope, _ := json.Marshal(map[string]any{"payload": map[string]any{"result_contract": "function_result_evidence/v1", "complete": true, "producer": producer, "result_artifact_ref": artifact, "content_hash": "sha256:" + ledgervo.CanonicalPayloadHash([]byte(result)), "output_hash": ledgervo.CanonicalPayloadHash(output.Inline)}})
				now := time.Now().UTC()
				source := ledgervo.Event{EventID: fmt.Sprintf("example-source-%d", i), EventType: "logic.execution.observed", SchemaVersion: "3.0.0", Owner: owner, ConversationID: interaction.ConversationID, InteractionID: interaction.ID, OperationID: op.ID, Attempt: op.Attempt, RequestID: "req-example", TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16), ProducerID: "agent-retrieval", ProducerStreamID: "example-stream", ProducerEpoch: 1, ProducerSequence: uint64(i + 1), StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: envelope, PayloadHash: ledgervo.CanonicalPayloadHash(envelope), ArtifactRefs: []string{artifact}, BusinessRefs: []sessionvo.BusinessRef{{RefType: sessionvo.BusinessRefKnowledgeNetwork, RefID: "kn:examples", Version: "unversioned"}, {RefType: sessionvo.BusinessRefFunction, RefID: "function:examples:" + tool, Version: "v1"}}}
				expectation := &sessionvo.EvidenceExpectation{Version: 1, Closed: true}
				if !test.skipSnapshot {
					if test.lateLast && i == len(test.results)-1 {
						deferredSource = &source
					} else if _, err := ledger.Ingest(ctx, source); err != nil {
						t.Fatal(err)
					}
					expectation.Events = []sessionvo.ExpectedEvidenceEvent{{EventID: source.EventID, EventType: source.EventType, PayloadHash: source.PayloadHash, ProducerID: source.ProducerID, PublishDisposition: "accepted"}}
				}
				_, _, err = service.CompleteOperationAttempt(ctx, sessionsvc.FinishAttemptCommand{Owner: owner, OperationID: op.ID, Attempt: op.Attempt, ReceiptID: receipt.ID, Output: output, BusinessRefs: source.BusinessRefs, EvidenceDurability: sessionvo.DurabilityDurable, RequestID: source.RequestID, TraceID: source.TraceID, SpanID: source.SpanID, EvidenceExpectation: expectation})
				if err != nil {
					t.Fatal(err)
				}
			}
			if deferredSource != nil {
				deadline := time.Now().UTC().Add(-time.Second)
				finish.Manifest.AssemblerDeadline = &deadline
			}
			if _, err := service.TerminateInteraction(ctx, finish); err != nil {
				t.Fatal(err)
			}
			var retained *sessionvo.Claim
			if deferredSource != nil {
				early, err := assemblysvc.NewQueryService(New(db), New(db)).GetInteraction(ctx, owner, interaction.ID)
				if err != nil {
					t.Fatal(err)
				}
				if early.Interaction.EvidenceStatus != sessionvo.EvidencePartial || len(early.Assembly.Claims) != 1 {
					t.Fatalf("first snapshot not sealed before late source: %+v", early)
				}
				claim := early.Assembly.Claims[0].Claim
				retained = &claim
				if _, err := ledger.Ingest(ctx, *deferredSource); err != nil {
					t.Fatal(err)
				}
			}
			view, err := assemblysvc.NewQueryService(New(db), New(db)).GetInteraction(ctx, owner, interaction.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantAssembly := sessionvo.EvidenceComplete
			if test.wantClaims == 0 {
				wantAssembly = sessionvo.EvidenceNotApplicable
			}
			if view.Revision == nil || view.Interaction.EvidenceStatus != sessionvo.EvidenceComplete || len(view.Revision.PartialReasons) != 0 || view.Assembly.Completeness != wantAssembly || len(view.Assembly.Claims) != test.wantClaims || len(view.Assembly.EvidenceRefs) != test.wantClaims {
				t.Fatalf("incomplete example chain: %+v", view)
			}
			if _, err := evidencesvc.New(evidencestore.New(), evidencesvc.WithSessionStore(store), evidencesvc.WithCurrentRecordIntegrity()).PersistRecordIntegrityBatch(ctx, "", 100); err != nil {
				t.Fatal(err)
			}
			var current sessionvo.Interaction
			if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { current, _ = tx.PeekInteraction(interaction.ID); return nil }); err != nil {
				t.Fatal(err)
			}
			if current.StoredRecordIntegrity == nil || current.StoredRecordIntegrity.Report == nil || current.StoredRecordIntegrity.Report.Status != "complete" {
				t.Fatalf("receipt integrity=%+v", current.StoredRecordIntegrity)
			}
			seen := map[string]bool{}
			for _, assembled := range view.Assembly.Claims {
				c := assembled.Claim
				if len(c.Supports) != 1 {
					t.Fatalf("unexpected supports: %+v", c)
				}
				support := c.Supports[0]
				expected, registered := operations[support.SourceOperationID]
				expectedRevision := view.Revision.ID
				if retained != nil && c.ID == retained.ID {
					expectedRevision = retained.Supports[0].SourceRevisionID
				}
				if c.Type != "function.result/v1" || support.SourceRevisionID != expectedRevision || !registered || seen[support.SourceOperationID] || c.ContentArtifactRef != expected.artifact || support.TargetRef != expected.artifact || support.ContentHash != expected.hash {
					t.Fatalf("cross-wired example result: %+v", c)
				}
				seen[support.SourceOperationID] = true
			}
			if _, err := service.TerminateInteraction(ctx, finish); err != nil {
				t.Fatalf("replay: %v", err)
			}
			if n := durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_evidence_event_ledger WHERE producer_id='agent-observability-core'"); n != test.wantClaims {
				t.Fatalf("duplicate result Claims: %d", n)
			}
		})
	}
}
