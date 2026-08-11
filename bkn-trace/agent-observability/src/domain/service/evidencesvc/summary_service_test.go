package evidencesvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ibusinessresolver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type capturingProjectionSource struct {
	result    iprojectionsource.Result
	resultFor func(iprojectionsource.Query) iprojectionsource.Result
	queries   []iprojectionsource.Query
}

type fixedTraceStatsSource map[string]int

func (source fixedTraceStatsSource) CountSpansByTraceIDs(_ context.Context, _ []string) (map[string]int, error) {
	return source, nil
}

func TestSummaryOffsetClampsOverflowingPage(t *testing.T) {
	options := evidencevo.SummaryQueryOptions{Page: int(^uint(0) >> 1), Limit: MaxSummaryQueryLimit}
	if offset := summaryOffset(options, 10); offset != 10 {
		t.Fatalf("overflowing page offset = %d, want end of result set", offset)
	}
}

func TestListConversationsUsesCanonicalSessionStatus(t *testing.T) {
	evidenceStore := evidencestore.New()
	seedBusinessProvenanceRequest(
		t, evidenceStore, "req_done", "trace_done", "conversation_active", "interaction_done",
		"2026-08-03T08:00:00Z", "查询库存", "库存 1756", "acct_demo",
	)
	sessions := sessionstore.New()
	terminalAt := time.Date(2026, 8, 3, 8, 0, 3, 0, time.UTC)
	err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{
			ID: "conversation_active", Status: sessionvo.ConversationActive,
			CreatedAt: time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 8, 3, 8, 2, 0, 0, time.UTC),
		})
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_done", ConversationID: "conversation_active",
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete,
			CreatedAt: time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC),
			UpdatedAt: terminalAt, TerminalAt: &terminalAt,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	service := New(
		evidenceStore,
		WithProjectionSource(evidenceStore),
		WithSessionStore(sessions),
	)

	page, err := service.ListConversations(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("list conversations: page=%+v err=%v", page, err)
	}
	if page.Entries[0].Status != "active" || page.Entries[0].CompletedAt != "" {
		t.Fatalf("child request completion must not close the conversation: %+v", page.Entries[0])
	}
	if page.Entries[0].DurationMS != 3000 {
		t.Fatalf("active conversation must retain completed interaction duration: %+v", page.Entries[0])
	}
	if page.Entries[0].StartedAt != "2026-08-03T08:00:00Z" {
		t.Fatalf("conversation start must come from the Core lifecycle: %+v", page.Entries[0])
	}
}

func TestListConversationsExcludesWholeConversationByStableAgentID(t *testing.T) {
	evidenceStore := evidencestore.New()
	seedBusinessProvenanceRequestWithAgent(
		t, evidenceStore, "req_internal", "trace_internal", "conversation_internal", "interaction_internal",
		"2026-08-10T08:00:00Z", "内部分析", "内部建议", "acct_demo", "business_provenance_optimizer", true,
	)
	seedBusinessProvenanceRequestWithAgent(
		t, evidenceStore, "req_business", "trace_business", "conversation_business", "interaction_business",
		"2026-08-10T08:01:00Z", "业务问题", "业务结果", "acct_demo", "business_agent", true,
	)

	page, err := New(evidenceStore, WithProjectionSource(evidenceStore)).ListConversations(
		context.Background(), evidencevo.SummaryQueryOptions{
			Scope: summaryScope("acct_demo"), Limit: 20,
			ExcludeAgentOrApp: "business_provenance_optimizer",
		},
	)
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].ConversationID != "conversation_business" {
		t.Fatalf("business conversation page = %+v, want only conversation_business", page)
	}
}

func TestListConversationsSumsCompletedInteractionDurations(t *testing.T) {
	evidenceStore := evidencestore.New()
	seedBusinessProvenanceRequest(
		t, evidenceStore, "req_first", "trace_first", "conversation_supply", "interaction_first",
		"2026-08-03T08:00:00Z", "查询库存", "库存 1756", "acct_demo",
	)
	seedBusinessProvenanceRequest(
		t, evidenceStore, "req_second", "trace_second", "conversation_supply", "interaction_second",
		"2026-08-03T08:10:00Z", "查询采购订单", "采购订单 23 张", "acct_demo",
	)
	sessions := sessionstore.New()
	firstTerminalAt := time.Date(2026, 8, 3, 8, 0, 2, 0, time.UTC)
	secondTerminalAt := time.Date(2026, 8, 3, 8, 10, 3, 0, time.UTC)
	err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{
			ID: "conversation_supply", Status: sessionvo.ConversationActive,
			CreatedAt: time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 8, 3, 8, 11, 0, 0, time.UTC),
		})
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_first", ConversationID: "conversation_supply",
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete,
			CreatedAt: time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC),
			UpdatedAt: firstTerminalAt, TerminalAt: &firstTerminalAt,
		})
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_second", ConversationID: "conversation_supply",
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete,
			CreatedAt: time.Date(2026, 8, 3, 8, 10, 0, 0, time.UTC),
			UpdatedAt: secondTerminalAt, TerminalAt: &secondTerminalAt,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed canonical session: %v", err)
	}
	service := New(evidenceStore, WithProjectionSource(evidenceStore), WithSessionStore(sessions))

	page, err := service.ListConversations(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("list conversations: page=%+v err=%v", page, err)
	}
	if page.Entries[0].DurationMS != 5000 {
		t.Fatalf("conversation duration must sum completed interactions, got: %+v", page.Entries[0])
	}
}

func TestBuildConversationSummaryUsesLatestCoherentInteractionAndHidesChildCallError(t *testing.T) {

	summary := buildConversationSummary("conversation_supply", []evidencevo.RequestSummary{
		{
			RequestID: "req_first", InteractionID: "interaction_first",
			StartedAt: "2026-08-07T08:00:00Z", CompletedAt: "2026-08-07T08:00:01Z",
			Status: "completed", EvidenceCompleteness: "complete",
			InteractionQuestion: "6月份有哪些需求预测单？", InteractionResult: "6月份需求总量为 11594。",
		},
		{
			RequestID: "req_rejected", InteractionID: "interaction_latest",
			StartedAt: "2026-08-07T08:02:00Z", CompletedAt: "2026-08-07T08:02:01Z",
			Status: "error", EvidenceCompleteness: "complete", ErrorSummary: "OpenBKN operation failed",
		},
		{
			RequestID: "req_latest", InteractionID: "interaction_latest",
			StartedAt: "2026-08-07T08:02:02Z", CompletedAt: "2026-08-07T08:02:03Z",
			Status: "completed", EvidenceCompleteness: "complete",
			InteractionQuestion: "迄今为止有多少销售订单？", InteractionResult: "共有 1441 张销售订单。",
		},
	})

	if summary.QuestionPreview != "迄今为止有多少销售订单？" ||
		summary.ResultPreview != "共有 1441 张销售订单。" {
		t.Fatalf("conversation must show one coherent latest interaction: %+v", summary)
	}
	if summary.ErrorSummary != "" {
		t.Fatalf("a recoverable child call error must remain at request level: %+v", summary)
	}
}

func TestListConversationsFiltersAfterApplyingCanonicalSessionStatus(t *testing.T) {
	evidenceStore := evidencestore.New()
	seedBusinessProvenanceRequest(
		t, evidenceStore, "req_done", "trace_done", "conversation_active", "interaction_done",
		"2026-08-03T08:00:00Z", "查询库存", "库存 1756", "acct_demo",
	)
	sessions := sessionstore.New()
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{
			ID: "conversation_active", Status: sessionvo.ConversationActive,
			CreatedAt: time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 8, 3, 8, 2, 0, 0, time.UTC),
		})
		return nil
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	service := New(evidenceStore, WithProjectionSource(evidenceStore), WithSessionStore(sessions))

	page, err := service.ListConversations(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20, Status: "active",
	})

	if err != nil || len(page.Entries) != 1 || page.Entries[0].Status != "active" {
		t.Fatalf("canonical conversation status must drive filtering: page=%+v err=%v", page, err)
	}
}

func TestListConversationsSeparatesAgentDisplayNameFromTrustedIdentity(t *testing.T) {
	evidenceStore := evidencestore.New()
	seedBusinessProvenanceRequest(
		t, evidenceStore, "req_identity", "trace_identity", "conversation_identity", "interaction_identity",
		"2026-08-04T08:00:00Z", "查询库存", "库存 1756", "acct_demo",
	)
	sessions := sessionstore.New()
	owner := sessionvo.Owner{
		TenantID: "tenant_demo", BusinessDomainID: "bd_demo",
		ApplicationPrincipalID: "266c6a42-6131-4d62-8f39-853e7093701c",
		EffectiveSubjectType:   sessionvo.SubjectUser, EffectiveSubjectID: "acct_demo",
	}
	err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{
			ID: "conversation_identity", AgentName: "供应链分析助手", Owner: owner,
			Status: sessionvo.ConversationActive, CreatedAt: time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 8, 4, 8, 2, 0, 0, time.UTC),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	service := New(evidenceStore, WithProjectionSource(evidenceStore), WithSessionStore(sessions))

	page, err := service.ListConversations(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})

	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("list conversations: page=%+v err=%v", page, err)
	}
	entry := page.Entries[0]
	if entry.AgentName != "供应链分析助手" ||
		entry.ApplicationPrincipalID != owner.ApplicationPrincipalID ||
		entry.EffectiveSubjectID != owner.EffectiveSubjectID {
		t.Fatalf("display and trusted identity must remain separate: %+v", entry)
	}
}

