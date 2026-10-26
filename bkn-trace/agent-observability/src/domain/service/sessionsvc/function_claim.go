// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionsvc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

const functionResultContract = "function_result_evidence/v1"
const functionResultClaimType = "function.result/v1"
const functionResultSourceModule = "context-loader-function-result/v1"

type resultProducer struct {
	KnowledgeNetworkID string `json:"knowledge_network_id"`
	ToolboxID          string `json:"toolbox_id"`
	ToolID             string `json:"tool_id"`
	FunctionVersion    string `json:"function_version"`
	CodeSHA256         string `json:"code_sha256"`
}

type functionResultEvidence struct {
	Contract    string         `json:"result_contract"`
	Complete    bool           `json:"complete"`
	Producer    resultProducer `json:"producer"`
	ArtifactRef string         `json:"result_artifact_ref"`
	ContentHash string         `json:"content_hash"`
	OutputHash  string         `json:"output_hash"`
}

func functionClaimID(operationID string, attempt uint32) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", operationID, attempt, functionResultContract)))
	return "claim_function_" + hex.EncodeToString(sum[:16])
}

// Summaries deliberately omit input/output bodies. Only registered Function attempts
// are loaded in full, and only while sealing their immutable evidence revision.
func derivedFunctionClaimIDs(tx isessionstore.Transaction, interactionID string, includeFailed bool) []string {
	var result []string
	for _, fact := range tx.ListOperationCallSummaries(interactionID) {
		if fact.SourceModule == functionResultSourceModule && (includeFailed || fact.Status != sessionvo.AttemptFailed) {
			result = appendUnique(result, functionClaimID(fact.OperationID, fact.Attempt))
		}
	}
	slices.Sort(result)
	return result
}

