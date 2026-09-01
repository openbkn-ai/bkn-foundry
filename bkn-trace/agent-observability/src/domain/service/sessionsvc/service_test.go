// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionsvc_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"github.com/openbkn-ai/bkn-foundry/comm-go/projectiongrant"
)

const (
	validTraceIDOne = "4b3d59daeff5bfbb23d46c47a5051ec9"
	validTraceIDTwo = "9c0d0000000000000000000000000001"
)

func TestEnsureCurrentConversationIsIdempotent(t *testing.T) {
	t.Parallel()

	store := sessionstore.New()
	service := sessionsvc.New(store, sessionsvc.Options{
		Now: func() time.Time { return time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC) },
	})
	owner := sessionvo.Owner{
		ApplicationPrincipalID: "app-1",
		EffectiveSubjectType:   sessionvo.SubjectUser,
		EffectiveSubjectID:     "user-1",
	}

	first, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner:                   owner,
		ExternalConversationKey: "cursor-thread-1",
		IdempotencyKey:          "idem-1",
		CreationRequestID:       "req-create-1",
		BusinessContext:         "managed",
		ActorNameSnapshot:       "供应链管理员",
		CreationAuthMethod:      "api_key",
	})
	if err != nil {
		t.Fatalf("ensure first conversation: %v", err)
	}
	second, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner:                   owner,
		ExternalConversationKey: "cursor-thread-1",
		IdempotencyKey:          "idem-2",
	})
	if err != nil {
		t.Fatalf("ensure second conversation: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("expected the current conversation to be reused, got %q and %q", first.ID, second.ID)
	}
	if first.Generation != 1 || second.Generation != 1 {
		t.Fatalf("expected generation 1, got %d and %d", first.Generation, second.Generation)
	}
	if first.Status != sessionvo.ConversationActive {
		t.Fatalf("expected active conversation, got %q", first.Status)
	}
	if first.CreationRequestID != "req-create-1" || first.BusinessContext != "managed" ||
		first.ActorNameSnapshot != "供应链管理员" || first.CreationAuthMethod != "api_key" {
		t.Fatalf("conversation creation context was not persisted: %+v", first)
	}
}

func TestStartInteractionSolidifiesConversationAgentName(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "agent-name")

	if _, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "agent-name-first",
		AgentName: "  供应链分析助手  ",
	}); err != nil {
		t.Fatalf("start interaction: %v", err)
	}

	stored, err := service.GetConversation(context.Background(), owner, conversation.ID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if stored.AgentName != "供应链分析助手" {
		t.Fatalf("agent name = %q, want normalized display declaration", stored.AgentName)
	}
	if !stored.Owner.Equal(owner) {
		t.Fatalf("display declaration must not alter trusted owner: %+v", stored.Owner)
	}
}

func TestStartInteractionRejectsConversationAgentNameChange(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "agent-name-conflict")
	if _, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "agent-name-original",
		AgentName: "供应链分析助手",
	}); err != nil {
		t.Fatalf("start original interaction: %v", err)
	}

	if _, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "agent-name-changed",
		AgentName: "另一个 Agent",
	}); !sessionsvc.IsCode(err, sessionsvc.CodeAgentNameConflict) {
		t.Fatalf("changed conversation agent name must be rejected, got %v", err)
	}
}

func TestStartInteractionReusesConversationAgentNameWhenOmitted(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "agent-name-reuse")
	started, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "agent-name-reuse-start",
		AgentName: "供应链分析助手",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}

	replayed, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "agent-name-reuse-start",
	})
	if err != nil || replayed.ID != started.ID {
		t.Fatalf("omitted agent name must reuse conversation declaration: replay=%+v err=%v", replayed, err)
	}
	stored, err := service.GetConversation(context.Background(), owner, conversation.ID)
	if err != nil || stored.AgentName != "供应链分析助手" {
		t.Fatalf("conversation agent name changed after omission: conversation=%+v err=%v", stored, err)
	}
}

func TestManagedLifecycleRecordsCoreOperationalMetrics(t *testing.T) {
	t.Parallel()

	metrics := newTestMetrics()
	service := sessionsvc.New(sessionstore.New(), sessionsvc.Options{Metrics: metrics})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "metrics")
	if _, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "metrics",
	}); err != nil {
		t.Fatalf("idempotent ensure: %v", err)
	}
	if _, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "metrics-1",
	}); err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	if _, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "metrics-2",
	}); !sessionsvc.IsCode(err, sessionsvc.CodeInteractionInProgress) {
		t.Fatalf("expected interaction conflict, got %v", err)
	}

	if metrics.count(icoremetrics.ConversationsTotal) != 1 ||
		metrics.count(icoremetrics.InteractionsTotal) != 1 ||
		metrics.count(icoremetrics.SessionRejectionsTotal) != 1 ||
		metrics.count(icoremetrics.SessionTransitionConflictsTotal) != 1 {
		t.Fatalf("unexpected lifecycle metrics: %#v", metrics.counters)
	}
}

func TestLifecycleMutationAppendsProjectionOutboxOnce(t *testing.T) {
	t.Parallel()

	store := sessionstore.New()
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	if _, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "projected", IdempotencyKey: "projected-1",
	}); err != nil {
		t.Fatalf("ensure conversation: %v", err)
	}
	if _, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "projected", IdempotencyKey: "projected-2",
	}); err != nil {
		t.Fatalf("replay conversation: %v", err)
	}
	if store.PendingProjectionCount() != 1 {
		t.Fatalf("expected one lifecycle projection event, got %d", store.PendingProjectionCount())
	}
}

func TestOperationAndReceiptUseIndependentVersionedProjectionDocuments(t *testing.T) {
	t.Parallel()

	store := sessionstore.New()
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "projection-model")
	interaction, err := service.StartInteraction(
		context.Background(),
		sessionsvc.StartInteractionCommand{
			Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start",
		},
	)
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	operation, receipt, err := service.EnsureOperation(
		context.Background(),
		sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
			OperationKey: "query", ToolName: "ontology-query",
			Input: operationInput("input"), Required: true,
			LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		},
	)
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	items, err := store.Lease(context.Background(), 100, time.Minute)
	if err != nil {
		t.Fatalf("lease projections: %v", err)
	}
	var operationFound, receiptFound bool
	for _, item := range items {
		switch {
		case item.AggregateType == "operation" && item.AggregateID == operation.ID:
			operationFound = true
			if item.AggregateVersion != operation.RowVersion {
				t.Fatalf("operation projection version: %#v", item)
			}
			var payload map[string]any
			if json.Unmarshal(item.Payload, &payload) != nil ||
				payload["operation_id"] != operation.ID || payload["operation"] != nil {
				t.Fatalf("operation projection is not a canonical operation snapshot: %s", item.Payload)
			}
		case item.AggregateType == "receipt" && item.AggregateID == receipt.ID:
			receiptFound = true
			if item.AggregateVersion != receipt.RowVersion {
				t.Fatalf("receipt projection version: %#v", item)
			}
		}
	}
	if !operationFound || !receiptFound {
		t.Fatalf("independent operation/receipt projections missing: %#v", items)
	}
}

func TestConversationGenerationAndOwnerIsolation(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	first := mustEnsureConversation(t, service, owner, "thread-1")
	if _, err := service.CloseConversation(context.Background(), sessionsvc.CloseConversationCommand{
		Owner: owner, ConversationID: first.ID, IdempotencyKey: "close-1",
	}); err != nil {
		t.Fatalf("close conversation: %v", err)
	}
	second := mustEnsureConversation(t, service, owner, "thread-1")
	if second.Generation != 2 || second.ID == first.ID {
		t.Fatalf("expected a new generation, got id=%q generation=%d", second.ID, second.Generation)
	}

	otherOwner := owner
	otherOwner.EffectiveSubjectID = "user-2"
	isolated := mustEnsureConversation(t, service, otherOwner, "thread-1")
	if isolated.ID == second.ID || isolated.Generation != 1 {
		t.Fatalf("expected an isolated generation 1 conversation, got %#v", isolated)
	}
	if _, err := service.GetConversation(context.Background(), otherOwner, second.ID); !sessionsvc.IsCode(err, sessionsvc.CodeResourceNotDisclosed) {
		t.Fatalf("expected non-disclosure for another owner, got %v", err)
	}
	if _, err := service.GetConversation(context.Background(), otherOwner, "missing-conversation"); !sessionsvc.IsCode(err, sessionsvc.CodeResourceNotDisclosed) {
		t.Fatalf("expected the same non-disclosure for a missing conversation, got %v", err)
	}
}

func TestManagedResourceLookupsDoNotDiscloseMissingVersusCrossOwner(t *testing.T) {
	t.Parallel()

	service, owner, _, interaction, operation, receipt := mustCreateOperation(t)
	otherOwner := owner
	otherOwner.EffectiveSubjectID = "other-user"
	tests := []struct {
		name       string
		missing    func() error
		crossOwner func() error
	}{
		{
			name: "interaction",
			missing: func() error {
				_, err := service.GetInteraction(context.Background(), owner, "missing-interaction")
				return err
			},
			crossOwner: func() error {
				_, err := service.GetInteraction(context.Background(), otherOwner, interaction.ID)
				return err
			},
		},
		{
			name: "operation",
			missing: func() error {
				_, err := service.GetOperation(context.Background(), owner, "missing-operation")
				return err
			},
			crossOwner: func() error {
				_, err := service.GetOperation(context.Background(), otherOwner, operation.ID)
				return err
			},
		},
		{
			name: "receipt",
			missing: func() error {
				_, err := service.GetReceipt(context.Background(), owner, "missing-receipt")
				return err
			},
			crossOwner: func() error {
				_, err := service.GetReceipt(context.Background(), otherOwner, receipt.ID)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			missingErr := test.missing()
			crossOwnerErr := test.crossOwner()
			if !sessionsvc.IsCode(missingErr, sessionsvc.CodeResourceNotDisclosed) ||
				!sessionsvc.IsCode(crossOwnerErr, sessionsvc.CodeResourceNotDisclosed) ||
				missingErr.Error() != crossOwnerErr.Error() {
				t.Fatalf("lookup disclosed resource state: missing=%v cross-owner=%v",
					missingErr, crossOwnerErr)
			}
		})
	}
}

func TestCrossConversationResourceReferencesAreNotDisclosed(t *testing.T) {
	t.Parallel()

	service, owner, firstConversation, firstInteraction, firstOperation, _ := mustCreateOperation(t)
	secondConversation := mustEnsureConversation(t, service, owner, "second-resource-scope")
	secondInteraction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: secondConversation.ID, IdempotencyKey: "second-interaction",
	})
	if err != nil {
		t.Fatalf("start second interaction: %v", err)
	}
	secondOperation, secondReceipt, err := service.EnsureOperation(
		context.Background(),
		sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: secondConversation.ID, InteractionID: secondInteraction.ID,
			OperationKey: "second-operation", ToolName: "ontology-query",
			Input: operationInput("second"), Required: true,
			LeaseToken: secondInteraction.LeaseToken, LeaseEpoch: secondInteraction.LeaseEpoch,
		},
	)
	if err != nil {
		t.Fatalf("ensure second operation: %v", err)
	}

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "interaction",
			call: func() error {
				_, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
					Owner: owner, ConversationID: firstConversation.ID, InteractionID: secondInteraction.ID,
					OperationKey: "foreign-interaction", ToolName: "ontology-query",
					Input:      operationInput("foreign-interaction"),
					LeaseToken: secondInteraction.LeaseToken, LeaseEpoch: secondInteraction.LeaseEpoch,
				})
				return err
			},
		},
		{
			name: "parent operation",
			call: func() error {
				_, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
					Owner: owner, ConversationID: firstConversation.ID, InteractionID: firstInteraction.ID,
					OperationKey: "foreign-parent", ToolName: "ontology-query",
					Input: operationInput("foreign-parent"), ParentOperationID: secondOperation.ID,
					LeaseToken: firstInteraction.LeaseToken, LeaseEpoch: firstInteraction.LeaseEpoch,
				})
				return err
			},
		},
		{
			name: "receipt",
			call: func() error {
				_, _, err := service.FailOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
					Owner: owner, OperationID: firstOperation.ID, Attempt: firstOperation.Attempt,
					ReceiptID: secondReceipt.ID, Error: operationError("foreign-receipt"),
					RequestID: "req-foreign-receipt", TraceID: validTraceIDOne,
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !sessionsvc.IsCode(err, sessionsvc.CodeResourceNotDisclosed) {
				t.Fatalf("cross-conversation %s disclosed state: %v", test.name, err)
			}
		})
	}
}