func TestListConversationsUsesWeakestCanonicalInteractionEvidence(t *testing.T) {
	evidenceStore := evidencestore.New()
	seedBusinessProvenanceRequest(
		t, evidenceStore, "req_complete", "trace_complete", "conversation_supply", "interaction_complete",
		"2026-08-04T08:00:00Z", "查询六月需求", "需求总量 11594", "acct_demo",
	)
	seedBusinessProvenanceRequest(
		t, evidenceStore, "req_partial", "trace_partial", "conversation_supply", "interaction_partial",
		"2026-08-04T08:01:00Z", "查询销售订单", "共有 1441 张", "acct_demo",
	)
	sessions := sessionstore.New()
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{
			ID: "conversation_supply", Status: sessionvo.ConversationActive,
			CreatedAt: time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 8, 4, 8, 2, 0, 0, time.UTC),
		})
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_complete", ConversationID: "conversation_supply",
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete,
		})
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_partial", ConversationID: "conversation_supply",
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidencePartial,
		})
		return nil
	}); err != nil {
		t.Fatalf("seed canonical session: %v", err)
	}
	service := New(evidenceStore, WithProjectionSource(evidenceStore), WithSessionStore(sessions))

	page, err := service.ListConversations(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})

	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("list conversations: page=%+v err=%v", page, err)
	}
	if page.Entries[0].EvidenceCompleteness != "partial" {
		t.Fatalf("conversation must expose its weakest canonical interaction evidence: %+v", page.Entries[0])
	}
}

func TestCanonicalInteractionStateKeepsFailedCallSeparateFromCompletedTurn(t *testing.T) {
	sessions := sessionstore.New()
	terminalAt := time.Date(2026, 8, 4, 8, 2, 0, 0, time.UTC)
	err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_inventory", ConversationID: "conversation_supply",
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete,
			CreatedAt: time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC),
			UpdatedAt: terminalAt, TerminalAt: &terminalAt,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed interaction: %v", err)
	}
	service := New(nil, WithSessionStore(sessions))
	entries := []evidencevo.InteractionListSummary{{
		InteractionID: "interaction_inventory", Status: "error",
		EvidenceCompleteness: "partial", ErrorSummary: "one OpenBKN call failed",
		PartialReasons: []string{"evidence_durability_failed"},
	}}

	if err := service.applyCanonicalInteractionState(context.Background(), entries); err != nil {
		t.Fatalf("apply canonical state: %v", err)
	}

	if entries[0].Status != "completed" || entries[0].EvidenceCompleteness != "complete" {
		t.Fatalf("turn state must come from the managed interaction: %+v", entries[0])
	}
	if entries[0].ErrorSummary != "one OpenBKN call failed" {
		t.Fatalf("failed child call must remain diagnosable: %+v", entries[0])
	}
	if len(entries[0].PartialReasons) != 0 {
		t.Fatalf("canonical complete evidence must clear request-local reasons: %+v", entries[0])
	}
}

func TestCanonicalRequestIdentityAddsSafeFallbackForFailedOperation(t *testing.T) {
	sessions := sessionstore.New()
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conversation_failed_call", AgentName: "Supply Agent"})
		tx.SaveOperation(sessionvo.Operation{
			ID: "operation_failed_call", ConversationID: "conversation_failed_call",
			InteractionID: "interaction_completed", ToolName: "run_sql",
			AttemptStatus: sessionvo.AttemptFailed,
		})
		return nil
	}); err != nil {
		t.Fatalf("seed failed operation: %v", err)
	}
	service := &Service{sessionStore: sessions}
	requests := []evidencevo.RequestSummary{{
		ConversationID: "conversation_failed_call", OperationID: "operation_failed_call",
		Status: "error",
	}}
	if err := service.applyCanonicalRequestIdentity(context.Background(), requests); err != nil {
		t.Fatalf("apply canonical request identity: %v", err)
	}
	if requests[0].ErrorSummary != "OpenBKN operation failed" {
		t.Fatalf("failed operation must remain diagnosable: %+v", requests[0])
	}
}

func TestCanonicalConversationEvidenceReplacesRequestDerivedPartial(t *testing.T) {
	sessions := sessionstore.New()
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_complete", ConversationID: "conversation_supply",
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete,
		})
		completeness := "partial"
		duration := int64(0)
		applyCanonicalConversationEvidenceAndDuration(
			&completeness, new([]string), &duration, tx,
			[]evidencevo.RequestSummary{{InteractionID: "interaction_complete"}},
		)
		if completeness != "complete" {
			t.Fatalf("conversation evidence must come from canonical interactions, got %q", completeness)
		}
		return nil
	}); err != nil {
		t.Fatalf("apply canonical conversation evidence: %v", err)
	}
}

func TestCanonicalConversationEvidenceKeepsMissingInteractionPartial(t *testing.T) {
	sessions := sessionstore.New()
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_complete", ConversationID: "conversation_supply",
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete,
		})
		completeness := "partial"
		reasons := []string{"request_partial"}
		duration := int64(0)
		applyCanonicalConversationEvidenceAndDuration(
			&completeness, &reasons, &duration, tx,
			[]evidencevo.RequestSummary{
				{InteractionID: "interaction_complete", EvidenceCompleteness: "partial"},
				{InteractionID: "interaction_missing", Status: "completed", EvidenceCompleteness: "partial", PartialReasons: []string{"missing_canonical_interaction"}},
			},
		)
		if completeness != "partial" || !containsSummaryValue(reasons, "missing_canonical_interaction") {
			t.Fatalf("missing canonical interaction must remain conservatively partial: completeness=%q reasons=%v", completeness, reasons)
		}
		return nil
	}); err != nil {
		t.Fatalf("apply mixed canonical conversation evidence: %v", err)
	}
}

func TestCanonicalConversationEvidenceNormalizesMissingInteractionContentUnavailable(t *testing.T) {
	sessions := sessionstore.New()
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_complete", ConversationID: "conversation_supply",
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete,
		})
		completeness := "complete"
		reasons := []string(nil)
		duration := int64(0)
		applyCanonicalConversationEvidenceAndDuration(
			&completeness, &reasons, &duration, tx,
			[]evidencevo.RequestSummary{
				{InteractionID: "interaction_complete", EvidenceCompleteness: "complete"},
				{
					InteractionID: "interaction_missing", Status: "completed",
					EvidenceCompleteness: "content_unavailable",
					PartialReasons:       []string{"missing_canonical_interaction"},
				},
			},
		)
		if completeness != "partial" || !containsSummaryValue(reasons, "missing_canonical_interaction") {
			t.Fatalf("request vocabulary must normalize to canonical partial: completeness=%q reasons=%v", completeness, reasons)
		}
		return nil
	}); err != nil {
		t.Fatalf("apply mixed canonical conversation evidence: %v", err)
	}
}

func TestAggregateRequestGroupDoesNotDowngradeBusinessEvidenceForAuxiliaryGap(t *testing.T) {
	base, _ := aggregateRequestGroup([]evidencevo.RequestSummary{
		{
			RequestID: "req_business", Status: "completed", EvidenceCompleteness: "complete",
			QuestionPreview: "查询库存", ResultPreview: "库存 1756",
		},
		{
			RequestID: "req_discovery", Status: "completed", EvidenceCompleteness: "partial",
			ToolName:       "list_knowledge_networks",
			PartialReasons: []string{"supporting_evidence_unavailable"},
		},
	})

	if base.EvidenceCompleteness != "complete" || len(base.PartialReasons) != 0 {
		t.Fatalf("auxiliary discovery must not downgrade business evidence: %+v", base)
	}
}

func TestAggregateRequestGroupDoesNotReportAvailableInteractionContentAsMissing(t *testing.T) {
	base, _ := aggregateRequestGroup([]evidencevo.RequestSummary{
		{
			RequestID: "req_search", Status: "completed", EvidenceCompleteness: "partial",
			InteractionQuestion: "查询 7 月需求预测", InteractionResult: "需求总量 4586",
			PartialReasons: []string{"question_content_unavailable", "result_content_unavailable"},
		},
	})

	if base.QuestionPreview != "查询 7 月需求预测" || base.ResultPreview != "需求总量 4586" {
		t.Fatalf("interaction content must remain readable: %+v", base)
	}
	if base.EvidenceCompleteness != "complete" || len(base.PartialReasons) != 0 {
		t.Fatalf("available interaction content must clear operation-local absence reasons: %+v", base)
	}
}

func TestAggregateRequestGroupKeepsIndependentEvidenceGap(t *testing.T) {
	base, _ := aggregateRequestGroup([]evidencevo.RequestSummary{
		{
			RequestID: "req_search", Status: "error", EvidenceCompleteness: "partial",
			InteractionQuestion: "查询 7 月需求预测", InteractionResult: "需求总量 4586",
			PartialReasons: []string{
				"question_content_unavailable",
				"result_content_unavailable",
				"supporting_evidence_unavailable",
			},
		},
	})

	if base.EvidenceCompleteness != "partial" ||
		len(base.PartialReasons) != 1 || base.PartialReasons[0] != "supporting_evidence_unavailable" {
		t.Fatalf("independent evidence gaps must survive content reconciliation: %+v", base)
	}
}