func (s *Service) sealFunctionClaims(tx isessionstore.Transaction, interaction *sessionvo.Interaction, revision *sessionvo.AssemblyRevision) ([]string, error) {
	if interaction.ClosureManifest == nil {
		return nil, nil
	}
	var artifacts []string
	var expected []string
	for _, id := range interaction.ClosureManifest.Claims {
		if strings.HasPrefix(id, "claim_function_") {
			expected = append(expected, id)
		}
	}
	if len(expected) == 0 {
		return nil, nil
	}
	// Prefixes are not reserved for Core. Only registered Function attempts own IDs.
	// Keep ordinary finishes on the existing path without any additional read.
	summaries := tx.ListOperationCallSummaries(interaction.ID)
	expected = slices.DeleteFunc(expected, func(id string) bool {
		return !slices.ContainsFunc(summaries, func(f sessionvo.OperationCallSummary) bool {
			return f.SourceModule == functionResultSourceModule && functionClaimID(f.OperationID, f.Attempt) == id
		})
	})
	if len(expected) == 0 {
		return nil, nil
	}
	writer, writable := tx.(isessionstore.DerivedEvidenceTransaction)
	reader, readable := tx.(isessionstore.EvidenceConfirmationTransaction)
	missing := func(id, reason string) {
		revision.PartialReasons = appendUnique(revision.PartialReasons, reason+":"+id)
		revision.Completeness = sessionvo.EvidencePartial
	}
	if !writable || !readable {
		for _, id := range expected {
			missing(id, "claim_materialization_unavailable")
		}
	} else {
		for _, id := range expected {
			var summary *sessionvo.OperationCallSummary
			for i := range summaries {
				f := &summaries[i]
				if f.SourceModule == functionResultSourceModule && functionClaimID(f.OperationID, f.Attempt) == id {
					summary = f
					break
				}
			}
			if summary == nil {
				missing(id, "claim_producer_missing")
				continue
			}
			fact, found := tx.FindOperationCallFact(summary.OperationID, summary.Attempt)
			receipt, hasReceipt := tx.FindReceiptByOperationAttempt(summary.OperationID, summary.Attempt)
			if !found || !hasReceipt || fact.Status != sessionvo.AttemptCompleted || receipt.Status != sessionvo.ReceiptCompleted || receipt.EvidenceDurability != sessionvo.DurabilityDurable || len(receipt.PartialReasons) > 0 || fact.CapabilityProfile == nil || fact.CapabilityProfile.EvidenceContract != functionResultContract {
				missing(id, "claim_result_not_evaluable")
				continue
			}
			sources, err := reader.ReadEvidenceEventsByIDs(receipt.ObservedEvidenceRefs)
			if errors.Is(err, isessionstore.ErrEvidenceConfirmationLimit) {
				missing(id, "claim_evidence_budget_exceeded")
				continue
			}
			if err != nil {
				return nil, err
			}
			var source *ledgervo.Event
			var proof functionResultEvidence
			for i := range sources {
				e := &sources[i]
				var envelope struct {
					Payload functionResultEvidence `json:"payload"`
				}
				if e.ProducerID != "agent-retrieval" || e.EventType != "logic.execution.observed" || e.OperationID != fact.OperationID || e.Attempt != fact.Attempt || e.InteractionID != interaction.ID || e.ConversationID != interaction.ConversationID || e.RequestID != receipt.RequestID || e.TraceID != receipt.TraceID || json.Unmarshal(e.Envelope, &envelope) != nil || envelope.Payload.Contract != functionResultContract {
					continue
				}
				if source != nil {
					source = nil
					break
				}
				source = e
				proof = envelope.Payload
			}
			if source == nil || !validFunctionProof(fact, proof) {
				missing(id, "claim_result_not_evaluable")
				continue
			}
			conversation, ok := tx.PeekConversation(interaction.ConversationID)
			if !ok || !source.Owner.Equal(conversation.Owner) {
				missing(id, "claim_owner_mismatch")
				continue
			}
			event := materializedFunctionClaim(*source, proof, id, receipt.ID, *revision, uint64(len(revision.IncludedEventIDs)+1))
			ack, err := writer.AppendDerivedEvidence(event)
			if errors.Is(err, isessionstore.ErrEvidenceConfirmationLimit) {
				missing(id, "claim_evidence_budget_exceeded")
				continue
			}
			// Validation and immutable-record conflicts do not undo business
			// completion. Database failures still abort the atomic transaction.
			if ledgersvc.IsCode(err, ledgersvc.CodeInvalidEvent) ||
				errors.Is(err, ievidenceledger.ErrPayloadConflict) || errors.Is(err, ievidenceledger.ErrSequenceConflict) ||
				errors.Is(err, ievidenceledger.ErrCausalityConflict) || errors.Is(err, ievidenceledger.ErrOwnerMismatch) ||
				errors.Is(err, ievidenceledger.ErrMissingScope) {
				missing(id, "claim_result_not_evaluable")
				continue
			}
			if err != nil {
				return nil, err
			}
			if !ack.Durable {
				return nil, fmt.Errorf("core claim append was not accepted")
			}
			revision.IncludedEventIDs = appendUnique(revision.IncludedEventIDs, event.EventID)
			artifacts = appendUnique(artifacts, proof.ArtifactRef)
		}
	}
	if revision.Completeness == sessionvo.EvidencePartial && interaction.EvidenceStatus != sessionvo.EvidencePartial {
		interaction.EvidenceStatus = sessionvo.EvidencePartial
		tx.SaveInteraction(*interaction)
	}
	return artifacts, nil
}

