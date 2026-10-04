// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionsvc_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// Keep the RED tests buildable before the new optional capability and service
// method exist. These structural interfaces are the proposed production ports.
type confirmationTestStore struct {
	*sessionstore.Store
	events         map[string]ledgervo.Event
	readErr        error
	reads          int
	externalReader func([]string) ([]ledgervo.Event, error)
}
type confirmationTestTx struct {
	isessionstore.Transaction
	store *confirmationTestStore
}

func (s *confirmationTestStore) WithinTransaction(ctx context.Context, fn func(isessionstore.Transaction) error) error {
	return s.Store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { return fn(confirmationTestTx{tx, s}) })
}
func (tx confirmationTestTx) ReadEvidenceEventsByIDs(ids []string) ([]ledgervo.Event, error) {
	tx.store.reads++
	if tx.store.externalReader != nil {
		return tx.store.externalReader(ids)
	}
	if tx.store.readErr != nil {
		return nil, tx.store.readErr
	}
	result := make([]ledgervo.Event, 0, len(ids))
	for _, id := range ids {
		if event, ok := tx.store.events[id]; ok {
			result = append(result, event)
		}
	}
	return result, nil
}

type confirmationFixture struct {
	store        *confirmationTestStore
	service      *sessionsvc.Service
	owner        sessionvo.Owner
	conversation sessionvo.Conversation
	interaction  sessionvo.Interaction
	operation    sessionvo.Operation
	receipt      sessionvo.Receipt
	finish       sessionsvc.FinishAttemptCommand
	now          *time.Time
}