func TestConversationAndRequestIsolationIncludesDelegation(t *testing.T) {
	t.Parallel()

	service := newTestService()
	firstOwner := testOwner()
	firstOwner.DelegationID = "delegation-a"
	secondOwner := firstOwner
	secondOwner.DelegationID = "delegation-b"

	first := mustEnsureConversation(t, service, firstOwner, "shared-delegated-thread")
	second := mustEnsureConversation(t, service, secondOwner, "shared-delegated-thread")
	if first.ID == second.ID {
		t.Fatal("different delegations reused the same conversation")
	}
	firstList, err := service.ListConversations(context.Background(), firstOwner, 10)
	if err != nil || len(firstList) != 1 || firstList[0].ID != first.ID {
		t.Fatalf("delegation-a list leaked another delegation: %#v, %v", firstList, err)
	}
	secondList, err := service.ListConversations(context.Background(), secondOwner, 10)
	if err != nil || len(secondList) != 1 || secondList[0].ID != second.ID {
		t.Fatalf("delegation-b list leaked another delegation: %#v, %v", secondList, err)
	}
}

func TestResumeByIDReturnsOnlyOwnedActiveConversation(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "resume-by-id")
	resumed, err := service.ResumeConversation(context.Background(), sessionsvc.ResumeConversationCommand{
		Owner: owner, ConversationID: conversation.ID,
	})
	if err != nil || resumed.ID != conversation.ID {
		t.Fatalf("resume active conversation: %#v, %v", resumed, err)
	}
	otherOwner := owner
	otherOwner.EffectiveSubjectID = "other-user"
	if _, err := service.ResumeConversation(context.Background(), sessionsvc.ResumeConversationCommand{
		Owner: otherOwner, ConversationID: conversation.ID,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeResourceNotDisclosed) {
		t.Fatalf("expected non-disclosure for another owner, got %v", err)
	}
	if _, err := service.CloseConversation(context.Background(), sessionsvc.CloseConversationCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "close-resume",
	}); err != nil {
		t.Fatalf("close conversation: %v", err)
	}
	if _, err := service.ResumeConversation(context.Background(), sessionsvc.ResumeConversationCommand{
		Owner: owner, ConversationID: conversation.ID,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeConversationClosed) {
		t.Fatalf("expected closed conversation conflict, got %v", err)
	}
}

func TestCreateNewGenerationRejectsActiveInteraction(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	current := mustEnsureConversation(t, service, owner, "thread-generation")
	if _, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: current.ID, IdempotencyKey: "active",
	}); err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	if _, err := service.CreateNewGeneration(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "thread-generation", IdempotencyKey: "new-generation",
	}); !sessionsvc.IsCode(err, sessionsvc.CodeInteractionInProgress) {
		t.Fatalf("expected interaction_in_progress, got %v", err)
	}
}

func TestCreateNewGenerationReplayReturnsSameConversation(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	mustEnsureConversation(t, service, owner, "thread-generation-replay")
	command := sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "thread-generation-replay",
		IdempotencyKey: "new-generation-replay", OneShot: false,
	}
	first, err := service.CreateNewGeneration(context.Background(), command)
	if err != nil {
		t.Fatalf("create generation: %v", err)
	}
	replayed, err := service.CreateNewGeneration(context.Background(), command)
	if err != nil {
		t.Fatalf("replay generation: %v", err)
	}
	if replayed.ID != first.ID || replayed.Generation != first.Generation {
		t.Fatalf("replay created another generation: first=%#v replay=%#v", first, replayed)
	}
	command.OneShot = true
	if _, err := service.CreateNewGeneration(context.Background(), command); !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
		t.Fatalf("expected changed replay to conflict, got %v", err)
	}
}

func TestOnlyOneActiveInteractionAndTerminalIsFenced(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "thread-2")
	first, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start-1",
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	if first.Ordinal != 1 || first.ExecutionStatus != sessionvo.InteractionActive {
		t.Fatalf("unexpected interaction: %#v", first)
	}
	if _, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start-2",
	}); !sessionsvc.IsCode(err, sessionsvc.CodeInteractionInProgress) {
		t.Fatalf("expected interaction_in_progress, got %v", err)
	}
	if _, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: first.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "stale-terminal", LeaseToken: "stale-token", LeaseEpoch: first.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeTerminalConflict) {
		t.Fatalf("expected stale lease fencing conflict, got %v", err)
	}

	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: first.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "terminal-1", LeaseToken: first.LeaseToken, LeaseEpoch: first.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{Version: "1", CompletionReason: "answer_returned"},
	})
	if err != nil {
		t.Fatalf("complete interaction: %v", err)
	}
	if completed.ExecutionStatus != sessionvo.InteractionCompleted {
		t.Fatalf("expected completed, got %q", completed.ExecutionStatus)
	}
	replayed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: first.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "terminal-1", LeaseToken: first.LeaseToken, LeaseEpoch: first.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{Version: "1", CompletionReason: "answer_returned"},
	})
	if err != nil || replayed.ExecutionStatus != sessionvo.InteractionCompleted {
		t.Fatalf("expected idempotent terminal replay, got %#v, %v", replayed, err)
	}
	if _, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: first.ID, Status: sessionvo.InteractionCanceled,
		TerminalIdempotencyKey: "terminal-2", LeaseToken: first.LeaseToken, LeaseEpoch: first.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeTerminalConflict) {
		t.Fatalf("expected terminal_conflict, got %v", err)
	}
}

func TestTerminalInteractionEnqueuesImmutableHistoricalProvenanceBuildRequest(t *testing.T) {
	t.Parallel()

	store, service, owner, _, interaction, operation, receipt := mustCreateOperationWithHistoricalProvenance(t)
	if _, _, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: operation.Attempt, ReceiptID: receipt.ID,
		Output: operationOutput("ok"), EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID: "req-provenance", TraceID: validTraceIDOne,
	}); err != nil {
		t.Fatalf("complete operation: %v", err)
	}
	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "terminal-provenance", LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{Version: "1", CompletionReason: "answer_returned", ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: true}}, ExpectedReceipts: []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: true}}},
	})
	if err != nil {
		t.Fatalf("terminate interaction: %v", err)
	}
	items, err := store.Lease(context.Background(), 20, time.Minute)
	if err != nil {
		t.Fatalf("lease outbox: %v", err)
	}
	var request sessionvo.HistoricalProvenanceBuildRequest
	var eventID string
	found := false
	for _, item := range items {
		if item.EventType != sessionvo.HistoricalProvenanceBuildRequestedEventType {
			continue
		}
		if err := json.Unmarshal(item.Payload, &request); err != nil {
			t.Fatalf("decode provenance request: %v", err)
		}
		eventID = item.EventID
		found = true
	}
	if !found {
		t.Fatalf("terminal interaction did not enqueue %q: %#v", sessionvo.HistoricalProvenanceBuildRequestedEventType, items)
	}
	if request.InteractionID != completed.ID {
		t.Fatalf("unexpected request interaction: %#v", request)
	}
	if request.FactsHash == "" || len(request.Facts) != 1 || request.Facts[0].OperationID != operation.ID {
		t.Fatalf("request does not contain sealed facts: %#v", request)
	}
	claims, err := projectiongrant.Verify(request.ProjectionReadGrant, map[string]ed25519.PublicKey{
		"test-key": testHistoricalProvenanceGrantPrivateKey().Public().(ed25519.PublicKey),
	}, projectiongrant.VerifyOptions{
		Now: time.Now(), ExpectedIssuer: "trace-core", ExpectedAudience: "bkn-projection-read",
	})
	if err != nil {
		t.Fatalf("verify projection read grant: %v", err)
	}
	if claims.EventID != eventID || claims.InteractionID != completed.ID || claims.FactsHash != request.FactsHash {
		t.Fatalf("grant does not bind the terminal event facts: %#v", claims)
	}
	if bytes.Contains(itemPayloadForEvent(items, sessionvo.HistoricalProvenanceBuildRequestedEventType), []byte("Bearer")) {
		t.Fatal("historical provenance request must not serialize a user bearer token")
	}
}

func TestTerminalInteractionFailsClosedWithoutHistoricalProvenanceGrantSigner(t *testing.T) {
	t.Parallel()

	store := sessionstore.New()
	service := sessionsvc.New(store, sessionsvc.Options{EnableHistoricalProvenance: true})
	_, _, owner, _, interaction, operation, receipt := mustCreateOperationForService(t, store, service)
	if _, _, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: operation.Attempt, ReceiptID: receipt.ID,
		Output: operationOutput("ok"), EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID: "req-provenance-without-grant", TraceID: validTraceIDOne,
	}); err != nil {
		t.Fatalf("complete operation: %v", err)
	}
	if _, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "terminal-provenance-without-grant", LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{Version: "1", CompletionReason: "answer_returned", ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: true}}, ExpectedReceipts: []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: true}}},
	}); err == nil {
		t.Fatal("terminal interaction must fail closed when historical provenance has no projection grant signer")
	}
	items, err := store.Lease(context.Background(), 20, time.Minute)
	if err != nil {
		t.Fatalf("lease outbox: %v", err)
	}
	if payload := itemPayloadForEvent(items, sessionvo.HistoricalProvenanceBuildRequestedEventType); payload != nil {
		t.Fatalf("unsigned historical provenance event must not be enqueued: %s", payload)
	}
}

func TestLateReceiptDoesNotEnqueueAnotherHistoricalProvenanceBuildRequest(t *testing.T) {
	t.Parallel()

	store, service, owner, _, interaction, operation, receipt := mustCreateOperationWithHistoricalProvenance(t)
	if _, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "terminal-late-receipt", LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{Version: "1", CompletionReason: "answer_returned", ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: true}}, ExpectedReceipts: []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: true}}},
	}); err != nil {
		t.Fatalf("terminate interaction: %v", err)
	}
	initial, err := store.Lease(context.Background(), 20, time.Minute)
	if err != nil {
		t.Fatalf("lease terminal events: %v", err)
	}
	for _, item := range initial {
		if err := store.MarkDelivered(context.Background(), item); err != nil {
			t.Fatalf("mark initial event delivered: %v", err)
		}
	}
	if _, _, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: operation.Attempt, ReceiptID: receipt.ID,
		Output: operationOutput("late"), EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID: "req-late", TraceID: validTraceIDOne,
	}); err != nil {
		t.Fatalf("complete late receipt: %v", err)
	}
	late, err := store.Lease(context.Background(), 20, time.Minute)
	if err != nil {
		t.Fatalf("lease late receipt events: %v", err)
	}
	for _, item := range late {
		if item.EventType == sessionvo.HistoricalProvenanceBuildRequestedEventType {
			t.Fatalf("late receipt must not enqueue a replacement provenance build request: %#v", item)
		}
	}
}