// Check transport identity and exact persisted output, not business semantics.
func validFunctionProof(fact sessionvo.OperationCallFact, proof functionResultEvidence) bool {
	p := proof.Producer
	if !proof.Complete || fact.CapabilityProfile == nil || fact.Input.Mode != sessionvo.PayloadInline || fact.Output == nil || fact.Output.Mode != sessionvo.PayloadInline || len(fact.Output.Inline) > sessionvo.MaxInlinePayloadBytes || proof.OutputHash != ledgervo.CanonicalPayloadHash(fact.Output.Inline) {
		return false
	}
	if !strings.HasPrefix(proof.ArtifactRef, "artifact:") || len(proof.ArtifactRef) <= len("artifact:") || p.FunctionVersion == "" || p.FunctionVersion == "unversioned" || len(p.CodeSHA256) != 71 || !strings.HasPrefix(p.CodeSHA256, "sha256:") || fact.CapabilityProfile.ToolVersion != p.FunctionVersion {
		return false
	}
	if _, err := hex.DecodeString(p.CodeSHA256[7:]); err != nil {
		return false
	}
	var input struct {
		KN      string `json:"kn_id"`
		Box     string `json:"toolbox_id"`
		Tool    string `json:"tool_id"`
		Version string `json:"function_version"`
	}
	if json.Unmarshal(fact.Input.Inline, &input) != nil || input.KN != p.KnowledgeNetworkID || input.Box != p.ToolboxID || input.Tool != p.ToolID || input.Version != p.FunctionVersion {
		return false
	}
	var output struct {
		StatusCode int `json:"status_code"`
		Execution  struct {
			Box     string `json:"toolbox_id"`
			Tool    string `json:"tool_id"`
			Version string `json:"function_version"`
			Hash    string `json:"code_sha256"`
		} `json:"function_execution"`
		Body struct {
			Exit   json.RawMessage `json:"exit_code"`
			Result json.RawMessage `json:"result"`
		} `json:"body"`
	}
	if json.Unmarshal(fact.Output.Inline, &output) != nil || output.StatusCode != 200 || output.Execution.Box != p.ToolboxID || output.Execution.Tool != p.ToolID || output.Execution.Version != p.FunctionVersion || output.Execution.Hash != p.CodeSHA256 {
		return false
	}
	if len(output.Body.Exit) > 0 {
		var code *int
		if json.Unmarshal(output.Body.Exit, &code) != nil || code == nil || *code != 0 {
			return false
		}
	}
	result := output.Body.Result
	if len(result) == 0 || (result[0] != '{' && result[0] != '[') {
		return false
	}
	// Verify the opaque result snapshot against the stored artifact hash.
	var content any
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.UseNumber()
	if decoder.Decode(&content) != nil {
		return false
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(content) != nil {
		return false
	}
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return proof.ContentHash == "sha256:"+hex.EncodeToString(sum[:])
}

func materializedFunctionClaim(source ledgervo.Event, proof functionResultEvidence, id, receiptID string, revision sessionvo.AssemblyRevision, sequence uint64) ledgervo.Event {
	ref := sessionvo.EvidenceRef{Ref: proof.ArtifactRef, RefType: sessionvo.EvidenceRefArtifact, SourceInteractionID: source.InteractionID, SourceRevisionID: revision.ID, SourceOperationID: source.OperationID, ArtifactRef: proof.ArtifactRef, Version: proof.Producer.FunctionVersion, ContentHash: proof.ContentHash}
	support := sessionvo.ClaimSupport{TargetRef: ref.Ref, TargetType: sessionvo.SupportEvidence, SourceInteractionID: ref.SourceInteractionID, SourceRevisionID: ref.SourceRevisionID, SourceOperationID: ref.SourceOperationID, Version: ref.Version, ContentHash: ref.ContentHash, Role: "computed_from", Status: sessionvo.SupportAdopted}
	claim := sessionvo.Claim{ID: id, Type: functionResultClaimType, Materiality: sessionvo.ClaimMaterial, Status: sessionvo.ClaimAsserted, ContentArtifactRef: proof.ArtifactRef, RequiredSupportRoles: []string{"computed_from"}, Supports: []sessionvo.ClaimSupport{support}}
	sum := sha256.Sum256([]byte(revision.ID + "|" + id))
	envelope, _ := json.Marshal(map[string]any{"result_contract": functionResultContract, "source_event_id": source.EventID, "receipt_bound_operation_id": source.OperationID, "source_receipt_id": receiptID, "producer": proof.Producer, "result_artifact_ref": proof.ArtifactRef})
	emittedAt := revision.CreatedAt
	if source.EmittedAt.After(emittedAt) {
		emittedAt = source.EmittedAt
	}
	return ledgervo.Event{EventID: "evt_claim_" + hex.EncodeToString(sum[:16]), EventType: "claim.created", SchemaVersion: "3.0.0", PayloadHash: ledgervo.CanonicalPayloadHash(envelope), Owner: source.Owner, ConversationID: source.ConversationID, InteractionID: source.InteractionID, OperationID: source.OperationID, Attempt: source.Attempt, RequestID: source.RequestID, TraceID: source.TraceID, SpanID: source.SpanID, ProducerID: "agent-observability-core", ProducerStreamID: "core-claims:" + revision.ID, ProducerEpoch: 1, ProducerSequence: sequence, CausationEventIDs: []string{source.EventID}, StartedAt: source.StartedAt, ObservedAt: source.ObservedAt, EmittedAt: emittedAt, Envelope: envelope, BusinessRefs: source.BusinessRefs, ArtifactRefs: []string{proof.ArtifactRef}, Claims: []sessionvo.Claim{claim}, EvidenceRefs: []sessionvo.EvidenceRef{ref}}
}
