// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

type remainingFilterSource struct {
	traces  []evidencevo.NormalizedTrace
	queries []iprojectionsource.Query
	scans   int
	err     error
}

func (s *remainingFilterSource) LoadExecutionProjection(_ context.Context, q iprojectionsource.Query) (iprojectionsource.Result, error) {
	s.queries = append(s.queries, q)
	n := min(len(s.traces), q.Limit)
	return iprojectionsource.Result{Traces: s.traces[:n], Truncated: n < len(s.traces)}, nil
}
func (s *remainingFilterSource) ScanTraceSummaryProjection(ctx context.Context, q iprojectionsource.Query, visit func(iprojectionsource.Result) error) error {
	s.scans++
	s.queries = append(s.queries, q)
	for i := 0; i < len(s.traces); i += 64 {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch := s.traces[i:min(i+64, len(s.traces))]
		roots := map[string]string{}
		for _, trace := range batch {
			roots[trace.TraceID] = trace.Events[0].OperationName
		}
		if err := visit(iprojectionsource.Result{Traces: batch, CanonicalRoots: roots}); err != nil {
			return err
		}
		if s.err != nil {
			return s.err
		}
	}
	return nil
}
func remainingFilterTraces(n int) []evidencevo.NormalizedTrace {
	traces := make([]evidencevo.NormalizedTrace, n)
	for i := range traces {
		traces[i] = pageSummaryTrace(fmt.Sprintf("trace-%05d", i), fmt.Sprintf("req-%05d", i), "2026-10-24T00:00:00Z", "acct_demo", "bd_demo")
		traces[i].Events[0].EventType = "retrieval.completed"
		traces[i].Events[0].OperationName = "ordinary"
	}
	return traces
}

// This public-service repro also compiles against the accepted Tool-only
// baseline: the optional scanner is then ignored and all three cases fail.
func TestRemainingFiltersReachBeyondGlobalCandidateCap(t *testing.T) {
	for _, name := range []string{"status", "error_keyword", "shared_tool"} {
		t.Run(name, func(t *testing.T) {
			traces := remainingFilterTraces(MaxSummaryScanEntries + 1)
			last := &traces[len(traces)-1]
			last.Events[0].Payload["status"] = "failed"
			last.Events[0].Payload["safe_error_summary"] = "Unicode needle 错误"
			last.Events[0].OperationName = "target"
			source := &remainingFilterSource{traces: traces}
			svc := New(evidencestore.New(), WithProjectionSource(source))
			o := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Limit: 20}
			switch name {
			case "status":
				o.Status = "error"
			case "error_keyword":
				o.ErrorKeyword = "NEEDLE 错误"
			case "shared_tool":
				o.Tool = "target"
				o.Scope.AccessProfile = &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "acct_demo", Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn-allowed"}}
			}
			page, err := svc.ListTraceExecutions(context.Background(), o)
			if err != nil || page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].TraceID != last.TraceID || page.Partial || page.Truncated {
				t.Fatalf("late filtered trace lost: total=%d entries=%d partial=%v truncated=%v err=%v", page.Total, len(page.Entries), page.Partial, page.Truncated, err)
			}
		})
	}
}

func TestScannedTraceSummaryTotalAndCursorTraversal(t *testing.T) {
	source := &remainingFilterSource{traces: remainingFilterTraces(2051)}
	svc := New(evidencestore.New(), WithProjectionSource(source))
	o := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Status: "completed", Limit: 200}
	seen := map[string]bool{}
	for {
		page, err := svc.ListTraceExecutions(context.Background(), o)
		if err != nil || page.Total != 2051 || page.Partial {
			t.Fatalf("page total=%d partial=%v err=%v", page.Total, page.Partial, err)
		}
		for _, trace := range page.Entries {
			if seen[trace.TraceID] {
				t.Fatal("duplicate trace")
			}
			seen[trace.TraceID] = true
		}
		if page.NextCursor == nil {
			break
		}
		o.Cursor = *page.NextCursor
	}
	if len(seen) != 2051 {
		t.Fatalf("only reached %d", len(seen))
	}
	o.Cursor = ""
	o.Page = 100
	page, err := svc.ListTraceExecutions(context.Background(), o)
	if err != nil { // Deep offset protection is explicit; a cursor avoids retention.
		if !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) {
			t.Fatal(err)
		}
	} else if page.Total != 2051 || len(page.Entries) != 0 {
		t.Fatalf("empty offset total=%d", page.Total)
	}
}