func itemPayloadForEvent(items []iprojectionoutbox.Item, eventType string) []byte {
	for _, item := range items {
		if item.EventType == eventType {
			return item.Payload
		}
	}
	return nil
}

func TestStartInteractionRejectsChangedPayloadForSameIdempotencyKey(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "start-payload-conflict")
	command := sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "host-turn-1",
		RequestHash: "question-hash-a",
	}
	first, err := service.StartInteraction(context.Background(), command)
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	replayed, err := service.StartInteraction(context.Background(), command)
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("same payload must replay first interaction: replay=%#v err=%v", replayed, err)
	}
	command.RequestHash = "question-hash-b"
	if _, err := service.StartInteraction(context.Background(), command); !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
		t.Fatalf("expected idempotency_conflict for changed start payload, got %v", err)
	}
}

func TestConcurrentStartInteractionAllowsOnlyOneActive(t *testing.T) {
	t.Parallel()

	service := sessionsvc.New(sessionstore.New(), sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "concurrent-active")
	const contenders = 12
	start := make(chan struct{})
	results := make(chan error, contenders)
	for index := 0; index < contenders; index++ {
		go func(index int) {
			<-start
			_, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
				Owner: owner, ConversationID: conversation.ID,
				IdempotencyKey: fmt.Sprintf("concurrent-%d", index),
			})
			results <- err
		}(index)
	}
	close(start)
	successes := 0
	conflicts := 0
	for index := 0; index < contenders; index++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case sessionsvc.IsCode(err, sessionsvc.CodeInteractionInProgress):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent start result: %v", err)
		}
	}
	if successes != 1 || conflicts != contenders-1 {
		t.Fatalf("active interaction race was not fenced: successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestStartInteractionReplayReturnsOriginalAfterTerminal(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "thread-start-replay")
	command := sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID,
		IdempotencyKey: "start-replay", LeaseDuration: time.Minute,
	}
	started, err := service.StartInteraction(context.Background(), command)
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	if _, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: started.ID, Status: sessionvo.InteractionCanceled,
		TerminalIdempotencyKey: "cancel-replay", LeaseToken: started.LeaseToken,
		LeaseEpoch: started.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "caller_canceled",
		},
	}); err != nil {
		t.Fatalf("cancel interaction: %v", err)
	}

	replayed, err := service.StartInteraction(context.Background(), command)
	if err != nil {
		t.Fatalf("replay start interaction: %v", err)
	}
	if replayed.ID != started.ID {
		t.Fatalf("replay created another interaction: first=%s replay=%s", started.ID, replayed.ID)
	}
}

func TestStartInteractionReplaysLegacyStartKeyAfterTerminal(t *testing.T) {
	t.Parallel()

	store := sessionstore.New()
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "legacy-start-replay")
	legacy := sessionvo.Interaction{
		ID:                  "int_legacy_start_replay",
		ConversationID:      conversation.ID,
		Ordinal:             1,
		ExecutionStatus:     sessionvo.InteractionCompleted,
		EvidenceStatus:      sessionvo.EvidenceComplete,
		StartIdempotencyKey: "legacy-start-key",
		CreatedAt:           time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
		UpdatedAt:           time.Date(2026, 8, 1, 9, 0, 1, 0, time.UTC),
	}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveInteraction(legacy)
		return nil
	}); err != nil {
		t.Fatalf("seed legacy interaction: %v", err)
	}

	replayed, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: legacy.StartIdempotencyKey,
	})
	if err != nil || replayed.ID != legacy.ID {
		t.Fatalf("legacy replay = %+v, %v; want %s", replayed, err, legacy.ID)
	}
}

func TestStartInteractionBackfillsAgentNameForExistingConversation(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "legacy-agent-name")
	first, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "legacy-agent-first",
	})
	if err != nil {
		t.Fatalf("start unnamed interaction: %v", err)
	}
	if _, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: first.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "legacy-agent-finish", LeaseToken: first.LeaseToken, LeaseEpoch: first.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{Version: "1", CompletionReason: "answer_returned"},
	}); err != nil {
		t.Fatalf("finish unnamed interaction: %v", err)
	}
	if _, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "legacy-agent-second", AgentName: "供应链分析助手",
	}); err != nil {
		t.Fatalf("backfill agent name: %v", err)
	}
	stored, err := service.GetConversation(context.Background(), owner, conversation.ID)
	if err != nil || stored.AgentName != "供应链分析助手" {
		t.Fatalf("conversation agent name = %q, err = %v", stored.AgentName, err)
	}
}

func TestConversationPersistsThreeSequentialInteractionRounds(t *testing.T) {
	t.Parallel()

	store := sessionstore.New()
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "three-rounds")

	for round := 1; round <= 3; round++ {
		interaction, err := service.StartInteraction(
			context.Background(),
			sessionsvc.StartInteractionCommand{
				Owner: owner, ConversationID: conversation.ID,
				IdempotencyKey: fmt.Sprintf("round-%d", round),
			},
		)
		if err != nil {
			t.Fatalf("start round %d: %v", round, err)
		}
		if interaction.Ordinal != uint64(round) {
			t.Fatalf("round ordinal = %d, want %d", interaction.Ordinal, round)
		}
		completed, err := service.TerminateInteraction(
			context.Background(),
			sessionsvc.TerminateInteractionCommand{
				Owner: owner, InteractionID: interaction.ID,
				Status:                 sessionvo.InteractionCompleted,
				TerminalIdempotencyKey: fmt.Sprintf("complete-%d", round),
				LeaseToken:             interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
				Manifest: sessionvo.ClosureManifest{
					Version: "1", CompletionReason: "answer_returned",
				},
			},
		)
		if err != nil || completed.ExecutionStatus != sessionvo.InteractionCompleted {
			t.Fatalf("complete round %d: %#v, %v", round, completed, err)
		}
	}
}

func TestEnsureOperationUsesStableLogicalKeyAndCanonicalInput(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "thread-3")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start-op",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	first, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "query-sales-orders", ToolName: "ontology-query",
		Input: operationInput("input-a"), CausationEventIDs: []string{"event-b", "event-a", "event-a"}, Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	if receipt.CausationEventIDs == nil || receipt.ObservedEvidenceRefs == nil ||
		receipt.BusinessRefs == nil || receipt.ArtifactRefs == nil || receipt.PartialReasons == nil {
		t.Fatalf("pending receipt must expose required collection fields as empty arrays: %#v", receipt)
	}
	replayed, replayedReceipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "query-sales-orders", ToolName: "ontology-query",
		Input: operationInput("input-a"), CausationEventIDs: []string{"event-a", "event-b"}, Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("replay operation: %v", err)
	}
	if first.ID != replayed.ID || receipt.ID != replayedReceipt.ID || first.Attempt != 1 {
		t.Fatalf("expected the same operation and receipt, got %#v / %#v", replayed, replayedReceipt)
	}
	if !slices.Equal(first.CausationEventIDs, []string{"event-a", "event-b"}) {
		t.Fatalf("causation IDs were not canonicalized as a set: %#v", first.CausationEventIDs)
	}
	if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "query-sales-orders", ToolName: "ontology-query",
		Input: operationInput("input-b"), CausationEventIDs: []string{"event-a", "event-b"}, Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
		t.Fatalf("expected idempotency_conflict, got %v", err)
	}
	for name, mutate := range map[string]func(*sessionsvc.EnsureOperationCommand){
		"parent operation": func(command *sessionsvc.EnsureOperationCommand) {
			command.ParentOperationID = first.ID
		},
		"causation events": func(command *sessionsvc.EnsureOperationCommand) {
			command.CausationEventIDs = []string{"event-different"}
		},
		"required flag": func(command *sessionsvc.EnsureOperationCommand) {
			command.Required = false
		},
	} {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			command := sessionsvc.EnsureOperationCommand{
				Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
				OperationKey: "query-sales-orders", ToolName: "ontology-query",
				Input: operationInput("input-a"), CausationEventIDs: []string{"event-a", "event-b"}, Required: true,
				LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
			}
			mutate(&command)
			if _, _, err := service.EnsureOperation(context.Background(), command); !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
				t.Fatalf("changed %s must conflict, got %v", name, err)
			}
		})
	}
	if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "child-with-unknown-parent", ToolName: "ontology-query",
		Input: operationInput("child"), Required: true,
		ParentOperationID: "op-from-another-interaction",
		LeaseToken:        interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeResourceNotDisclosed) {
		t.Fatalf("unknown parent operation was disclosed: %v", err)
	}
}

func TestEnsureOperationReportsCreatedDisposition(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "created-disposition")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start-created-disposition",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	command := sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "logical-call", ToolName: "context-loader",
		Input: operationInput("input"), Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}
	first, err := service.EnsureOperationWithDisposition(context.Background(), command)
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if !first.Created || !first.Execute {
		t.Fatalf("first ensure must report created=true and execute=true: %#v", first)
	}
	replayed, err := service.EnsureOperationWithDisposition(context.Background(), command)
	if err != nil {
		t.Fatalf("replay ensure: %v", err)
	}
	if replayed.Created || replayed.Execute || replayed.Operation.ID != first.Operation.ID ||
		replayed.Receipt.ID != first.Receipt.ID {
		t.Fatalf("replay must report created=false and reuse resources: %#v", replayed)
	}
}

func TestOversizedOperationInputIsOmittedAndMakesInteractionPartial(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "oversized-input")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "oversized-input-start",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	input, err := sessionvo.InlineJSONPayload(
		json.RawMessage(`{"value":"` + strings.Repeat("a", sessionvo.MaxInlinePayloadBytes) + `"}`),
	)
	if err != nil {
		t.Fatalf("build oversized input envelope: %v", err)
	}
	operation, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "oversized-input-call", ToolName: "run_sql", Input: input, Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	if _, receipt, err = service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: operation.Attempt, ReceiptID: receipt.ID,
		Output: operationOutput("ok"), EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID: "req-oversized-input", TraceID: validTraceIDOne,
	}); err != nil {
		t.Fatalf("complete operation: %v", err)
	}
	if len(receipt.PartialReasons) != 1 ||
		receipt.PartialReasons[0] != sessionvo.PayloadOmittedReasonTooLarge {
		t.Fatalf("oversized input must make receipt partial: %#v", receipt.PartialReasons)
	}
	facts, err := service.ListOperationCallFacts(context.Background(), owner, interaction.ID)
	if err != nil {
		t.Fatalf("list operation call facts: %v", err)
	}
	if len(facts) != 1 || facts[0].Input.Mode != sessionvo.PayloadOmitted ||
		facts[0].Input.OmittedReason != sessionvo.PayloadOmittedReasonTooLarge ||
		facts[0].Input.Inline != nil {
		t.Fatalf("oversized input fact = %#v", facts)
	}
	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "oversized-input-terminal",
		LeaseToken:             interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "answer_returned",
			ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: true}},
			ExpectedReceipts:   []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: true}},
		},
	})
	if err != nil {
		t.Fatalf("terminate interaction: %v", err)
	}
	if completed.EvidenceStatus != sessionvo.EvidencePartial {
		t.Fatalf("EvidenceStatus = %q, want %q", completed.EvidenceStatus, sessionvo.EvidencePartial)
	}
}