func TestListRequestsAddsResolvedBusinessSummaryWithoutGatingAccess(t *testing.T) {
	store := evidencestore.New()
	seedBusinessProvenanceRequest(
		t, store, "req_inventory", "trace_inventory", "conversation_supply_chain", "interaction_inventory",
		"2026-08-03T08:00:00Z", "查询物料库存", "库存 1756", "acct_demo",
	)
	resolver := &fakeBusinessResolver{resolutions: []ibusinessresolver.Resolution{{
		RefID: "object:kn_demo:item", Visibility: "visible",
		Display: &evidencevo.BusinessDisplay{Name: "物料库存", ResolutionStatus: "resolved"},
	}}}
	service := New(store, WithProjectionSource(store), WithBusinessResolver(resolver))

	page, err := service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})

	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("list requests: page=%+v err=%v", page, err)
	}
	if page.Entries[0].ControlledSummary != "物料库存" {
		t.Fatalf("controlled summary = %q, want resolved business name", page.Entries[0].ControlledSummary)
	}
	if len(resolver.requests) != 1 {
		t.Fatalf("business display refs must be resolved in one batch: %+v", resolver.requests)
	}
}

func TestListRequestsSummarizesSchemaDiscoveryWithKnowledgeNetworkName(t *testing.T) {
	resolver := &fakeBusinessResolver{resolutions: []ibusinessresolver.Resolution{
		{RefID: "action_type:kn_supply:create_po", Visibility: "visible", Display: &evidencevo.BusinessDisplay{Name: "发起采购订单"}},
		{RefID: "kn:kn_supply", Visibility: "visible", Display: &evidencevo.BusinessDisplay{Name: "HD供应链业务知识网络_v3"}},
		{RefID: "object:kn_supply:sales_order", Visibility: "visible", Display: &evidencevo.BusinessDisplay{Name: "销售订单"}},
	}}
	service := New(nil, WithBusinessResolver(resolver))
	requests := []evidencevo.RequestSummary{{
		ToolName: "search_schema",
		BusinessRefs: []string{
			"action_type:kn_supply:create_po",
			"kn:kn_supply",
			"object:kn_supply:sales_order",
		},
	}}

	service.enrichRequestBusinessSummaries(context.Background(), requests, summaryScope("acct_demo"))

	if requests[0].ControlledSummary != "HD供应链业务知识网络_v3" {
		t.Fatalf("schema discovery summary = %q, want knowledge network name", requests[0].ControlledSummary)
	}
}

func TestListTraceSummariesUsesAuthoritativeSpanStats(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_spans", "trace_spans", "2026-08-03T08:00:00Z", "问题", "结果", "cursor", "bd_demo", "acct_demo")
	service := New(
		store,
		WithProjectionSource(store),
		WithTraceStatsSource(fixedTraceStatsSource{"trace_spans": 16}),
	)

	page, err := service.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("list traces: page=%+v err=%v", page, err)
	}
	if page.Entries[0].SpanCount != 16 || page.Entries[0].SpanCountStatus != "available" {
		t.Fatalf("trace summary must use the trace store span count: %+v", page.Entries[0])
	}
}

func TestTraceSummaryTechnicalFiltersAreIndependent(t *testing.T) {
	t.Parallel()

	summary := evidencevo.TraceSummary{
		TraceID: "trace-filter", AgentOrApp: "cursor-agent", RootService: "context-loader",
		RootOperation: "run_sql", ErrorSummary: "database timeout while executing query",
	}
	if !matchesTraceFilters(summary, evidencevo.SummaryQueryOptions{
		Service: "context-loader", Tool: "run_sql", ErrorKeyword: "timeout",
	}) {
		t.Fatal("matching service, tool and error keyword must keep the trace")
	}
	for _, options := range []evidencevo.SummaryQueryOptions{
		{Service: "bkn-sdk"},
		{Tool: "search_schema"},
		{ErrorKeyword: "permission denied"},
	} {
		if matchesTraceFilters(summary, options) {
			t.Fatalf("independent technical filter must reject a mismatch: %+v", options)
		}
	}
}

func TestTraceSummaryRootServiceComesFromTraceProducer(t *testing.T) {
	t.Parallel()

	trace := evidencevo.NormalizedTrace{
		TraceID: "trace-service", RequestID: "req-service",
		Events: []evidencevo.EvidenceEvent{{
			EventID: "event-service", EventType: "agent.interaction.started",
			ObservedAt: "2026-08-10T09:00:00Z", Producer: "context-loader",
			OperationName: "run_sql", Payload: map[string]any{"agent_id": "cursor-agent"},
		}},
	}
	_, traces := evidencevo.BuildExecutionSummaries([]evidencevo.NormalizedTrace{trace}, nil)
	if len(traces) != 1 || traces[0].RootService != "context-loader" {
		t.Fatalf("root service must preserve the trace producer: %+v", traces)
	}
}

func (s *capturingProjectionSource) LoadExecutionProjection(_ context.Context, query iprojectionsource.Query) (iprojectionsource.Result, error) {
	s.queries = append(s.queries, query)
	if s.resultFor != nil {
		return s.resultFor(query), nil
	}
	return s.result, nil
}

func TestListRequestsUsesStableCursorPagination(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_old", "trace_old", "2026-07-26T08:00:00Z", "旧问题", "旧结果", "agent-a", "bd_demo", "acct_demo")
	seedSummaryRequest(t, store, "req_new_b", "trace_new_b", "2026-07-26T09:00:00Z", "新问题 B", "新结果 B", "agent-b", "bd_demo", "acct_demo")
	seedSummaryRequest(t, store, "req_new_a", "trace_new_a", "2026-07-26T09:00:00Z", "新问题 A", "新结果 A", "agent-a", "bd_demo", "acct_demo")
	service := NewWithProjectionSource(store, store)
	options := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Limit: 1}

	first, err := service.ListRequests(context.Background(), options)
	if err != nil || len(first.Entries) != 1 || first.Entries[0].RequestID != "req_new_a" || first.NextCursor == nil {
		t.Fatalf("unexpected first page: %+v err=%v", first, err)
	}
	options.Cursor = *first.NextCursor
	second, err := service.ListRequests(context.Background(), options)
	if err != nil || len(second.Entries) != 1 || second.Entries[0].RequestID != "req_new_b" || second.NextCursor == nil {
		t.Fatalf("unexpected second page: %+v err=%v", second, err)
	}
	options.Cursor = *second.NextCursor
	third, err := service.ListRequests(context.Background(), options)
	if err != nil || len(third.Entries) != 1 || third.Entries[0].RequestID != "req_old" || third.NextCursor != nil {
		t.Fatalf("unexpected third page: %+v err=%v", third, err)
	}
}

func TestListRequestsSupportsPageNumberPagination(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_old", "trace_old", "2026-07-26T08:00:00Z", "旧问题", "旧结果", "agent-a", "bd_demo", "acct_demo")
	seedSummaryRequest(t, store, "req_new", "trace_new", "2026-07-26T09:00:00Z", "新问题", "新结果", "agent-a", "bd_demo", "acct_demo")
	service := NewWithProjectionSource(store, store)

	page, err := service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 1, Page: 2,
	})
	if err != nil || page.Page != 2 || page.PageSize != 1 || page.Total != 2 || len(page.Entries) != 1 || page.Entries[0].RequestID != "req_old" {
		t.Fatalf("unexpected numbered request page: %+v err=%v", page, err)
	}
}

func TestListRequestsFiltersReliableProjectionFields(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_match", "trace_match", "2026-07-26T09:00:00Z", "查询七月预测单", "共 12 张", "agent-match", "bd_demo", "acct_demo")
	seedSummaryRequest(t, store, "req_other", "trace_other", "2026-07-25T09:00:00Z", "查询库存", "库存正常", "agent-other", "bd_demo", "acct_demo")
	service := NewWithBusinessResolver(store, &fakeBusinessResolver{
		resolutions: []ibusinessresolver.Resolution{{
			RefID: "object:kn_demo:item", Visibility: "visible",
		}},
	})

	page, err := service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
		From:   time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
		Status: "completed", AgentOrApp: "agent-match", BusinessDomain: "bd_demo", Keyword: "七月",
		KnowledgeNetwork: "kn_demo", EvidenceCompleteness: "complete",
	})

	if err != nil || len(page.Entries) != 1 || page.Entries[0].RequestID != "req_match" {
		t.Fatalf("expected only matching request: %+v err=%v", page, err)
	}

	page, err = service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
		KnowledgeNetwork: "kn_missing", EvidenceCompleteness: "complete",
	})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("knowledge network filter must not match another network: %+v err=%v", page, err)
	}
}