func TestScannedCursorBindsFiltersAndAuthorization(t *testing.T) {
	source := &remainingFilterSource{traces: remainingFilterTraces(3)}
	svc := New(evidencestore.New(), WithProjectionSource(source))
	o := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Status: "completed", Limit: 1}
	o.Scope.AccessProfile = &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "acct_demo", Fingerprint: "grants-v1"}
	page, err := svc.ListTraceExecutions(context.Background(), o)
	if err != nil || page.NextCursor == nil {
		t.Fatal("first cursor missing", err)
	}
	o.Cursor = *page.NextCursor
	for _, change := range []string{"status", "keyword", "permission"} {
		copy := o
		p := *o.Scope.AccessProfile
		copy.Scope.AccessProfile = &p
		switch change {
		case "status":
			copy.Status = "error"
		case "keyword":
			copy.ErrorKeyword = "other"
		case "permission":
			p.Fingerprint = "grants-v2"
		}
		_, err := svc.ListTraceExecutions(context.Background(), copy)
		if !errors.Is(err, ErrSummaryCursorInvalid) {
			t.Fatalf("%s cursor error=%v", change, err)
		}
	}
}

func TestScannedSummaryFailsWithoutPartialCount(t *testing.T) {
	sentinel := errors.New("storage failed after first batch")
	source := &remainingFilterSource{traces: remainingFilterTraces(100), err: sentinel}
	svc := New(evidencestore.New(), WithProjectionSource(source))
	o := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Status: "completed", Limit: 1}
	page, err := svc.ListTraceExecutions(context.Background(), o)
	if !errors.Is(err, sentinel) || page.Total != 0 || len(page.Entries) != 0 {
		t.Fatalf("partial success leaked: %+v %v", page, err)
	}
	o.Page = math.MaxInt
	_, err = svc.ListTraceExecutions(context.Background(), o)
	if !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) {
		t.Fatalf("deep offset error=%v", err)
	}
}

type scanStatisticsSpy struct{ calls int }

func (s *scanStatisticsSpy) CountSpansByTraceIDs(_ context.Context, ids []string) (map[string]int, error) {
	s.calls++
	values := map[string]int{}
	for _, id := range ids {
		values[id] = 999
	}
	return values, nil
}
func TestScannedStatisticsRequireGlobalAuthorization(t *testing.T) {
	for _, role := range []string{"legacy-owner", "normal_user", "network_builder", "admin"} {
		t.Run(role, func(t *testing.T) {
			source := &remainingFilterSource{traces: remainingFilterTraces(1)}
			stats := &scanStatisticsSpy{}
			svc := New(evidencestore.New(), WithProjectionSource(source), WithTraceStatsSource(stats))
			o := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Status: "completed"}
			if role != "legacy-owner" {
				o.Scope.AccessProfile = &evidencevo.AccessProfile{EffectiveSubjectID: "acct_demo", AccountActive: true, Roles: []string{role}, ManagedKnowledgeNetworkIDs: []string{"kn"}}
			}
			p, err := svc.ListTraceExecutions(context.Background(), o)
			if err != nil || len(p.Entries) != 1 {
				t.Fatal("missing page", err)
			}
			if role == "admin" {
				if stats.calls != 1 || p.Entries[0].SpanCount != 999 {
					t.Fatal("admin stats missing")
				}
			} else if stats.calls != 0 || p.Entries[0].SpanCount != 0 || p.Entries[0].SpanCountStatus != "unavailable" {
				t.Fatal("unscoped foreign span count exposed")
			}
		})
	}
}