func newConfirmationFixture(t *testing.T, overrides ...*sessionvo.CapabilityProfile) confirmationFixture {
	t.Helper()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	store := &confirmationTestStore{Store: sessionstore.NewWithClock(func() time.Time { return now }), events: map[string]ledgervo.Event{}}
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	owner.DelegationID = "delegation-confirmation"
	conversation := mustEnsureConversation(t, service, owner, "confirmation")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "confirmation-start"})
	if err != nil {
		t.Fatal(err)
	}
	profile := &sessionvo.CapabilityProfile{
		ManifestID: "openbkn.context-loader.mcp", ManifestVersion: "0.1.5", CanonicalToolName: "ontology-query", ToolVersion: "1.0.0",
		InputSchemaDigest: "sha256:input", OutputSchemaDigest: "sha256:output", ExecutionRole: "semantic_query", EvidenceContract: "ontology_result/v1",
		ChildEvidencePolicy: "cover_physical_descendants", FailurePolicy: "preserve_execution_and_downgrade", Resolution: "matched",
	}
	if len(overrides) > 0 {
		profile = overrides[0]
	}
	toolName := "list_knowledge_networks"
	if profile != nil {
		toolName = profile.CanonicalToolName
	}
	operation, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID, OperationKey: "confirmation-call", ToolName: toolName,
		Input: operationInput("confirmation"), Required: true, CapabilityProfile: profile, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	return confirmationFixture{store, service, owner, conversation, interaction, operation, receipt, sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, ReceiptID: receipt.ID, Attempt: receipt.Attempt, Output: operationOutput("<>& confirmation result"),
		RequestID: "request-confirmation", TraceID: validTraceIDOne, SpanID: "1234567890abcdef", EvidenceDurability: sessionvo.DurabilityPending,
	}, &now}
}
func (f confirmationFixture) event(t *testing.T, id string) ledgervo.Event {
	t.Helper()
	envelope, err := json.Marshal(map[string]any{
		"event_id": id, "event_type": "retrieval.completed", "owner": f.owner, "conversation_id": f.conversation.ID,
		"interaction_id": f.interaction.ID, "operation_id": f.operation.ID, "attempt": f.receipt.Attempt,
		"bkn.request.id": f.finish.RequestID, "trace_id": f.finish.TraceID, "span_id": f.finish.SpanID,
		"payload": map[string]any{"complete": true, "truncated": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ledgervo.Event{EventID: id, EventType: "retrieval.completed", SchemaVersion: "3.0.0", PayloadHash: ledgervo.CanonicalPayloadHash(envelope),
		Owner: f.owner, ConversationID: f.conversation.ID, InteractionID: f.interaction.ID, OperationID: f.operation.ID, Attempt: f.receipt.Attempt,
		RequestID: f.finish.RequestID, TraceID: f.finish.TraceID, SpanID: f.finish.SpanID, ProducerID: "context-loader", Envelope: envelope}
}
func expectedConfirmationEvent(event ledgervo.Event, disposition string) map[string]any {
	return map[string]any{"event_id": event.EventID, "event_type": event.EventType, "payload_hash": event.PayloadHash, "producer_id": event.ProducerID, "publish_disposition": disposition}
}
func withConfirmationExpectation(t *testing.T, command sessionsvc.FinishAttemptCommand, events ...map[string]any) sessionsvc.FinishAttemptCommand {
	t.Helper()
	// Domain commands use Go names; the nested object uses the public wire names.
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	value["EvidenceExpectation"], err = json.Marshal(map[string]any{"version": 1, "closed": true, "events": events})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &command); err != nil {
		t.Fatal(err)
	}
	return command
}
func confirmEvidence(t *testing.T, service *sessionsvc.Service, event ledgervo.Event) error {
	t.Helper()
	confirmer, ok := any(service).(interface {
		ReconcileEvidence(context.Context, ledgervo.Event) error
	})
	if !ok {
		t.Fatal("Core lacks the independent durable evidence confirmation transition")
	}
	return confirmer.ReconcileEvidence(context.Background(), event)
}
func confirmationReceipt(t *testing.T, f confirmationFixture) sessionvo.Receipt {
	t.Helper()
	receipt, err := f.service.GetReceipt(context.Background(), f.owner, f.receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestEvidenceConfirmationEventFirst(t *testing.T) {
	f := newConfirmationFixture(t)
	event := f.event(t, "event-first")
	f.store.events[event.EventID] = event
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
	_, receipt, err := f.service.CompleteOperationAttempt(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.EvidenceDurability != sessionvo.DurabilityDurable || !reflect.DeepEqual(receipt.ObservedEvidenceRefs, []string{event.EventID}) {
		t.Fatalf("committed event-first evidence must be confirmed during first finish, got durability=%s refs=%v", receipt.EvidenceDurability, receipt.ObservedEvidenceRefs)
	}
}
func TestEvidenceConfirmationReceiptFirstAndDuplicate(t *testing.T) {
	f := newConfirmationFixture(t)
	event := f.event(t, "receipt-first")
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
	_, before, err := f.service.CompleteOperationAttempt(context.Background(), command)
	if err != nil || before.EvidenceDurability != sessionvo.DurabilityPending {
		t.Fatalf("finish pending: %#v %v", before, err)
	}
	f.store.events[event.EventID] = event
	if err := confirmEvidence(t, f.service, event); err != nil {
		t.Fatal(err)
	}
	after := confirmationReceipt(t, f)
	if after.EvidenceDurability != sessionvo.DurabilityDurable || !reflect.DeepEqual(after.ObservedEvidenceRefs, []string{event.EventID}) {
		t.Fatalf("receipt-first confirmation = %#v", after)
	}
	if err := confirmEvidence(t, f.service, event); err != nil {
		t.Fatal(err)
	}
	replay := confirmationReceipt(t, f)
	if replay.RowVersion != after.RowVersion {
		t.Fatalf("duplicate confirmation changed row version %d -> %d", after.RowVersion, replay.RowVersion)
	}
}
func TestEvidenceConfirmationRequiresTheClosedWholeSet(t *testing.T) {
	f := newConfirmationFixture(t)
	first, second := f.event(t, "whole-first"), f.event(t, "whole-second")
	f.store.events[first.EventID] = first
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(first, "accepted"), expectedConfirmationEvent(second, "accepted"))
	_, receipt, err := f.service.CompleteOperationAttempt(context.Background(), command)
	if err != nil || receipt.EvidenceDurability != sessionvo.DurabilityPending {
		t.Fatalf("one of two must remain pending: %#v %v", receipt, err)
	}
	if f.store.reads == 0 {
		t.Fatal("finish never examined the stored evidence set")
	}
	f.store.events[second.EventID] = second
	if err := confirmEvidence(t, f.service, second); err != nil {
		t.Fatal(err)
	}
	receipt = confirmationReceipt(t, f)
	if receipt.EvidenceDurability != sessionvo.DurabilityDurable || len(receipt.ObservedEvidenceRefs) != 2 {
		t.Fatalf("closed set not confirmed: %#v", receipt)
	}
}
func TestEvidenceConfirmationPartialPublishCannotBecomeDurable(t *testing.T) {
	f := newConfirmationFixture(t)
	accepted, dropped := f.event(t, "partial-accepted"), f.event(t, "partial-dropped")
	f.store.events[accepted.EventID] = accepted
	drop := expectedConfirmationEvent(dropped, "dropped")
	drop["payload_hash"] = "" // Serialization failures may lack a prepared hash.
	drop["drop_reason"] = "serialization_failed"
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(accepted, "accepted"), drop)
	_, receipt, err := f.service.CompleteOperationAttempt(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.EvidenceDurability != sessionvo.DurabilityFailed {
		t.Fatalf("one accepted does not cover one dropped: %s", receipt.EvidenceDurability)
	}
	if !reflect.DeepEqual(receipt.ObservedEvidenceRefs, []string{accepted.EventID}) {
		t.Fatalf("confirmed accepted subset missing: %v", receipt.ObservedEvidenceRefs)
	}
}
func TestEvidenceConfirmationPreservesOriginalFinishReplay(t *testing.T) {
	f := newConfirmationFixture(t)
	event := f.event(t, "original-finish")
	f.store.events[event.EventID] = event
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
	_, receipt, err := f.service.CompleteOperationAttempt(context.Background(), command)
	if err != nil || receipt.EvidenceDurability != sessionvo.DurabilityDurable {
		t.Fatalf("initial confirmation = %#v %v", receipt, err)
	}
	_, replayed, err := f.service.CompleteOperationAttempt(context.Background(), command)
	if err != nil || replayed.RowVersion != receipt.RowVersion {
		t.Fatalf("original pending finish must replay after internal confirmation: %#v %v", replayed, err)
	}
	mutations := map[string]func(*sessionsvc.FinishAttemptCommand){
		"durability": func(c *sessionsvc.FinishAttemptCommand) { c.EvidenceDurability = sessionvo.DurabilityDurable },
		"refs":       func(c *sessionsvc.FinishAttemptCommand) { c.ObservedEvidenceRefs = []string{event.EventID} },
		"output":     func(c *sessionsvc.FinishAttemptCommand) { c.Output = operationOutput("different") },
		"request":    func(c *sessionsvc.FinishAttemptCommand) { c.RequestID = "different" },
		"trace":      func(c *sessionsvc.FinishAttemptCommand) { c.TraceID = validTraceIDTwo },
		"span":       func(c *sessionsvc.FinishAttemptCommand) { c.SpanID = "different" },
		"partial":    func(c *sessionsvc.FinishAttemptCommand) { c.PartialReasons = []string{"different"} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := command
			mutate(&changed)
			if _, _, err := f.service.CompleteOperationAttempt(context.Background(), changed); !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
				t.Fatalf("altered finish must conflict: %v", err)
			}
		})
	}
	different := withConfirmationExpectation(t, command, expectedConfirmationEvent(f.event(t, "other-id"), "accepted"))
	if _, _, err := f.service.CompleteOperationAttempt(context.Background(), different); !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
		t.Fatalf("changed closed set must conflict: %v", err)
	}
	if _, _, err := f.service.CompleteOperationAttempt(context.Background(), f.finish); !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
		t.Fatalf("removed closed set must conflict: %v", err)
	}
}
func TestEvidenceConfirmationReadFailureRollsBackFinish(t *testing.T) {
	f := newConfirmationFixture(t)
	event := f.event(t, "read-error")
	f.store.readErr = errors.New("confirmation database unavailable")
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
	if _, _, err := f.service.CompleteOperationAttempt(context.Background(), command); !errors.Is(err, f.store.readErr) {
		t.Fatalf("stored Ledger read failure must propagate, got %v", err)
	}
}
func TestEvidenceConfirmationUsesStoredIdentityRatherThanCallback(t *testing.T) {
	f := newConfirmationFixture(t)
	event := f.event(t, "stored-binding")
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
	if _, _, err := f.service.CompleteOperationAttempt(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	corrupted := event
	corrupted.TraceID = validTraceIDTwo
	f.store.events[event.EventID] = corrupted
	if err := confirmEvidence(t, f.service, event); err != nil {
		t.Fatal(err)
	}
	receipt := confirmationReceipt(t, f)
	if receipt.EvidenceDurability == sessionvo.DurabilityDurable || len(receipt.ObservedEvidenceRefs) != 0 {
		t.Fatalf("callback must not override mismatching committed identity: %#v", receipt)
	}
}
func TestEvidenceConfirmationMetadataIsPersistedOutsideBusinessPayload(t *testing.T) {
	f := newConfirmationFixture(t)
	event := f.event(t, "stored-metadata")
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
	if _, _, err := f.service.CompleteOperationAttempt(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	err := f.store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		fact, found := tx.FindOperationCallFact(f.operation.ID, f.receipt.Attempt)
		if !found {
			t.Fatal("call fact missing")
		}
		encoded, err := json.Marshal(fact)
		if err != nil {
			return err
		}
		if !strings.Contains(string(encoded), `"evidence_completion"`) {
			t.Fatalf("immutable expectation not persisted with terminal call fact: %s", encoded)
		}
		if fact.Output == nil || !reflect.DeepEqual(*fact.Output, command.Output) {
			t.Fatal("confirmation metadata altered the business payload envelope")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestEvidenceConfirmationAfterDeadlineCreatesNewImmutableRevision(t *testing.T) {
	f := newConfirmationFixture(t)
	event := f.event(t, "deadline-late")
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
	if _, _, err := f.service.CompleteOperationAttempt(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	deadline := f.now.Add(-time.Second)
	terminated, err := f.service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: f.owner, InteractionID: f.interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "deadline-terminate", LeaseToken: f.interaction.LeaseToken, LeaseEpoch: f.interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{Version: "1", CompletionReason: "answer_returned", AssemblerDeadline: &deadline,
			ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: f.operation.ID, Required: true}}, ExpectedReceipts: []sessionvo.ExpectedReceipt{{ReceiptID: f.receipt.ID, Required: true}}},
	})
	if err != nil || terminated.EvidenceStatus != sessionvo.EvidencePartial {
		t.Fatalf("deadline termination: %#v %v", terminated, err)
	}
	before, err := f.service.ListAssemblyRevisions(context.Background(), f.owner, f.interaction.ID)
	if err != nil || len(before) != 1 {
		t.Fatalf("deadline revision: %#v %v", before, err)
	}
	f.store.events[event.EventID] = event
	if err := confirmEvidence(t, f.service, event); err != nil {
		t.Fatal(err)
	}
	after, err := f.service.ListAssemblyRevisions(context.Background(), f.owner, f.interaction.ID)
	if err != nil || len(after) != 2 {
		t.Fatalf("late evidence must add revision: %#v %v", after, err)
	}
	if !reflect.DeepEqual(after[0], before[0]) || after[1].ParentRevisionID != before[0].ID || after[1].Completeness != sessionvo.EvidenceComplete {
		t.Fatalf("invalid immutable chain: %#v", after)
	}
	if err := confirmEvidence(t, f.service, event); err != nil {
		t.Fatal(err)
	}
	duplicates, err := f.service.ListAssemblyRevisions(context.Background(), f.owner, f.interaction.ID)
	if err != nil || len(duplicates) != 2 {
		t.Fatalf("duplicate emitted another revision: %#v %v", duplicates, err)
	}
}

func TestEvidenceConfirmationAllStoredBindingsMustMatch(t *testing.T) {
	mutations := map[string]func(*ledgervo.Event){
		"owner_principal":  func(e *ledgervo.Event) { e.Owner.ApplicationPrincipalID = "other" },
		"owner_type":       func(e *ledgervo.Event) { e.Owner.EffectiveSubjectType = sessionvo.SubjectService },
		"owner_subject":    func(e *ledgervo.Event) { e.Owner.EffectiveSubjectID = "other" },
		"owner_delegation": func(e *ledgervo.Event) { e.Owner.DelegationID = "other" },
		"conversation":     func(e *ledgervo.Event) { e.ConversationID = "other" },
		"interaction":      func(e *ledgervo.Event) { e.InteractionID = "other" },
		"operation":        func(e *ledgervo.Event) { e.OperationID = "other" },
		"attempt":          func(e *ledgervo.Event) { e.Attempt++ },
		"request":          func(e *ledgervo.Event) { e.RequestID = "other" },
		"trace":            func(e *ledgervo.Event) { e.TraceID = validTraceIDTwo },
		"span":             func(e *ledgervo.Event) { e.SpanID = "other" },
		"type":             func(e *ledgervo.Event) { e.EventType = "other" },
		"producer":         func(e *ledgervo.Event) { e.ProducerID = "other" },
		"hash":             func(e *ledgervo.Event) { e.PayloadHash = strings.Repeat("0", 64) },
	}
	for _, key := range []string{"event_id", "event_type", "conversation_id", "interaction_id", "operation_id", "bkn.request.id", "trace_id", "span_id", "owner", "attempt"} {
		key := key
		mutations["inner_"+key] = func(e *ledgervo.Event) {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(e.Envelope, &fields)
			switch key {
			case "owner":
				fields[key] = json.RawMessage(`{"application_principal_id":"other"}`)
			case "attempt":
				fields[key] = json.RawMessage(`99`)
			default:
				fields[key] = json.RawMessage(`"other"`)
			}
			e.Envelope, _ = json.Marshal(fields)
		}
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f := newConfirmationFixture(t)
			event := f.event(t, "binding-negative")
			command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
			corrupt := event
			mutate(&corrupt)
			f.store.events[event.EventID] = corrupt
			_, receipt, err := f.service.CompleteOperationAttempt(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Status != sessionvo.ReceiptCompleted || receipt.EvidenceDurability == sessionvo.DurabilityDurable || len(receipt.ObservedEvidenceRefs) != 0 {
				t.Fatalf("incorrect binding accepted: %#v", receipt)
			}
		})
	}
}
func TestEvidenceConfirmationInvalidContractPreservesBusinessOutcome(t *testing.T) {
	cases := map[string]func(confirmationFixture, ledgervo.Event) []map[string]any{
		"too_many": func(f confirmationFixture, e ledgervo.Event) []map[string]any {
			events := make([]map[string]any, sessionvo.MaxExpectedEvidenceEvents+1)
			for i := range events {
				item := expectedConfirmationEvent(e, "accepted")
				item["event_id"] = strings.Repeat("x", i+1)
				events[i] = item
			}
			return events
		},
		"duplicate": func(f confirmationFixture, e ledgervo.Event) []map[string]any {
			return []map[string]any{expectedConfirmationEvent(e, "accepted"), expectedConfirmationEvent(e, "accepted")}
		},
		"invalid_hash": func(f confirmationFixture, e ledgervo.Event) []map[string]any {
			item := expectedConfirmationEvent(e, "accepted")
			item["payload_hash"] = "bad"
			return []map[string]any{item}
		},
		"missing_required_kind": func(f confirmationFixture, e ledgervo.Event) []map[string]any {
			item := expectedConfirmationEvent(e, "accepted")
			item["event_type"] = "metric.query.completed"
			return []map[string]any{item}
		},
		"empty_matched": func(f confirmationFixture, e ledgervo.Event) []map[string]any { return []map[string]any{} },
	}
	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			f := newConfirmationFixture(t)
			command := withConfirmationExpectation(t, f.finish, events(f, f.event(t, "invalid-contract"))...)
			_, receipt, err := f.service.CompleteOperationAttempt(context.Background(), command)
			if err != nil || receipt.Status != sessionvo.ReceiptCompleted || receipt.EvidenceDurability != sessionvo.DurabilityFailed || len(receipt.PartialReasons) == 0 {
				t.Fatalf("bad evidence contract lost business result: %#v %v", receipt, err)
			}
			if _, _, err := f.service.CompleteOperationAttempt(context.Background(), command); err != nil {
				t.Fatalf("rejected evidence contract exact replay failed: %v", err)
			}
		})
	}
}
func TestEvidenceConfirmationByteBudgetPreservesBusinessOutcome(t *testing.T) {
	f := newConfirmationFixture(t)
	event := f.event(t, "byte-budget")
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
	event.Envelope = json.RawMessage(`{"payload":"` + strings.Repeat("x", sessionvo.MaxEvidenceConfirmationBytes) + `"}`)
	f.store.events[event.EventID] = event
	_, receipt, err := f.service.CompleteOperationAttempt(context.Background(), command)
	if err != nil || receipt.Status != sessionvo.ReceiptCompleted || receipt.EvidenceDurability != sessionvo.DurabilityFailed {
		t.Fatalf("byte overflow must preserve business result: %#v %v", receipt, err)
	}
}
func TestEvidenceConfirmationLegacyPendingIsNotInferred(t *testing.T) {
	f := newConfirmationFixture(t)
	if _, _, err := f.service.CompleteOperationAttempt(context.Background(), f.finish); err != nil {
		t.Fatal(err)
	}
	event := f.event(t, "legacy-event")
	f.store.events[event.EventID] = event
	if err := confirmEvidence(t, f.service, event); err != nil {
		t.Fatal(err)
	}
	receipt := confirmationReceipt(t, f)
	if receipt.EvidenceDurability != sessionvo.DurabilityPending || len(receipt.ObservedEvidenceRefs) != 0 {
		t.Fatalf("legacy set was inferred: %#v", receipt)
	}
}

