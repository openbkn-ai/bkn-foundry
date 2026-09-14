// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/extension/enterpriseroute"
)

func TestEnterpriseInteractionReaderUsesTrustedTechnicalScope(t *testing.T) {
	scope := evidencevo.QueryScope{
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
	if facts.SourceRead.Authorization != "" || facts.SourceRead.AccountID != "user-1" {
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

func TestEnterpriseInteractionReaderReturnsFullTextOnlyForReferencedAuthorizedArtifact(t *testing.T) {
	scope := evidencevo.QueryScope{AccountID: "user-1", AccountType: "user"}
	fullResult := "完整结果：这是一段超过摘要预览上限的原文。"
	for i := 0; i < 40; i++ {
		fullResult += "保真"
	}
	reader := NewEnterpriseInteractionFactsReader(
		fakeInteractionSummarySource{
			summary: evidencevo.InteractionSummary{
				InteractionID: "int-1", QuestionArtifactRef: "artifact:question-1", ResultArtifactRef: "artifact:result-1",
			},
			found: true,
			artifacts: map[string]evidencevo.EvidenceArtifact{
				"result-1": {ArtifactID: "result-1", ArtifactType: evidencevo.ArtifactTypeResult, Content: map[string]any{"answer": fullResult}},
				"other-1":  {ArtifactID: "other-1", ArtifactType: evidencevo.ArtifactTypeResult, Content: "unrelated"},
			},
		},
		fakeInteractionOperationSource{},
	)
	artifactReader, ok := reader.(interface {
		ReadInteractionArtifact(context.Context, string, string) (string, bool, error)
	})
	if !ok {
		t.Fatal("enterprise reader must expose an authorized interaction artifact reader")
	}
	ctx := context.WithValue(context.Background(), trustedQueryScopeContextKey{}, scope)
	text, found, err := artifactReader.ReadInteractionArtifact(ctx, "int-1", "artifact:result-1")
	if err != nil || !found || text != fullResult {
		t.Fatalf("ReadInteractionArtifact() = (%q, %v, %v), want full authorized result", text, found, err)
	}
	if _, found, err := artifactReader.ReadInteractionArtifact(ctx, "int-1", "artifact:other-1"); err != nil || found {
		t.Fatalf("unreferenced artifact must stay unavailable: found=%v err=%v", found, err)
	}
}

func TestEnterpriseInteractionReaderListsOnlyTrustedTechnicalScope(t *testing.T) {
	scope := evidencevo.QueryScope{
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
	artifacts     map[string]evidencevo.EvidenceArtifact
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

func (s fakeInteractionSummarySource) GetArtifact(_ context.Context, artifactID string, _ evidencevo.QueryScope) (evidencevo.EvidenceArtifact, bool, error) {
	artifact, found := s.artifacts[artifactID]
	return artifact, found, s.err
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

func TestEnterpriseCaptureRequiresAuthorizedInteraction(t *testing.T) {
	for _, found := range []bool{false, true} {
		calls := 0
		reader := NewEnterpriseInteractionFactsReader(fakeInteractionSummarySource{found: found}, fakeInteractionOperationSource{}, func(context.Context, string, evidencevo.QueryScope) (json.RawMessage, string, bool, error) {
			calls++
			return json.RawMessage(`{}`), "hash", true, nil
		})
		capture := reader.(interface {
			ReadExplanationCapture(context.Context, string) (json.RawMessage, string, bool, error)
		})
		if _, _, ok, err := capture.ReadExplanationCapture(context.Background(), "i"); err != nil || ok || calls != 0 {
			t.Fatal("untrusted read reached capture")
		}
		ctx := context.WithValue(context.Background(), trustedQueryScopeContextKey{}, evidencevo.QueryScope{AccountID: "u", AccountType: "user"})
		_, _, ok, err := capture.ReadExplanationCapture(ctx, "i")
		if err != nil || ok != found || (calls == 1) != found {
			t.Fatal("scope bypass")
		}
	}
}
