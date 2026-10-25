// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type rootServiceScanSource struct {
	remainingFilterSource
	result iprojectionsource.Result
	budget time.Duration
}

func (s *rootServiceScanSource) ScanTraceSummaryProjection(ctx context.Context, q iprojectionsource.Query, visit func(iprojectionsource.Result) error) error {
	s.scans++
	s.queries = append(s.queries, q)
	if deadline, ok := ctx.Deadline(); ok {
		s.budget = time.Until(deadline)
	}
	if err := visit(s.result); err != nil {
		return err
	}
	return s.err
}
func rootServiceResult(names ...string) iprojectionsource.Result {
	traces := remainingFilterTraces(len(names))
	roots := map[string]string{}
	for i := range traces {
		traces[i].Events[0].Producer = names[i]
		roots[traces[i].TraceID] = "root-tool"
	}
	return iprojectionsource.Result{Traces: traces, CanonicalRoots: roots}
}
func TestTraceRootServicesBoundedCanonicalSearchAndNoStatistics(t *testing.T) {
	names := []string{"Canonical-Service", "Canonical-Service", "unmatched"}
	for i := 0; i < 102; i++ {
		names = append(names, fmt.Sprintf("canonical-%03d", i))
	}
	source := &rootServiceScanSource{result: rootServiceResult(names...)}
	stats := &scanStatisticsSpy{}
	svc := New(evidencestore.New(), WithProjectionSource(source), WithTraceStatsSource(stats))
	scope := summaryScope("acct_demo")
	scope.AccessProfile = &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "acct_demo", Roles: []string{"admin"}}
	result, err := svc.ListTraceRootServices(context.Background(), evidencevo.SummaryQueryOptions{Scope: scope, Service: "ignore-self"}, "CANONICAL", 0)
	if err != nil || len(result.Entries) != 100 || !result.HasMore || result.Entries[0] != "Canonical-Service" || stats.calls != 0 {
		t.Fatalf("result=%+v stats=%d err=%v", result, stats.calls, err)
	}
	if source.budget <= 0 || source.budget > 3*time.Second || !source.queries[0].SummaryOnly || !source.queries[0].OmitSummaryTerminalContent {
		t.Fatalf("query=%+v budget=%v", source.queries, source.budget)
	}
	result, err = svc.ListTraceRootServices(context.Background(), evidencevo.SummaryQueryOptions{Scope: scope}, "unmatched", 1)
	if err != nil || result.HasMore || !reflect.DeepEqual(result.Entries, []string{"unmatched"}) {
		t.Fatalf("exact last name %+v %v", result, err)
	}
}
func TestTraceRootServicesFiltersAndAtomicFailure(t *testing.T) {
	source := &rootServiceScanSource{result: rootServiceResult("match", "wrong-status", "wrong-window")}
	for _, i := range []int{0, 2} {
		source.result.Traces[i].Events[0].Payload["status"] = "failed"
		source.result.Traces[i].Events[0].Payload["safe_error_summary"] = "Needle 错误"
	}
	source.result.Traces[2].Events[0].ObservedAt = "2025-01-01T00:00:00Z"
	svc := New(evidencestore.New(), WithProjectionSource(source))
	from := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	to := from.Add(48 * time.Hour)
	o := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), From: from, To: to, TraceID: "trace-00000", Tool: "root-tool", ErrorKeyword: "NEEDLE 错误"}
	result, err := svc.ListTraceRootServices(context.Background(), o, "", 100)
	if err != nil || !reflect.DeepEqual(result.Entries, []string{"match"}) {
		t.Fatalf("filters %+v %v", result, err)
	}
	q := source.queries[0]
	if q.TraceID != o.TraceID || q.From != from || q.To != to || q.Tool != o.Tool || q.Status != "error" || q.Scope != o.Scope {
		t.Fatalf("handoff %+v", q)
	}
	source.err = context.DeadlineExceeded
	result, err = svc.ListTraceRootServices(context.Background(), o, "", 100)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) || len(result.Entries) != 0 || result.HasMore {
		t.Fatalf("partial leaked %+v %v", result, err)
	}
	source.err = nil
	source.result.Truncated = true
	_, err = svc.ListTraceRootServices(context.Background(), o, "", 100)
	if !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) {
		t.Fatal(err)
	}
}
func TestTraceRootServicesRejectsUnsupportedAndUntrustedScopes(t *testing.T) {
	source := &rootServiceScanSource{result: rootServiceResult("private")}
	svc := New(evidencestore.New(), WithProjectionSource(source))
	for _, o := range []evidencevo.SummaryQueryOptions{{Cursor: "c"}, {Page: 1}, {Limit: 20}, {ConversationID: "c"}, {InteractionID: "i"}, {AgentOrApp: "a"}, {Keyword: "x"}, {KnowledgeNetwork: "kn"}, {RecordIntegrity: "unknown"}, {EvidenceCompleteness: "complete"}, {ExcludeAgentOrApp: "x"}, {ExcludeAgentOrApps: []string{"x"}}} {
		o.Scope = summaryScope("acct_demo")
		if _, err := svc.ListTraceRootServices(context.Background(), o, "", 100); !errors.Is(err, ErrTraceRootServicesUnsupported) {
			t.Fatalf("unsupported %+v: %v", o, err)
		}
	}
	for _, scope := range []evidencevo.QueryScope{{}, {AccountID: "acct_demo", AccountType: "app", AccessProfile: &evidencevo.AccessProfile{AccountActive: false}}, {AccountID: "acct_demo", AccountType: "app", AccessProfile: &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "foreign"}}} {
		result, err := svc.ListTraceRootServices(context.Background(), evidencevo.SummaryQueryOptions{Scope: scope}, "", 100)
		if err != nil || len(result.Entries) != 0 {
			t.Fatalf("untrusted %+v %v", result, err)
		}
	}
	if source.scans != 0 {
		t.Fatal("unauthorized scan")
	}
	_, err := New(evidencestore.New()).ListTraceRootServices(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo")}, "", 100)
	if !errors.Is(err, ErrTraceRootServicesUnavailable) {
		t.Fatal(err)
	}
}
func TestTraceRootServicesUsesOnlyAuthorizedCanonicalMetadata(t *testing.T) {
	source := &rootServiceScanSource{result: rootServiceResult("projection-root")}
	trace := &source.result.Traces[0]
	trace.ConversationID = "conv"
	trace.Events[0].InteractionID = "allowed"
	source.result.CanonicalScopes = map[string][]iprojectionsource.CanonicalTraceScope{trace.TraceID: {{ConversationID: "conv", InteractionID: "allowed"}}}
	store := sessionstore.New()
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{TraceID: trace.TraceID, ConversationID: "conv", InteractionID: "foreign", OperationID: "first", SourceModule: "private-root", StartedAt: time.Unix(1, 0)})
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{TraceID: trace.TraceID, ConversationID: "conv", InteractionID: "allowed", OperationID: "second", SourceModule: "authorized-root", StartedAt: time.Unix(2, 0)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := New(evidencestore.New(), WithProjectionSource(source), WithSessionStore(store))
	scope := summaryScope("acct_demo")
	scope.AccessProfile = &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "acct_demo", Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn"}}
	result, err := svc.ListTraceRootServices(context.Background(), evidencevo.SummaryQueryOptions{Scope: scope}, "", 100)
	if err != nil || !reflect.DeepEqual(result.Entries, []string{"authorized-root"}) {
		t.Fatalf("collision leaked %+v %v", result, err)
	}
	delete(source.result.CanonicalScopes, trace.TraceID)
	if _, err = svc.ListTraceRootServices(context.Background(), evidencevo.SummaryQueryOptions{Scope: scope}, "", 100); !errors.Is(err, ErrSummaryProjectionLag) {
		t.Fatalf("missing auth scope %v", err)
	}
}