func TestListRequestsFiltersByConversationAndInteraction(t *testing.T) {
	store := evidencestore.New()
	trace := evidencevo.NormalizedTrace{
		TraceID: "trace_identity", RequestID: "req_identity",
		ConversationID: "conversation_supply_chain",
		TenantID:       "tenant_demo", BusinessDomain: "bd_demo",
		AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: evidencevo.ArtifactContractVersion,
		Events: []evidencevo.EvidenceEvent{{
			EventID: "event_identity", EventType: "agent.interaction.started",
			SchemaVersion: evidencevo.ArtifactContractVersion,
			ObservedAt:    "2026-07-27T08:00:00Z", EmittedAt: "2026-07-27T08:00:00Z",
			Producer: "bkn-agent", TraceID: "trace_identity", SpanID: "span_identity",
			RequestID: "req_identity", InteractionID: "interaction_june_forecast",
			OperationName: "agent.chat",
			Payload:       map[string]any{"agent_id": "agent_supply_chain"},
		}},
	}
	if err := store.StoreEvidence(context.Background(), trace); err != nil {
		t.Fatalf("store evidence: %v", err)
	}
	service := NewWithProjectionSource(store, store)

	for _, options := range []evidencevo.SummaryQueryOptions{
		{Scope: summaryScope("acct_demo"), ConversationID: "conversation_supply_chain"},
		{Scope: summaryScope("acct_demo"), InteractionID: "interaction_june_forecast"},
	} {
		page, err := service.ListRequests(context.Background(), options)
		if err != nil {
			t.Fatalf("list requests: %v", err)
		}
		if page.Total != 1 || len(page.Entries) != 1 {
			t.Fatalf("expected one matching request, got %+v", page)
		}
	}

	page, err := service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), InteractionID: "interaction_other",
	})
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("unexpected interaction match: %+v", page)
	}
}

func TestGetInteractionSummaryAggregatesMultipleRequestsAndTraces(t *testing.T) {
	store := evidencestore.New()
	for _, item := range []struct {
		requestID string
		traceID   string
		at        string
	}{
		{"req_schema", "trace_schema", "2026-07-27T08:00:00Z"},
		{"req_sql", "trace_sql", "2026-07-27T08:00:01Z"},
	} {
		trace := evidencevo.NormalizedTrace{
			TraceID: item.traceID, RequestID: item.requestID,
			ConversationID: "conversation_supply_chain",
			TenantID:       "tenant_demo", BusinessDomain: "bd_demo",
			AccountID: "acct_demo", AccountType: "app",
			SchemaVersion: evidencevo.ArtifactContractVersion,
			Events: []evidencevo.EvidenceEvent{{
				EventID: "event_" + item.traceID, EventType: "agent.interaction.started",
				SchemaVersion: evidencevo.ArtifactContractVersion,
				ObservedAt:    item.at, EmittedAt: item.at,
				Producer: "third-party-agent", TraceID: item.traceID, SpanID: "1000000000000001",
				RequestID: item.requestID, InteractionID: "interaction_june_forecast",
				OperationName: "agent.run",
				Payload:       map[string]any{"app_ref": "supply-chain-agent"},
			}},
		}
		if err := store.StoreEvidence(context.Background(), trace); err != nil {
			t.Fatalf("store evidence: %v", err)
		}
	}
	service := NewWithProjectionSource(store, store)

	summary, found, err := service.GetInteractionSummary(
		context.Background(),
		"interaction_june_forecast",
		summaryScope("acct_demo"),
	)

	if err != nil || !found {
		t.Fatalf("get interaction: found=%v err=%v", found, err)
	}
	if summary.ConversationID != "conversation_supply_chain" ||
		len(summary.Requests) != 2 || len(summary.Traces) != 2 {
		t.Fatalf("interaction must aggregate both calls: %+v", summary)
	}
}

func TestGetInteractionSummaryRetainsCompleteTextForAuthorizedInProcessReader(t *testing.T) {
	store := evidencestore.New()
	seedBusinessProvenanceRequest(
		t, store, "req_complete_text", "trace_complete_text", "conversation_complete_text", "interaction_complete_text",
		"2026-08-10T10:00:00Z", "完整的用户问题", "完整的业务结果", "acct_demo",
	)
	service := NewWithProjectionSource(store, store)
	summary, found, err := service.GetInteractionSummary(context.Background(), "interaction_complete_text", summaryScope("acct_demo"))
	if err != nil || !found {
		t.Fatalf("GetInteractionSummary() found=%v err=%v", found, err)
	}
	if summary.InteractionQuestion != "完整的用户问题" || summary.InteractionResult != "完整的业务结果" {
		t.Fatalf("complete interaction text = question %q result %q", summary.InteractionQuestion, summary.InteractionResult)
	}
}

func TestBusinessProvenanceListsUseTrueConversationInteractionAndRequestCardinality(t *testing.T) {
	store := evidencestore.New()
	seedBusinessProvenanceRequest(t, store, "req_plan", "trace_plan", "conversation_supply", "interaction_june", "2026-07-27T08:00:00Z", "查询六月预测", "六月共 63 条", "acct_demo")
	seedBusinessProvenanceRequest(t, store, "req_data", "trace_data", "conversation_supply", "interaction_june", "2026-07-27T08:00:01Z", "读取预测数据", "合计 11594", "acct_demo")
	seedBusinessProvenanceRequest(t, store, "req_compare", "trace_compare", "conversation_supply", "interaction_july", "2026-07-27T09:00:00Z", "对比七月预测", "七月下降 60%", "acct_demo")
	seedBusinessProvenanceRequest(t, store, "req_secret", "trace_secret", "conversation_secret", "interaction_secret", "2026-07-27T10:00:00Z", "不可见问题", "不可见结果", "other-account")
	service := NewWithProjectionSource(store, store)
	options := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Limit: 20}

	conversations, err := service.ListConversations(context.Background(), options)
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	interactions, err := service.ListInteractions(context.Background(), options)
	if err != nil {
		t.Fatalf("list interactions: %v", err)
	}
	requests, err := service.ListRequests(context.Background(), options)
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}

	if conversations.Total != 1 || len(conversations.Entries) != 1 {
		t.Fatalf("expected one authorized conversation, got %+v", conversations)
	}
	conversation := conversations.Entries[0]
	if conversation.ConversationID != "conversation_supply" || conversation.InteractionCount != 2 || conversation.RequestCount != 3 ||
		conversation.QuestionPreview == "不可见问题" || conversation.ResultPreview == "不可见结果" {
		t.Fatalf("unexpected conversation projection: %+v", conversation)
	}
	if interactions.Total != 2 || len(interactions.Entries) != 2 {
		t.Fatalf("expected two authorized interactions, got %+v", interactions)
	}
	if requests.Total != 3 || len(requests.Entries) != 3 {
		t.Fatalf("expected three authorized requests, got %+v", requests)
	}
}

func TestListInteractionsIncludesCanonicalInteractionWithoutRequests(t *testing.T) {
	evidenceStore := evidencestore.New()
	seedBusinessProvenanceRequest(
		t, evidenceStore, "req_first", "trace_first", "conversation_three_turns", "interaction_first",
		"2026-08-10T08:00:00Z", "第一轮问题", "第一轮结果", "acct_demo",
	)
	sessions := sessionstore.New()
	owner := sessionvo.Owner{
		TenantID: "tenant_demo", BusinessDomainID: "bd_demo",
		ApplicationPrincipalID: "cursor-agent",
		EffectiveSubjectType:   sessionvo.SubjectUser, EffectiveSubjectID: "acct_demo",
	}
	secondTerminalAt := time.Date(2026, 8, 10, 8, 5, 2, 0, time.UTC)
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{
			ID: "conversation_three_turns", AgentName: "Cursor Agent", Owner: owner,
			Status:    sessionvo.ConversationActive,
			CreatedAt: time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC),
			UpdatedAt: secondTerminalAt,
		})
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_first", ConversationID: "conversation_three_turns", Ordinal: 1,
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete,
			CreatedAt: time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 8, 10, 8, 0, 1, 0, time.UTC),
		})
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_second", ConversationID: "conversation_three_turns", Ordinal: 2,
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceNotApplicable,
			CreatedAt: time.Date(2026, 8, 10, 8, 5, 0, 0, time.UTC),
			UpdatedAt: secondTerminalAt, TerminalAt: &secondTerminalAt,
		})
		return nil
	}); err != nil {
		t.Fatalf("seed canonical session: %v", err)
	}
	service := New(evidenceStore, WithProjectionSource(evidenceStore), WithSessionStore(sessions))

	page, err := service.ListInteractions(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), ConversationID: "conversation_three_turns", Limit: 20,
	})

	if err != nil {
		t.Fatalf("list interactions: %v", err)
	}
	if page.Total != 2 || len(page.Entries) != 2 {
		t.Fatalf("canonical interactions must not disappear when they have no requests: %+v", page)
	}
	second := page.Entries[0]
	if second.InteractionID != "interaction_second" || second.ConversationID != "conversation_three_turns" ||
		second.RequestCount != 0 || second.Status != "completed" || second.EvidenceCompleteness != "not_applicable" {
		t.Fatalf("unexpected canonical-only interaction: %+v", second)
	}
}

