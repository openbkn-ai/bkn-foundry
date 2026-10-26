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
	sources   []ledgervo.Event
	saved     []ledgervo.Event
	readErr   error
	writeErr  error
	factReads int
}

func (tx *functionSealTransaction) FindOperationCallFact(id string, attempt uint32) (sessionvo.OperationCallFact, bool) {
	tx.factReads++
	return tx.Transaction.FindOperationCallFact(id, attempt)
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
	for _, name := range []string{"normal", "zero", "no source refs", "output changed", "untrusted producer", "missing output", "wrong owner", "duplicate evidence", "unsupported store", "read budget", "write budget", "missing result", "null result", "text result", "changed content", "embedded declaration", "clock skew", "precise content", "caller prefix", "invalid event", "payload conflict", "sequence conflict", "causality conflict", "owner conflict", "scope conflict", "storage failure", "incomplete proof"} {
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
			var nativeResult any = content
			if name == "embedded declaration" {
				content["trace_evidence"] = map[string]any{"claim_type": "untrusted/v1", "complete": false}
			}
			switch name {
			case "missing result", "null result":
				nativeResult = nil
			case "text result":
				nativeResult = "unstructured answer"
			case "changed content":
				nativeResult = map[string]any{"changed": true}
			}

			output := map[string]any{"status_code": 200, "function_execution": map[string]any{"toolbox_id": "box", "tool_id": "quality_check", "function_version": "version", "code_sha256": producer.CodeSHA256}, "body": map[string]any{"exit_code": 0, "result": nativeResult}}
			if name == "missing result" {
				delete(output["body"].(map[string]any), "result")
			}
			outputRaw, _ := json.Marshal(output)
			payload, _ := sessionvo.InlineJSONPayload(outputRaw)
			input, _ := sessionvo.InlineJSONPayload(json.RawMessage(`{"kn_id":"kn","toolbox_id":"box","tool_id":"quality_check","function_version":"version"}`))
			fact := sessionvo.OperationCallFact{OperationID: "op", Attempt: 1, ConversationID: "conv", InteractionID: "int", ReceiptID: "receipt", ToolName: "quality_check", Protocol: sessionvo.ProtocolInternal, SourceModule: functionResultSourceModule, Status: sessionvo.AttemptCompleted, Input: input, Output: &payload, CapabilityProfile: &sessionvo.CapabilityProfile{EvidenceContract: "managed_function_execution/v1", ToolVersion: "version"}}
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
			proof := functionResultEvidence{Contract: functionResultContract, Complete: true, Producer: producer, ArtifactRef: "artifact:function-result", ContentHash: "sha256:" + hex.EncodeToString(sum[:]), OutputHash: ledgervo.CanonicalPayloadHash(payload.Inline)}
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
				interaction := sessionvo.Interaction{ID: "int", ConversationID: "conv", EvidenceStatus: sessionvo.EvidenceComplete, ClosureManifest: &sessionvo.ClosureManifest{ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: "op"}}}}
				revision := sessionvo.AssemblyRevision{ID: "real-revision", InteractionID: "int", IncludedEventIDs: []string{"source"}, Completeness: sessionvo.EvidenceComplete, CreatedAt: now.Add(time.Second)}
				custom := &functionSealTransaction{Transaction: tx, sources: []ledgervo.Event{source}}
				switch name {
				case "no source refs":
					receipt, _ := tx.FindReceiptByOperationAttempt("op", 1)
					receipt.ObservedEvidenceRefs = nil
					tx.SaveReceipt(receipt)
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
					if len(custom.saved) != 1 || len(interaction.ClosureManifest.Claims) != 2 || !slices.Contains(interaction.ClosureManifest.Claims, "claim_function_manual") || revision.Completeness != sessionvo.EvidenceComplete || len(revision.PartialReasons) != 0 || interaction.EvidenceStatus != sessionvo.EvidenceComplete {
						t.Fatalf("caller Claim altered: %+v", revision)
					}
					return nil
				}
				good := name == "normal" || name == "zero" || name == "clock skew" || name == "embedded declaration"
				if !good {
					if name == "no source refs" && custom.factReads != 0 {
						t.Fatalf("uncaptured snapshot loaded full output: %d", custom.factReads)
					}
					if len(custom.saved) != 0 || revision.Completeness != sessionvo.EvidenceComplete || interaction.EvidenceStatus != sessionvo.EvidenceComplete || len(interaction.ClosureManifest.Claims) != 0 || len(revision.PartialReasons) != 0 {
						t.Fatalf("unverified Claim: events=%d revision=%+v interaction=%+v", len(custom.saved), revision, interaction)
					}
					return nil
				}
				if len(custom.saved) != 1 {
					t.Fatalf("events=%d", len(custom.saved))
				}
				event := custom.saved[0]
				claim := event.Claims[0]
				if claim.Type != functionResultClaimType || claim.ID != functionClaimID("op", 1) || claim.Supports[0].SourceRevisionID != revision.ID || event.EvidenceRefs[0].ContentHash != proof.ContentHash || claim.ContentArtifactRef != proof.ArtifactRef {
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

func TestFunctionResultCandidatesDoNotBecomeManifestObligations(t *testing.T) {
	store := sessionstore.New()
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		for i := 0; i < 33; i++ {
			tx.SaveOperation(sessionvo.Operation{ID: fmt.Sprintf("op-%d", i), InteractionID: "int", Attempt: 1})
			tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: fmt.Sprintf("op-%d", i), InteractionID: "int", Attempt: 1, SourceModule: functionResultSourceModule, Status: sessionvo.AttemptCompleted})
		}
		manifest := deriveClosureManifest(tx, "int", sessionvo.ClosureManifest{Version: "3.0.0", CompletionReason: "completed"})
		if len(manifest.Claims) != 0 || len(manifest.SystemPartialReasons) != 0 {
			t.Fatalf("optional candidates became required: %+v", manifest)
		}
		current := sessionvo.Interaction{ID: "int", ExecutionStatus: sessionvo.InteractionCompleted, TerminalIdempotencyKey: "finish", ClosureManifest: &manifest}
		command := TerminateInteractionCommand{InteractionID: "int", Status: sessionvo.InteractionCompleted, TerminalIdempotencyKey: "finish", Manifest: sessionvo.ClosureManifest{Version: "3.0.0", CompletionReason: "completed"}}
		if !managedTerminalReplayMatches(current, command, tx) {
			t.Fatal("optional candidate discovery broke exact finish replay")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type summaryReadCountingTransaction struct {
	isessionstore.Transaction
	reads int
}

func (tx *summaryReadCountingTransaction) ListOperationCallSummaries(id string) []sessionvo.OperationCallSummary {
	tx.reads++
	return tx.Transaction.ListOperationCallSummaries(id)
}
func TestEmptyInteractionManifestDoesNotReadCallSummaries(t *testing.T) {
	store := sessionstore.New()
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		counting := &summaryReadCountingTransaction{Transaction: tx}
		manifest := deriveClosureManifest(counting, "empty", sessionvo.ClosureManifest{Version: "3.0.0"})
		if counting.reads != 0 || len(manifest.Claims) != 0 || len(manifest.ExpectedOperations) != 0 {
			t.Fatalf("empty finish performed extra reads: %d %+v", counting.reads, manifest)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