type rootServiceOptionStore struct {
	*sessionstore.Store
	query  isessionstore.SummaryPageQuery
	search string
	calls  int
	err    error
}

func (s *rootServiceOptionStore) ListTraceRootServiceOptions(ctx context.Context, q isessionstore.SummaryPageQuery, search string) ([]string, bool, error) {
	s.calls++
	s.query = q
	s.search = search
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 3*time.Second {
		return nil, false, errors.New("missing discovery deadline")
	}
	return []string{"SQL-canonical"}, true, s.err
}
func TestTraceRootServicesSimpleMembershipUsesOptionalHotSQLStore(t *testing.T) {
	store := &rootServiceOptionStore{Store: sessionstore.New()}
	source := &rootServiceScanSource{result: rootServiceResult("archived-projection-only")}
	svc := New(evidencestore.New(), WithSessionStore(store), WithProjectionSource(source))
	from := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	o := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), From: from, To: to, Tool: "selected-root", Service: "exclude-current-selection"}
	result, err := svc.ListTraceRootServices(context.Background(), o, " CaNoN ", 7)
	if err != nil || !reflect.DeepEqual(result.Entries, []string{"SQL-canonical"}) || !result.HasMore || store.calls != 1 || source.scans != 0 {
		t.Fatalf("dispatch result=%+v SQL=%d scanner=%d err=%v", result, store.calls, source.scans, err)
	}
	q := store.query
	if q.Scope != o.Scope || q.From != from || q.To != to || q.Tool != o.Tool || q.Service != "" || q.Limit != 7 || q.Offset != 0 || store.search != "canon" {
		t.Fatalf("scope/time/tool handoff %+v search=%q", q, store.search)
	}
	store.err = context.DeadlineExceeded
	result, err = svc.ListTraceRootServices(context.Background(), o, "", 7)
	if !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) || len(result.Entries) != 0 || result.HasMore {
		t.Fatalf("SQL partial leaked %+v %v", result, err)
	}
	store.err = nil
	o.TraceID = "trace-00000"
	source.result.CanonicalScopes = map[string][]iprojectionsource.CanonicalTraceScope{"trace-00000": {{}}}
	if _, err = svc.ListTraceRootServices(context.Background(), o, "", 7); err != nil {
		t.Fatal(err)
	}
	if source.scans != 1 || store.calls != 2 {
		t.Fatal("TraceID must retain scanner path")
	}
}

