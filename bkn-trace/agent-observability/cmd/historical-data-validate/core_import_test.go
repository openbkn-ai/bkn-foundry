// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	memory "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestHistoricalCorePreservesActiveConversationWithTerminalRounds(t *testing.T) {
	p := repairCoreFixture(1)
	p.Conversations[0].Status = sessionvo.ConversationActive
	p.Conversations[0].ClosedAt = nil
	store := memory.New()
	if _, err := importCorePlan(context.Background(), store, p); err != nil {
		t.Fatal("stable active conversation rejected", err)
	}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		c, _ := tx.PeekConversation(p.Conversations[0].ID)
		if c.Status != sessionvo.ConversationActive || c.ClosedAt != nil {
			t.Fatal("historical conversation state changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHistoricalCoreWirePreservesIdempotencyMetadata(t *testing.T) {
	p := repairCoreFixture(1)
	p.Interactions[0].StartIdempotencyKey = "original-start"
	p.Interactions[0].TerminalIdempotencyKey = "original-terminal"
	p.Interactions[0].TerminalPayloadHash = strings.Repeat("a", 64)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded coreImportPlan
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Interactions[0].StartIdempotencyKey != "original-start" || decoded.Interactions[0].TerminalIdempotencyKey != "original-terminal" || decoded.Interactions[0].TerminalPayloadHash != strings.Repeat("a", 64) {
		t.Fatal("offline wire format dropped persisted idempotency fields")
	}
	if err = prepareCorePlan(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Interactions[0].StartIdempotencyKey != "original-start" {
		t.Fatal("preparation overwrote original start key")
	}
}

func TestHistoricalCoreComparisonIncludesIdempotencyMetadata(t *testing.T) {
	a := repairCoreFixture(1).Interactions[0]
	b := a
	b.StartIdempotencyKey = "different-start"
	if sameCore(a, b) {
		t.Fatal("readback ignored a changed persisted start key")
	}
	b = a
	b.TerminalPayloadHash = strings.Repeat("b", 64)
	if sameCore(a, b) {
		t.Fatal("readback ignored a changed terminal payload hash")
	}
}

func TestHistoricalCorePreservesPendingCallWithoutExecutionContext(t *testing.T) {
	p := repairCoreFixture(1)
	p.Receipts[0].Status = sessionvo.ReceiptPending
	p.Receipts[0].TerminalAt = nil
	p.Receipts[0].RequestID = ""
	p.Receipts[0].TraceID = ""
	p.CallFacts[0].Status = sessionvo.AttemptPending
	p.CallFacts[0].FinishedAt = nil
	p.CallFacts[0].RequestID = ""
	p.CallFacts[0].TraceID = ""
	store := memory.New()
	if _, err := importCorePlan(context.Background(), store, p); err != nil {
		t.Fatal("recorded pending call rejected", err)
	}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		r, _ := tx.FindReceipt(p.Receipts[0].ID)
		f, _ := tx.FindOperationCallFact(p.CallFacts[0].OperationID, 1)
		if r.Status != sessionvo.ReceiptPending || r.TerminalAt != nil || r.TraceID != "" || f.FinishedAt != nil {
			t.Fatal("pending execution was completed or given fabricated context")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHistoricalCoreRestoresIdempotencyAndAssemblyRecords(t *testing.T) {
	p := repairCoreFixture(1)
	raw, _ := json.Marshal(p)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	i := p.Interactions[0]
	owner := p.Conversations[0].Owner
	fields["idempotency_records"], _ = json.Marshal([]map[string]any{{"scope": "interaction.start", "owner": owner, "external_conversation_key": "external", "idempotency_key": "original", "request_hash": "hash", "resource_type": "interaction", "resource_id": i.ID, "created_at": i.CreatedAt}})
	revision := sessionvo.AssemblyRevision{ID: "original-revision", InteractionID: i.ID, RevisionNo: 3, ParentRevisionID: "prior-revision", CreatedAt: i.CreatedAt, Completeness: sessionvo.EvidencePartial, Trigger: "original-trigger"}
	fields["assembly_revisions"], _ = json.Marshal([]sessionvo.AssemblyRevision{revision})
	raw, _ = json.Marshal(fields)
	var restored coreImportPlan
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal("historical persistence records rejected", err)
	}
	store := memory.New()
	if _, err := importCorePlan(context.Background(), store, restored); err != nil {
		t.Fatal(err)
	}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		key, found := tx.FindIdempotency("interaction.start", owner, "external", "original")
		if !found || key.ResourceID != i.ID || key.RequestHash != "hash" || !key.CreatedAt.Equal(i.CreatedAt) {
			t.Fatal("original idempotency identity not restored")
		}
		versions := tx.ListAssemblyRevisions(i.ID)
		if len(versions) != 1 || !sameCore(versions[0], revision) {
			t.Fatal("original assembly version or historical time changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := importCorePlan(context.Background(), store, restored)
	if err != nil || result.Created != 0 {
		t.Fatal("historical records not idempotent", result, err)
	}
}

func TestHistoricalCorePartitionsPreserveEmptyConversationAndPersistenceRecords(t *testing.T) {
	p := repairCoreFixture(101)
	c := p.Conversations[0]
	c.ID = "empty-original-conversation"
	p.Conversations = append(p.Conversations, c)
	i := p.Interactions[100]
	p.IdempotencyRecords = []coreIdempotencyRecord{{Scope: "interaction.start", Owner: c.Owner, ExternalConversationKey: "key", IdempotencyKey: "original", ResourceType: "interaction", ResourceID: i.ID, CreatedAt: i.CreatedAt}}
	p.AssemblyRevisions = []sessionvo.AssemblyRevision{{ID: "historical-version", InteractionID: i.ID, RevisionNo: 1, CreatedAt: i.CreatedAt}}
	store := memory.New()
	result, err := importCorePlan(context.Background(), store, p)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 508 {
		t.Fatalf("partitioning lost original records: %+v", result)
	}
	if err = store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		if _, found := tx.PeekConversation(c.ID); !found || len(tx.ListAssemblyRevisions(i.ID)) != 1 {
			t.Fatal("empty conversation or original assembly was lost")
		}
		if _, found := tx.FindIdempotency("interaction.start", c.Owner, "key", "original"); !found {
			t.Fatal("partitioned idempotency record missing")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestImportCoreIdempotentAndRejectsConflict(t *testing.T) {
	store := memory.New()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	plan := coreImportPlan{Conversations: []sessionvo.Conversation{{ID: "c", Owner: sessionvo.Owner{ApplicationPrincipalID: "a", EffectiveSubjectType: "user", EffectiveSubjectID: "u"}, ExternalConversationKey: "c", Generation: 1, Status: sessionvo.ConversationClosed, RowVersion: 1, CreatedAt: now, UpdatedAt: now, ClosedAt: &now}}}
	result, err := importCorePlan(context.Background(), store, plan)
	if err != nil || !result.Verified || result.Created != 1 {
		t.Fatalf("first import: %+v %v", result, err)
	}
	result, err = importCorePlan(context.Background(), store, plan)
	if err != nil || result.AlreadyVerified != 1 || result.Created != 0 {
		t.Fatalf("repeat import: %+v %v", result, err)
	}
	plan.Conversations[0].AgentName = "changed"
	if _, err = importCorePlan(context.Background(), store, plan); err == nil {
		t.Fatal("conflict accepted")
	}
}
func TestCorePlanRejectsBrokenReferences(t *testing.T) {
	var plan coreImportPlan
	if err := json.Unmarshal([]byte(`{"interactions":[{"interaction_id":"i","conversation_id":"absent"}]}`), &plan); err != nil {
		t.Fatal(err)
	}
	if err := validateCorePlan(plan); err == nil {
		t.Fatal("broken references accepted")
	}
}

func TestImportPartitionsNativeIntegrityTransactions(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	p := coreImportPlan{}
	for index := 0; index < 1001; index++ {
		id := fmt.Sprintf("id-%d", index)
		p.Conversations = append(p.Conversations, sessionvo.Conversation{ID: id, Owner: sessionvo.Owner{ApplicationPrincipalID: "a", EffectiveSubjectType: "user", EffectiveSubjectID: "u"}, ExternalConversationKey: id, Status: sessionvo.ConversationClosed, CreatedAt: now, UpdatedAt: now})
		p.Interactions = append(p.Interactions, sessionvo.Interaction{ID: id, ConversationID: id, Ordinal: 1, ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now, TerminalAt: &now})
	}
	store := memory.New()
	result, err := importCorePlan(context.Background(), store, p)
	if err != nil || !result.Verified || result.Created != 2002 {
		t.Fatalf("partitioned import: %+v %v", result, err)
	}
	result, err = importCorePlan(context.Background(), store, p)
	if err != nil || result.AlreadyVerified != 2002 {
		t.Fatalf("partitioned repeat: %+v %v", result, err)
	}
}

func TestCoreValidationModeDoesNotRequireDatabase(t *testing.T) {
	t.Setenv("BKN_TRACE_CORE_MARIADB_DSN", "")
	var output bytes.Buffer
	if err := runCommand([]string{"--validate-core-records"}, strings.NewReader(`{}`), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"verified":true`) {
		t.Fatal(output.String())
	}
	if err := runCommand([]string{"--validate-core-records"}, strings.NewReader(`{"interactions":[{"interaction_id":"bad"}]}`), &output); err == nil {
		t.Fatal("invalid native batch accepted")
	}
}

func TestCoreValidationRejectsInvalidPayloadWithoutDatabase(t *testing.T) {
	t.Setenv("BKN_TRACE_CORE_MARIADB_DSN", "")
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	owner := sessionvo.Owner{ApplicationPrincipalID: "a", EffectiveSubjectType: "user", EffectiveSubjectID: "u"}
	p := coreImportPlan{
		Conversations: []sessionvo.Conversation{{ID: "c", Owner: owner, Status: sessionvo.ConversationClosed, CreatedAt: now, UpdatedAt: now}},
		Interactions:  []sessionvo.Interaction{{ID: "i", ConversationID: "c", Ordinal: 1, ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now, TerminalAt: &now}},
		Operations:    []sessionvo.Operation{{ID: "o", ConversationID: "c", InteractionID: "i", Attempt: 1, ToolName: "tool", CreatedAt: now}},
		Receipts:      []sessionvo.Receipt{{ID: "r", ConversationID: "c", InteractionID: "i", OperationID: "o", Owner: owner, RequestID: "request", TraceID: "trace", Attempt: 1, Status: sessionvo.ReceiptCompleted, TerminalAt: &now}},
		CallFacts:     []sessionvo.OperationCallFact{{ReceiptID: "r", ConversationID: "c", InteractionID: "i", OperationID: "o", RequestID: "request", TraceID: "trace", Attempt: 1, Protocol: sessionvo.ProtocolMCP, FinishedAt: &now}},
	}
	if err := validateCorePlan(p); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = runCommand([]string{"--validate-core-records"}, bytes.NewReader(data), &output)
	if err == nil || err.Error() != "invalid converted input" {
		t.Fatalf("payload validation: %v %s", err, output.String())
	}
}

func repairCoreFixture(count int) coreImportPlan {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	owner := sessionvo.Owner{ApplicationPrincipalID: "a", EffectiveSubjectType: "user", EffectiveSubjectID: "u"}
	p := coreImportPlan{}
	for n := 0; n < count; n++ {
		id := fmt.Sprintf("repair-%d", n)
		p.Conversations = append(p.Conversations, sessionvo.Conversation{ID: id, Owner: owner, Status: sessionvo.ConversationClosed, CreatedAt: now, UpdatedAt: now})
		p.Interactions = append(p.Interactions, sessionvo.Interaction{ID: id, ConversationID: id, Ordinal: 1, ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now, TerminalAt: &now})
		p.Operations = append(p.Operations, sessionvo.Operation{ID: id, ConversationID: id, InteractionID: id, Attempt: 1, ToolName: "tool", CreatedAt: now})
		p.Receipts = append(p.Receipts, sessionvo.Receipt{ID: id, Owner: owner, ConversationID: id, InteractionID: id, OperationID: id, Attempt: 1, RequestID: id, TraceID: "old-trace", Status: sessionvo.ReceiptCompleted, TerminalAt: &now})
		p.CallFacts = append(p.CallFacts, sessionvo.OperationCallFact{ReceiptID: id, ConversationID: id, InteractionID: id, OperationID: id, Attempt: 1, RequestID: id, TraceID: "old-trace", Protocol: sessionvo.ProtocolMCP, FinishedAt: &now, Input: sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, MediaType: "application/json", Inline: json.RawMessage(`{"input":true}`)}})
	}
	return p
}

func repairCoreTarget(t *testing.T, prior coreImportPlan, withPrior bool) coreImportPlan {
	t.Helper()
	raw, _ := json.Marshal(prior)
	var p coreImportPlan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	for n := range p.Receipts {
		p.Receipts[n].TraceID = "original-trace"
		p.CallFacts[n].TraceID = "original-trace"
		p.CallFacts[n].ParentOperationID = "parent"
	}
	if withPrior {
		raw, _ = json.Marshal(p)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		fields["previous_receipts"], _ = json.Marshal(prior.Receipts)
		fields["previous_call_facts"], _ = json.Marshal(prior.CallFacts)
		raw, _ = json.Marshal(fields)
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func coreUpdated(t *testing.T, result coreImportResult) int {
	t.Helper()
	raw, _ := json.Marshal(result)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	value, ok := fields["updated"].(float64)
	if !ok {
		t.Fatal("updated count absent", string(raw))
	}
	return int(value)
}

func TestCoreRepairExactPriorAndIdempotentReadback(t *testing.T) {
	store := memory.New()
	prior := repairCoreFixture(1)
	if _, err := importCorePlan(context.Background(), store, prior); err != nil {
		t.Fatal(err)
	}
	target := repairCoreTarget(t, prior, true)
	result, err := importCorePlan(context.Background(), store, target)
	if err != nil {
		t.Fatal("exact prior repair rejected", err)
	}
	if !result.Verified || coreUpdated(t, result) != 2 || result.Created != 0 || result.AlreadyVerified != 3 {
		t.Fatalf("repair counts: %+v", result)
	}
	if err = store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		r, _ := tx.PeekReceipt(target.Receipts[0].ID)
		f, _ := tx.FindOperationCallFact(target.CallFacts[0].OperationID, 1)
		if r.TraceID != "original-trace" || f.TraceID != r.TraceID || f.ParentOperationID != "parent" {
			t.Fatalf("repair readback: %+v %+v", r, f)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err = importCorePlan(context.Background(), store, target)
	if err != nil || coreUpdated(t, result) != 0 || result.AlreadyVerified != 5 {
		t.Fatalf("repeat: %+v %v", result, err)
	}
}

func TestCoreRepairRejectsUnknownTargetAndMissingPrior(t *testing.T) {
	for _, kind := range []string{"no-prior", "unrelated-target"} {
		t.Run(kind, func(t *testing.T) {
			store := memory.New()
			prior := repairCoreFixture(1)
			if _, err := importCorePlan(context.Background(), store, prior); err != nil {
				t.Fatal(err)
			}
			target := repairCoreTarget(t, prior, kind != "no-prior")
			if kind == "unrelated-target" {
				if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
					v, _ := tx.PeekReceipt(prior.Receipts[0].ID)
					v.ToolName = "other"
					tx.SaveReceipt(v)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := importCorePlan(context.Background(), store, target); err == nil {
				t.Fatal("conflicting target overwritten")
			}
		})
	}
}

func TestCoreRepairPartitionsPreservePriors(t *testing.T) {
	store := memory.New()
	prior := repairCoreFixture(101)
	if _, err := importCorePlan(context.Background(), store, prior); err != nil {
		t.Fatal(err)
	}
	result, err := importCorePlan(context.Background(), store, repairCoreTarget(t, prior, true))
	if err != nil || !result.Verified {
		t.Fatalf("partition repair: %+v %v", result, err)
	}
	if coreUpdated(t, result) != 202 || result.AlreadyVerified != 303 {
		t.Fatalf("partition counts: %+v", result)
	}
}

func TestCoreRepairRejectsUnrelatedPriorChangesBeforeWrites(t *testing.T) {
	prior := repairCoreFixture(1)
	target := repairCoreTarget(t, prior, true)
	raw, _ := json.Marshal(target)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	invalid := prior.Receipts
	invalid[0].Owner.EffectiveSubjectID = "other"
	fields["previous_receipts"], _ = json.Marshal(invalid)
	raw, _ = json.Marshal(fields)
	var output bytes.Buffer
	if err := runCommand([]string{"--validate-core-records"}, bytes.NewReader(raw), &output); err == nil {
		t.Fatal("owner-changing prior accepted")
	}
}

func TestCoreRepairRejectsUnknownCallAndNoCallPrior(t *testing.T) {
	for _, kind := range []string{"no-prior", "unrelated-target"} {
		t.Run(kind, func(t *testing.T) {
			store := memory.New()
			prior := repairCoreFixture(1)
			if _, err := importCorePlan(context.Background(), store, prior); err != nil {
				t.Fatal(err)
			}
			target := repairCoreTarget(t, prior, kind != "no-prior")
			// Keep the receipt unchanged so this exercises the call-fact protection.
			target.Receipts[0].TraceID = prior.Receipts[0].TraceID
			target.CallFacts[0].TraceID = prior.CallFacts[0].TraceID
			if kind == "unrelated-target" {
				if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
					v, _ := tx.FindOperationCallFact(prior.CallFacts[0].OperationID, 1)
					v.SourceModule = "other"
					tx.SaveOperationCallFact(v)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := importCorePlan(context.Background(), store, target); err == nil {
				t.Fatal("conflicting call overwritten")
			}
		})
	}
}

func TestCoreRepairRejectsCallPriorChangesInValidation(t *testing.T) {
	for _, kind := range []string{"payload", "identity", "existing-parent", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			prior := repairCoreFixture(1)
			target := repairCoreTarget(t, prior, true)
			raw, _ := json.Marshal(target)
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			switch kind {
			case "payload":
				prior.CallFacts[0].Input.Inline = json.RawMessage(`{"input":false}`)
			case "identity":
				prior.CallFacts[0].ReceiptID = "other"
			case "existing-parent":
				prior.CallFacts[0].ParentOperationID = "another-parent"
			case "duplicate":
				prior.CallFacts = append(prior.CallFacts, prior.CallFacts[0])
			}
			fields["previous_call_facts"], _ = json.Marshal(prior.CallFacts)
			raw, _ = json.Marshal(fields)
			var output bytes.Buffer
			if err := runCommand([]string{"--validate-core-records"}, bytes.NewReader(raw), &output); err == nil {
				t.Fatal("unrelated prior accepted")
			}
		})
	}
}