func TestEvidenceConfirmationOldAttemptDoesNotMutateCurrentAttempt(t *testing.T) {
	f := newConfirmationFixture(t)
	event := f.event(t, "old-attempt")
	failed := f.finish
	failed.Output = sessionvo.PayloadEnvelope{}
	failed.Error = operationError("retryable business error")
	failed.Retryable = true
	failed.EvidenceDurability = sessionvo.DurabilityFailed
	failed = withConfirmationExpectation(t, failed, expectedConfirmationEvent(event, "accepted"))
	if _, _, err := f.service.FailOperationAttempt(context.Background(), failed); err != nil {
		t.Fatal(err)
	}
	current, _, err := f.service.StartOperationAttempt(context.Background(), sessionsvc.StartAttemptCommand{Owner: f.owner, OperationID: f.operation.ID, LeaseToken: f.interaction.LeaseToken, LeaseEpoch: f.interaction.LeaseEpoch})
	if err != nil || current.Attempt != 2 {
		t.Fatalf("create new attempt: %#v %v", current, err)
	}
	f.store.events[event.EventID] = event
	if err := confirmEvidence(t, f.service, event); err != nil {
		t.Fatal(err)
	}
	after, err := f.service.GetOperation(context.Background(), f.owner, f.operation.ID)
	if err != nil || !reflect.DeepEqual(after, current) {
		t.Fatalf("old evidence mutated current attempt: %#v %v", after, err)
	}
	if receipt := confirmationReceipt(t, f); receipt.EvidenceDurability != sessionvo.DurabilityDurable || receipt.Status != sessionvo.ReceiptFailed {
		t.Fatalf("old evidence/business states conflated: %#v", receipt)
	}
}

