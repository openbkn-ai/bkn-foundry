// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

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

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

const bomIntersectionContract = "bom.structure_intersection/v1"
const bomIntersectionSourceModule = "context-loader-bom-intersection/v1"

type bomProducer struct {
	KnowledgeNetworkID string `json:"knowledge_network_id"`
	ToolboxID          string `json:"toolbox_id"`
	ToolID             string `json:"tool_id"`
	FunctionVersion    string `json:"function_version"`
	CodeSHA256         string `json:"code_sha256"`
}

type bomConclusion struct {
	Products          []string `json:"products"`
	IncludeSubstitute bool     `json:"include_substitute"`
	Depth             *int     `json:"depth"`
	SharedCodes       []string `json:"shared_codes"`
	SharedCount       int      `json:"shared_count"`
	Caliber           string   `json:"caliber"`
}

type bomResultEvidence struct {
	Contract    string        `json:"result_contract"`
	Producer    bomProducer   `json:"producer"`
	Conclusion  bomConclusion `json:"conclusion"`
	ArtifactRef string        `json:"result_artifact_ref"`
	ContentHash string        `json:"content_hash"`
	OutputHash  string        `json:"output_hash"`
}

func bomClaimID(operationID string, attempt uint32) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", operationID, attempt, bomIntersectionContract)))
	return "claim_bom_" + hex.EncodeToString(sum[:16])
}

// Summaries deliberately omit input/output bodies. Only registered BOM attempts
// are loaded in full, and only while sealing their immutable evidence revision.
func derivedBOMClaimIDs(tx isessionstore.Transaction, interactionID string, includeFailed bool) []string {
	var result []string
	for _, fact := range tx.ListOperationCallSummaries(interactionID) {
		if fact.SourceModule == bomIntersectionSourceModule && (includeFailed || fact.Status != sessionvo.AttemptFailed) {
			result = appendUnique(result, bomClaimID(fact.OperationID, fact.Attempt))
		}
	}
	slices.Sort(result)
	return result
}

