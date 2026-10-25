// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"testing"
)

// The source contains more than the historical global candidate cap; only the
// selected identity may be expanded. Real SQL selection has separate integration coverage.
func TestIssue2144LateRootToolUsesSelectedIdentity(t *testing.T) {
	for _, serviceFilter := range []string{"", "root-service"} {
		t.Run("service="+serviceFilter, func(t *testing.T) {
			traces := make([]evidencevo.NormalizedTrace, MaxSummaryScanEntries+1)
			for i := range traces {
				traces[i] = pageSummaryTrace(fmt.Sprintf("trace-%04d", i), fmt.Sprintf("req-%04d", i), "2026-08-19T09:00:00Z", "acct_demo", "bd_demo")
				traces[i].Events[0].Producer = "root-service"
				traces[i].Events[0].OperationName = "other-tool"
			}
			traces[len(traces)-1].Events[0].OperationName = "target-tool"
			store := &pagingSessionStore{Store: sessionstore.New(), tracePage: isessionstore.SummaryIdentityPage{Entries: []isessionstore.SummaryIdentity{{ID: traces[len(traces)-1].TraceID, StartedAt: "2026-08-19T09:00:00Z"}}, Total: 1}}
			if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
				tx.SaveReceipt(sessionvo.Receipt{ID: "selected-root", Owner: sessionvo.Owner{EffectiveSubjectType: "app", EffectiveSubjectID: "acct_demo"}, TraceID: traces[len(traces)-1].TraceID, RequestID: "selected-request", ToolName: "target-tool"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			source := &capturingProjectionSource{resultFor: func(q iprojectionsource.Query) iprojectionsource.Result {
				if len(q.TraceIDs) == 1 && q.TraceIDs[0] == traces[len(traces)-1].TraceID {
					return iprojectionsource.Result{Traces: traces[len(traces)-1:]}
				}
				n := min(q.Limit, len(traces))
				return iprojectionsource.Result{Traces: traces[:n], Truncated: n < len(traces)}
			}}
			svc := New(evidencestore.New(), WithProjectionSource(source), WithSessionStore(store))
			options := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Service: serviceFilter, Tool: "target-tool", Limit: 20}
			page, err := svc.ListTraceExecutions(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 1 || len(page.Entries) != 1 || page.Partial {
				t.Fatalf("selected identity page=%+v", page)
			}
			if len(store.traceQueries) != 1 || store.traceQueries[0].Tool != "target-tool" || store.traceQueries[0].Service != serviceFilter {
				t.Fatalf("identity filters=%+v", store.traceQueries)
			}
			if len(source.queries) != 1 || len(source.queries[0].TraceIDs) != 1 {
				t.Fatalf("unbounded projection=%+v", source.queries)
			}

		})
	}
}

func TestIssue2144RejectsRootChangedAfterIdentitySelection(t *testing.T) {
	trace := pageSummaryTrace("trace-race", "req-race", "2026-08-19T09:00:00Z", "acct_demo", "bd_demo")
	trace.Events[0].OperationName = "old-root"
	store := &pagingSessionStore{Store: sessionstore.New(), tracePage: isessionstore.SummaryIdentityPage{Entries: []isessionstore.SummaryIdentity{{ID: trace.TraceID, StartedAt: "2026-08-19T09:00:00Z"}}, Total: 1}}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveReceipt(sessionvo.Receipt{ID: "changed-root", Owner: sessionvo.Owner{EffectiveSubjectType: "app", EffectiveSubjectID: "acct_demo"}, TraceID: trace.TraceID, RequestID: "req-race", ToolName: "new-root"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	source := &capturingProjectionSource{resultFor: func(iprojectionsource.Query) iprojectionsource.Result {
		return iprojectionsource.Result{Traces: []evidencevo.NormalizedTrace{trace}}
	}}
	svc := New(evidencestore.New(), WithProjectionSource(source), WithSessionStore(store))
	_, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Tool: "old-root", Limit: 20})
	if !errors.Is(err, ErrSummaryProjectionLag) {
		t.Fatalf("mismatched root must fail closed, got %v", err)
	}
}

func TestIssue2144RejectsRootMissingAfterIdentitySelection(t *testing.T) {
	for _, serviceFilter := range []string{"", "root-service"} {
		t.Run("service="+serviceFilter, func(t *testing.T) {
			trace := pageSummaryTrace("trace-deleted", "req-deleted", "2026-08-19T09:00:00Z", "acct_demo", "bd_demo")
			trace.Events[0].OperationName = "old-root"
			// Identity selection happened before deletion. Core now has no usable
			// receipt, while the asynchronously maintained projection is stale.
			store := &pagingSessionStore{Store: sessionstore.New(), tracePage: isessionstore.SummaryIdentityPage{Entries: []isessionstore.SummaryIdentity{{ID: trace.TraceID, StartedAt: "2026-08-19T09:00:00Z"}}, Total: 1}}
			source := &capturingProjectionSource{resultFor: func(iprojectionsource.Query) iprojectionsource.Result {
				return iprojectionsource.Result{Traces: []evidencevo.NormalizedTrace{trace}}
			}}
			svc := New(evidencestore.New(), WithProjectionSource(source), WithSessionStore(store))
			_, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Tool: "old-root", Service: serviceFilter, Limit: 20})
			if !errors.Is(err, ErrSummaryProjectionLag) {
				t.Fatalf("missing canonical root must fail closed, got %v", err)
			}
			page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Service: serviceFilter, Limit: 20})
			if err != nil || len(page.Entries) != 1 || page.Entries[0].RootOperation != "old-root" {
				t.Fatalf("unfiltered legacy fallback changed: page=%+v err=%v", page, err)
			}
		})
	}
}
