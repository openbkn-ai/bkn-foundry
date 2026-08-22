// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/extension/enterpriseroute"
)

func TestEnterpriseInteractionReaderUsesTrustedTechnicalScope(t *testing.T) {
	scope := evidencevo.QueryScope{
		TenantID: "tenant-1", BusinessDomain: "domain-1",
		AccountID: "user-1", AccountType: "user", View: evidencevo.AccessViewTechnical,
		AccessProfile: &evidencevo.AccessProfile{
			ApplicationPrincipalID: "openbkn-studio", EffectiveSubjectID: "user-1", DelegationID: "delegation-1",
		},
	}
	reader := NewEnterpriseInteractionFactsReader(
		fakeInteractionSummarySource{summary: evidencevo.InteractionSummary{
			InteractionID: "int-1", InteractionQuestion: "完整问题", InteractionResult: "完整结果",
		}, found: true},
		fakeInteractionOperationSource{entries: []sessionvo.OperationExecution{{
			Fact: sessionvo.OperationCallFact{OperationID: "op-1", InteractionID: "int-1"},
		}}, expectedScope: scope},
	)

	ctx := context.WithValue(context.Background(), trustedQueryScopeContextKey{}, scope)
	facts, found, err := reader.ReadInteraction(ctx, "int-1")
	if err != nil || !found {
		t.Fatalf("ReadInteraction() found=%v err=%v, want authorized facts", found, err)
	}
	if facts.Summary.InteractionID != "int-1" || len(facts.Operations) != 1 || facts.Operations[0].Fact.OperationID != "op-1" {
		t.Fatalf("ReadInteraction() = %+v, want summary and one operation", facts)
	}
	if facts.SourceRead.Authorization != "" || facts.SourceRead.AccountID != "user-1" || facts.SourceRead.BusinessDomain != "domain-1" {
		t.Fatalf("ReadInteraction() source read context = %+v, want current caller context without response serialization", facts.SourceRead)
	}
	if facts.SourceRead.ApplicationPrincipalID != "openbkn-studio" || facts.SourceRead.EffectiveSubjectType != "user" ||
		facts.SourceRead.EffectiveSubjectID != "user-1" || facts.SourceRead.DelegationID != "delegation-1" {
		t.Fatalf("ReadInteraction() trusted owner context = %+v", facts.SourceRead)
	}
	if facts.Summary.InteractionQuestion != "完整问题" || facts.Summary.InteractionResult != "完整结果" {
		t.Fatalf("ReadInteraction() lost complete interaction text: %+v", facts.Summary)
	}
}

func TestEnterpriseInteractionReaderListsOnlyTrustedTechnicalScope(t *testing.T) {
	scope := evidencevo.QueryScope{
		TenantID: "tenant-1", BusinessDomain: "domain-1",
		AccountID: "user-1", AccountType: "user", View: evidencevo.AccessViewTechnical,
	}
	reader := NewEnterpriseInteractionFactsReader(
		fakeInteractionSummarySource{
			conversations: evidencevo.ConversationSummaryPage{Entries: []evidencevo.ConversationSummary{{ConversationID: "conv-1"}}},
			interactions:  evidencevo.InteractionSummaryPage{Entries: []evidencevo.InteractionListSummary{{InteractionID: "int-1", ConversationID: "conv-1"}}},
			expectedScope: scope,
		},
		fakeInteractionOperationSource{},
	)
	ctx := context.WithValue(context.Background(), trustedQueryScopeContextKey{}, scope)
	query := enterpriseroute.ListQuery{Page: 2, PageSize: 20, Keyword: "采购订单"}
	conversations, err := reader.ListConversations(ctx, query)
	if err != nil || len(conversations.Entries) != 1 || conversations.Entries[0].ConversationID != "conv-1" {
		t.Fatalf("ListConversations() = %+v, %v", conversations, err)
	}
	interactions, err := reader.ListInteractions(ctx, query)
	if err != nil || len(interactions.Entries) != 1 || interactions.Entries[0].InteractionID != "int-1" {
		t.Fatalf("ListInteractions() = %+v, %v", interactions, err)
	}
}

type fakeInteractionSummarySource struct {
	summary       evidencevo.InteractionSummary
	found         bool
	err           error
	conversations evidencevo.ConversationSummaryPage
	interactions  evidencevo.InteractionSummaryPage
	expectedScope evidencevo.QueryScope
}

func (s fakeInteractionSummarySource) GetInteractionSummary(_ context.Context, _ string, _ evidencevo.QueryScope) (evidencevo.InteractionSummary, bool, error) {
	return s.summary, s.found, s.err
}

func (s fakeInteractionSummarySource) ListConversations(_ context.Context, scope evidencevo.SummaryQueryOptions) (evidencevo.ConversationSummaryPage, error) {
	if s.expectedScope != (evidencevo.QueryScope{}) && scope.Scope != s.expectedScope {
		return evidencevo.ConversationSummaryPage{}, context.Canceled
	}
	return s.conversations, s.err
}

func (s fakeInteractionSummarySource) ListInteractions(_ context.Context, scope evidencevo.SummaryQueryOptions) (evidencevo.InteractionSummaryPage, error) {
	if s.expectedScope != (evidencevo.QueryScope{}) && scope.Scope != s.expectedScope {
		return evidencevo.InteractionSummaryPage{}, context.Canceled
	}
	return s.interactions, s.err
}

type fakeInteractionOperationSource struct {
	entries       []sessionvo.OperationExecution
	expectedScope evidencevo.QueryScope
	err           error
}

func (s fakeInteractionOperationSource) ListOperationExecutionsByInteractionIDScoped(_ context.Context, scope evidencevo.QueryScope, _ string) ([]sessionvo.OperationExecution, error) {
	if scope != s.expectedScope {
		return nil, context.Canceled
	}
	return s.entries, s.err
}