func (s *Service) sealBOMClaims(tx isessionstore.Transaction, interaction *sessionvo.Interaction, revision *sessionvo.AssemblyRevision) ([]string, error) {
	if interaction.ClosureManifest == nil {
		return nil, nil
	}
	var artifacts []string
	var expected []string
	for _, id := range interaction.ClosureManifest.Claims {
		if strings.HasPrefix(id, "claim_bom_") {
			expected = append(expected, id)
		}
	}
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
		summaries := tx.ListOperationCallSummaries(interaction.ID)
		for _, id := range expected {
			var summary *sessionvo.OperationCallSummary
			for i := range summaries {
				f := &summaries[i]
				if f.SourceModule == bomIntersectionSourceModule && bomClaimID(f.OperationID, f.Attempt) == id {
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
			if !found || !hasReceipt || fact.Status != sessionvo.AttemptCompleted || receipt.Status != sessionvo.ReceiptCompleted || receipt.EvidenceDurability != sessionvo.DurabilityDurable || len(receipt.PartialReasons) > 0 || fact.CapabilityProfile == nil || fact.CapabilityProfile.EvidenceContract != bomIntersectionContract {
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
			var proof bomResultEvidence
			for i := range sources {
				e := &sources[i]
				var envelope struct {
					Payload bomResultEvidence `json:"payload"`
				}
				if e.ProducerID != "agent-retrieval" || e.EventType != "logic.execution.observed" || e.OperationID != fact.OperationID || e.Attempt != fact.Attempt || e.InteractionID != interaction.ID || e.ConversationID != interaction.ConversationID || e.RequestID != receipt.RequestID || e.TraceID != receipt.TraceID || json.Unmarshal(e.Envelope, &envelope) != nil || envelope.Payload.Contract != bomIntersectionContract {
					continue
				}
				if source != nil {
					source = nil
					break
				}
				source = e
				proof = envelope.Payload
			}
			if source == nil || !validBOMProof(fact, proof) {
				missing(id, "claim_result_not_evaluable")
				continue
			}
			conversation, ok := tx.PeekConversation(interaction.ConversationID)
			if !ok || !source.Owner.Equal(conversation.Owner) {
				missing(id, "claim_owner_mismatch")
				continue
			}
			event := materializedBOMClaim(*source, proof, id, receipt.ID, *revision, uint64(len(revision.IncludedEventIDs)+1))
			ack, err := writer.AppendDerivedEvidence(event)
			if errors.Is(err, isessionstore.ErrEvidenceConfirmationLimit) {
				missing(id, "claim_evidence_budget_exceeded")
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

func validBOMProof(fact sessionvo.OperationCallFact, proof bomResultEvidence) bool {
	p, c := proof.Producer, proof.Conclusion
	if fact.CapabilityProfile == nil || fact.Input.Mode != sessionvo.PayloadInline || fact.Output == nil ||
		fact.Output.Mode != sessionvo.PayloadInline || len(fact.Output.Inline) > sessionvo.MaxInlinePayloadBytes ||
		proof.OutputHash != ledgervo.CanonicalPayloadHash(fact.Output.Inline) {
		return false
	}
	if !strings.HasPrefix(proof.ArtifactRef, "artifact:") || len(proof.ArtifactRef) <= len("artifact:") ||
		p.FunctionVersion == "" || p.FunctionVersion == "unversioned" || len(p.CodeSHA256) != 71 ||
		!strings.HasPrefix(p.CodeSHA256, "sha256:") || fact.CapabilityProfile.ToolVersion != p.FunctionVersion {
		return false
	}
	if len(c.Products) < 2 || c.Caliber != "structure_intersect_only" || c.SharedCodes == nil ||
		c.SharedCount != len(c.SharedCodes) || c.SharedCount > 5000 || !slices.IsSorted(c.SharedCodes) {
		return false
	}
	if _, err := hex.DecodeString(p.CodeSHA256[7:]); err != nil {
		return false
	}
	for i, code := range c.SharedCodes {
		if code == "" || strings.TrimSpace(code) != code || i > 0 && code == c.SharedCodes[i-1] {
			return false
		}
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
			ExitCode int `json:"exit_code"`
			Result   struct {
				bomConclusion
				DataSource struct {
					KnowledgeNetworkID string   `json:"knowledge_network_id"`
					Datasets           []string `json:"loaded_datasets"`
				} `json:"data_source"`
			} `json:"result"`
		} `json:"body"`
	}
	if json.Unmarshal(fact.Output.Inline, &output) != nil || output.StatusCode != 200 || output.Body.ExitCode != 0 || output.Execution.Box != p.ToolboxID || output.Execution.Tool != p.ToolID || output.Execution.Version != p.FunctionVersion || output.Execution.Hash != p.CodeSHA256 {
		return false
	}
	var shape struct {
		Body struct {
			Result struct {
				Count      *int  `json:"shared_count"`
				Substitute *bool `json:"include_substitute"`
			} `json:"result"`
			Exit json.RawMessage `json:"exit_code"`
		} `json:"body"`
	}
	if json.Unmarshal(fact.Output.Inline, &shape) != nil || shape.Body.Result.Count == nil || shape.Body.Result.Substitute == nil {
		return false
	}
	if len(shape.Body.Exit) > 0 {
		var code *int
		if json.Unmarshal(shape.Body.Exit, &code) != nil || code == nil || *code != 0 {
			return false
		}
	}
	if output.Body.Result.DataSource.KnowledgeNetworkID != p.KnowledgeNetworkID || !slices.Equal(output.Body.Result.DataSource.Datasets, []string{"bom"}) {
		return false
	}
	left, _ := json.Marshal(c)
	right, _ := json.Marshal(output.Body.Result.bomConclusion)
	if !bytes.Equal(left, right) {
		return false
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	var canonicalContent any
	if json.Unmarshal(left, &canonicalContent) != nil {
		return false
	}
	if encoder.Encode(canonicalContent) != nil {
		return false
	}
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return proof.ContentHash == "sha256:"+hex.EncodeToString(sum[:])
}

func materializedBOMClaim(source ledgervo.Event, proof bomResultEvidence, id, receiptID string, revision sessionvo.AssemblyRevision, sequence uint64) ledgervo.Event {
	ref := sessionvo.EvidenceRef{Ref: proof.ArtifactRef, RefType: sessionvo.EvidenceRefArtifact, SourceInteractionID: source.InteractionID, SourceRevisionID: revision.ID, SourceOperationID: source.OperationID, ArtifactRef: proof.ArtifactRef, Version: proof.Producer.FunctionVersion, ContentHash: proof.ContentHash}
	support := sessionvo.ClaimSupport{TargetRef: ref.Ref, TargetType: sessionvo.SupportEvidence, SourceInteractionID: ref.SourceInteractionID, SourceRevisionID: ref.SourceRevisionID, SourceOperationID: ref.SourceOperationID, Version: ref.Version, ContentHash: ref.ContentHash, Role: "computed_from", Status: sessionvo.SupportAdopted}
	claim := sessionvo.Claim{ID: id, Type: bomIntersectionContract, Materiality: sessionvo.ClaimMaterial, Status: sessionvo.ClaimAsserted, ContentArtifactRef: proof.ArtifactRef, RequiredSupportRoles: []string{"computed_from"}, Supports: []sessionvo.ClaimSupport{support}}
	sum := sha256.Sum256([]byte(revision.ID + "|" + id))
	envelope, _ := json.Marshal(map[string]any{"result_contract": bomIntersectionContract, "source_event_id": source.EventID, "receipt_bound_operation_id": source.OperationID, "source_receipt_id": receiptID, "producer": proof.Producer, "result_artifact_ref": proof.ArtifactRef})
	return ledgervo.Event{EventID: "evt_claim_" + hex.EncodeToString(sum[:16]), EventType: "claim.created", SchemaVersion: "3.0.0", PayloadHash: ledgervo.CanonicalPayloadHash(envelope), Owner: source.Owner, ConversationID: source.ConversationID, InteractionID: source.InteractionID, OperationID: source.OperationID, Attempt: source.Attempt, RequestID: source.RequestID, TraceID: source.TraceID, SpanID: source.SpanID, ProducerID: "agent-observability-core", ProducerStreamID: "core-claims:" + revision.ID, ProducerEpoch: 1, ProducerSequence: sequence, CausationEventIDs: []string{source.EventID}, StartedAt: source.StartedAt, ObservedAt: source.ObservedAt, EmittedAt: revision.CreatedAt, Envelope: envelope, BusinessRefs: source.BusinessRefs, ArtifactRefs: []string{proof.ArtifactRef}, Claims: []sessionvo.Claim{claim}, EvidenceRefs: []sessionvo.EvidenceRef{ref}}
}
