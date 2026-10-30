// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchcoreprojection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestErrorKeywordScreensBeforeReceiptAndArtifactExpansion(t *testing.T) {
	for _, keyword := range []string{"absent", "private-marker", "NEEDLE 错误"} {
		t.Run(keyword, func(t *testing.T) {
			f := &scanFixture{}
			for i := 0; i < 2101; i++ {
				r := scanReceipt(fmt.Sprintf("r-%04d", i), fmt.Sprintf("trace-%04d", i), fmt.Sprintf("int-%04d", i), "owner", "tool", "2026-10-29T00:00:00Z")
				r.ConversationID, r.Status = "conv", "completed"
				if i == 2100 {
					r.Status, r.IssuedAt = "error", "2020-01-01T00:00:00Z"
				}
				f.seeds = append(f.seeds, r)
				f.receipts = append(f.receipts, r)
			}
			last := f.receipts[len(f.receipts)-1]
			// A nonmatching sibling still participates in authorization, but
			// must not be enqueued for its own Trace expansion after filtering.
			f.seeds[2099].InteractionID, f.receipts[2099].InteractionID = last.InteractionID, last.InteractionID
			foreign := scanReceipt("foreign", last.TraceID, "private", "other-owner", "private-tool", last.IssuedAt)
			foreign.ConversationID = "private"
			f.seeds, f.receipts = append(f.seeds, foreign), append(f.receipts, foreign)
			sessions := sessionstore.New()
			err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
				tx.SaveOperationCallFact(sessionvo.OperationCallFact{TraceID: last.TraceID, ConversationID: last.ConversationID, InteractionID: last.InteractionID, OperationID: "failed", Attempt: 1, Status: sessionvo.AttemptFailed, StartedAt: time.Unix(1, 0), Error: &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`"Unicode needle 错误"`)}})
				tx.SaveOperationCallFact(sessionvo.OperationCallFact{TraceID: last.TraceID, ConversationID: "private", InteractionID: "private", OperationID: "private", Attempt: 1, Status: sessionvo.AttemptFailed, StartedAt: time.Unix(0, 0), Error: &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`"private-marker"`)}})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			source := scanSource(t, f)
			artifacts := &scanArtifactSource{}
			source.artifacts = artifacts
			svc := evidencesvc.New(evidencestore.New(), evidencesvc.WithProjectionSource(source), evidencesvc.WithSessionStore(sessions))
			page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}, ErrorKeyword: keyword, Limit: 20})
			want, expansions := 0, 0
			if keyword == "NEEDLE 错误" {
				want, expansions = 1, 2 // selected Trace receipts + its complete Interaction
			}
			if err != nil || page.Total != want || len(page.Entries) != want || page.Truncated || page.Partial || f.receiptQueries != expansions || len(artifacts.artifactProjectionQueries) != want {
				t.Fatalf("page=%+v err=%v receipt_expansions=%d artifact_reads=%d", page, err, f.receiptQueries, len(artifacts.artifactProjectionQueries))
			}
			if f.seedPages <= 1 || f.closed != 1 {
				t.Fatalf("candidate traversal/cleanup lost: pages=%d closed=%d", f.seedPages, f.closed)
			}
			if want == 1 && page.Entries[0].TraceID != last.TraceID {
				t.Fatalf("historical match lost: %+v", page)
			}
			fields := fmt.Sprint(f.seedBody["_source"])
			if !strings.Contains(fields, "owner") || !strings.Contains(fields, "conversation_id") || strings.Contains(fields, "content") {
				t.Fatalf("candidate scan is not a scoped lightweight projection: %s", fields)
			}
			t.Logf("candidates=%d seed_pages=%d receipt_expansions=%d artifact_reads=%d matches=%d", len(f.seeds), f.seedPages, f.receiptQueries, len(artifacts.artifactProjectionQueries), page.Total)
		})
	}
}

