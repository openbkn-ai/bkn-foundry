// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionsvc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/assemblysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type functionSealTransaction struct {
	isessionstore.Transaction
	sources  []ledgervo.Event
	saved    []ledgervo.Event
	readErr  error
	writeErr error
}

func (tx *functionSealTransaction) ReadEvidenceEventsByIDs([]string) ([]ledgervo.Event, error) {
	return tx.sources, tx.readErr
}
func (tx *functionSealTransaction) AppendDerivedEvidence(e ledgervo.Event) (ledgervo.DurableAck, error) {
	if tx.writeErr != nil {
		return ledgervo.DurableAck{}, tx.writeErr
	}
	if err := ledgersvc.ValidateHistoricalEvent(e); err != nil {
		return ledgervo.DurableAck{}, err
	}
	tx.saved = append(tx.saved, e)
	return ledgervo.DurableAck{EventID: e.EventID, Durable: true}, nil
}

func TestFunctionResultClaimSealsExistingModelWithRealRevision(t *testing.T) {
	for _, name := range []string{"normal", "zero", "output changed", "untrusted producer", "missing output", "wrong owner", "duplicate evidence", "unsupported store", "read budget", "write budget", "missing declaration", "invalid complete", "changed content", "changed claim type", "clock skew", "precise content", "caller prefix", "invalid event", "payload conflict", "sequence conflict", "causality conflict", "owner conflict", "scope conflict", "storage failure", "incomplete proof"} {
		t.Run(name, func(t *testing.T) {
			now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
			owner := sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "user"}
			// A domain-neutral conclusion proves Core does not require sample fields.
			content := map[string]any{"measurement": "passed", "count": 2}
			if name == "zero" {
				content["count"] = 0
			}
			if name == "precise content" {
				content["count"], content["text"] = json.Number("9007199254740993"), "<verified>"
			}
			producer := resultProducer{KnowledgeNetworkID: "kn", ToolboxID: "box", ToolID: "quality_check", FunctionVersion: "version", CodeSHA256: "sha256:" + strings.Repeat("a", 64)}
			declaration := map[string]any{"claim_type": "quality.check/v1", "complete": true, "content": content}
			nativeResult := map[string]any{"trace_evidence": declaration}
			switch name {
			case "missing declaration":
				delete(nativeResult, "trace_evidence")
			case "invalid complete":
				declaration["complete"] = "true"
			case "changed content":
				declaration["content"] = map[string]any{"changed": true}
			case "changed claim type":
				declaration["claim_type"] = "other/v1"
			}
			output := map[string]any{"status_code": 200, "function_execution": map[string]any{"toolbox_id": "box", "tool_id": "quality_check", "function_version": "version", "code_sha256": producer.CodeSHA256}, "body": map[string]any{"exit_code": 0, "result": nativeResult}}
			outputRaw, _ := json.Marshal(output)
			payload, _ := sessionvo.InlineJSONPayload(outputRaw)
			input, _ := sessionvo.InlineJSONPayload(json.RawMessage(`{"kn_id":"kn","toolbox_id":"box","tool_id":"quality_check","function_version":"version"}`))
			fact := sessionvo.OperationCallFact{OperationID: "op", Attempt: 1, ConversationID: "conv", InteractionID: "int", ReceiptID: "receipt", ToolName: "quality_check", Protocol: sessionvo.ProtocolInternal, SourceModule: functionResultSourceModule, Status: sessionvo.AttemptCompleted, Input: input, Output: &payload, CapabilityProfile: &sessionvo.CapabilityProfile{EvidenceContract: functionResultContract, ToolVersion: "version"}}
			raw, _ := json.Marshal(content)
			var value any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			_ = decoder.Decode(&value)
			var canonical bytes.Buffer
			encoder := json.NewEncoder(&canonical)
			encoder.SetEscapeHTML(false)
			_ = encoder.Encode(value)
			sum := sha256.Sum256(bytes.TrimSuffix(canonical.Bytes(), []byte("\n")))
			proof := functionResultEvidence{Contract: functionResultContract, Complete: true, Producer: producer, ClaimType: "quality.check/v1", ArtifactRef: "artifact:function-result", ContentHash: "sha256:" + hex.EncodeToString(sum[:]), OutputHash: ledgervo.CanonicalPayloadHash(payload.Inline)}
			if name == "incomplete proof" {
				proof.Complete = false
			}
			envelope, _ := json.Marshal(map[string]any{"payload": proof})
			source := ledgervo.Event{EventID: "source", EventType: "logic.execution.observed", SchemaVersion: "3.0.0", Envelope: envelope, PayloadHash: ledgervo.CanonicalPayloadHash(envelope), Owner: owner, ConversationID: "conv", InteractionID: "int", OperationID: "op", Attempt: 1, RequestID: "req", TraceID: "trace", ProducerID: "agent-retrieval", StartedAt: now, ObservedAt: now, EmittedAt: now}
			switch name {
			case "clock skew":
				source.StartedAt, source.ObservedAt, source.EmittedAt = now.Add(5*time.Second), now.Add(5*time.Second), now.Add(6*time.Second)
			case "output changed":
				fact.Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`{"changed":true}`)}
			case "untrusted producer":
				source.ProducerID = "untrusted"
			case "missing output":
				fact.Output = nil
			case "wrong owner":
				source.Owner.EffectiveSubjectID = "another-user"
			}
			store := sessionstore.NewWithClock(func() time.Time { return now.Add(time.Second) })
			service := New(store, Options{})
			err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
				tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner})
				tx.SaveOperationCallFact(fact)
				tx.SaveReceipt(sessionvo.Receipt{ID: "receipt", ConversationID: "conv", InteractionID: "int", OperationID: "op", Attempt: 1, Status: sessionvo.ReceiptCompleted, EvidenceDurability: sessionvo.DurabilityDurable, ObservedEvidenceRefs: []string{"source"}, RequestID: "req", TraceID: "trace"})
				interaction := sessionvo.Interaction{ID: "int", ConversationID: "conv", EvidenceStatus: sessionvo.EvidenceComplete, ClosureManifest: &sessionvo.ClosureManifest{Claims: []string{functionClaimID("op", 1)}}}
				revision := sessionvo.AssemblyRevision{ID: "real-revision", InteractionID: "int", IncludedEventIDs: []string{"source"}, Completeness: sessionvo.EvidenceComplete, CreatedAt: now.Add(time.Second)}
				custom := &functionSealTransaction{Transaction: tx, sources: []ledgervo.Event{source}}
				switch name {
				case "caller prefix":
					interaction.ClosureManifest.Claims = []string{"claim_function_manual"}
				case "invalid event":
					custom.writeErr = &ledgersvc.DomainError{Code: ledgersvc.CodeInvalidEvent, Message: "invalid proof"}
				case "payload conflict":
					custom.writeErr = ievidenceledger.ErrPayloadConflict
				case "sequence conflict":
					custom.writeErr = ievidenceledger.ErrSequenceConflict
				case "causality conflict":
					custom.writeErr = ievidenceledger.ErrCausalityConflict
				case "owner conflict":
					custom.writeErr = ievidenceledger.ErrOwnerMismatch
				case "scope conflict":
					custom.writeErr = ievidenceledger.ErrMissingScope
				case "storage failure":
					custom.writeErr = errors.New("database unavailable")
				}
				if name == "read budget" {
					custom.readErr = isessionstore.ErrEvidenceConfirmationLimit
				}
				if name == "write budget" {
					custom.writeErr = isessionstore.ErrEvidenceConfirmationLimit
				}
				if name == "duplicate evidence" {
					custom.sources = append(custom.sources, source)
				}
				var sealing isessionstore.Transaction = custom
				if name == "unsupported store" {
					sealing = tx
				}
				_, sealErr := service.sealFunctionClaims(sealing, &interaction, &revision)
				if name == "storage failure" {
					if !errors.Is(sealErr, custom.writeErr) {
						t.Fatalf("storage error swallowed: %v", sealErr)
					}
					return nil
				}
				if sealErr != nil {
					return sealErr
				}
				if name == "caller prefix" {
					if len(custom.saved) != 0 || revision.Completeness != sessionvo.EvidenceComplete || len(revision.PartialReasons) != 0 || interaction.EvidenceStatus != sessionvo.EvidenceComplete {
						t.Fatalf("caller Claim altered: %+v", revision)
					}
					return nil
				}
				good := name == "normal" || name == "zero" || name == "clock skew"
				if !good {
					if len(custom.saved) != 0 || revision.Completeness != sessionvo.EvidencePartial || interaction.EvidenceStatus != sessionvo.EvidencePartial {
						t.Fatalf("unverified Claim: events=%d revision=%+v interaction=%+v", len(custom.saved), revision, interaction)
					}
					return nil
				}
				if len(custom.saved) != 1 {
					t.Fatalf("events=%d", len(custom.saved))
				}
				event := custom.saved[0]
				claim := event.Claims[0]
				if claim.Type != proof.ClaimType || claim.ID != functionClaimID("op", 1) || claim.Supports[0].SourceRevisionID != revision.ID || event.EvidenceRefs[0].ContentHash != proof.ContentHash || claim.ContentArtifactRef != proof.ArtifactRef {
					t.Fatalf("incorrect binding: %+v", event)
				}
				assembled := assemblysvc.Assemble("int", []ledgervo.Event{source, event}, interaction.ClosureManifest.Claims)
				if assembled.Completeness != sessionvo.EvidenceComplete || len(assembled.Claims) != 1 || len(assembled.EvidenceRefs) != 1 {
					t.Fatalf("assembly=%+v", assembled)
				}
				replay := materializedFunctionClaim(source, proof, claim.ID, "receipt", sessionvo.AssemblyRevision{ID: revision.ID, CreatedAt: revision.CreatedAt}, 2)
				if ledgervo.ImmutableRecordHash(replay) != ledgervo.ImmutableRecordHash(event) {
					t.Fatal("same revision generated a different event")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSampleFunctionResultProof(t *testing.T) {
	raw, err := os.ReadFile("../../../drivenadapter/dbaccess/mariadb/sessionstore/testdata/sample-function-result.json")
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
	if fixture.Source.PayloadHash != ledgervo.CanonicalPayloadHash(fixture.Source.Envelope) {
		t.Fatal("fixture source hash does not cover its completeness declaration")
	}
	var envelope struct {
		Payload functionResultEvidence `json:"payload"`
	}
	if err := json.Unmarshal(fixture.Source.Envelope, &envelope); err != nil {
		t.Fatal(err)
	}
	if !validFunctionProof(sessionvo.OperationCallFact{Input: fixture.Input, Output: &fixture.Output, CapabilityProfile: fixture.Profile}, envelope.Payload) {
		t.Fatalf("real CL proof rejected: output hash=%s expected=%s", ledgervo.CanonicalPayloadHash(fixture.Output.Inline), envelope.Payload.OutputHash)
	}
}

func TestFunctionResultClaimCapacityKeepsServerManifestBoundedAndReplayable(t *testing.T) {
	store := sessionstore.New()
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		for i := 0; i < 33; i++ {
			tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: fmt.Sprintf("op-%d", i), InteractionID: "int", Attempt: 1, SourceModule: functionResultSourceModule, Status: sessionvo.AttemptCompleted})
		}
		manifest := deriveClosureManifest(tx, "int", sessionvo.ClosureManifest{Version: "3.0.0", CompletionReason: "completed"}, 32)
		if len(manifest.Claims) != 32 || !slices.Contains(manifest.SystemPartialReasons, "function_claim_capacity_exceeded") {
			t.Fatalf("capacity not represented: %+v", manifest)
		}
		current := sessionvo.Interaction{ID: "int", ExecutionStatus: sessionvo.InteractionCompleted, TerminalIdempotencyKey: "finish", ClosureManifest: &manifest}
		command := TerminateInteractionCommand{InteractionID: "int", Status: sessionvo.InteractionCompleted, TerminalIdempotencyKey: "finish", Manifest: sessionvo.ClosureManifest{Version: "3.0.0", CompletionReason: "completed"}}
		if !managedTerminalReplayMatches(current, command, tx) {
			t.Fatal("server-owned claim capacity reason broke exact finish replay")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