func TestOversizedOperationOutputIsOmittedAndMakesReceiptPartial(t *testing.T) {
	t.Parallel()

	service, owner, _, interaction, operation, receipt := mustCreateOperation(t)
	output := mustPayloadEnvelope(
		t, json.RawMessage(`{"rows":"`+strings.Repeat("a", sessionvo.MaxInlinePayloadBytes)+`"}`),
	)
	_, completedReceipt, err := service.CompleteOperationAttempt(
		context.Background(),
		sessionsvc.FinishAttemptCommand{
			Owner: owner, OperationID: operation.ID, Attempt: operation.Attempt,
			ReceiptID: receipt.ID, Output: output,
			EvidenceDurability: sessionvo.DurabilityDurable,
			RequestID:          "req-oversized-output", TraceID: validTraceIDOne,
		},
	)
	if err != nil {
		t.Fatalf("complete operation: %v", err)
	}
	if len(completedReceipt.PartialReasons) != 1 ||
		completedReceipt.PartialReasons[0] != sessionvo.PayloadOmittedReasonTooLarge {
		t.Fatalf("oversized output must make receipt partial: %#v", completedReceipt.PartialReasons)
	}
	facts, err := service.ListOperationCallFacts(context.Background(), owner, interaction.ID)
	if err != nil {
		t.Fatalf("list operation call facts: %v", err)
	}
	if len(facts) != 1 || facts[0].Output == nil ||
		facts[0].Output.Mode != sessionvo.PayloadOmitted ||
		facts[0].Output.OmittedReason != sessionvo.PayloadOmittedReasonTooLarge ||
		facts[0].Output.Inline != nil {
		t.Fatalf("oversized output fact = %#v", facts)
	}
}

func TestOmittedInputCannotBeClaimedAsIdempotentReplay(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "omitted-replay")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "omitted-replay-start",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	command := sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "omitted-replay-call", ToolName: "run_sql",
		Input: mustPayloadEnvelope(
			t, json.RawMessage(`{"value":"`+strings.Repeat("a", sessionvo.MaxInlinePayloadBytes)+`"}`),
		),
		Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}
	if _, err := service.EnsureOperationWithDisposition(context.Background(), command); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if _, err := service.EnsureOperationWithDisposition(context.Background(), command); !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
		t.Fatalf("omitted input replay must fail closed, got %v", err)
	}
}

func TestEnsureOperationCreatedDispositionDoesNotLeakAcrossTransactionRetry(t *testing.T) {
	t.Parallel()

	store := &retryAfterVisibleCommitStore{Store: sessionstore.New()}
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "retry-visible-commit")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "retry-visible-start",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	store.retryNext = true
	result, err := service.EnsureOperationWithDisposition(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "retry-visible-operation", ToolName: "execute_action",
		Input: operationInput("retry-visible"), Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation after transaction retry: %v", err)
	}
	if store.callbackCalls != 2 {
		t.Fatalf("expected two transaction callbacks, got %d", store.callbackCalls)
	}
	if result.Created || result.Execute {
		t.Fatalf("second callback replay leaked first callback execution claim: %#v", result)
	}
}

type retryAfterVisibleCommitStore struct {
	isessionstore.Store
	retryNext     bool
	callbackCalls int
}

func (s *retryAfterVisibleCommitStore) WithinTransaction(
	ctx context.Context,
	callback func(isessionstore.Transaction) error,
) error {
	if !s.retryNext {
		return s.Store.WithinTransaction(ctx, callback)
	}
	s.retryNext = false
	s.callbackCalls = 0
	if err := s.Store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		s.callbackCalls++
		return callback(tx)
	}); err != nil {
		return err
	}
	return s.Store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		s.callbackCalls++
		return callback(tx)
	})
}

func TestOperationIntentRequiresCurrentInteractionLeaseAndRenewsIt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := sessionstore.NewWithClock(func() time.Time { return now })
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "operation-lease")
	interaction, err := service.StartInteraction(
		context.Background(),
		sessionsvc.StartInteractionCommand{
			Owner: owner, ConversationID: conversation.ID,
			IdempotencyKey: "start", LeaseDuration: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	if _, _, err := service.EnsureOperation(
		context.Background(),
		sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: conversation.ID,
			InteractionID: interaction.ID, OperationKey: "query",
			ToolName: "ontology-query", Input: operationInput("query"),
			LeaseToken: "stale-token", LeaseEpoch: interaction.LeaseEpoch,
		},
	); !sessionsvc.IsCode(err, sessionsvc.CodeTerminalConflict) {
		t.Fatalf("stale operation lease was not fenced: %v", err)
	}

	now = now.Add(30 * time.Second)
	if _, _, err := service.EnsureOperation(
		context.Background(),
		sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: conversation.ID,
			InteractionID: interaction.ID, OperationKey: "query",
			ToolName: "ontology-query", Input: operationInput("query"),
			LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		},
	); err != nil {
		t.Fatalf("ensure operation with current lease: %v", err)
	}
	renewed, err := service.GetInteraction(context.Background(), owner, interaction.ID)
	if err != nil {
		t.Fatalf("get renewed interaction: %v", err)
	}
	if !renewed.LeaseExpiresAt.Equal(now.Add(5*time.Minute)) ||
		renewed.LeaseVersion != interaction.LeaseVersion+1 {
		t.Fatalf("operation intent did not renew interaction lease: %#v", renewed)
	}
}

func TestRetryAttemptRequiresRetryableFailure(t *testing.T) {
	t.Parallel()

	service, owner, conversation, interaction, operation, receipt := mustCreateOperation(t)
	if _, _, err := service.StartOperationAttempt(context.Background(), sessionsvc.StartAttemptCommand{
		Owner: owner, OperationID: operation.ID,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeReceiptPending) {
		t.Fatalf("expected receipt_pending before a retryable failure, got %v", err)
	}
	if _, _, err := service.FailOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: 1,
		ReceiptID: receipt.ID, Error: operationError("failed"), Retryable: true,
		RequestID: "req-retry", TraceID: validTraceIDOne,
	}); err != nil {
		t.Fatalf("fail operation attempt: %v", err)
	}
	retry, retryReceipt, err := service.StartOperationAttempt(context.Background(), sessionsvc.StartAttemptCommand{
		Owner: owner, OperationID: operation.ID,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("start retry: %v", err)
	}
	if retry.Attempt != 2 || retryReceipt.Attempt != 2 || retryReceipt.ID == receipt.ID {
		t.Fatalf("expected a new attempt and receipt, got %#v / %#v", retry, retryReceipt)
	}
	if retry.AttemptStatus != sessionvo.AttemptReady {
		t.Fatalf("explicit retry must prepare, not execute, the next attempt: %#v", retry)
	}
	if _, _, err := service.CompleteOperationAttempt(
		context.Background(),
		sessionsvc.FinishAttemptCommand{
			Owner: owner, OperationID: retry.ID, Attempt: retry.Attempt,
			ReceiptID: retryReceipt.ID, Output: operationOutput("not-executed"),
			EvidenceDurability: sessionvo.DurabilityDurable,
			RequestID:          "req-not-executed", TraceID: validTraceIDOne,
		},
	); !sessionsvc.IsCode(err, sessionsvc.CodeReceiptPending) {
		t.Fatalf("unclaimed retry attempt was allowed to finalize: %v", err)
	}
	if _, err := service.EnsureOperationWithDisposition(
		context.Background(),
		sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
			OperationKey: operation.OperationKey, ToolName: operation.ToolName,
			Protocol: sessionvo.ProtocolSDK, SourceModule: "different-producer",
			Input: operationInput("different-input"), Required: true,
			LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		},
	); !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
		t.Fatalf("retry changed producer and input without conflict: %v", err)
	}

	claimed, err := service.EnsureOperationWithDisposition(
		context.Background(),
		sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
			OperationKey: operation.OperationKey, ToolName: operation.ToolName,
			Input: operationInput("input"), Required: true,
			LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		},
	)
	if err != nil {
		t.Fatalf("claim retry attempt: %v", err)
	}
	if claimed.Created || !claimed.Execute ||
		claimed.Operation.AttemptStatus != sessionvo.AttemptPending {
		t.Fatalf("first ensure after retry must claim execution exactly once: %#v", claimed)
	}

	replayed, err := service.EnsureOperationWithDisposition(
		context.Background(),
		sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
			OperationKey: operation.OperationKey, ToolName: operation.ToolName,
			Input: operationInput("input"), Required: true,
			LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		},
	)
	if err != nil {
		t.Fatalf("replay claimed retry attempt: %v", err)
	}
	if replayed.Execute {
		t.Fatalf("retry attempt execution was claimed more than once: %#v", replayed)
	}
}

func TestCompleteOperationAttemptDefaultsEvidenceDurabilityToPending(t *testing.T) {
	t.Parallel()

	service, owner, _, _, operation, receipt := mustCreateOperation(t)
	_, completed, err := service.CompleteOperationAttempt(
		context.Background(),
		sessionsvc.FinishAttemptCommand{
			Owner: owner, OperationID: operation.ID, Attempt: operation.Attempt,
			ReceiptID: receipt.ID, Output: operationOutput("completed-without-durable-ack"),
			RequestID: "req-pending-default", TraceID: validTraceIDOne,
		},
	)
	if err != nil {
		t.Fatalf("complete operation attempt: %v", err)
	}
	if completed.EvidenceDurability != sessionvo.DurabilityPending {
		t.Fatalf("missing durable ACK must fail closed to pending: %#v", completed)
	}
}

func TestTrustedAdapterCanMarkAnyFailedOperationRetryable(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "adapter-retryability")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "adapter-retryability",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	operation, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "unregistered-side-effect", ToolName: "unregistered-destructive-tool",
		Input: operationInput("unregistered"), Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	failed, failedReceipt, err := service.FailOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: operation.Attempt,
		ReceiptID: receipt.ID, Error: operationError("malicious-true"), Retryable: true,
		RequestID: "req-malicious-true", TraceID: validTraceIDOne,
	})
	if err != nil {
		t.Fatalf("fail operation: %v", err)
	}
	if !failed.Retryable {
		t.Fatal("Core discarded the trusted adapter retryability observation")
	}
	if len(failedReceipt.PartialReasons) != 1 ||
		failedReceipt.PartialReasons[0] != "evidence_durability_failed" {
		t.Fatalf("failed evidence durability must carry an objective reason: %#v", failedReceipt)
	}
	if _, _, err := service.StartOperationAttempt(context.Background(), sessionsvc.StartAttemptCommand{
		Owner: owner, OperationID: operation.ID,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); err != nil {
		t.Fatalf("Core rejected retry authorized by the trusted adapter: %v", err)
	}
}

func TestFinishAttemptRejectsInvalidTraceID(t *testing.T) {
	t.Parallel()

	service, owner, _, _, operation, receipt := mustCreateOperation(t)
	_, _, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: operation.Attempt,
		ReceiptID: receipt.ID, Output: operationOutput("complete"),
		RequestID: "req-invalid-trace", TraceID: "not-a-valid-trace-id",
	})
	if !sessionsvc.IsCode(err, sessionsvc.CodeOperationRequired) {
		t.Fatalf("expected operation_required for invalid trace ID, got %v", err)
	}
}

