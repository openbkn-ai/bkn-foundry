package sessionsvc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
)

func TestEnsureCurrentConversationIsIdempotent(t *testing.T) {
	t.Parallel()

	store := sessionstore.New()
	service := sessionsvc.New(store, sessionsvc.Options{
		Now: func() time.Time { return time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC) },
	})
	owner := sessionvo.Owner{
		TenantID:               "tenant-1",
		BusinessDomainID:       "domain-1",
		ApplicationPrincipalID: "app-1",
		EffectiveSubjectType:   sessionvo.SubjectUser,
		EffectiveSubjectID:     "user-1",
	}

	first, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner:                   owner,
		ExternalConversationKey: "cursor-thread-1",
		IdempotencyKey:          "idem-1",
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
			NormalizedInputHash: "sha256:input", Required: true,
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
	if _, err := service.GetConversation(context.Background(), otherOwner, second.ID); !sessionsvc.IsCode(err, sessionsvc.CodeConversationOwnerMismatch) {
		t.Fatalf("expected owner mismatch, got %v", err)
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
	}); !sessionsvc.IsCode(err, sessionsvc.CodeConversationOwnerMismatch) {
		t.Fatalf("expected owner mismatch, got %v", err)
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

func TestEnsureOperationUsesStableLogicalKeyAndInputHash(t *testing.T) {
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
		NormalizedInputHash: "sha256:input-a", Required: true,
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
		NormalizedInputHash: "sha256:input-a", Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("replay operation: %v", err)
	}
	if first.ID != replayed.ID || receipt.ID != replayedReceipt.ID || first.Attempt != 1 {
		t.Fatalf("expected the same operation and receipt, got %#v / %#v", replayed, replayedReceipt)
	}
	if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "query-sales-orders", ToolName: "ontology-query",
		NormalizedInputHash: "sha256:input-b", Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
		t.Fatalf("expected idempotency_conflict, got %v", err)
	}
	if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "child-with-unknown-parent", ToolName: "ontology-query",
		NormalizedInputHash: "sha256:child", Required: true,
		ParentOperationID: "op-from-another-interaction",
		LeaseToken:        interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeOperationRequired) {
		t.Fatalf("unknown parent operation was accepted: %v", err)
	}
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
			ToolName: "ontology-query", NormalizedInputHash: "sha256:query",
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
			ToolName: "ontology-query", NormalizedInputHash: "sha256:query",
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

	service, owner, _, interaction, operation, receipt := mustCreateOperation(t)
	if _, _, err := service.StartOperationAttempt(context.Background(), sessionsvc.StartAttemptCommand{
		Owner: owner, OperationID: operation.ID,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeReceiptPending) {
		t.Fatalf("expected receipt_pending before a retryable failure, got %v", err)
	}
	if _, _, err := service.FailOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: 1,
		ReceiptID: receipt.ID, PayloadHash: "sha256:failed", Retryable: true,
		RequestID: "req-retry", TraceID: "trace-retry",
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
		NormalizedInputHash: "sha256:pending", Required: true,
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
		OperationKey: "future", ToolName: "query", NormalizedInputHash: "sha256:future", Required: true,
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
		OperationKey: "late", ToolName: "query", NormalizedInputHash: "sha256:late", Required: true,
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
		ReceiptID: receipt.ID, PayloadHash: "sha256:late-result",
		EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID:          "req-late", TraceID: "trace-late",
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
		revisions[1].ParentRevisionID != revisions[0].ID || revisions[1].Trigger != "late_receipt" {
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
		ReceiptID: receipt.ID, PayloadHash: "sha256:before-deadline",
		EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID:          "req-before-deadline", TraceID: "trace-before-deadline",
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
		ReceiptID: receipt.ID, PayloadHash: "sha256:complete",
		EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID:          "req-complete", TraceID: "trace-complete",
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
		RefType: "object_type", RefID: "supplychain_hd0202_forecast",
		BusinessDomainID: owner.BusinessDomainID, Version: "v3",
		DisplayHint: "需求预测单",
	}
	_, durableReceipt, err := service.CompleteOperationAttempt(
		context.Background(),
		sessionsvc.FinishAttemptCommand{
			Owner: owner, OperationID: operation.ID, Attempt: 1,
			ReceiptID: receipt.ID, PayloadHash: "sha256:complete-with-evidence",
			EvidenceDurability:   sessionvo.DurabilityDurable,
			RequestID:            "req-complete-with-evidence",
			TraceID:              "trace-complete-with-evidence",
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
			ReceiptID: receipt.ID, PayloadHash: "sha256:complete-with-evidence",
			EvidenceDurability:   sessionvo.DurabilityDurable,
			RequestID:            "req-complete-with-evidence",
			TraceID:              "trace-complete-with-evidence",
			ObservedEvidenceRefs: []string{"evt-rewritten"},
			BusinessRefs:         []sessionvo.BusinessRef{businessRef},
			ArtifactRefs:         []string{"artifact://answer/fragment-1"},
		},
	)
	if !sessionsvc.IsCode(err, sessionsvc.CodeIdempotencyConflict) {
		t.Fatalf("receipt replay changed immutable evidence refs: %v", err)
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
		NormalizedInputHash: "sha256:expired", Required: true,
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
		PayloadHash: "sha256:request-result", EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID: "req-shared", TraceID: "trace-one",
	}); err != nil {
		t.Fatalf("complete first operation: %v", err)
	}
	secondOperation, secondReceipt, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "logical-call-2", ToolName: "metric-query",
		NormalizedInputHash: "sha256:input-2", Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure second operation: %v", err)
	}
	if _, _, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: secondOperation.ID, Attempt: 1, ReceiptID: secondReceipt.ID,
		PayloadHash: "sha256:request-result-2", EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID: "req-shared", TraceID: "trace-two",
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

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "operation-capacity")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "operation-capacity",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	for index := 0; index < 128; index++ {
		if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
			OperationKey: fmt.Sprintf("operation-%03d", index), ToolName: "query",
			NormalizedInputHash: fmt.Sprintf("sha256:%03d", index),
			LeaseToken:          interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		}); err != nil {
			t.Fatalf("ensure operation %d: %v", index, err)
		}
	}
	if _, _, err := service.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "operation-over-limit", ToolName: "query",
		NormalizedInputHash: "sha256:over-limit",
		LeaseToken:          interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeOperationRequired) {
		t.Fatalf("expected operation capacity rejection, got %v", err)
	}
}