func TestGetInteractionSummaryReturnsAuthorizedCanonicalInteractionWithoutRequests(t *testing.T) {
	evidenceStore := evidencestore.New()
	sessions := sessionstore.New()
	owner := sessionvo.Owner{
		TenantID: "tenant_demo", BusinessDomainID: "bd_demo",
		ApplicationPrincipalID: "cursor-agent",
		EffectiveSubjectType:   sessionvo.SubjectUser, EffectiveSubjectID: "acct_demo",
	}
	terminalAt := time.Date(2026, 8, 10, 8, 5, 2, 0, time.UTC)
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{
			ID: "conversation_three_turns", AgentName: "Cursor Agent", Owner: owner,
			Status:    sessionvo.ConversationActive,
			CreatedAt: time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC), UpdatedAt: terminalAt,
		})
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_without_calls", ConversationID: "conversation_three_turns", Ordinal: 2,
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceNotApplicable,
			CreatedAt: time.Date(2026, 8, 10, 8, 5, 0, 0, time.UTC),
			UpdatedAt: terminalAt, TerminalAt: &terminalAt,
		})
		return nil
	}); err != nil {
		t.Fatalf("seed canonical session: %v", err)
	}
	service := New(evidenceStore, WithProjectionSource(evidenceStore), WithSessionStore(sessions))

	summary, found, err := service.GetInteractionSummary(
		context.Background(), "interaction_without_calls", summaryScope("acct_demo"),
	)

	if err != nil || !found {
		t.Fatalf("canonical interaction must be readable: found=%v err=%v", found, err)
	}
	if summary.ConversationID != "conversation_three_turns" || summary.AgentName != "Cursor Agent" ||
		summary.Status != "completed" || summary.EvidenceCompleteness != "not_applicable" ||
		len(summary.Requests) != 0 || len(summary.Traces) != 0 {
		t.Fatalf("unexpected canonical-only interaction summary: %+v", summary)
	}
}

func TestCanonicalInteractionWithoutRequestsDoesNotBypassRecordScope(t *testing.T) {
	evidenceStore := evidencestore.New()
	sessions := sessionstore.New()
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{
			ID: "conversation_private", Owner: sessionvo.Owner{
				TenantID: "tenant_demo", BusinessDomainID: "bd_demo",
				ApplicationPrincipalID: "private-agent",
				EffectiveSubjectType:   sessionvo.SubjectUser, EffectiveSubjectID: "other_account",
			},
			Status: sessionvo.ConversationActive, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
		tx.SaveInteraction(sessionvo.Interaction{
			ID: "interaction_private", ConversationID: "conversation_private", Ordinal: 1,
			ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceNotApplicable,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
		return nil
	}); err != nil {
		t.Fatalf("seed private interaction: %v", err)
	}
	service := New(evidenceStore, WithProjectionSource(evidenceStore), WithSessionStore(sessions))

	page, err := service.ListInteractions(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), ConversationID: "conversation_private", Limit: 20,
	})
	if err != nil || page.Total != 0 {
		t.Fatalf("unauthorized canonical interaction leaked through list: page=%+v err=%v", page, err)
	}
	if _, found, err := service.GetInteractionSummary(context.Background(), "interaction_private", summaryScope("acct_demo")); err != nil || found {
		t.Fatalf("unauthorized canonical interaction leaked through detail: found=%v err=%v", found, err)
	}
}

func TestBusinessProvenanceConversationAndInteractionListsUseStablePagination(t *testing.T) {
	store := evidencestore.New()
	seedBusinessProvenanceRequest(t, store, "req_old", "trace_old", "conversation_old", "interaction_old", "2026-07-27T08:00:00Z", "旧问题", "旧结果", "acct_demo")
	seedBusinessProvenanceRequest(t, store, "req_new", "trace_new", "conversation_new", "interaction_new", "2026-07-27T09:00:00Z", "新问题", "新结果", "acct_demo")
	service := NewWithProjectionSource(store, store)
	options := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Limit: 1}

	firstConversations, err := service.ListConversations(context.Background(), options)
	if err != nil || len(firstConversations.Entries) != 1 || firstConversations.Entries[0].ConversationID != "conversation_new" || firstConversations.NextCursor == nil {
		t.Fatalf("unexpected first conversation page: %+v err=%v", firstConversations, err)
	}
	options.Cursor = *firstConversations.NextCursor
	secondConversations, err := service.ListConversations(context.Background(), options)
	if err != nil || len(secondConversations.Entries) != 1 || secondConversations.Entries[0].ConversationID != "conversation_old" || secondConversations.NextCursor != nil {
		t.Fatalf("unexpected second conversation page: %+v err=%v", secondConversations, err)
	}

	options.Cursor = ""
	firstInteractions, err := service.ListInteractions(context.Background(), options)
	if err != nil || len(firstInteractions.Entries) != 1 || firstInteractions.Entries[0].InteractionID != "interaction_new" || firstInteractions.NextCursor == nil {
		t.Fatalf("unexpected first interaction page: %+v err=%v", firstInteractions, err)
	}
	options.Cursor = *firstInteractions.NextCursor
	secondInteractions, err := service.ListInteractions(context.Background(), options)
	if err != nil || len(secondInteractions.Entries) != 1 || secondInteractions.Entries[0].InteractionID != "interaction_old" || secondInteractions.NextCursor != nil {
		t.Fatalf("unexpected second interaction page: %+v err=%v", secondInteractions, err)
	}
}

func TestRequestAndTraceSummarySupportMultipleTracesAndReverseLookup(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_multi", "trace_multi_a", "2026-07-26T08:00:00Z", "多阶段问题", "最终结果", "agent-a", "bd_demo", "acct_demo")
	seedSummaryTrace(t, store, "req_multi", "trace_multi_b", "2026-07-26T08:05:00Z", "agent-worker", "bd_demo", "acct_demo")
	service := NewWithProjectionSource(store, store)

	request, found, err := service.GetRequestSummary(context.Background(), "req_multi", summaryScope("acct_demo"))
	if err != nil || !found || request.TraceCount != 2 {
		t.Fatalf("request must aggregate two traces: request=%+v found=%v err=%v", request, found, err)
	}
	traces, err := service.ListRequestTraces(context.Background(), "req_multi", evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})
	if err != nil || len(traces.Entries) != 2 {
		t.Fatalf("expected two request traces: %+v err=%v", traces, err)
	}
	for _, trace := range traces.Entries {
		if trace.RequestID != "req_multi" {
			t.Fatalf("trace must reverse-link request: %+v", trace)
		}
	}
}

func TestListRequestsDoesNotLeakUnauthorizedPreview(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_owned", "trace_owned", "2026-07-26T09:00:00Z", "可见问题", "可见结果", "agent-a", "bd_demo", "acct_demo")
	seedSummaryRequest(t, store, "req_secret", "trace_secret", "2026-07-26T10:00:00Z", "不可见问题", "不可见结果", "agent-b", "bd_demo", "other-account")
	service := NewWithProjectionSource(store, store)

	page, err := service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Limit: 20})

	if err != nil || len(page.Entries) != 1 || page.Entries[0].RequestID != "req_owned" {
		t.Fatalf("unauthorized request and preview must be absent: %+v err=%v", page, err)
	}
}

func TestListRequestsKeepsRecordAuthorizedBusinessRefsWithoutResolverAuthorization(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_refs", "trace_refs", "2026-07-26T09:00:00Z", "查询业务引用", "返回业务结果", "agent-a", "bd_demo", "acct_demo")
	if err := store.StoreEvidence(context.Background(), evidencevo.NormalizedTrace{
		TraceID: "trace_refs", RequestID: "req_refs",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: evidencevo.ArtifactContractVersion,
		Events: []evidencevo.EvidenceEvent{{
			EventID: "event_refs", EventType: "claim.created",
			SchemaVersion: evidencevo.ArtifactContractVersion,
			ObservedAt:    "2026-07-26T09:00:01Z", EmittedAt: "2026-07-26T09:00:01Z",
			TraceID: "trace_refs", RequestID: "req_refs", OperationName: "claim.create",
			Payload: map[string]any{
				"claim_id": "claim_refs",
				"business_refs": []any{
					map[string]any{"ref_id": "object:kn_secret:item", "visibility": "visible"},
					map[string]any{"ref_id": "object:kn_hidden:item", "visibility": "hidden"},
				},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	resolver := &fakeBusinessResolver{resolutions: []ibusinessresolver.Resolution{{
		RefID: "object:kn_secret:item", Visibility: "unauthorized",
	}}}
	service := NewWithBusinessResolver(store, resolver)

	page, err := service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})

	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("expected one request summary: page=%+v err=%v", page, err)
	}
	summary := page.Entries[0]
	if !containsSummaryValue(summary.BusinessRefs, "object:kn_secret:item") ||
		!containsSummaryValue(summary.KnowledgeNetworks, "kn_secret") {
		t.Fatalf("record-authorized visible refs must remain in summary: %+v", summary)
	}
	if containsSummaryValue(summary.BusinessRefs, "object:kn_hidden:item") {
		t.Fatalf("producer-hidden refs must remain hidden: %+v", summary)
	}
	if len(resolver.requests) != 1 || summary.ControlledSummary != "" {
		t.Fatalf("resolver may enrich display but must not expose unauthorized names or gate the record: requests=%+v summary=%+v", resolver.requests, summary)
	}

	keywordPage, err := service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20, Keyword: "kn_secret",
	})
	if err != nil || len(keywordPage.Entries) != 1 {
		t.Fatalf("record-authorized ref must remain keyword searchable: page=%+v err=%v", keywordPage, err)
	}
}