func TestCompletionManifestRejectsUnknownReceipt(t *testing.T) {
	t.Parallel()

	service, owner, _, interaction, _, _ := mustCreateOperation(t)
	if _, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "terminal-invalid", LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "answer_returned",
			ExpectedReceipts: []sessionvo.ExpectedReceipt{{ReceiptID: "rcpt_unknown", Required: true}},
		},
	}); !sessionsvc.IsCode(err, sessionsvc.CodeClosureManifestInvalid) {
		t.Fatalf("expected closure_manifest_invalid, got %v", err)
	}
}

func TestCompletionManifestRejectsOmittedRegisteredOperationAndReceipt(t *testing.T) {
	t.Parallel()

	service, owner, _, interaction, _, _ := mustCreateOperation(t)
	if _, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "terminal-omitted", LeaseToken: interaction.LeaseToken,
		LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "answer_returned",
		},
	}); !sessionsvc.IsCode(err, sessionsvc.CodeClosureManifestInvalid) {
		t.Fatalf("expected closure_manifest_invalid, got %v", err)
	}
}

func TestExpiredAssemblerDeadlineFreezesPartialRevision(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	store := sessionstore.NewWithClock(func() time.Time { return now })
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "deadline")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "deadline-start",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	operation, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "pending", ToolName: "ontology-query",
		Input: operationInput("pending"), Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	deadline := now.Add(-time.Second)
	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "deadline-terminal", LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "answer_returned", AssemblerDeadline: &deadline,
			ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: true}},
			ExpectedReceipts:   []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: true}},
		},
	})
	if err != nil {
		t.Fatalf("complete interaction: %v", err)
	}
	if completed.EvidenceStatus != sessionvo.EvidencePartial {
		t.Fatalf("expected partial after deadline, got %q", completed.EvidenceStatus)
	}
	revisions, err := service.ListAssemblyRevisions(context.Background(), owner, interaction.ID)
	if err != nil || len(revisions) != 1 || revisions[0].Trigger != "deadline" {
		t.Fatalf("unexpected deadline revision: %#v, %v", revisions, err)
	}
}

func TestAssemblerFreezesPendingInteractionWhenDeadlineLaterExpires(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	store := sessionstore.NewWithClock(func() time.Time { return now })
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "future-deadline")
	interaction, _ := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "future-start",
	})
	operation, receipt, _ := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "future", ToolName: "query", Input: operationInput("future"), Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	deadline := now.Add(time.Minute)
	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "future-terminal", LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "answer_returned", AssemblerDeadline: &deadline,
			ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: true}},
			ExpectedReceipts:   []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: true}},
		},
	})
	if err != nil || completed.EvidenceStatus != sessionvo.EvidenceAssembling {
		t.Fatalf("expected assembling before deadline, got %#v, %v", completed, err)
	}
	now = now.Add(2 * time.Minute)
	assembled, err := service.AssembleDueInteractions(context.Background(), 10)
	if err != nil || len(assembled) != 1 || assembled[0].EvidenceStatus != sessionvo.EvidencePartial {
		t.Fatalf("unexpected deadline assembly: %#v, %v", assembled, err)
	}
}

func TestLateDurableReceiptCreatesNewImmutableAssemblyRevision(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	store := sessionstore.NewWithClock(func() time.Time { return now })
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "late-receipt")
	interaction, _ := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "late-start",
	})
	operation, receipt, _ := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "late", ToolName: "query", Input: operationInput("late"), Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	deadline := now.Add(-time.Second)
	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "late-terminal", LeaseToken: interaction.LeaseToken,
		LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "answer_returned", AssemblerDeadline: &deadline,
			ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: true}},
			ExpectedReceipts:   []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: true}},
		},
	})
	if err != nil || completed.EvidenceStatus != sessionvo.EvidencePartial {
		t.Fatalf("freeze partial revision: %#v, %v", completed, err)
	}

	_, _, err = service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: receipt.Attempt,
		ReceiptID: receipt.ID, Output: operationOutput("late-result"),
		EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID:          "req-late", TraceID: validTraceIDOne,
		ObservedEvidenceRefs: []string{"evt-late-evidence"},
	})
	if err != nil {
		t.Fatalf("complete late receipt: %v", err)
	}
	revisions, err := service.ListAssemblyRevisions(context.Background(), owner, interaction.ID)
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(revisions) != 2 || revisions[0].Completeness != sessionvo.EvidencePartial ||
		revisions[1].Completeness != sessionvo.EvidenceComplete ||
		revisions[1].ParentRevisionID != revisions[0].ID || revisions[1].Trigger != "late_receipt" ||
		revisions[0].ArtifactManifestHash == revisions[1].ArtifactManifestHash {
		t.Fatalf("unexpected immutable revision chain: %#v", revisions)
	}
}

func TestPendingReceiptCompletedBeforeDeadlineCreatesFirstAssemblyRevision(t *testing.T) {
	t.Parallel()

	service, owner, _, interaction, operation, receipt := mustCreateOperation(t)
	deadline := time.Now().Add(time.Hour)
	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "terminal-before-deadline",
		LeaseToken:             interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "answer_returned", AssemblerDeadline: &deadline,
			ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: true}},
			ExpectedReceipts:   []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: true}},
		},
	})
	if err != nil || completed.EvidenceStatus != sessionvo.EvidenceAssembling {
		t.Fatalf("expected assembling interaction: %#v, %v", completed, err)
	}

	_, _, err = service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: receipt.Attempt,
		ReceiptID: receipt.ID, Output: operationOutput("before-deadline"),
		EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID:          "req-before-deadline", TraceID: validTraceIDOne,
	})
	if err != nil {
		t.Fatalf("complete receipt before deadline: %v", err)
	}

	current, err := service.GetInteraction(context.Background(), owner, interaction.ID)
	if err != nil || current.EvidenceStatus != sessionvo.EvidenceComplete {
		t.Fatalf("expected complete evidence after receipt: %#v, %v", current, err)
	}
	revisions, err := service.ListAssemblyRevisions(context.Background(), owner, interaction.ID)
	if err != nil || len(revisions) != 1 ||
		revisions[0].RevisionNo != 1 || revisions[0].Trigger != "late_receipt" ||
		revisions[0].Completeness != sessionvo.EvidenceComplete {
		t.Fatalf("expected first immutable completion revision: %#v, %v", revisions, err)
	}
}

func TestDurableReceiptMakesCompletedInteractionEvidenceComplete(t *testing.T) {
	t.Parallel()

	service, owner, _, interaction, operation, receipt := mustCreateOperation(t)
	_, durableReceipt, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: 1,
		ReceiptID: receipt.ID, Output: operationOutput("complete"),
		EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID:          "req-complete", TraceID: validTraceIDOne,
	})
	if err != nil {
		t.Fatalf("complete operation attempt: %v", err)
	}
	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "terminal-complete", LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "answer_returned",
			ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: true}},
			ExpectedReceipts:   []sessionvo.ExpectedReceipt{{ReceiptID: durableReceipt.ID, Required: true}},
		},
	})
	if err != nil {
		t.Fatalf("complete interaction: %v", err)
	}
	if completed.EvidenceStatus != sessionvo.EvidenceComplete {
		t.Fatalf("expected complete evidence, got %q", completed.EvidenceStatus)
	}
	revisions, err := service.ListAssemblyRevisions(context.Background(), owner, interaction.ID)
	if err != nil {
		t.Fatalf("list assembly revisions: %v", err)
	}
	if len(revisions) != 1 || revisions[0].RevisionNo != 1 ||
		revisions[0].Completeness != sessionvo.EvidenceComplete ||
		len(revisions[0].IncludedReceiptIDs) != 1 {
		t.Fatalf("unexpected frozen revision: %#v", revisions)
	}
}

func TestDurableReceiptFreezesEvidenceBusinessAndArtifactReferences(t *testing.T) {
	t.Parallel()

	service, owner, _, interaction, operation, receipt := mustCreateOperation(t)
	businessRef := sessionvo.BusinessRef{
		RefType: "object_type", RefID: "object:supplychain_hd0202:forecast",
		Version:     "v3",
		DisplayHint: "需求预测单",
	}
	_, durableReceipt, err := service.CompleteOperationAttempt(
		context.Background(),
		sessionsvc.FinishAttemptCommand{
			Owner: owner, OperationID: operation.ID, Attempt: 1,
			ReceiptID: receipt.ID, Output: operationOutput("complete-with-evidence"),
			EvidenceDurability:   sessionvo.DurabilityDurable,
			RequestID:            "req-complete-with-evidence",
			TraceID:              validTraceIDOne,
			ObservedEvidenceRefs: []string{"evt-data-query-1"},
			BusinessRefs:         []sessionvo.BusinessRef{businessRef},
			ArtifactRefs:         []string{"artifact://answer/fragment-1"},
		},
	)
	if err != nil {
		t.Fatalf("complete operation attempt: %v", err)
	}
	if len(durableReceipt.ObservedEvidenceRefs) != 1 ||
		len(durableReceipt.BusinessRefs) != 1 ||
		len(durableReceipt.ArtifactRefs) != 1 {
		t.Fatalf("receipt omitted evidence metadata: %#v", durableReceipt)
	}
	_, err = service.TerminateInteraction(
		context.Background(),
		sessionsvc.TerminateInteractionCommand{
			Owner: owner, InteractionID: interaction.ID,
			Status:                 sessionvo.InteractionCompleted,
			TerminalIdempotencyKey: "terminal-complete-with-evidence",
			LeaseToken:             interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
			Manifest: sessionvo.ClosureManifest{
				Version: "1", CompletionReason: "answer_returned",
				ExpectedOperations: []sessionvo.ExpectedOperation{{
					OperationID: operation.ID, Required: true,
				}},
				ExpectedReceipts: []sessionvo.ExpectedReceipt{{
					ReceiptID: durableReceipt.ID, Required: true,
				}},
			},
		},
	)
	if err != nil {
		t.Fatalf("complete interaction: %v", err)
	}
	revisions, err := service.ListAssemblyRevisions(context.Background(), owner, interaction.ID)
	if err != nil {
		t.Fatalf("list assembly revisions: %v", err)
	}
	if len(revisions) != 1 ||
		len(revisions[0].IncludedEventIDs) != 1 ||
		revisions[0].IncludedEventIDs[0] != "evt-data-query-1" {
		t.Fatalf("assembly omitted evidence event references: %#v", revisions)
	}

	_, _, err = service.CompleteOperationAttempt(
		context.Background(),
		sessionsvc.FinishAttemptCommand{
			Owner: owner, OperationID: operation.ID, Attempt: 1,
			ReceiptID: receipt.ID, Output: operationOutput("complete-with-evidence"),
			EvidenceDurability:   sessionvo.DurabilityDurable,
			RequestID:            "req-complete-with-evidence",
			TraceID:              validTraceIDOne,
			ObservedEvidenceRefs: []string{"evt-rewritten"},
			BusinessRefs:         []sessionvo.BusinessRef{businessRef},
			ArtifactRefs:         []string{"artifact://answer/fragment-1"},
		},
	)
	if !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
		t.Fatalf("receipt replay changed immutable evidence refs: %v", err)
	}
}