func TestEvidenceConfirmationRealMemoryLedgerBridge(t *testing.T) {
	for _, order := range []string{"event_first", "receipt_first", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			f := newConfirmationFixture(t)
			ledger := ledgerstore.New()
			f.store.externalReader = ledger.ReadEvidenceEventsByIDs
			service := ledgersvc.New(ledger)
			service.SetDurableObserver(f.service.ReconcileEvidence)
			event := f.event(t, "actual-bridge")
			event.ProducerStreamID = "bridge-stream"
			event.ProducerEpoch = 1
			event.ProducerSequence = 1
			event.StartedAt = *f.now
			event.ObservedAt = *f.now
			event.EmittedAt = *f.now
			command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
			finish := func() error {
				_, _, err := f.service.CompleteOperationAttempt(context.Background(), command)
				return err
			}
			ingest := func() error {
				ack, err := service.Ingest(context.Background(), event)
				if err == nil && !ack.Durable {
					return errors.New("bridge ack not durable")
				}
				return err
			}
			if order == "concurrent" {
				var wg sync.WaitGroup
				errs := make(chan error, 2)
				start := make(chan struct{})
				for _, run := range []func() error{finish, ingest} {
					wg.Add(1)
					go func(run func() error) { defer wg.Done(); <-start; errs <- run() }(run)
				}
				close(start)
				wg.Wait()
				close(errs)
				for err := range errs {
					if err != nil {
						t.Fatal(err)
					}
				}
			} else {
				runs := []func() error{finish, ingest}
				if order == "event_first" {
					runs = []func() error{ingest, finish}
				}
				for _, run := range runs {
					if err := run(); err != nil {
						t.Fatal(err)
					}
				}
			}
			receipt := confirmationReceipt(t, f)
			if receipt.EvidenceDurability != sessionvo.DurabilityDurable || !reflect.DeepEqual(receipt.ObservedEvidenceRefs, []string{event.EventID}) {
				t.Fatalf("real memory bridge did not converge: %#v", receipt)
			}
		})
	}
}