func TestListRequestsResolvesAllSummaryBusinessRefsOncePerQuery(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_resolver_batch", "trace_resolver_batch", "2026-07-26T09:00:00Z", "批量授权问题", "批量授权结果", "agent-a", "bd_demo", "acct_demo")
	support, validationErrors := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{
		ArtifactID: "support_resolver_batch", ArtifactType: evidencevo.ArtifactTypeDataResult,
		RequestID: "req_resolver_batch", TraceID: "trace_resolver_batch",
		SourceRef: "resource:orders", BusinessRefs: []string{"object:kn_demo:item", "object:kn_demo:item"},
		ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion,
		ObservedAt: "2026-07-26T09:00:01Z", Content: map[string]any{"count": 12},
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
	})
	if len(validationErrors) != 0 {
		t.Fatalf("normalize support artifact: %+v", validationErrors)
	}
	if _, err := store.StoreArtifact(context.Background(), support); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreEvidence(context.Background(), evidencevo.NormalizedTrace{
		TraceID: "trace_resolver_batch", RequestID: "req_resolver_batch",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: evidencevo.ArtifactContractVersion,
		Events: []evidencevo.EvidenceEvent{{
			EventID: "event_resolver_batch", EventType: "data.query.observed",
			SchemaVersion: evidencevo.ArtifactContractVersion,
			ObservedAt:    "2026-07-26T09:00:01Z", EmittedAt: "2026-07-26T09:00:01Z",
			TraceID: "trace_resolver_batch", RequestID: "req_resolver_batch", OperationName: "data.query",
			Payload: map[string]any{
				"result_artifact_ref": "artifact:support_resolver_batch",
				"resource_refs": []any{
					map[string]any{"ref_id": "resource:orders", "visibility": "visible"},
					map[string]any{"ref_id": "object:kn_demo:item", "visibility": "visible"},
				},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	resolver := &fakeBusinessResolver{resolutions: []ibusinessresolver.Resolution{
		{RefID: "object:kn_demo:item", Visibility: "visible"},
		{RefID: "resource:orders", Visibility: "visible"},
	}}

	page, err := NewWithBusinessResolver(store, resolver).ListRequests(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})

	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("expected authorized summary: page=%+v err=%v", page, err)
	}
	if len(resolver.requests) != 1 || len(resolver.requests[0].Refs) != 2 {
		t.Fatalf("request summary must resolve all display refs in one batch: %+v", resolver.requests)
	}
}

func TestListRequestsKeepsTwoPointOneRequestReadableWithoutArtifacts(t *testing.T) {
	store := evidencestore.New()
	seedTwoPointOneSummaryTrace(t, store, "req_no_artifact", "trace_no_artifact", "2026-07-26T08:00:00Z", "bd_demo", "acct_demo")
	service := NewWithProjectionSource(store, store)

	page, err := service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Limit: 20})

	if err != nil || len(page.Entries) != 1 || page.Entries[0].EvidenceCompleteness != "content_unavailable" {
		t.Fatalf("2.1 request must remain readable with content_unavailable: %+v err=%v", page, err)
	}
}

func TestListRequestsFailsSoftWhenBusinessResolverIsUnavailable(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(
		t,
		store,
		"req_resolver_unavailable",
		"trace_resolver_unavailable",
		"2026-07-26T09:00:00Z",
		"查询采购履约风险",
		"发现一条逾期采购订单",
		"agent-a",
		"bd_demo",
		"acct_demo",
	)
	service := NewWithBusinessResolver(store, &fakeBusinessResolver{
		err: errors.New("resolver upstream unavailable"),
	})

	page, err := service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})

	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("resolver outage must not hide the business run: page=%+v err=%v", page, err)
	}
	entry := page.Entries[0]
	if entry.QuestionPreview != "查询采购履约风险" || entry.ResultPreview != "发现一条逾期采购订单" {
		t.Fatalf("authorized question and result must remain visible: %+v", entry)
	}
	if page.Partial && containsSummaryReason(page.PartialReasons, "business_resolver_unavailable") {
		t.Fatalf("resolver outage must not degrade a summary that does not require display resolution: %+v", page)
	}
	if containsSummaryReason(entry.PartialReasons, "business_resolver_unavailable") {
		t.Fatalf("resolver outage must not change objective evidence completeness: %+v", entry)
	}
}

func TestListRequestsFailsClosedWithoutTrustedScope(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_unscoped", "trace_unscoped", "2026-07-26T09:00:00Z", "不应泄露问题", "不应泄露结果", "agent-a", "bd_demo", "acct_demo")
	service := NewWithProjectionSource(store, store)

	page, err := service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{Limit: 20})

	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("missing trusted scope must return no summaries: %+v err=%v", page, err)
	}
}

func TestListRequestsReportsBoundedProjectionTruncationAndPushdownQuery(t *testing.T) {
	trace := evidencevo.NormalizedTrace{
		TraceID: "trace_capped", RequestID: "req_capped",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		Events: []evidencevo.EvidenceEvent{{
			EventID: "event_capped", EventType: "agent.interaction.started",
			ObservedAt: "2026-07-26T09:00:00Z", RequestID: "req_capped", TraceID: "trace_capped",
			OperationName: "agent.run", Payload: map[string]any{"agent_id": "agent-capped"},
		}},
	}
	source := &capturingProjectionSource{result: iprojectionsource.Result{
		Traces: []evidencevo.NormalizedTrace{trace}, Truncated: true,
	}}
	service := NewWithProjectionSource(evidencestore.New(), source)
	from := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)

	page, err := service.ListRequests(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), From: from, To: to,
		Status: "running", BusinessDomain: "bd_demo", Keyword: "capped",
	})

	if err != nil || len(page.Entries) != 1 || !page.Truncated || !page.Partial ||
		!containsSummaryReason(page.PartialReasons, "projection_scan_cap_reached") {
		t.Fatalf("bounded projection truncation must be explicit: page=%+v err=%v", page, err)
	}
	if len(source.queries) != 1 {
		t.Fatalf("expected one bounded source call, got %+v", source.queries)
	}
	query := source.queries[0]
	if query.Limit != MaxSummaryScanEntries || !query.From.Equal(from) || !query.To.Equal(to) ||
		query.BusinessDomain != "bd_demo" || query.Status != "running" {
		t.Fatalf("reliable filters and cap must be pushed to source: %+v", query)
	}
}

func TestExactRequestSummaryAndTraceLookupDoNotCallListProjection(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_exact", "trace_exact", "2026-07-26T09:00:00Z", "精确问题", "精确结果", "agent-exact", "bd_demo", "acct_demo")
	if err := store.StoreEvidence(context.Background(), evidencevo.NormalizedTrace{
		TraceID: "trace_exact", RequestID: "req_exact",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: evidencevo.ArtifactContractVersion,
		Events: []evidencevo.EvidenceEvent{{
			EventID: "event_exact_append", EventType: "claim.created",
			SchemaVersion: evidencevo.ArtifactContractVersion,
			ObservedAt:    "2026-07-26T09:00:01Z", EmittedAt: "2026-07-26T09:00:01Z",
			TraceID: "trace_exact", RequestID: "req_exact", OperationName: "claim.append",
			Payload: map[string]any{"claim_id": "claim_exact_append"},
		}},
	}); err != nil {
		t.Fatalf("append exact trace batch: %v", err)
	}
	source := &capturingProjectionSource{}
	service := NewWithProjectionSource(store, source)

	request, found, err := service.GetRequestSummary(context.Background(), "req_exact", summaryScope("acct_demo"))
	if err != nil || !found || request.RequestID != "req_exact" {
		t.Fatalf("exact request lookup failed: request=%+v found=%v err=%v", request, found, err)
	}
	traces, err := service.ListRequestTraces(context.Background(), "req_exact", evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})
	if err != nil || len(traces.Entries) != 1 || traces.Entries[0].TraceID != "trace_exact" {
		t.Fatalf("exact request trace lookup failed: traces=%+v err=%v", traces, err)
	}
	if len(source.queries) != 0 {
		t.Fatalf("exact request queries must not scan list projection: %+v", source.queries)
	}
}

func TestExactRequestSummaryAndTraceLookupFallsBackToReceiptProjection(t *testing.T) {
	trace := evidencevo.NormalizedTrace{
		TraceID: "trace_receipt", RequestID: "req_receipt",
		ConversationID: "conversation_supply_chain",
		TenantID:       "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		Events: []evidencevo.EvidenceEvent{{
			EventID: "receipt:receipt_1", EventType: "retrieval.completed",
			ObservedAt: "2026-08-02T09:00:00Z", EmittedAt: "2026-08-02T09:00:01Z",
			RequestID: "req_receipt", TraceID: "trace_receipt", InteractionID: "interaction_july",
			OperationID: "op_run_sql", OperationName: "run_sql",
			Payload: map[string]any{
				"status": "completed", "operation_key": "forecast-data-query",
			},
		}},
	}
	source := &capturingProjectionSource{result: iprojectionsource.Result{
		Traces: []evidencevo.NormalizedTrace{trace},
	}}
	service := NewWithProjectionSource(evidencestore.New(), source)

	request, found, err := service.GetRequestSummary(context.Background(), "req_receipt", summaryScope("acct_demo"))
	if err != nil || !found {
		t.Fatalf("receipt-backed request must support exact lookup: request=%+v found=%v err=%v", request, found, err)
	}
	if request.OperationID != "op_run_sql" || request.OperationKey != "forecast-data-query" || request.ToolName != "run_sql" {
		t.Fatalf("receipt-backed request must preserve operation identity: %+v", request)
	}

	traces, err := service.ListRequestTraces(context.Background(), "req_receipt", evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})
	if err != nil || len(traces.Entries) != 1 || traces.Entries[0].TraceID != "trace_receipt" {
		t.Fatalf("receipt-backed request traces must support exact lookup: traces=%+v err=%v", traces, err)
	}
	if len(source.queries) != 3 {
		t.Fatalf("request detail must query its receipt and interaction context, while trace lookup remains request-scoped: %+v", source.queries)
	}
	if source.queries[0].RequestID != "req_receipt" || source.queries[1].InteractionID != "interaction_july" ||
		source.queries[2].RequestID != "req_receipt" {
		t.Fatalf("exact fallback must push request or interaction identity: %+v", source.queries)
	}
	for _, query := range source.queries {
		if query.Limit != MaxSummaryScanEntries {
			t.Fatalf("exact fallback must remain bounded: %+v", query)
		}
	}
}