type boundedRootServiceSource struct {
	rootServiceScanSource
	batches   []iprojectionsource.Result
	visited   int
	failAfter int
	failure   error
	cancel    context.CancelFunc
	wrapStop  bool
}

func (s *boundedRootServiceSource) ScanTraceSummaryProjection(_ context.Context, _ iprojectionsource.Query, visit func(iprojectionsource.Result) error) error {
	for _, batch := range s.batches {
		s.visited++
		err := visit(batch)
		if s.cancel != nil && err != nil {
			s.cancel()
		}
		if err != nil {
			if s.wrapStop {
				return errors.Join(err, s.failure)
			}
			return err
		}
		if s.failAfter == s.visited {
			return s.failure
		}
	}
	return nil
}
func TestTraceRootServicesStopsOnlyAfterAuthorizedExtraDistinctName(t *testing.T) {
	source := &boundedRootServiceSource{batches: []iprojectionsource.Result{rootServiceResult("first", "first"), rootServiceResult("second"), rootServiceResult("must-not-load")}}
	svc := New(evidencestore.New(), WithProjectionSource(source))
	o := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Status: "completed"}
	result, err := svc.ListTraceRootServices(context.Background(), o, "", 1)
	if err != nil || !result.HasMore || !reflect.DeepEqual(result.Entries, []string{"first"}) || source.visited != 2 {
		t.Fatalf("bounded result=%+v visits=%d err=%v", result, source.visited, err)
	}
	for _, failure := range []error{iprojectionsource.ErrSummaryScanBudgetExceeded, ErrSummaryProjectionLag, errors.New("unrecognized source failure")} {
		source.visited = 0
		source.failAfter = 1
		source.failure = failure
		result, err = svc.ListTraceRootServices(context.Background(), o, "", 1)
		if !errors.Is(err, failure) || len(result.Entries) != 0 || result.HasMore || source.visited != 1 {
			t.Fatalf("failure discarded: %+v visits=%d err=%v", result, source.visited, err)
		}
	}
	// A source must not turn a wrapped/combined error into successful truncation.
	source.visited = 0
	source.failAfter = 0
	source.wrapStop = true
	result, err = svc.ListTraceRootServices(context.Background(), o, "", 1)
	if !errors.Is(err, source.failure) || len(result.Entries) != 0 || result.HasMore {
		t.Fatalf("unknown wrapped failure swallowed: %+v %v", result, err)
	}
	// Cancellation wins even if a source returns the exact stop signal.
	source.visited = 0
	source.wrapStop = false
	ctx, cancel := context.WithCancel(context.Background())
	source.cancel = cancel
	result, err = svc.ListTraceRootServices(ctx, o, "", 1)
	if !errors.Is(err, context.Canceled) || len(result.Entries) != 0 || result.HasMore {
		t.Fatalf("cancellation swallowed: %+v %v", result, err)
	}
}
