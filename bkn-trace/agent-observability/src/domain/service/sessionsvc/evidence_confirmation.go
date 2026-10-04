// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func freezeEvidenceCompletion(receipt sessionvo.Receipt, fact sessionvo.OperationCallFact, expectation *sessionvo.EvidenceExpectation) *sessionvo.EvidenceCompletion {
	completion := &sessionvo.EvidenceCompletion{
		OriginalDurability: receipt.EvidenceDurability, OriginalObservedRefs: cloneStrings(receipt.ObservedEvidenceRefs), OriginalPartialReasons: cloneStrings(receipt.PartialReasons),
	}
	if reason := invalidEvidenceExpectation(expectation, fact); reason != "" {
		completion.RejectionReason = reason
		completion.RejectedExpectationHash = evidenceExpectationHash(expectation)
		return completion
	}
	copied := *expectation
	if expectation.Events != nil {
		copied.Events = append(make([]sessionvo.ExpectedEvidenceEvent, 0, len(expectation.Events)), expectation.Events...)
	}
	completion.Expectation = &copied
	return completion
}

func evidenceExpectationHash(expectation *sessionvo.EvidenceExpectation) string {
	// The contract contains only strings, uint32 and bool. This fingerprint is
	// used only for rejected/oversized contracts, never business payloads.
	sum := sha256.New()
	_ = json.NewEncoder(sum).Encode(expectation)
	return hex.EncodeToString(sum.Sum(nil))
}

func invalidEvidenceExpectation(expectation *sessionvo.EvidenceExpectation, fact sessionvo.OperationCallFact) string {
	if len(expectation.Events) > sessionvo.MaxExpectedEvidenceEvents {
		return "evidence_expectation_limit_exceeded"
	}
	if !sessionvo.EvidenceExpectationShapeValid(expectation) {
		return "evidence_expectation_invalid"
	}
	if len(expectation.Events) == 0 {
		if receiptOnlyEvidenceContract(fact.CapabilityProfile) {
			return ""
		}
		// Finish has already validated the actual terminal error. A backend
		// failure may return before any event is planned; the failure receipt
		// itself is durable. Business-target/error-content checks remain separate.
		if fact.Status == sessionvo.AttemptFailed && fact.Error != nil {
			return ""
		}
		return "evidence_events_missing"
	}
	if fact.Status == sessionvo.AttemptCompleted && fact.CapabilityProfile != nil && fact.CapabilityProfile.Resolution == "matched" && !receiptOnlyEvidenceContract(fact.CapabilityProfile) {
		requiredType := requiredEvidenceEventType(fact.CapabilityProfile.EvidenceContract)
		if requiredType == "" {
			return "evidence_contract_unsupported"
		}
		if !slices.ContainsFunc(expectation.Events, func(e sessionvo.ExpectedEvidenceEvent) bool { return e.EventType == requiredType }) {
			return "evidence_required_event_missing"
		}
	}
	return ""
}

func receiptOnlyEvidenceContract(profile *sessionvo.CapabilityProfile) bool {
	if profile == nil {
		return false
	}
	switch profile.EvidenceContract {
	case "execution_only":
		return true
	case "managed_function_execution/v1":
		return profile.Resolution == "matched"
	default:
		return false
	}
}

func requiredEvidenceEventType(contract string) string {
	switch contract {
	case "ontology_schema_snapshot/v1":
		return "ontology.schema.snapshot"
	case "ontology_metric_result/v1":
		return "metric.query.completed"
	case "ontology_result/v1", "ontology_retrieval/v1", "ontology_subgraph/v1":
		return "retrieval.completed"
	case "data_query/v1", "mapped_sql_result/v1", "semantic_query_descriptor/v1":
		return "data.query.observed"
	default:
		return ""
	}
}

func evidenceFinishReplayMatches(receipt sessionvo.Receipt, operation sessionvo.Operation, fact sessionvo.OperationCallFact, terminal sessionvo.PayloadEnvelope, command FinishAttemptCommand, status sessionvo.ReceiptStatus, retryable bool) bool {
	completion := fact.EvidenceCompletion
	if completion == nil {
		return command.EvidenceExpectation == nil && receiptTerminalMatches(receipt, operation, fact, terminal, command, status, retryable)
	}
	if command.EvidenceExpectation == nil {
		return false
	}
	if completion.RejectionReason != "" {
		if completion.RejectedExpectationHash != evidenceExpectationHash(command.EvidenceExpectation) {
			return false
		}
	} else if !reflect.DeepEqual(completion.Expectation, command.EvidenceExpectation) {
		return false
	}
	receipt.EvidenceDurability = completion.OriginalDurability
	receipt.ObservedEvidenceRefs = completion.OriginalObservedRefs
	receipt.PartialReasons = completion.OriginalPartialReasons
	return receiptTerminalMatches(receipt, operation, fact, terminal, command, status, retryable)
}

