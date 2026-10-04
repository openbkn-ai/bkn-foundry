// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func conversationPreviewFixture(t *testing.T) (*Service, *capturingProjectionSource) {
	t.Helper()
	sessions := sessionstore.New()
	at := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)
	traces := []evidencevo.NormalizedTrace{}
	questions := []evidencevo.EvidenceArtifact{}
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		for _, id := range []string{"a", "b"} {
			tx.SaveConversation(sessionvo.Conversation{ID: "conv-" + id, AgentName: "business-agent", CreatedAt: at, UpdatedAt: at})
			tx.SaveInteraction(sessionvo.Interaction{ID: "first-" + id, ConversationID: "conv-" + id, Ordinal: 1, CreatedAt: at, UpdatedAt: at})
			tx.SaveInteraction(sessionvo.Interaction{ID: "later-" + id, ConversationID: "conv-" + id, Ordinal: 2, CreatedAt: at.Add(time.Minute), UpdatedAt: at})
			trace := pageSummaryTrace("trace-"+id, "req-"+id, at.Format(time.RFC3339), "acct_demo", "")
			trace.ConversationID = "conv-" + id
			trace.Events[0].InteractionID = "later-" + id
			traces = append(traces, trace)
			questions = append(questions, summaryServiceArtifact(t, "question-"+id, evidencevo.ArtifactTypeQuestion, "start-"+id, "", "first-"+id, "有哪些白酒品牌"))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	source := &capturingProjectionSource{
		resultFor: func(q iprojectionsource.Query) iprojectionsource.Result {
			selected := []evidencevo.NormalizedTrace{}
			for _, trace := range traces {
				if len(q.ConversationIDs) == 0 || containsSummaryID(q.ConversationIDs, trace.ConversationID) {
					selected = append(selected, trace)
				}
			}
			// Thousands of unrelated operation artifacts crowd out terminal content in
			// the old broad scan. A terminal-only read has no such truncation.
			return iprojectionsource.Result{Traces: selected, Truncated: len(q.ArtifactTypes) == 0}
		},
		artifactResultFor: func(q iprojectionsource.Query) iprojectionsource.ArtifactResult {
			selected := []evidencevo.EvidenceArtifact{}
			for _, question := range questions {
				if containsSummaryID(q.InteractionIDs, question.InteractionID) {
					selected = append(selected, question)
				}
			}
			return iprojectionsource.ArtifactResult{Artifacts: selected}
		},
	}
	return New(evidencestore.New(), WithProjectionSource(source), WithSessionStore(sessions)), source
}

func TestConversationKeywordUsesFirstTerminalPreviewBeforePaging(t *testing.T) {
	for _, size := range []int{1, 20} {
		service, source := conversationPreviewFixture(t)
		page, err := service.ListConversations(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Keyword: "白酒", Limit: size})
		want := size
		if want > 2 {
			want = 2
		}
		if err != nil || page.Total != 2 || len(page.Entries) != want || page.Partial {
			t.Fatalf("size=%d page=%+v err=%v", size, page, err)
		}
		if page.Entries[0].ConversationID != "conv-a" || page.Entries[0].QuestionPreview != "有哪些白酒品牌" {
			t.Fatalf("preview/order=%+v", page.Entries)
		}
		if len(source.artifactProjectionQueries) != 1 {
			t.Fatalf("preview read must be batched: %+v", source.artifactProjectionQueries)
		}
		q := source.artifactProjectionQueries[0]
		if len(q.InteractionIDs) != 2 || containsSummaryID(q.InteractionIDs, "later-a") || !containsArtifactType(q.ArtifactTypes, evidencevo.ArtifactTypeQuestion) || q.Limit > MaxSummaryScanEntries {
			t.Fatalf("preview scope=%+v", q)
		}
	}
}

func TestConversationIDBoundsProjectionAndLoadsItsFirstPreview(t *testing.T) {
	service, source := conversationPreviewFixture(t)
	page, err := service.ListConversations(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), ConversationID: "conv-b", ExcludeAgentOrApp: "optimizer", Limit: 1})
	if err != nil || page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].QuestionPreview != "有哪些白酒品牌" || page.Partial {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if len(source.queries) != 1 || len(source.queries[0].ConversationIDs) != 1 || source.queries[0].ConversationIDs[0] != "conv-b" {
		t.Fatalf("exact selector not pushed: %+v", source.queries)
	}
}