func TestClosureManifestRejectsClaimsBeyondCapacityLimit(t *testing.T) {
	t.Parallel()

	service := newTestService()
	owner := testOwner()
	conversation := mustEnsureConversation(t, service, owner, "claim-capacity")
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "claim-capacity",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	claims := make([]string, 33)
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

	service, owner, _, _, operation, receipt := mustCreateOperation(t)
	references := make([]string, 2049)
	for index := range references {
		references[index] = fmt.Sprintf("event-%04d", index)
	}
	if _, _, err := service.CompleteOperationAttempt(context.Background(), sessionsvc.FinishAttemptCommand{
		Owner: owner, OperationID: operation.ID, Attempt: 1, ReceiptID: receipt.ID,
		PayloadHash: "sha256:too-many-evidence-refs", EvidenceDurability: sessionvo.DurabilityDurable,
		RequestID: "req-capacity", TraceID: "trace-capacity", ObservedEvidenceRefs: references,
	}); !sessionsvc.IsCode(err, sessionsvc.CodeOperationRequired) {
		t.Fatalf("expected evidence reference capacity rejection, got %v", err)
	}
}

func newTestService() *sessionsvc.Service {
	return sessionsvc.New(sessionstore.New(), sessionsvc.Options{})
}

func testOwner() sessionvo.Owner {
	return sessionvo.Owner{
		TenantID:               "tenant-1",
		BusinessDomainID:       "domain-1",
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
	service := newTestService()
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
		NormalizedInputHash: "sha256:input", Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	return service, owner, conversation, interaction, operation, receipt
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
		NormalizedInputHash: "sha256:optional-input", Required: false,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure optional operation: %v", err)
	}
	return service, owner, conversation, interaction, operation, receipt
}