// ReconcileEvidence is an internal after-Ledger-commit transition. The callback
// only identifies the attempt; the frozen set is checked against stored rows.
func (s *Service) ReconcileEvidence(ctx context.Context, callback ledgervo.Event) error {
	if callback.OperationID == "" || callback.Attempt == 0 {
		return nil
	}
	return s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		operationRef, found := tx.PeekOperation(callback.OperationID)
		if !found {
			return nil
		}
		conversationRef, found := tx.PeekConversation(operationRef.ConversationID)
		if !found {
			return errors.New("evidence confirmation conversation invariant violated")
		}
		if _, err := ownedConversation(tx, conversationRef.Owner, conversationRef.ID); err != nil {
			return err
		}
		interaction, found := tx.FindInteraction(operationRef.InteractionID)
		if !found {
			return errors.New("evidence confirmation interaction invariant violated")
		}
		operation, found := tx.FindOperation(operationRef.ID)
		if !found || operation.InteractionID != interaction.ID || operation.ConversationID != conversationRef.ID {
			return errors.New("evidence confirmation operation invariant violated")
		}
		receipt, found := tx.FindReceiptByOperationAttempt(operation.ID, callback.Attempt)
		if !found || receipt.Status == sessionvo.ReceiptPending {
			return nil
		}
		fact, found := tx.FindOperationCallFact(operation.ID, callback.Attempt)
		if !found {
			return errors.New("evidence confirmation call fact invariant violated")
		}
		if fact.EvidenceCompletion == nil {
			return nil
		} // Legacy pending is not inferred.
		if fact.ReceiptID != receipt.ID || fact.ConversationID != receipt.ConversationID || fact.InteractionID != receipt.InteractionID || !receipt.Owner.Equal(conversationRef.Owner) {
			return errors.New("evidence confirmation receipt invariant violated")
		}
		// Only a stored, identity-checked durable receipt may skip duplicate
		// confirmation. The first finish still validates its claimed durability.
		if receipt.EvidenceDurability == sessionvo.DurabilityDurable {
			return nil
		}
		changed, settled, err := s.confirmReceiptEvidence(tx, &receipt, fact)
		if err != nil || !changed {
			return err
		}
		receipt.RowVersion++
		tx.SaveReceipt(receipt)
		if err := s.appendProjection(tx, "receipt", receipt.ID, "receipt.evidence.confirmed", receipt); err != nil {
			return err
		}
		if settled && interaction.IsTerminal() && interaction.ClosureManifest != nil && manifestContainsReceipt(*interaction.ClosureManifest, receipt.ID) {
			next := evidenceStatusAtTermination(tx, *interaction.ClosureManifest)
			if next != sessionvo.EvidenceAssembling {
				interaction.EvidenceStatus = next
				interaction.RowVersion++
				interaction.UpdatedAt = tx.Now()
				tx.SaveInteraction(interaction)
				if err := s.freezeAssemblyRevision(tx, interaction, *interaction.ClosureManifest, "evidence_confirmed"); err != nil {
					return err
				}
				if err := s.appendProjection(tx, "interaction", interaction.ID, "interaction.evidence.revised", interaction); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Service) confirmReceiptEvidence(tx isessionstore.Transaction, receipt *sessionvo.Receipt, fact sessionvo.OperationCallFact) (changed, settled bool, err error) {
	completion := fact.EvidenceCompletion
	if completion == nil {
		return false, false, nil
	}
	var durability sessionvo.EvidenceDurability
	refs := make([]string, 0)
	reasons := cloneStrings(completion.OriginalPartialReasons)
	if completion.RejectionReason != "" {
		durability = sessionvo.DurabilityFailed
		reasons = appendUnique(reasons, completion.RejectionReason)
		settled = true
	} else {
		reader, ok := tx.(isessionstore.EvidenceConfirmationTransaction)
		if !ok {
			return false, false, errors.New("session store lacks evidence confirmation reader")
		}
		expected := completion.Expectation.Events
		ids := make([]string, 0, len(expected))
		dropped := false
		for _, item := range expected {
			if item.PublishDisposition == "dropped" {
				dropped = true
				reasons = appendUnique(reasons, "evidence_publish_dropped")
				continue
			}
			ids = append(ids, item.EventID)
		}
		events, readErr := reader.ReadEvidenceEventsByIDs(ids)
		if readErr == nil {
			totalBytes := 0
			for _, event := range events {
				totalBytes += len(event.Envelope)
				if totalBytes > sessionvo.MaxEvidenceConfirmationBytes {
					readErr = isessionstore.ErrEvidenceConfirmationLimit
					break
				}
			}
		}
		if errors.Is(readErr, isessionstore.ErrEvidenceConfirmationLimit) {
			durability = sessionvo.DurabilityFailed
			reasons = appendUnique(reasons, "evidence_confirmation_limit_exceeded")
			settled = true
		} else if readErr != nil {
			return false, false, readErr
		} else {
			stored := make(map[string]ledgervo.Event, len(events))
			for _, event := range events {
				stored[event.EventID] = event
			}
			missing, mismatch := false, false
			for _, item := range expected {
				if item.PublishDisposition != "accepted" {
					continue
				}
				event, found := stored[item.EventID]
				if !found {
					missing = true
					continue
				}
				if !evidenceEventMatches(event, item, *receipt, fact) {
					mismatch = true
					continue
				}
				refs = append(refs, item.EventID)
			}
			settled = !missing
			switch {
			case mismatch:
				durability = sessionvo.DurabilityFailed
				reasons = appendUnique(reasons, "evidence_binding_mismatch")
			case dropped:
				durability = sessionvo.DurabilityFailed
			case missing:
				durability = sessionvo.DurabilityPending
			default:
				durability = sessionvo.DurabilityDurable
			}
		}
	}
	if !slices.Equal(receipt.ObservedEvidenceRefs, refs) && evidenceReferenceCount(tx.ListReceipts(receipt.InteractionID), receipt.ID, refs) > s.capacity.MaxEvidenceRefsPerInteraction {
		refs = []string{}
		durability = sessionvo.DurabilityFailed
		reasons = appendUnique(reasons, "evidence_reference_capacity_exceeded")
		settled = true
	}
	if durability == sessionvo.DurabilityFailed {
		reasons = appendUnique(reasons, "evidence_durability_failed")
	}
	changed = receipt.EvidenceDurability != durability || !slices.Equal(receipt.ObservedEvidenceRefs, refs) || !slices.Equal(receipt.PartialReasons, reasons)
	receipt.EvidenceDurability, receipt.ObservedEvidenceRefs, receipt.PartialReasons = durability, refs, reasons
	return changed, settled, nil
}

func evidenceEventMatches(event ledgervo.Event, expected sessionvo.ExpectedEvidenceEvent, receipt sessionvo.Receipt, fact sessionvo.OperationCallFact) bool {
	if event.EventID != expected.EventID || event.EventType != expected.EventType || event.PayloadHash != expected.PayloadHash || event.ProducerID != expected.ProducerID ||
		!event.Owner.Equal(receipt.Owner) || event.ConversationID != receipt.ConversationID || event.InteractionID != receipt.InteractionID || event.OperationID != receipt.OperationID || event.Attempt != receipt.Attempt ||
		event.RequestID != receipt.RequestID || event.TraceID != receipt.TraceID || event.SpanID != fact.SpanID {
		return false
	}
	var inner map[string]json.RawMessage
	if json.Unmarshal(event.Envelope, &inner) != nil || inner == nil {
		return false
	}
	for key, expectedValue := range map[string]string{
		"event_id": event.EventID, "event_type": event.EventType, "conversation_id": receipt.ConversationID, "interaction_id": receipt.InteractionID,
		"operation_id": receipt.OperationID, "bkn.request.id": receipt.RequestID, "request_id": receipt.RequestID, "trace_id": receipt.TraceID, "span_id": fact.SpanID,
	} {
		if raw, exists := inner[key]; exists {
			var actual string
			if json.Unmarshal(raw, &actual) != nil || actual != expectedValue {
				return false
			}
		}
	}
	if raw, exists := inner["attempt"]; exists {
		var attempt uint32
		if json.Unmarshal(raw, &attempt) != nil || attempt != receipt.Attempt {
			return false
		}
	}
	if raw, exists := inner["owner"]; exists {
		var owner sessionvo.Owner
		if json.Unmarshal(raw, &owner) != nil || !owner.Equal(receipt.Owner) {
			return false
		}
	}
	return true
}