func TestOperationReceiptRejectsInvalidTypedBusinessReference(t *testing.T) {
	t.Parallel()

	tests := map[string]sessionvo.BusinessRef{
		"unknown type": {
			RefType: "result", RefID: "result:forecast", Version: "1",
		},
		"short ref id": {
			RefType: sessionvo.BusinessRefObjectType, RefID: "object:forecast", Version: "1",
		},
		"missing version": {
			RefType: sessionvo.BusinessRefObjectType, RefID: "object:supplychain:forecast"},
	}
	for name, businessRef := range tests {
		name, businessRef := name, businessRef
		t.Run(name, func(t *testing.T) {
			service, owner, _, _, operation, receipt := mustCreateOperation(t)
			_, _, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
				Owner: owner, OperationID: operation.ID, Attempt: receipt.Attempt,
				ReceiptID: receipt.ID, Output: operationOutput("invalid-business-ref"),
				EvidenceDurability: sessionvo.DurabilityDurable,
				RequestID:          "req-invalid-business-ref", TraceID: validTraceIDOne,
				BusinessRefs: []sessionvo.BusinessRef{businessRef},
			})
			if !sessionsvc.IsCode(err, sessionsvc.CodeOperationRequired) {
				t.Fatalf("invalid receipt business ref must be rejected, got %v", err)
			}
		})
	}
}

func TestOperationReceiptReplaysLegacyTerminalBusinessReferenceIdempotently(t *testing.T) {
	t.Parallel()

	store, service, owner, _, _, operation, receipt := mustCreateOperationWithStore(t)
	command := sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: receipt.Attempt,
		ReceiptID: receipt.ID, Output: operationOutput("legacy-business-ref"),
		EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID:          "req-legacy-business-ref", TraceID: validTraceIDOne,
		BusinessRefs: []sessionvo.BusinessRef{{
			RefType: sessionvo.BusinessRefObjectType, RefID: "object:supplychain:forecast",
			Version: "1",
		}},
	}
	if _, _, err := service.CompleteOperationAttempt(context.Background(), command); err != nil {
		t.Fatalf("complete canonical receipt: %v", err)
	}
	legacyRef := command.BusinessRefs[0]
	legacyRef.RefID = "object:forecast"
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		stored, found := tx.FindReceipt(receipt.ID)
		if !found {
			t.Fatal("completed receipt missing")
		}
		stored.BusinessRefs = []sessionvo.BusinessRef{legacyRef}
		tx.SaveReceipt(stored)
		return nil
	}); err != nil {
		t.Fatalf("seed legacy receipt: %v", err)
	}
	command.BusinessRefs = []sessionvo.BusinessRef{legacyRef}

	_, replayed, err := service.CompleteOperationAttempt(context.Background(), command)
	if err != nil || len(replayed.BusinessRefs) != 1 || replayed.BusinessRefs[0] != legacyRef {
		t.Fatalf("legacy terminal receipt replay must remain idempotent: receipt=%#v err=%v", replayed, err)
	}
}

func TestOperationReceiptReplaysAutoLinkedReferencedOutputIdempotently(t *testing.T) {
	t.Parallel()

	service, owner, _, _, operation, receipt := mustCreateOperation(t)
	command := sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: receipt.Attempt,
		ReceiptID: receipt.ID,
		Output: sessionvo.PayloadEnvelope{
			Mode: sessionvo.PayloadReferenced, MediaType: "application/json",
			ByteLength: sessionvo.MaxInlinePayloadBytes + 1, Ref: "artifact:operation-output-1",
		},
		EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID:          "req-referenced-output", TraceID: validTraceIDOne,
	}
	if _, completed, err := service.CompleteOperationAttempt(context.Background(), command); err != nil {
		t.Fatalf("complete referenced output: %v", err)
	} else if !slices.Contains(completed.ArtifactRefs, command.Output.Ref) {
		t.Fatalf("referenced output was not auto-linked: %#v", completed.ArtifactRefs)
	}

	if _, _, err := service.CompleteOperationAttempt(context.Background(), command); err != nil {
		t.Fatalf("same referenced terminal output must replay idempotently: %v", err)
	}
}

func TestOperationReceiptReplaysAutoDerivedDurabilityReasonIdempotently(t *testing.T) {
	t.Parallel()

	service, owner, _, _, operation, receipt := mustCreateOperation(t)
	command := sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: receipt.Attempt,
		ReceiptID: receipt.ID, Error: operationError("retryable failure"),
		EvidenceDurability: sessionvo.DurabilityFailed, Retryable: true,
		RequestID: "req-durability-failed", TraceID: validTraceIDOne,
	}
	if _, failed, err := service.FailOperationAttempt(context.Background(), command); err != nil {
		t.Fatalf("fail operation: %v", err)
	} else if !slices.Contains(failed.PartialReasons, "evidence_durability_failed") {
		t.Fatalf("durability reason was not derived: %#v", failed.PartialReasons)
	}

	if _, _, err := service.FailOperationAttempt(context.Background(), command); err != nil {
		t.Fatalf("same derived durability reason must replay idempotently: %v", err)
	}
}

func TestOperationReceiptComparesBusinessReferenceAsOfByValue(t *testing.T) {
	t.Parallel()

	service, owner, _, _, operation, receipt := mustCreateOperation(t)
	firstAsOf := time.Date(2026, 7, 1, 0, 0, 0, 123, time.UTC)
	command := sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: receipt.Attempt,
		ReceiptID: receipt.ID, Output: operationOutput("business-ref-as-of"),
		EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID:          "req-business-ref-as-of", TraceID: validTraceIDOne,
		BusinessRefs: []sessionvo.BusinessRef{{
			RefType: sessionvo.BusinessRefObjectType, RefID: "object:supplychain:forecast",
			Version: "1", AsOf: &firstAsOf,
		}},
	}
	if _, _, err := service.CompleteOperationAttempt(context.Background(), command); err != nil {
		t.Fatalf("complete receipt with as_of: %v", err)
	}
	replayedAsOf := firstAsOf.In(time.FixedZone("UTC+8", 8*60*60))
	command.BusinessRefs[0].AsOf = &replayedAsOf

	_, _, err := service.CompleteOperationAttempt(context.Background(), command)
	if err != nil {
		t.Fatalf("equal as_of instants must replay idempotently: %v", err)
	}
}

func TestOptionalPendingReceiptDoesNotBlockEvidenceCompletion(t *testing.T) {
	t.Parallel()

	service, owner, _, interaction, operation, receipt := mustCreateOptionalOperation(t)
	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "terminal-optional", LeaseToken: interaction.LeaseToken,
		LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "answer_returned",
			ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: false}},
			ExpectedReceipts:   []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: false}},
		},
	})
	if err != nil {
		t.Fatalf("complete interaction: %v", err)
	}
	if completed.EvidenceStatus != sessionvo.EvidenceComplete {
		t.Fatalf("optional pending receipt blocked completion: %s", completed.EvidenceStatus)
	}
}

func TestManagedTerminationAssignsDeadlineOnlyForRequiredPendingReceipt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	store := sessionstore.NewWithClock(func() time.Time { return now })
	service := sessionsvc.New(store, sessionsvc.Options{AssemblyTimeout: 90 * time.Second})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "managed-assembly-timeout")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "managed-timeout-start",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	_, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "schema", ToolName: "get_object_types", Input: operationInput("schema"),
		Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}

	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "managed-timeout-finish", DeriveManifest: true,
		Manifest: sessionvo.ClosureManifest{Version: "3.0.0", CompletionReason: "answer_returned"},
	})
	if err != nil {
		t.Fatalf("terminate interaction: %v", err)
	}
	if completed.EvidenceStatus != sessionvo.EvidenceAssembling || completed.ClosureManifest == nil {
		t.Fatalf("expected assembling interaction with closure manifest, got %#v", completed)
	}
	deadline := completed.ClosureManifest.AssemblerDeadline
	if deadline == nil || !deadline.Equal(now.Add(90*time.Second)) {
		t.Fatalf("assembler deadline = %v, want %s", deadline, now.Add(90*time.Second))
	}
	if len(completed.ClosureManifest.ExpectedReceipts) != 1 || completed.ClosureManifest.ExpectedReceipts[0].ReceiptID != receipt.ID {
		t.Fatalf("expected authoritative pending receipt in manifest: %#v", completed.ClosureManifest)
	}
}

func TestExplicitTerminationAssignsDeadlineForRequiredPendingReceipt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	store := sessionstore.NewWithClock(func() time.Time { return now })
	service := sessionsvc.New(store, sessionsvc.Options{AssemblyTimeout: 90 * time.Second})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "explicit-assembly-timeout")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "explicit-timeout-start",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	operation, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "schema", ToolName: "get_object_types", Input: operationInput("schema"),
		Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}

	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "explicit-timeout-finish",
		LeaseToken:             interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "3.0.0", CompletionReason: "answer_returned",
			ExpectedOperations: []sessionvo.ExpectedOperation{{OperationID: operation.ID, Required: true}},
			ExpectedReceipts:   []sessionvo.ExpectedReceipt{{ReceiptID: receipt.ID, Required: true}},
		},
	})
	if err != nil || completed.EvidenceStatus != sessionvo.EvidenceAssembling || completed.ClosureManifest == nil {
		t.Fatalf("expected assembling interaction with closure manifest, got %#v, %v", completed, err)
	}
	deadline := completed.ClosureManifest.AssemblerDeadline
	if deadline == nil || !deadline.Equal(now.Add(90*time.Second)) {
		t.Fatalf("assembler deadline = %v, want %s", deadline, now.Add(90*time.Second))
	}
}

func TestLicenseLossStillTerminatesAndRecordsEvidenceOmission(t *testing.T) {
	t.Parallel()

	store := sessionstore.New()
	service := sessionsvc.New(store, sessionsvc.Options{
		EvidenceCollectionState: func() string { return "not_collected_due_to_license" },
	})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "license-loss")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "license-start",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	completed, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "license-terminal", LeaseToken: interaction.LeaseToken,
		LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "answer_returned",
		},
	})
	if err != nil || completed.ExecutionStatus != sessionvo.InteractionCompleted ||
		completed.EvidenceStatus != sessionvo.EvidencePartial {
		t.Fatalf("license loss blocked lifecycle completion: %#v, %v", completed, err)
	}
	revisions, err := service.ListAssemblyRevisions(context.Background(), owner, interaction.ID)
	if err != nil || len(revisions) != 1 ||
		len(revisions[0].PartialReasons) != 1 ||
		revisions[0].PartialReasons[0] != "not_collected_due_to_license" {
		t.Fatalf("license omission was not preserved: %#v, %v", revisions, err)
	}
}

func TestAbandonExpiredOnlyTransitionsMatchingActiveLease(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := sessionstore.NewWithClock(func() time.Time { return now })
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "thread-expired")
	expired, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "expired",
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("start expired interaction: %v", err)
	}
	now = now.Add(2 * time.Minute)
	abandoned, err := service.AbandonExpiredInteractions(context.Background(), 10)
	if err != nil {
		t.Fatalf("abandon expired interactions: %v", err)
	}
	if len(abandoned) != 1 || abandoned[0].ID != expired.ID ||
		abandoned[0].ExecutionStatus != sessionvo.InteractionAbandoned {
		t.Fatalf("unexpected abandoned interactions: %#v", abandoned)
	}
}

