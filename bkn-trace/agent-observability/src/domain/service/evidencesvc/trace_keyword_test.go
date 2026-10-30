// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

func TestTraceKeywordFindsOldCandidateAcrossAllExistingFields(t *testing.T) {
	traces := remainingFilterTraces(MaxSummaryScanEntries + 1)
	last := &traces[len(traces)-1]
	last.TraceID, last.RequestID, last.ConversationID = "old-trace", "old-request", "old-conversation"
	last.ApplicationPrincipalID = "old-agent"
	event := &last.Events[0]
	event.TraceID, event.RequestID, event.InteractionID = last.TraceID, last.RequestID, "old-interaction"
	event.ObservedAt, event.EmittedAt = "2020-01-01T00:00:00Z", "2020-01-01T00:00:00Z"
	event.OperationName, event.Producer = "old-tool", "old-service"
	event.Payload["agent_id"] = "old-agent"
	event.Payload["status"], event.Payload["safe_error_summary"] = "failed", "old-error 错误"
	for _, keyword := range []string{"OLD-TRACE", "old-request", "old-conversation", "old-interaction", "old-agent", "old-service", "OLD-TOOL", "OLD-ERROR 错误", "  d-trac  ", "absent"} {
		t.Run(keyword, func(t *testing.T) {
			source := &remainingFilterSource{traces: traces}
			svc := New(evidencestore.New(), WithProjectionSource(source))
			page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Keyword: keyword, Limit: 1})
			want := 1
			if keyword == "absent" {
				want = 0
			}
			if err != nil || page.Total != want || len(page.Entries) != want || page.Partial || page.Truncated || page.NextCursor != nil || page.Page != 1 || page.PageSize != 1 {
				t.Fatalf("old candidate/empty result: page=%+v err=%v", page, err)
			}
			if want == 1 && page.Entries[0].TraceID != last.TraceID {
				t.Fatalf("wrong match: %+v", page.Entries)
			}
			if source.scans != 1 || len(source.queries) != 1 || !source.queries[0].From.IsZero() || !source.queries[0].To.IsZero() || !source.queries[0].SummaryOnly || source.queries[0].OmitSummaryTerminalContent {
				t.Fatalf("keyword must preserve summary previews and omit implicit time bounds: scans=%d queries=%+v", source.scans, source.queries)
			}
		})
	}
}

func TestTraceKeywordCountOffsetAndCursorReachBothEnds(t *testing.T) {
	source := &remainingFilterSource{traces: remainingFilterTraces(MaxSummaryScanEntries + 7)}
	svc := New(evidencestore.New(), WithProjectionSource(source))
	opts := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Keyword: "TRACE-", Limit: 200}
	seen := map[string]bool{}
	var first, last string
	for {
		page, err := svc.ListTraceExecutions(context.Background(), opts)
		if err != nil || page.Total != len(source.traces) || page.Partial || page.Truncated {
			t.Fatalf("incomplete keyword page: %+v err=%v", page, err)
		}
		for _, trace := range page.Entries {
			if seen[trace.TraceID] {
				t.Fatalf("duplicate trace %s", trace.TraceID)
			}
			seen[trace.TraceID] = true
			if first == "" {
				first = trace.TraceID
			}
			last = trace.TraceID
		}
		if page.NextCursor == nil {
			break
		}
		opts.Cursor = *page.NextCursor
		if len(seen) >= len(source.traces) {
			t.Fatal("cursor must terminate after the last match")
		}
	}
	if len(seen) != len(source.traces) || first != source.traces[0].TraceID || last != source.traces[len(source.traces)-1].TraceID {
		t.Fatalf("missed boundaries: count=%d first=%s last=%s", len(seen), first, last)
	}
	opts.Cursor, opts.Page = "", 11
	page, err := svc.ListTraceExecutions(context.Background(), opts)
	if err != nil || page.Total != len(source.traces) || len(page.Entries) != 7 || page.Entries[0].TraceID != source.traces[2000].TraceID || page.NextCursor != nil {
		t.Fatalf("offset beyond old window: %+v err=%v", page, err)
	}
	opts.Page = math.MaxInt
	if _, err := svc.ListTraceExecutions(context.Background(), opts); !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) {
		t.Fatalf("unbounded retained page: %v", err)
	}
}

func TestTraceKeywordCursorBindsKeywordAndAuthorization(t *testing.T) {
	source := &remainingFilterSource{traces: remainingFilterTraces(3)}
	svc := New(evidencestore.New(), WithProjectionSource(source))
	opts := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Keyword: "trace-", Limit: 1}
	opts.Scope.AccessProfile = &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "acct_demo", Fingerprint: "grants-v1"}
	page, err := svc.ListTraceExecutions(context.Background(), opts)
	if err != nil || page.NextCursor == nil {
		t.Fatalf("missing keyword cursor: %+v err=%v", page, err)
	}
	opts.Cursor = *page.NextCursor
	for _, change := range []string{"keyword", "time", "permission"} {
		t.Run(change, func(t *testing.T) {
			changed := opts
			profile := *opts.Scope.AccessProfile
			changed.Scope.AccessProfile = &profile
			switch change {
			case "keyword":
				changed.Keyword = "other"
			case "time":
				changed.From = time.Unix(1, 0)
			case "permission":
				profile.Fingerprint = "grants-v2"
			}
			if _, err := svc.ListTraceExecutions(context.Background(), changed); !errors.Is(err, ErrSummaryCursorInvalid) {
				t.Fatalf("cursor survived %s change: %v", change, err)
			}
		})
	}
}

func TestTraceKeywordScanFailureNeverReturnsPartialCount(t *testing.T) {
	for _, failure := range []error{iprojectionsource.ErrSummaryScanBudgetExceeded, context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			source := &remainingFilterSource{traces: remainingFilterTraces(100), err: failure}
			svc := New(evidencestore.New(), WithProjectionSource(source))
			page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Keyword: "trace-", Limit: 1})
			if !errors.Is(err, failure) || len(page.Entries) != 0 || page.Total != 0 || page.NextCursor != nil || source.scans != 1 {
				t.Fatalf("failure certified partial results: %+v err=%v scans=%d", page, err, source.scans)
			}
		})
	}
}

func TestTraceKeywordCompatibilityTruncationIsBudgetError(t *testing.T) {
	for _, keyword := range []string{"trace-", "absent"} {
		for _, truncated := range []bool{false, true} {
			source := &capturingProjectionSource{result: iprojectionsource.Result{Traces: remainingFilterTraces(1), Truncated: truncated}}
			svc := New(evidencestore.New(), WithProjectionSource(source))
			page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Keyword: keyword, Limit: 1})
			if truncated {
				if !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) || page.Total != 0 || len(page.Entries) != 0 {
					t.Fatalf("compatibility window reported success: keyword=%s page=%+v err=%v", keyword, page, err)
				}
			} else if err != nil || page.Truncated || page.Partial {
				t.Fatalf("complete compatibility result rejected: %+v err=%v", page, err)
			}
		}
	}
	// A combined filter outside the scanner's supported set must not silently
	// restore the old window-local count, even if the source supports scanning.
	source := &remainingFilterSource{traces: remainingFilterTraces(MaxSummaryScanEntries + 1)}
	svc := New(evidencestore.New(), WithProjectionSource(source))
	page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Keyword: "absent", AgentOrApp: "agent-demo", Limit: 1})
	if !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) || page.Total != 0 || len(page.Entries) != 0 || source.scans != 0 {
		t.Fatalf("combined filter certified a partial count: %+v err=%v scans=%d", page, err, source.scans)
	}
}
