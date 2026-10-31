// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type traceFallbackStore struct {
	*pagingSessionStore
	rows     []evidencevo.TraceSummary
	err      error
	ids      []string
	scope    evidencevo.QueryScope
	deadline time.Time
	calls    int
}

func (s *traceFallbackStore) LoadTraceSummaryFallback(ctx context.Context, ids []string, scope evidencevo.QueryScope) ([]evidencevo.TraceSummary, error) {
	s.calls++
	s.ids = append([]string(nil), ids...)
	s.scope = scope
	s.deadline, _ = ctx.Deadline()
	return s.rows, s.err
}
func traceFallbackFixture() (*traceFallbackStore, *capturingProjectionSource) {
	store := &traceFallbackStore{pagingSessionStore: &pagingSessionStore{Store: sessionstore.New(), tracePage: isessionstore.SummaryIdentityPage{
		Entries: []isessionstore.SummaryIdentity{{ID: "missing", StartedAt: "2026-08-19T09:00:00Z"}, {ID: "present", StartedAt: "2026-08-19T08:59:00Z"}}, Total: 3, HasMore: true,
	}}, rows: []evidencevo.TraceSummary{{TraceID: "missing", RequestID: "req-missing", Status: "error", RootOperation: "db-root", SpanCountStatus: "unavailable"}}}
	source := &capturingProjectionSource{result: iprojectionsource.Result{Traces: []evidencevo.NormalizedTrace{pageSummaryTrace("present", "req-present", "2026-08-19T08:59:00Z", "acct_demo", "bd_demo")}}}
	return store, source
}
func TestTracePageFillsOnlyMissingIdentitiesWithoutChangingPagination(t *testing.T) {
	store, source := traceFallbackFixture()
	// A fallback implementation must not replace a healthy projection or add an unselected ID.
	store.rows = append(store.rows, evidencevo.TraceSummary{TraceID: "present", Status: "wrong"}, evidencevo.TraceSummary{TraceID: "outside"})
	svc := New(evidencestore.New(), WithProjectionSource(source), WithSessionStore(store))
	scope := summaryScope("acct_demo")
	started := time.Now()
	page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: scope, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[0].TraceID != "missing" || page.Entries[1].TraceID != "present" || page.Entries[1].Status == "wrong" || page.Total != 3 || page.NextCursor == nil || page.Partial || page.Truncated {
		t.Fatalf("page=%+v", page)
	}
	if page.Entries[0].StartedAt != store.tracePage.Entries[0].StartedAt || page.Entries[0].SpanCountStatus != "unavailable" {
		t.Fatalf("fallback invented page metadata: %+v", page.Entries[0])
	}
	if store.calls != 1 || !reflect.DeepEqual(store.ids, []string{"missing"}) || !reflect.DeepEqual(store.scope, scope) {
		t.Fatalf("unbounded/unscoped fallback: calls=%d ids=%v scope=%+v", store.calls, store.ids, store.scope)
	}
	if store.deadline.IsZero() || store.deadline.After(started.Add(4*time.Second)) {
		t.Fatal("fallback must have a bounded deadline")
	}
	cursor, ok, e := decodeSummaryCursor(*page.NextCursor)
	if e != nil || !ok || cursor.ID != "present" || cursor.StartedAt != store.tracePage.Entries[1].StartedAt {
		t.Fatalf("cursor=%+v err=%v", cursor, e)
	}
}
func TestTracePageFallbackFailsClosedAndDoesNotHideSourceErrors(t *testing.T) {
	failure := errors.New("database unavailable")
	for _, tc := range []struct {
		name                         string
		rows                         []evidencevo.TraceSummary
		fallbackErr, sourceErr, want error
	}{
		{name: "missing canonical records", want: ErrSummaryProjectionLag},
		{name: "database failure", fallbackErr: failure, want: failure},
		{name: "projection dependency failure", sourceErr: failure, want: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, source := traceFallbackFixture()
			store.rows, store.err = tc.rows, tc.fallbackErr
			source.errFor = func(iprojectionsource.Query) error { return tc.sourceErr }
			svc := New(evidencestore.New(), WithProjectionSource(source), WithSessionStore(store))
			page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Limit: 2})
			if !errors.Is(err, tc.want) || len(page.Entries) != 0 || page.NextCursor != nil {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			if tc.fallbackErr != nil && !errors.Is(err, ErrSummaryFallbackUnavailable) {
				t.Fatal("missing fallback dependency classification")
			}
			if tc.sourceErr != nil && store.calls != 0 {
				t.Fatal("source failure must not become an unreviewed full dependency fallback")
			}
		})
	}
}
func TestTracePageFallbackDoesNotExtendToFiltersOrHealthyPages(t *testing.T) {
	for _, filter := range []string{"healthy", "tool", "service", "keyword", "status", "inactive", "mismatched profile", "shared network"} {
		t.Run(filter, func(t *testing.T) {
			store, source := traceFallbackFixture()
			options := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Limit: 2}
			switch filter {
			case "healthy":
				store.tracePage.Entries = store.tracePage.Entries[1:]
			case "tool":
				options.Tool = "db-root"
			case "service":
				options.Service = "context-loader"
			case "keyword":
				options.Keyword = "missing"
			case "status":
				options.Status = "error"
			case "inactive":
				options.Scope.AccessProfile = &evidencevo.AccessProfile{EffectiveSubjectID: "acct_demo"}
			case "mismatched profile":
				options.Scope.AccessProfile = &evidencevo.AccessProfile{EffectiveSubjectID: "another-owner", AccountActive: true}
			case "shared network":
				options.Scope.AccessProfile = &evidencevo.AccessProfile{EffectiveSubjectID: "acct_demo", AccountActive: true, Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"network"}}
			}
			svc := New(evidencestore.New(), WithProjectionSource(source), WithSessionStore(store))
			_, _ = svc.ListTraceExecutions(context.Background(), options)
			if store.calls != 0 {
				t.Fatalf("fallback unexpectedly expanded to %s", filter)
			}
		})
	}
}
func TestTracePageFallbackPreservesExistingTruncation(t *testing.T) {
	store, source := traceFallbackFixture()
	source.result.Truncated = true
	svc := New(evidencestore.New(), WithProjectionSource(source), WithSessionStore(store))
	page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Limit: 2})
	if err != nil || !page.Partial || !page.Truncated || len(page.Entries) != 2 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestTracePageFallbackHonorsCanceledContext(t *testing.T) {
	store, source := traceFallbackFixture()
	svc := New(evidencestore.New(), WithProjectionSource(source), WithSessionStore(store))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	summaries := map[string]evidencevo.TraceSummary{}
	err := svc.fillMissingTracePage(ctx, evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo")}, []string{"missing"}, summaries)
	if !errors.Is(err, context.Canceled) || len(summaries) != 0 {
		t.Fatalf("cancellation became successful data: %v %+v", err, summaries)
	}
}