func TestAbandonExpiredInteractionPreservesDurableOperationEvidence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	store := sessionstore.NewWithClock(func() time.Time { return now })
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "expired-with-evidence")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "expired-with-evidence",
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	operation, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "inventory", ToolName: "query_object_instance", Input: operationInput("inventory"),
		Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	if _, _, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: receipt.Attempt, ReceiptID: receipt.ID,
		Output: operationOutput("inventory-result"), EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID: "req-inventory", TraceID: validTraceIDOne,
		ObservedEvidenceRefs: []string{"evt-inventory"},
	}); err != nil {
		t.Fatalf("complete operation: %v", err)
	}
	active, err := service.GetInteraction(context.Background(), owner, interaction.ID)
	if err != nil {
		t.Fatalf("get active interaction: %v", err)
	}
	now = active.LeaseExpiresAt.Add(time.Second)

	abandoned, err := service.AbandonExpiredInteractions(context.Background(), 10)
	if err != nil {
		t.Fatalf("abandon expired interaction: %v", err)
	}
	if len(abandoned) != 1 || abandoned[0].ExecutionStatus != sessionvo.InteractionAbandoned ||
		abandoned[0].EvidenceStatus != sessionvo.EvidenceComplete || abandoned[0].ClosureManifest == nil {
		t.Fatalf("abandoned interaction lost durable evidence: %#v", abandoned)
	}
	revisions, err := service.ListAssemblyRevisions(context.Background(), owner, interaction.ID)
	if err != nil || len(revisions) != 1 || revisions[0].Trigger != "lease_expired" ||
		revisions[0].Completeness != sessionvo.EvidenceComplete ||
		len(revisions[0].IncludedEventIDs) != 1 || revisions[0].IncludedEventIDs[0] != "evt-inventory" {
		t.Fatalf("abandoned interaction did not freeze durable evidence: %#v, %v", revisions, err)
	}
}

func TestIdleOneShotConversationExpiresAndEnsureCreatesNextGeneration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := sessionstore.NewWithClock(func() time.Time { return now })
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	oneShot, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "idle-one-shot", OneShot: true,
	})
	if err != nil {
		t.Fatalf("ensure one-shot conversation: %v", err)
	}
	regular := mustEnsureConversation(t, service, owner, "idle-regular")

	now = now.Add(16 * time.Minute)
	expired, err := service.ExpireIdleOneShotConversations(context.Background(), 15*time.Minute, 10)
	if err != nil {
		t.Fatalf("expire idle one-shot conversations: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != oneShot.ID ||
		expired[0].Status != sessionvo.ConversationExpired {
		t.Fatalf("unexpected expired conversations: %#v", expired)
	}
	if _, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: oneShot.ID, IdempotencyKey: "expired-start",
	}); !sessionsvc.IsCode(err, sessionsvc.CodeConversationExpired) {
		t.Fatalf("expected conversation_expired when starting an expired one-shot, got %v", err)
	}
	if _, err := service.CloseConversation(context.Background(), sessionsvc.CloseConversationCommand{
		Owner: owner, ConversationID: oneShot.ID, IdempotencyKey: "expired-close",
	}); !sessionsvc.IsCode(err, sessionsvc.CodeConversationExpired) {
		t.Fatalf("expected closing an expired conversation to preserve expiry, got %v", err)
	}
	preserved, err := service.GetConversation(context.Background(), owner, oneShot.ID)
	if err != nil || preserved.Status != sessionvo.ConversationExpired {
		t.Fatalf("expired status was rewritten: %#v, %v", preserved, err)
	}
	stillActive, err := service.GetConversation(context.Background(), owner, regular.ID)
	if err != nil || stillActive.Status != sessionvo.ConversationActive {
		t.Fatalf("regular conversation was expired: %#v, %v", stillActive, err)
	}
	next, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "idle-one-shot", OneShot: true,
	})
	if err != nil {
		t.Fatalf("ensure next one-shot generation: %v", err)
	}
	if next.Generation != 2 || next.ID == oneShot.ID {
		t.Fatalf("expected generation 2 after expiry: %#v", next)
	}
}

func TestAbandonExpiredOneShotInteractionClosesConversation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := sessionstore.NewWithClock(func() time.Time { return now })
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "abandoned-one-shot", OneShot: true,
	})
	if err != nil {
		t.Fatalf("ensure one-shot conversation: %v", err)
	}
	if _, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "abandoned-one-shot",
		LeaseDuration: time.Minute,
	}); err != nil {
		t.Fatalf("start one-shot interaction: %v", err)
	}

	now = now.Add(2 * time.Minute)
	if _, err := service.AbandonExpiredInteractions(context.Background(), 10); err != nil {
		t.Fatalf("abandon expired interaction: %v", err)
	}
	closed, err := service.GetConversation(context.Background(), owner, conversation.ID)
	if err != nil {
		t.Fatalf("get one-shot conversation: %v", err)
	}
	if closed.Status != sessionvo.ConversationClosed || closed.ClosedAt == nil {
		t.Fatalf("abandoned one-shot conversation was not closed: %#v", closed)
	}
}

func TestExpiredLeaseCannotTerminateOrCreateOperationBeforeReaper(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := sessionstore.NewWithClock(func() time.Time { return now })
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "thread-expired-write")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "expired-write",
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "expired-op", ToolName: "query",
		Input: operationInput("expired"), Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeInteractionTerminal) {
		t.Fatalf("expected expired lease to reject operation, got %v", err)
	}
	if _, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCanceled,
		TerminalIdempotencyKey: "expired-terminal", LeaseToken: interaction.LeaseToken,
		LeaseEpoch: interaction.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeTerminalConflict) {
		t.Fatalf("expected expired lease to reject terminal write, got %v", err)
	}
}

func TestRequestProjectionHasRequestRatherThanReceiptCardinality(t *testing.T) {
	t.Parallel()

	service, owner, conversation, interaction, operation, receipt := mustCreateOperation(t)
	if _, _, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: 1, ReceiptID: receipt.ID,
		Output: operationOutput("request-result"), EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID: "req-shared", TraceID: validTraceIDOne,
	}); err != nil {
		t.Fatalf("complete first operation: %v", err)
	}
	secondOperation, secondReceipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "logical-call-2", ToolName: "metric-query",
		Input: operationInput("input-2"), Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure second operation: %v", err)
	}
	if _, _, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: secondOperation.ID, Attempt: 1, ReceiptID: secondReceipt.ID,
		Output: operationOutput("request-result-2"), EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID: "req-shared", TraceID: validTraceIDTwo,
	}); err != nil {
		t.Fatalf("complete second operation: %v", err)
	}

	requests, err := service.ListRequests(context.Background(), owner, 20)
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	if len(requests) != 1 || requests[0].RequestID != "req-shared" ||
		requests[0].ReceiptCount != 2 || requests[0].OperationCount != 2 {
		t.Fatalf("unexpected request projection: %#v", requests)
	}
}

func TestInteractionRejectsOperationBeyondCapacityLimit(t *testing.T) {
	t.Parallel()

	service := newTestServiceWithCapacity(sessionsvc.CapacityLimits{
		MaxOperationsPerInteraction:   2,
		MaxClaimsPerInteraction:       2,
		MaxEvidenceRefsPerInteraction: 4,
	})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "operation-capacity")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "operation-capacity",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	for index := 0; index < 2; index++ {
		if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
			OperationKey: fmt.Sprintf("operation-%03d", index), ToolName: "query",
			Input:      operationInput(fmt.Sprintf("%03d", index)),
			LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		}); err != nil {
			t.Fatalf("ensure operation %d: %v", index, err)
		}
	}
	if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "operation-over-limit", ToolName: "query",
		Input:      operationInput("over-limit"),
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeOperationRequired) {
		t.Fatalf("expected operation capacity rejection, got %v", err)
	}
}

func TestClosureManifestRejectsClaimsBeyondCapacityLimit(t *testing.T) {
	t.Parallel()

	service := newTestServiceWithCapacity(sessionsvc.CapacityLimits{
		MaxOperationsPerInteraction:   2,
		MaxClaimsPerInteraction:       2,
		MaxEvidenceRefsPerInteraction: 4,
	})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "claim-capacity")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "claim-capacity",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	claims := make([]string, 3)
	for index := range claims {
		claims[index] = fmt.Sprintf("claim-%02d", index)
	}
	if _, err := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
		Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted,
		TerminalIdempotencyKey: "claim-capacity", LeaseToken: interaction.LeaseToken,
		LeaseEpoch: interaction.LeaseEpoch,
		Manifest: sessionvo.ClosureManifest{
			Version: "1", CompletionReason: "answer_returned", Claims: claims,
		},
	}); !sessionsvc.IsCode(err, sessionsvc.CodeClosureManifestInvalid) {
		t.Fatalf("expected claim capacity rejection, got %v", err)
	}
}

func TestReceiptRejectsEvidenceReferencesBeyondCapacityLimit(t *testing.T) {
	t.Parallel()

	service := newTestServiceWithCapacity(sessionsvc.CapacityLimits{
		MaxOperationsPerInteraction:   2,
		MaxClaimsPerInteraction:       2,
		MaxEvidenceRefsPerInteraction: 4,
	})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "evidence-capacity")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "evidence-capacity",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	operation, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "evidence-capacity", ToolName: "query", Input: operationInput("evidence-capacity"),
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	references := make([]string, 5)
	for index := range references {
		references[index] = fmt.Sprintf("event-%04d", index)
	}
	if _, _, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: 1, ReceiptID: receipt.ID,
		Output: operationOutput("too-many-evidence-refs"), EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID: "req-capacity", TraceID: validTraceIDOne, ObservedEvidenceRefs: references,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeOperationRequired) {
		t.Fatalf("expected evidence reference capacity rejection, got %v", err)
	}
}

func operationInput(value string) sessionvo.PayloadEnvelope {
	payload, _ := json.Marshal(map[string]string{"test_input": value})
	result, _ := sessionvo.InlineJSONPayload(payload)
	return result
}

func mustPayloadEnvelope(t *testing.T, payload json.RawMessage) sessionvo.PayloadEnvelope {
	t.Helper()
	result, err := sessionvo.InlineJSONPayload(payload)
	if err != nil {
		t.Fatalf("build payload envelope: %v", err)
	}
	return result
}

func operationOutput(value string) sessionvo.PayloadEnvelope {
	payload, _ := json.Marshal(map[string]string{"result": value})
	result, _ := sessionvo.InlineJSONPayload(payload)
	return result
}

func operationError(value string) sessionvo.PayloadEnvelope {
	payload, _ := json.Marshal(map[string]any{
		"code": "TEST_FAILURE", "message": value, "stage": "backend", "retryable": true,
	})
	result, _ := sessionvo.InlineJSONPayload(payload)
	return result
}

func newTestService() *sessionsvc.Service {
	return sessionsvc.New(sessionstore.New(), sessionsvc.Options{})
}

func newTestServiceWithCapacity(capacity sessionsvc.CapacityLimits) *sessionsvc.Service {
	return sessionsvc.New(sessionstore.New(), sessionsvc.Options{Capacity: capacity})
}

func testOwner() sessionvo.Owner {
	return sessionvo.Owner{
		ApplicationPrincipalID: "app-1",
		EffectiveSubjectType:   sessionvo.SubjectUser,
		EffectiveSubjectID:     "user-1",
	}
}

type testMetrics struct {
	counters map[string]uint64
	gauges   map[string]float64
}

func newTestMetrics() *testMetrics {
	return &testMetrics{counters: make(map[string]uint64), gauges: make(map[string]float64)}
}

func (m *testMetrics) Increment(name string) {
	m.Add(name, 1)
}

func (m *testMetrics) Add(name string, delta uint64) {
	m.counters[name] += delta
}