func TestConversationFirstPreviewFailureDoesNotBecomeNoResults(t *testing.T) {
	service, source := conversationPreviewFixture(t)
	source.artifactErrFor = func(iprojectionsource.Query) error { return errors.New("read unavailable") }
	_, err := service.ListConversations(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Keyword: "白酒", Limit: 20})
	if err == nil {
		t.Fatal("preview read error must not become an empty search result")
	}
}

func TestConversationFirstPreviewNeverBorrowsLaterRound(t *testing.T) {
	service, source := conversationPreviewFixture(t)
	source.artifactResultFor = func(q iprojectionsource.Query) iprojectionsource.ArtifactResult {
		return iprojectionsource.ArtifactResult{Artifacts: []evidencevo.EvidenceArtifact{summaryServiceArtifact(t, "later", evidencevo.ArtifactTypeQuestion, "start-later", "", "later-a", "后续问题")}}
	}
	page, err := service.ListConversations(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), ConversationID: "conv-a", Limit: 20})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].QuestionPreview != "" {
		t.Fatalf("must not use later question: page=%+v err=%v", page, err)
	}
}

func TestConversationKeywordPagingAndEmptyKeywordRemainStable(t *testing.T) {
	service, _ := conversationPreviewFixture(t)
	ctx := context.Background()
	base := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Limit: 1}
	unfiltered, err := service.ListConversations(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	base.Keyword = "  "
	empty, err := service.ListConversations(ctx, base)
	if err != nil || empty.Total != unfiltered.Total || empty.Entries[0].ConversationID != unfiltered.Entries[0].ConversationID {
		t.Fatalf("empty keyword changed result: %+v %+v %v", unfiltered, empty, err)
	}
	base.Keyword = "白酒"
	base.Page = 2
	second, err := service.ListConversations(ctx, base)
	if err != nil || second.Total != 2 || len(second.Entries) != 1 || second.Entries[0].ConversationID != "conv-b" {
		t.Fatalf("second page=%+v err=%v", second, err)
	}
}

func TestConversationPreviewTruncationRemainsExplicit(t *testing.T) {
	service, source := conversationPreviewFixture(t)
	source.artifactResultFor = func(iprojectionsource.Query) iprojectionsource.ArtifactResult {
		return iprojectionsource.ArtifactResult{Truncated: true}
	}
	page, err := service.ListConversations(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), ConversationID: "conv-a", Limit: 20})
	if err != nil || len(page.Entries) != 1 || !page.Partial || !page.Truncated || !containsSummaryValue(page.PartialReasons, "artifact_projection_scan_cap_reached") || page.Entries[0].QuestionPreview != "" {
		t.Fatalf("truncation must not become absence: %+v err=%v", page, err)
	}
}

func TestConversationKeywordMatchesTerminalResultAndRecordedIDs(t *testing.T) {
	for _, keyword := range []string{"库存42", "req-a", "later-a"} {
		service, source := conversationPreviewFixture(t)
		original := source.artifactResultFor
		source.artifactResultFor = func(q iprojectionsource.Query) iprojectionsource.ArtifactResult {
			result := original(q)
			if containsSummaryID(q.InteractionIDs, "first-a") {
				result.Artifacts = append(result.Artifacts, summaryServiceArtifact(t, "result-a", evidencevo.ArtifactTypeResult, "finish-a", "", "first-a", "库存42"))
			}
			return result
		}
		page, err := service.ListConversations(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Keyword: keyword, Limit: 20})
		if err != nil || len(page.Entries) != 1 || page.Entries[0].ConversationID != "conv-a" || page.Entries[0].QuestionPreview != "有哪些白酒品牌" {
			t.Fatalf("keyword=%q page=%+v err=%v", keyword, page, err)
		}
	}
}

func TestConversationKeywordRetainsRecordedBusinessReferenceAndError(t *testing.T) {
	for _, keyword := range []string{"object:kn:item", "safe failure"} {
		entry := evidencevo.ConversationSummary{ConversationID: "conv-a", QuestionPreview: "首轮问题"}
		requests := []evidencevo.RequestSummary{{ConversationID: "conv-a", BusinessRefs: []string{"object:kn:item"}, ErrorSummary: "safe failure"}}
		if !matchesConversationKeyword(entry, requests, keyword) {
			t.Fatalf("recorded keyword %q disappeared", keyword)
		}
	}
}