func TestErrorCandidateFilterCannotPruneSharedInteractionSiblings(t *testing.T) {
	a := scanReceipt("a", "a", "i", "foreign", "a", "2026-10-29T00:00:00Z")
	a.KnowledgeNetworkIDs = []string{"managed"}
	sibling := scanReceipt("b", "b", "i", "foreign", "b", a.IssuedAt)
	f := &scanFixture{seeds: []receiptDocument{a}, receipts: []receiptDocument{a, sibling}}
	source := scanSource(t, f)
	called := false
	query := iprojectionsource.Query{Scope: evidencevo.QueryScope{AccessProfile: &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "owner", Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"managed"}}}, FilterTraceCandidates: func(context.Context, []iprojectionsource.TraceSummaryCandidate) ([]string, error) {
		called = true
		return nil, errors.New("shared filter must not be called")
	}}
	seen := map[string]bool{}
	err := source.ScanTraceSummaryProjection(context.Background(), query, func(result iprojectionsource.Result) error {
		for _, trace := range result.Traces {
			seen[trace.TraceID] = true
		}
		return nil
	})
	if err != nil || called || !seen["a"] || !seen["b"] || f.closed != 1 {
		t.Fatalf("shared sibling lost: seen=%v filter=%v err=%v", seen, called, err)
	}
}

func TestErrorCandidateFilterFailureClosesSnapshotWithoutVisiting(t *testing.T) {
	r := scanReceipt("r", "t", "i", "owner", "tool", "2026-10-29T00:00:00Z")
	f := &scanFixture{seeds: []receiptDocument{r}, receipts: []receiptDocument{r}}
	source := scanSource(t, f)
	failure := errors.New("failure metadata unavailable")
	visited := false
	err := source.ScanTraceSummaryProjection(context.Background(), iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}, FilterTraceCandidates: func(context.Context, []iprojectionsource.TraceSummaryCandidate) ([]string, error) {
		return nil, failure
	}}, func(iprojectionsource.Result) error { visited = true; return nil })
	if !errors.Is(err, failure) || visited || f.closed != 1 || f.receiptQueries != 0 {
		t.Fatalf("partial result/cleanup: visited=%v closed=%d expansions=%d err=%v", visited, f.closed, f.receiptQueries, err)
	}
}

func TestErrorKeywordScreeningKeepsExactTotalAndCursorTraversal(t *testing.T) {
	f := &scanFixture{}
	store := sessionstore.New()
	err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		for i := 0; i < 4; i++ {
			id := fmt.Sprint(i)
			r := scanReceipt("r"+id, "t"+id, "i"+id, "owner", "root", "2026-10-29T00:00:00Z")
			r.ConversationID, r.Status = "conv", "error"
			f.seeds, f.receipts = append(f.seeds, r), append(f.receipts, r)
			message := `"matching needle"`
			if i == 1 {
				message = `"different error"`
			}
			tx.SaveOperationCallFact(sessionvo.OperationCallFact{TraceID: r.TraceID, ConversationID: "conv", InteractionID: r.InteractionID, OperationID: "op" + id, Attempt: 1, Status: sessionvo.AttemptFailed, StartedAt: time.Unix(1, 0), Error: &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(message)}})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := evidencesvc.New(evidencestore.New(), evidencesvc.WithProjectionSource(scanSource(t, f)), evidencesvc.WithSessionStore(store))
	opts := evidencevo.SummaryQueryOptions{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}, ErrorKeyword: "NEEDLE", Limit: 1}
	seen := map[string]bool{}
	for pageNumber := 0; ; pageNumber++ {
		page, err := svc.ListTraceExecutions(context.Background(), opts)
		if err != nil || page.Total != 3 || len(page.Entries) != 1 || page.Partial || page.Truncated || seen[page.Entries[0].TraceID] || pageNumber >= 3 {
			t.Fatalf("inconsistent screened page: %+v err=%v seen=%v", page, err, seen)
		}
		seen[page.Entries[0].TraceID] = true
		if page.NextCursor == nil {
			break
		}
		opts.Cursor = *page.NextCursor
	}
	if len(seen) != 3 || seen["t1"] || !seen["t0"] || !seen["t3"] {
		t.Fatalf("matching boundary or exclusion lost: %v", seen)
	}
}
