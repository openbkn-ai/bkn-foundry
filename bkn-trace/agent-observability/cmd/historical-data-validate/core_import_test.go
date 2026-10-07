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
)

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