func TestExactRequestSummaryKeepsInteractionContentOutOfOperation(t *testing.T) {
	receipt := evidencevo.NormalizedTrace{
		TraceID: "trace_enriched", RequestID: "req_enriched", ConversationID: "conversation_supply_chain",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		Events: []evidencevo.EvidenceEvent{{
			EventID: "receipt:enriched", EventType: "retrieval.completed",
			ObservedAt: "2026-08-02T09:00:01Z", EmittedAt: "2026-08-02T09:00:02Z",
			RequestID: "req_enriched", TraceID: "trace_enriched", InteractionID: "interaction_june",
			OperationID: "op_enriched", OperationName: "run_sql",
			Payload: map[string]any{"status": "completed", "operation_key": "june-query"},
		}},
	}
	interactionTrace := receipt
	interactionTrace.Events = append([]evidencevo.EvidenceEvent{
		{
			EventID: "question:enriched", EventType: "agent.interaction.started",
			ObservedAt: "2026-08-02T09:00:00Z", EmittedAt: "2026-08-02T09:00:00Z",
			RequestID: "req_enriched", TraceID: "trace_enriched", InteractionID: "interaction_june",
			Payload: map[string]any{"question_artifact_ref": "artifact:question_enriched"},
		},
		{
			EventID: "result:enriched", EventType: "claim.created",
			ObservedAt: "2026-08-02T09:00:03Z", EmittedAt: "2026-08-02T09:00:03Z",
			RequestID: "req_enriched", TraceID: "trace_enriched", InteractionID: "interaction_june",
			Payload: map[string]any{
				"claim_id": "claim_enriched", "result_artifact_ref": "artifact:result_enriched",
				"business_refs": []any{map[string]any{"ref_id": "object:kn_demo:forecast"}},
			},
		},
	}, receipt.Events...)
	question := summaryServiceArtifact(t, "question_enriched", evidencevo.ArtifactTypeQuestion, "req_enriched", "trace_enriched", "interaction_june", "六月预测是多少？")
	result := summaryServiceArtifact(t, "result_enriched", evidencevo.ArtifactTypeResult, "req_enriched", "trace_enriched", "interaction_june", "合计 11594")
	source := &capturingProjectionSource{resultFor: func(query iprojectionsource.Query) iprojectionsource.Result {
		if query.InteractionID == "interaction_june" {
			return iprojectionsource.Result{Traces: []evidencevo.NormalizedTrace{interactionTrace}, Artifacts: []evidencevo.EvidenceArtifact{question, result}}
		}
		return iprojectionsource.Result{Traces: []evidencevo.NormalizedTrace{receipt}}
	}}
	service := NewWithProjectionSource(evidencestore.New(), source)

	request, found, err := service.GetRequestSummary(context.Background(), "req_enriched", summaryScope("acct_demo"))

	if err != nil || !found {
		t.Fatalf("exact request lookup failed: request=%+v found=%v err=%v", request, found, err)
	}
	if request.QuestionPreview != "" || request.ResultPreview != "" {
		t.Fatalf("exact request must not expose interaction content as operation input or result: %+v", request)
	}
	if request.InteractionQuestion != "六月预测是多少？" || request.InteractionResult != "合计 11594" {
		t.Fatalf("interaction content must remain available for interaction aggregation: %+v", request)
	}
	if request.StartedAt != "2026-08-02T09:00:01Z" || request.CompletedAt != "2026-08-02T09:00:02Z" || request.DurationMS != 1000 {
		t.Fatalf("interaction enrichment must not overwrite operation timing: %+v", request)
	}
}

func summaryServiceArtifact(
	t *testing.T,
	id string,
	typeName evidencevo.ArtifactType,
	requestID string,
	traceID string,
	interactionID string,
	text string,
) evidencevo.EvidenceArtifact {
	t.Helper()
	artifact, validationErrors := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{
		ArtifactID: id, ArtifactType: typeName, RequestID: requestID, TraceID: traceID,
		InteractionID: interactionID, ContentType: "application/json",
		SchemaVersion: evidencevo.ArtifactContractVersion, ObservedAt: "2026-08-02T09:00:00Z",
		Content: map[string]any{"text": text}, BusinessRefs: []string{"object:kn_demo:forecast"},
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
	})
	if len(validationErrors) != 0 {
		t.Fatalf("normalize summary artifact: %+v", validationErrors)
	}
	return artifact
}

func TestExactTraceExecutionLookupDoesNotCallListProjectionAndReturnsRequest(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_trace_exact", "trace_id_exact", "2026-07-26T09:00:00Z", "问题", "结果", "agent-exact", "bd_demo", "acct_demo")
	source := &capturingProjectionSource{}
	service := NewWithProjectionSource(store, source)

	page, err := service.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), TraceID: "trace_id_exact", Limit: 20,
	})

	if err != nil || len(page.Entries) != 1 || page.Entries[0].TraceID != "trace_id_exact" ||
		page.Entries[0].RequestID != "req_trace_exact" {
		t.Fatalf("exact trace lookup must reverse-return request: page=%+v err=%v", page, err)
	}
	if len(source.queries) != 0 {
		t.Fatalf("exact trace query must not scan list projection: %+v", source.queries)
	}
}

func TestListTraceExecutionsUsesCanonicalConversationAgentName(t *testing.T) {
	evidenceStore := evidencestore.New()
	seedBusinessProvenanceRequest(
		t, evidenceStore, "req_trace_agent_name", "trace_agent_name", "conversation_trace_agent_name", "interaction_trace_agent_name",
		"2026-08-10T09:00:00Z", "查询库存", "库存 1756", "acct_demo",
	)
	sessions := sessionstore.New()
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{
			ID: "conversation_trace_agent_name", AgentName: "供应链分析助手",
			Owner: sessionvo.Owner{
				TenantID: "tenant_demo", BusinessDomainID: "bd_demo",
				ApplicationPrincipalID: "266c6a42-6131-4d62-8f39-853e7093701c",
				EffectiveSubjectType:   sessionvo.SubjectUser, EffectiveSubjectID: "acct_demo",
			},
		})
		return nil
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	service := New(evidenceStore, WithProjectionSource(evidenceStore), WithSessionStore(sessions))

	page, err := service.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), TraceID: "trace_agent_name", Limit: 20,
	})

	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("list trace executions: page=%+v err=%v", page, err)
	}
	if page.Entries[0].AgentName != "供应链分析助手" {
		t.Fatalf("trace must use canonical conversation agent name: %+v", page.Entries[0])
	}
}

func TestListTraceExecutionsUsesOperationSourceModuleForRootService(t *testing.T) {
	evidenceStore := evidencestore.New()
	seedBusinessProvenanceRequest(
		t, evidenceStore, "req_trace_source_module", "trace_source_module", "conversation_trace_source_module", "interaction_trace_source_module",
		"2026-08-10T09:00:00Z", "查询库存", "库存 1756", "acct_demo",
	)
	sessions := sessionstore.New()
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{
			OperationID: "op_trace_source_module", Attempt: 1, TraceID: "trace_source_module",
			ToolName: "run_sql", SourceModule: "context-loader",
			StartedAt: time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC),
		})
		return nil
	}); err != nil {
		t.Fatalf("seed operation fact: %v", err)
	}
	service := New(evidenceStore, WithProjectionSource(evidenceStore), WithSessionStore(sessions))

	page, err := service.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), TraceID: "trace_source_module", Limit: 20,
	})

	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("list trace executions: page=%+v err=%v", page, err)
	}
	if page.Entries[0].RootService != "context-loader" {
		t.Fatalf("trace root service must use the operation source module: %+v", page.Entries[0])
	}
}