func (m *testMetrics) Set(name string, value float64) {
	m.gauges[name] = value
}

func (m *testMetrics) count(name string) uint64 {
	return m.counters[name]
}

func mustEnsureConversation(t *testing.T, service *sessionsvc.Service, owner sessionvo.Owner, externalKey string) sessionvo.Conversation {
	t.Helper()
	conversation, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: externalKey, IdempotencyKey: "ensure-" + externalKey,
	})
	if err != nil {
		t.Fatalf("ensure conversation: %v", err)
	}
	return conversation
}

func mustCreateOperation(t *testing.T) (*sessionsvc.Service, sessionvo.Owner, sessionvo.Conversation, sessionvo.Interaction, sessionvo.Operation, sessionvo.Receipt) {
	t.Helper()
	_, service, owner, conversation, interaction, operation, receipt := mustCreateOperationWithStore(t)
	return service, owner, conversation, interaction, operation, receipt
}

func mustCreateOperationWithStore(t *testing.T) (*sessionstore.Store, *sessionsvc.Service, sessionvo.Owner, sessionvo.Conversation, sessionvo.Interaction, sessionvo.Operation, sessionvo.Receipt) {
	t.Helper()
	store := sessionstore.New()
	service := sessionsvc.New(store, sessionsvc.Options{})
	return mustCreateOperationForService(t, store, service)
}

func mustCreateOperationWithHistoricalProvenance(t *testing.T) (*sessionstore.Store, *sessionsvc.Service, sessionvo.Owner, sessionvo.Conversation, sessionvo.Interaction, sessionvo.Operation, sessionvo.Receipt) {
	t.Helper()
	store := sessionstore.New()
	service := sessionsvc.New(store, sessionsvc.Options{
		EnableHistoricalProvenance: true,
		ProjectionGrantIssuer:      "trace-core",
		ProjectionGrantKeyID:       "test-key",
		ProjectionGrantAudience:    "bkn-projection-read",
		ProjectionGrantSigner: func(claims projectiongrant.Claims) (string, error) {
			return projectiongrant.Sign(claims, testHistoricalProvenanceGrantPrivateKey())
		},
	})
	return mustCreateOperationForService(t, store, service)
}

func testHistoricalProvenanceGrantPrivateKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))
}

func mustCreateOperationForService(t *testing.T, store *sessionstore.Store, service *sessionsvc.Service) (*sessionstore.Store, *sessionsvc.Service, sessionvo.Owner, sessionvo.Conversation, sessionvo.Interaction, sessionvo.Operation, sessionvo.Receipt) {
	t.Helper()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "thread-operation")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start-operation",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	operation, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "logical-call", ToolName: "ontology-query",
		Input: operationInput("input"), Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	return store, service, owner, conversation, interaction, operation, receipt
}

func mustCreateOptionalOperation(t *testing.T) (*sessionsvc.Service, sessionvo.Owner, sessionvo.Conversation, sessionvo.Interaction, sessionvo.Operation, sessionvo.Receipt) {
	t.Helper()
	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "thread-optional-operation")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start-optional-operation",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	operation, receipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "optional-call", ToolName: "optional-query",
		Input: operationInput("optional-input"), Required: false,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure optional operation: %v", err)
	}
	return service, owner, conversation, interaction, operation, receipt
}

func TestOperationLimitDoesNotKeepAFullInteractionAlive(t *testing.T) {
	t.Parallel()

	// Renewing the lease before the capacity check leaves a full interaction
	// holding a fresh lease on every rejected call, so the reaper never sees it
	// expire and the caller's retries keep it alive indefinitely. The in-memory
	// store makes that permanent — it has no rollback, so the renewal written by
	// the failed call survives.
	service := newTestServiceWithCapacity(sessionsvc.CapacityLimits{
		MaxOperationsPerInteraction:   128,
		MaxClaimsPerInteraction:       32,
		MaxEvidenceRefsPerInteraction: 2048,
	})
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "operation-limit")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start-limit",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	for index := range 128 {
		if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
			OperationKey: fmt.Sprintf("op-%d", index),
			ToolName:     "ontology-query",
			Input:        operationInput(fmt.Sprintf("input-%d", index)),
			Required:     true,
			LeaseToken:   interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		}); err != nil {
			t.Fatalf("ensure operation %d: %v", index, err)
		}
	}

	before, err := service.GetInteraction(context.Background(), owner, interaction.ID)
	if err != nil {
		t.Fatalf("read interaction before the rejected call: %v", err)
	}
	if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "op-over-limit", ToolName: "ontology-query",
		Input: operationInput("over-limit"), Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeOperationRequired) {
		t.Fatalf("expected the 128 operation limit to reject, got %v", err)
	}
	after, err := service.GetInteraction(context.Background(), owner, interaction.ID)
	if err != nil {
		t.Fatalf("read interaction after the rejected call: %v", err)
	}
	// Assert on LeaseVersion, not LeaseExpiresAt: the in-memory store stamps real
	// wall-clock time, so two transactions landing in the same tick would make an
	// expiry comparison pass even if the renewal came back. renewInteractionLease
	// bumps the version unconditionally, so it registers regardless of clock
	// resolution.
	if after.LeaseVersion != before.LeaseVersion {
		t.Fatalf("a rejected call renewed the lease: version %d → %d",
			before.LeaseVersion, after.LeaseVersion)
	}
	if after.LeaseExpiresAt.After(before.LeaseExpiresAt) {
		t.Fatalf("a rejected call extended the lease: before=%s after=%s",
			before.LeaseExpiresAt, after.LeaseExpiresAt)
	}

	// A replayed key still resolves against a full interaction, and that path
	// legitimately renews — the caller is finishing work already accounted for.
	if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "op-0", ToolName: "ontology-query",
		Input: operationInput("input-0"), Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); err != nil {
		t.Fatalf("replaying an existing operation on a full interaction must still work: %v", err)
	}
}

func TestListOperationExecutionsByTraceIDReturnsAuthorizedAttemptsInTimeOrder(t *testing.T) {
	t.Parallel()

	store := sessionstore.New()
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := testOwner()
	otherOwner := owner
	otherOwner.EffectiveSubjectID = "user-2"
	otherOwner.ApplicationPrincipalID = "app-2"
	started := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)

	seedTraceExecution := func(
		conversationID, interactionID, operationID, receiptID string,
		traceID string, factStarted time.Time, recordOwner sessionvo.Owner,
	) {
		t.Helper()
		if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
			tx.SaveConversation(sessionvo.Conversation{
				ID: conversationID, Owner: recordOwner, ExternalConversationKey: conversationID,
				Generation: 1, Status: sessionvo.ConversationActive, RowVersion: 1,
				CreatedAt: factStarted, UpdatedAt: factStarted,
			})
			tx.SaveInteraction(sessionvo.Interaction{
				ID: interactionID, ConversationID: conversationID, Ordinal: 1,
				ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete,
				RowVersion: 1, CreatedAt: factStarted, UpdatedAt: factStarted,
			})
			tx.SaveOperation(sessionvo.Operation{
				ID: operationID, ConversationID: conversationID, InteractionID: interactionID,
				OperationKey: operationID, ToolName: "run_sql", Attempt: 1,
				AttemptStatus: sessionvo.AttemptCompleted, RowVersion: 1,
				CreatedAt: factStarted, UpdatedAt: factStarted,
			})
			tx.SaveReceipt(sessionvo.Receipt{
				ID: receiptID, SchemaVersion: "3.0.0", Owner: recordOwner,
				ConversationID: conversationID, InteractionID: interactionID,
				OperationID: operationID, Attempt: 1, OperationKey: operationID,
				ToolName: "run_sql", Status: sessionvo.ReceiptCompleted,
				EvidenceDurability: sessionvo.DurabilityDurable, Required: true,
				TraceID: traceID, RowVersion: 1, IssuedAt: factStarted,
			})
			tx.SaveOperationCallFact(sessionvo.OperationCallFact{
				OperationID: operationID, Attempt: 1, ConversationID: conversationID,
				InteractionID: interactionID, ReceiptID: receiptID, ToolName: "run_sql",
				Protocol: sessionvo.ProtocolMCP, SourceModule: "context-loader",
				Input: sessionvo.PayloadEnvelope{
					Mode: sessionvo.PayloadInline, MediaType: "application/json",
					ByteLength: 2, Inline: json.RawMessage(`{}`),
				},
				TraceID: traceID, StartedAt: factStarted, Status: sessionvo.AttemptCompleted,
			})
			return nil
		}); err != nil {
			t.Fatalf("seed trace execution: %v", err)
		}
	}

	seedTraceExecution("conv-late", "int-late", "op-late", "rcpt-late", validTraceIDOne, started.Add(time.Second), owner)
	seedTraceExecution("conv-early", "int-early", "op-early", "rcpt-early", validTraceIDOne, started, owner)
	seedTraceExecution("conv-other", "int-other", "op-other", "rcpt-other", validTraceIDOne, started.Add(-time.Second), otherOwner)
	seedTraceExecution("conv-trace-2", "int-trace-2", "op-trace-2", "rcpt-trace-2", validTraceIDTwo, started, owner)

	executions, err := service.ListOperationExecutionsByTraceID(context.Background(), owner, validTraceIDOne)
	if err != nil {
		t.Fatalf("list operation executions: %v", err)
	}
	if len(executions) != 2 || executions[0].Fact.OperationID != "op-early" || executions[1].Fact.OperationID != "op-late" {
		t.Fatalf("expected two authorized attempts in time order, got %+v", executions)
	}
	if executions[0].InteractionStatus != sessionvo.InteractionCompleted || executions[0].Receipt.ID != "rcpt-early" {
		t.Fatalf("expected joined interaction and receipt state, got %+v", executions[0])
	}

	profile := evidencevo.AccessProfile{
		EffectiveSubjectID:     owner.EffectiveSubjectID,
		ApplicationPrincipalID: owner.ApplicationPrincipalID,
		AccountActive:          true,
	}
	scoped, err := service.ListOperationExecutionsByTraceIDScoped(context.Background(), evidencevo.QueryScope{
		AccountID:     owner.EffectiveSubjectID,
		AccountType:   "user",
		AccessProfile: &profile,
		View:          evidencevo.AccessViewTechnical,
	}, validTraceIDOne)
	if err != nil || len(scoped) != 2 {
		t.Fatalf("owner technical scope must return only owned attempts: executions=%+v err=%v", scoped, err)
	}
	profile.Roles = []string{"super_admin"}
	scoped, err = service.ListOperationExecutionsByTraceIDScoped(context.Background(), evidencevo.QueryScope{
		AccountID: owner.EffectiveSubjectID, AccountType: "user",
		AccessProfile: &profile, View: evidencevo.AccessViewTechnical,
	}, validTraceIDOne)
	if err != nil || len(scoped) != 3 {
		t.Fatalf("super_admin technical scope must include the other owner: executions=%+v err=%v", scoped, err)
	}

	interactionScoped, err := service.ListOperationExecutionsByInteractionIDScoped(context.Background(), evidencevo.QueryScope{
		AccountID:     owner.EffectiveSubjectID,
		AccountType:   "user",
		AccessProfile: &profile,
		View:          evidencevo.AccessViewTechnical,
	}, "int-early")
	if err != nil || len(interactionScoped) != 1 || interactionScoped[0].Fact.OperationID != "op-early" {
		t.Fatalf("scoped interaction must return its one authorized attempt: executions=%+v err=%v", interactionScoped, err)
	}
}