func TestDurableDuplicateCallbackSkipsLedgerReadWithoutMutations(t *testing.T) {
	f := newConfirmationFixture(t)
	event := f.event(t, "durable-fast-path")
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
	if _, _, err := f.service.CompleteOperationAttempt(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	deadline := f.now.Add(-time.Second)
	_, err := f.service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: f.owner, InteractionID: f.interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "durable-fast-terminate", LeaseToken: f.interaction.LeaseToken, LeaseEpoch: f.interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{Version: "1", CompletionReason: "answer_returned", AssemblerDeadline: &deadline,
			ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: f.operation.ID, Required: true}}, ExpectedReceipts: []sessionvo.ExpectedReceipt{{ReceiptID: f.receipt.ID, Required: true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.store.events[event.EventID] = event
	if err := confirmEvidence(t, f.service, event); err != nil {
		t.Fatal(err)
	}
	before := confirmationReceipt(t, f)
	if before.EvidenceDurability != sessionvo.DurabilityDurable {
		t.Fatalf("not confirmed: %#v", before)
	}
	revisions, err := f.service.ListAssemblyRevisions(context.Background(), f.owner, f.interaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 {
		t.Fatalf("late chain missing: %#v", revisions)
	}
	reads, outbox := f.store.reads, f.store.PendingProjectionCount()
	f.store.readErr = errors.New("duplicate must not re-read durable ledger")
	for i := 0; i < 3; i++ {
		if err := confirmEvidence(t, f.service, event); err != nil {
			t.Fatalf("durable callback re-read: %v", err)
		}
	}
	after := confirmationReceipt(t, f)
	afterRevisions, err := f.service.ListAssemblyRevisions(context.Background(), f.owner, f.interaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.store.reads != reads || f.store.PendingProjectionCount() != outbox || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(revisions, afterRevisions) {
		t.Fatalf("duplicate mutated durable state: reads %d->%d outbox %d->%d receiptversion %d->%d", reads, f.store.reads, outbox, f.store.PendingProjectionCount(), before.RowVersion, after.RowVersion)
	}
	// The fast path must follow validation of the actual stored call-fact binding.
	if err := f.store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		fact, found := tx.FindOperationCallFact(f.operation.ID, f.receipt.Attempt)
		if !found {
			return errors.New("missing fact")
		}
		fact.ReceiptID = "wrong-receipt"
		tx.SaveOperationCallFact(fact)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := confirmEvidence(t, f.service, event); err == nil || !strings.Contains(err.Error(), "receipt invariant") {
		t.Fatalf("durable shortcut bypassed stored identity: %v", err)
	}
}

func TestFirstFinishDurableClaimStillChecksLedger(t *testing.T) {
	f := newConfirmationFixture(t)
	event := f.event(t, "unproven-durable-claim")
	command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
	command.EvidenceDurability = sessionvo.DurabilityDurable
	if _, _, err := f.service.CompleteOperationAttempt(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	receipt := confirmationReceipt(t, f)
	if receipt.EvidenceDurability != sessionvo.DurabilityPending || f.store.reads != 1 {
		t.Fatalf("client durability claim bypassed ledger: durability=%s reads=%d", receipt.EvidenceDurability, f.store.reads)
	}
}

func testConfirmationProfile(tool, contract, resolution string) *sessionvo.CapabilityProfile {
	return &sessionvo.CapabilityProfile{ManifestID: "openbkn.context-loader.mcp", ManifestVersion: "0.1.6", CanonicalToolName: tool, ToolVersion: "1.0.0", InputSchemaDigest: "sha256:input", OutputSchemaDigest: "sha256:output", ExecutionRole: "business_function", EvidenceContract: contract, ChildEvidencePolicy: "managed_descendants", RequiredTraceFields: []string{"receipt", "business_refs", "result_completeness"}, FailurePolicy: "preserve_execution_and_downgrade", Resolution: resolution}
}
func TestReceiptOnlyContractsAllowClosedEmptySet(t *testing.T) {
	for _, contract := range []string{"execution_only", "managed_function_execution/v1"} {
		for _, failed := range []bool{false, true} {
			t.Run(contract+map[bool]string{false: "/completed", true: "/failed"}[failed], func(t *testing.T) {
				f := newConfirmationFixture(t, testConfirmationProfile("function-quality", contract, "matched"))
				command := withConfirmationExpectation(t, f.finish)
				command.BusinessRefs = []sessionvo.BusinessRef{{RefType: "function", RefID: "function:kn-1:function-quality", Version: "unversioned"}}
				var err error
				if failed {
					command.Output = sessionvo.PayloadEnvelope{}
					command.Error, err = sessionvo.InlineJSONPayload(json.RawMessage(`{"code":"function_execution_failed","stage":"function_execution"}`))
					if err != nil {
						t.Fatal(err)
					}
					_, _, err = f.service.FailOperationAttempt(context.Background(), command)
				} else {
					_, _, err = f.service.CompleteOperationAttempt(context.Background(), command)
				}
				if err != nil {
					t.Fatal(err)
				}
				receipt := confirmationReceipt(t, f)
				if receipt.EvidenceDurability != sessionvo.DurabilityDurable || len(receipt.PartialReasons) != 0 || len(receipt.ObservedEvidenceRefs) != 0 {
					t.Fatalf("complete receipt-only call falsely missing: %#v", receipt)
				}
			})
		}
	}
}

func TestUnprofiledManagedRESTCallAllowsClosedEmptyEvidenceSet(t *testing.T) {
	f := newConfirmationFixture(t, nil)
	command := withConfirmationExpectation(t, f.finish)
	command.EvidenceDurability = sessionvo.DurabilityDurable
	if _, _, err := f.service.CompleteOperationAttempt(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	receipt := confirmationReceipt(t, f)
	if receipt.Status != sessionvo.ReceiptCompleted || receipt.EvidenceDurability != sessionvo.DurabilityDurable || len(receipt.PartialReasons) != 0 {
		t.Fatalf("unprofiled REST success falsely rejected: %#v", receipt)
	}
}

func TestUnprofiledManagedRESTCallStillConfirmsPlannedEvents(t *testing.T) {
	for _, stored := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "stored"}[stored], func(t *testing.T) {
			f := newConfirmationFixture(t, nil)
			event := f.event(t, "rest-planned-event")
			want := sessionvo.DurabilityPending
			if stored {
				f.store.events[event.EventID] = event
				want = sessionvo.DurabilityDurable
			}
			command := withConfirmationExpectation(t, f.finish, expectedConfirmationEvent(event, "accepted"))
			command.EvidenceDurability = sessionvo.DurabilityDurable
			if _, _, err := f.service.CompleteOperationAttempt(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			if receipt := confirmationReceipt(t, f); receipt.EvidenceDurability != want {
				t.Fatalf("unprofiled planned evidence bypassed confirmation: %#v", receipt)
			}
		})
	}
}
func TestFailedDataContractEmptySetSelfRecordsFailure(t *testing.T) {
	for _, entry := range []struct{ tool, contract, ref string }{
		{"get_kn_detail", "ontology_schema_snapshot/v1", "kn:kn-1"},
		{"get_object_types", "ontology_schema_snapshot/v1", "kn:kn-1"},
		{"query_metric", "ontology_metric_result/v1", "metric:kn-1:metric-1"},
	} {
		t.Run(entry.tool, func(t *testing.T) {
			f := newConfirmationFixture(t, testConfirmationProfile(entry.tool, entry.contract, "matched"))
			command := withConfirmationExpectation(t, f.finish)
			command.Output = sessionvo.PayloadEnvelope{}
			errorPayload, err := sessionvo.InlineJSONPayload(json.RawMessage(`{"code":"tool_error","stage":"tool_execution","message":"backend failure recorded"}`))
			if err != nil {
				t.Fatal(err)
			}
			command.Error = errorPayload
			command.BusinessRefs = []sessionvo.BusinessRef{{RefType: sessionvo.BusinessRefType(strings.Split(entry.ref, ":")[0]), RefID: entry.ref, Version: "unversioned"}}
			if command.BusinessRefs[0].RefType == "kn" {
				command.BusinessRefs[0].RefType = "knowledge_network"
			}
			if _, _, err := f.service.FailOperationAttempt(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			receipt := confirmationReceipt(t, f)
			if receipt.Status != sessionvo.ReceiptFailed || receipt.EvidenceDurability != sessionvo.DurabilityDurable || len(receipt.ObservedEvidenceRefs) != 0 || len(receipt.PartialReasons) != 0 {
				t.Fatalf("recorded business failure falsely missing: %#v", receipt)
			}
		})
	}
}

func TestReceiptOnlyAndFailedCallsStillRequireAllPlannedEvents(t *testing.T) {
	for _, contract := range []string{"execution_only", "managed_function_execution/v1"} {
		for _, failed := range []bool{false, true} {
			for _, state := range []string{"stored", "missing", "dropped"} {
				t.Run(contract+map[bool]string{false: "/completed/", true: "/failed/"}[failed]+state, func(t *testing.T) {
					f := newConfirmationFixture(t, testConfirmationProfile("function-quality", contract, "matched"))
					event := f.event(t, "planned-function-event")
					disposition := "accepted"
					want := sessionvo.DurabilityPending
					if state == "stored" {
						f.store.events[event.EventID] = event
						want = sessionvo.DurabilityDurable
					}
					if state == "dropped" {
						disposition = "dropped"
						want = sessionvo.DurabilityFailed
					}
					item := expectedConfirmationEvent(event, disposition)
					if state == "dropped" {
						item["drop_reason"] = "queue_full"
					}
					command := withConfirmationExpectation(t, f.finish, item)
					command.BusinessRefs = []sessionvo.BusinessRef{{RefType: "function", RefID: "function:kn-1:function-quality", Version: "unversioned"}}
					var err error
					if failed {
						command.Output = sessionvo.PayloadEnvelope{}
						command.Error, err = sessionvo.InlineJSONPayload(json.RawMessage(`{"code":"tool_error","stage":"tool_execution"}`))
						if err != nil {
							t.Fatal(err)
						}
						_, _, err = f.service.FailOperationAttempt(context.Background(), command)
					} else {
						_, _, err = f.service.CompleteOperationAttempt(context.Background(), command)
					}
					if err != nil {
						t.Fatal(err)
					}
					receipt := confirmationReceipt(t, f)
					if receipt.EvidenceDurability != want {
						t.Fatalf("planned set ignored: state=%s want=%s receipt=%#v", state, want, receipt)
					}
				})
			}
		}
	}
}

// A real failed data call may also have planned evidence. Its recorded terminal
// error only permits the closed empty set, never bypasses a nonempty contract.
func TestFailedDataCallStillRequiresPlannedEvent(t *testing.T) {
	for _, state := range []string{"stored", "missing", "dropped", "wrong_hash"} {
		t.Run(state, func(t *testing.T) {
			f := newConfirmationFixture(t, testConfirmationProfile("query_metric", "ontology_metric_result/v1", "matched"))
			event := f.event(t, "failed-metric-event")
			var envelope map[string]any
			if err := json.Unmarshal(event.Envelope, &envelope); err != nil {
				t.Fatal(err)
			}
			event.EventType = "metric.query.completed"
			envelope["event_type"] = event.EventType
			var err error
			event.Envelope, err = json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			event.PayloadHash = ledgervo.CanonicalPayloadHash(event.Envelope)
			disposition := "accepted"
			want := sessionvo.DurabilityPending
			if state == "stored" || state == "wrong_hash" {
				f.store.events[event.EventID] = event
				want = sessionvo.DurabilityDurable
			}
			if state == "dropped" {
				disposition = "dropped"
				want = sessionvo.DurabilityFailed
			}
			item := expectedConfirmationEvent(event, disposition)
			if state == "dropped" {
				item["drop_reason"] = "queue_full"
			}
			if state == "wrong_hash" {
				item["payload_hash"] = strings.Repeat("0", 64)
				want = sessionvo.DurabilityFailed
			}
			command := withConfirmationExpectation(t, f.finish, item)
			command.Output = sessionvo.PayloadEnvelope{}
			command.Error, err = sessionvo.InlineJSONPayload(json.RawMessage(`{"code":"tool_error","stage":"tool_execution"}`))
			if err != nil {
				t.Fatal(err)
			}
			command.BusinessRefs = []sessionvo.BusinessRef{{RefType: "metric", RefID: "metric:kn-1:metric-1", Version: "unversioned"}}
			if _, _, err := f.service.FailOperationAttempt(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			if receipt := confirmationReceipt(t, f); receipt.EvidenceDurability != want {
				t.Fatalf("failed bypassed planned evidence: state=%s want=%s receipt=%#v", state, want, receipt)
			}
		})
	}
}