func TestExactRequestAndTracePropagateArtifactQueryTruncation(t *testing.T) {
	store := evidencestore.New()
	seedSummaryRequest(t, store, "req_artifact_cap", "trace_artifact_cap", "2026-07-26T09:00:00Z", "问题", "结果", "agent-a", "bd_demo", "acct_demo")
	for index := 0; index < MaxEvidenceQueryLimit-1; index++ {
		artifact, validationErrors := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{
			ArtifactID:   "zz_filler_" + time.Unix(int64(index), 0).UTC().Format("150405.000000000"),
			ArtifactType: evidencevo.ArtifactTypeDataResult,
			RequestID:    "req_artifact_cap", TraceID: "trace_artifact_cap",
			ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion,
			ObservedAt: "2026-07-26T09:00:00Z", Content: map[string]any{"row_count": index},
			TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		})
		if len(validationErrors) != 0 {
			t.Fatalf("normalize filler artifact %d: %+v", index, validationErrors)
		}
		if _, err := store.StoreArtifact(context.Background(), artifact); err != nil {
			t.Fatalf("store filler artifact %d: %v", index, err)
		}
	}
	service := New(store)

	request, found, err := service.GetRequestSummary(context.Background(), "req_artifact_cap", summaryScope("acct_demo"))
	if err != nil || !found || request.EvidenceCompleteness != "partial" ||
		!containsSummaryReason(request.PartialReasons, "artifact_query_truncated") {
		t.Fatalf("request must report artifact truncation: request=%+v found=%v err=%v", request, found, err)
	}

	requestTraces, err := service.ListRequestTraces(context.Background(), "req_artifact_cap", evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), Limit: 20,
	})
	if err != nil || !requestTraces.Truncated || !requestTraces.Partial ||
		!containsSummaryReason(requestTraces.PartialReasons, "artifact_query_truncated") {
		t.Fatalf("request traces must report artifact truncation: page=%+v err=%v", requestTraces, err)
	}

	traceExecutions, err := service.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{
		Scope: summaryScope("acct_demo"), TraceID: "trace_artifact_cap", Limit: 20,
	})
	if err != nil || !traceExecutions.Truncated || !traceExecutions.Partial ||
		!containsSummaryReason(traceExecutions.PartialReasons, "artifact_query_truncated") {
		t.Fatalf("trace execution must report artifact truncation: page=%+v err=%v", traceExecutions, err)
	}
}

func seedSummaryRequest(t *testing.T, store *evidencestore.Store, requestID, traceID, at, question, result, agent, domain, account string) {
	t.Helper()
	seedSummaryTrace(t, store, requestID, traceID, at, agent, domain, account)
	for _, item := range []struct {
		id           string
		artifactType evidencevo.ArtifactType
		text         string
	}{
		{"question_" + requestID, evidencevo.ArtifactTypeQuestion, question},
		{"result_" + requestID, evidencevo.ArtifactTypeResult, result},
	} {
		artifact, validationErrors := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{
			ArtifactID: item.id, ArtifactType: item.artifactType,
			RequestID: requestID, TraceID: traceID, ContentType: "application/json",
			SchemaVersion: evidencevo.ArtifactContractVersion, ObservedAt: at,
			Content: map[string]any{"text": item.text}, AgentOrApp: agent,
			TenantID: "tenant_demo", BusinessDomain: domain, AccountID: account, AccountType: "app",
		})
		if len(validationErrors) != 0 {
			t.Fatalf("normalize artifact: %+v", validationErrors)
		}
		if _, err := store.StoreArtifact(context.Background(), artifact); err != nil {
			t.Fatal(err)
		}
	}
}

func seedSummaryTrace(t *testing.T, store *evidencestore.Store, requestID, traceID, at, agent, domain, account string) {
	t.Helper()
	trace := evidencevo.NormalizedTrace{
		TraceID: traceID, RequestID: requestID,
		TenantID: "tenant_demo", BusinessDomain: domain, AccountID: account, AccountType: "app",
		SchemaVersion: evidencevo.ArtifactContractVersion,
		Events: []evidencevo.EvidenceEvent{
			{
				EventID: "question_event_" + traceID, EventType: "agent.interaction.started",
				SchemaVersion: evidencevo.ArtifactContractVersion,
				ObservedAt:    at, EmittedAt: at, Producer: "bkn-agent", TraceID: traceID,
				SpanID: "span_" + traceID, RequestID: requestID, OperationName: "agent.run",
				Payload: map[string]any{
					"agent_id": agent, "question_artifact_ref": "artifact:question_" + requestID,
				},
			},
			{
				EventID: "result_event_" + traceID, EventType: "claim.created",
				SchemaVersion: evidencevo.ArtifactContractVersion,
				ObservedAt:    at, EmittedAt: at, Producer: "bkn-agent", TraceID: traceID,
				SpanID: "span_" + traceID, RequestID: requestID, OperationName: "claim.create",
				Payload: map[string]any{
					"claim_id": "claim_" + traceID, "visibility": "visible",
					"result_artifact_ref": "artifact:result_" + requestID,
					"business_refs": []any{
						map[string]any{"ref_id": "object:kn_demo:item", "visibility": "visible"},
					},
				},
			},
		},
	}
	if err := store.StoreEvidence(context.Background(), evidencevo.WithEvents(trace, trace.Events)); err != nil {
		t.Fatal(err)
	}
}

func seedBusinessProvenanceRequest(
	t *testing.T,
	store *evidencestore.Store,
	requestID, traceID, conversationID, interactionID, at, question, result, account string,
) {
	seedBusinessProvenanceRequestWithAgent(
		t, store, requestID, traceID, conversationID, interactionID, at, question, result, account,
		"supply-chain-agent", false,
	)
}

func seedBusinessProvenanceRequestWithAgent(
	t *testing.T,
	store *evidencestore.Store,
	requestID, traceID, conversationID, interactionID, at, question, result, account, agent string,
	useAgentID bool,
) {
	t.Helper()
	identity := map[string]any{"app_ref": agent, "question_artifact_ref": "artifact:question_" + requestID}
	if useAgentID {
		delete(identity, "app_ref")
		identity["agent_id"] = agent
	}
	trace := evidencevo.NormalizedTrace{
		TraceID: traceID, RequestID: requestID, ConversationID: conversationID,
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: account, AccountType: "app",
		ApplicationPrincipalID: "openbkn-studio", EffectiveSubjectID: account,
		SchemaVersion: evidencevo.ArtifactContractVersion,
		Events: []evidencevo.EvidenceEvent{
			{
				EventID: "question_event_" + traceID, EventType: "agent.interaction.started",
				SchemaVersion: evidencevo.ArtifactContractVersion,
				ObservedAt:    at, EmittedAt: at, Producer: "third-party-agent", TraceID: traceID,
				SpanID: "span_" + traceID, RequestID: requestID, InteractionID: interactionID, OperationName: "agent.run",
				Payload: identity,
			},
			{
				EventID: "result_event_" + traceID, EventType: "claim.created",
				SchemaVersion: evidencevo.ArtifactContractVersion,
				ObservedAt:    at, EmittedAt: at, Producer: "third-party-agent", TraceID: traceID,
				SpanID: "span_" + traceID, RequestID: requestID, InteractionID: interactionID, OperationName: "claim.create",
				Payload: map[string]any{
					"claim_id": "claim_" + traceID, "visibility": "visible",
					"result_artifact_ref": "artifact:result_" + requestID,
					"business_refs":       []any{map[string]any{"ref_id": "object:kn_demo:item", "visibility": "visible"}},
				},
			},
		},
	}
	if err := store.StoreEvidence(context.Background(), evidencevo.WithEvents(trace, trace.Events)); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id           string
		artifactType evidencevo.ArtifactType
		text         string
	}{
		{"question_" + requestID, evidencevo.ArtifactTypeQuestion, question},
		{"result_" + requestID, evidencevo.ArtifactTypeResult, result},
	} {
		artifact, validationErrors := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{
			ArtifactID: item.id, ArtifactType: item.artifactType,
			RequestID: requestID, TraceID: traceID, InteractionID: interactionID,
			ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion, ObservedAt: at,
			Content: map[string]any{"text": item.text}, AgentOrApp: agent,
			TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: account, AccountType: "app",
		})
		if len(validationErrors) != 0 {
			t.Fatalf("normalize artifact: %+v", validationErrors)
		}
		if _, err := store.StoreArtifact(context.Background(), artifact); err != nil {
			t.Fatal(err)
		}
	}
}

func seedTwoPointOneSummaryTrace(t *testing.T, store *evidencestore.Store, requestID, traceID, at, domain, account string) {
	t.Helper()
	trace := evidencevo.NormalizedTrace{
		TraceID: traceID, RequestID: requestID,
		TenantID: "tenant_demo", BusinessDomain: domain, AccountID: account, AccountType: "app",
		SchemaVersion: evidencevo.ContractVersion,
		Events: []evidencevo.EvidenceEvent{{
			EventID: "event_" + traceID, EventType: "claim.created",
			SchemaVersion: evidencevo.ContractVersion,
			ObservedAt:    at, EmittedAt: at, Producer: "bkn-agent", TraceID: traceID,
			SpanID: "span_" + traceID, RequestID: requestID, OperationName: "claim.create",
			Payload: map[string]any{"claim_id": "claim_" + traceID, "visibility": "visible"},
		}},
	}
	if err := store.StoreEvidence(context.Background(), evidencevo.WithEvents(trace, trace.Events)); err != nil {
		t.Fatal(err)
	}
}

func summaryScope(account string) evidencevo.QueryScope {
	return evidencevo.QueryScope{
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: account, AccountType: "app",
	}
}

func containsSummaryReason(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
