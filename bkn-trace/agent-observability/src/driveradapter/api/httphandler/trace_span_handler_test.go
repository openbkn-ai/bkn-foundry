// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/opensearchvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

type handlerSpanPort struct {
	result            opensearchvo.SearchResult
	err               error
	calls, graphCalls int
	query             json.RawMessage
}

func (p *handlerSpanPort) SearchTraces(context.Context, json.RawMessage) (opensearchvo.SearchResult, error) {
	p.graphCalls++
	return opensearchvo.SearchResult(handlerTraceGraphSearchResult()), p.err
}
func (p *handlerSpanPort) SearchTraceSpans(_ context.Context, q json.RawMessage) (opensearchvo.SearchResult, error) {
	p.calls++
	p.query = append([]byte(nil), q...)
	return p.result, p.err
}

type handlerSpanSummary struct {
	page     evidencevo.TraceSummaryPage
	calls    int
	scope    evidencevo.QueryScope
	deadline time.Time
}

func (s *handlerSpanSummary) ListTraceExecutions(ctx context.Context, o evidencevo.SummaryQueryOptions) (evidencevo.TraceSummaryPage, error) {
	s.calls++
	s.deadline, _ = ctx.Deadline()
	s.scope = o.Scope
	return s.page, nil
}

type handlerSpanOperations struct {
	entries []sessionvo.OperationExecution
	calls   int
}

func (s *handlerSpanOperations) ListOperationExecutionsByTraceIDScoped(context.Context, evidencevo.QueryScope, string) ([]sessionvo.OperationExecution, error) {
	s.calls++
	return s.entries, nil
}
func spanHandlerScope() evidencevo.QueryScope {
	return evidencevo.QueryScope{AccountID: "user-1", AccountType: "user", AccessProfile: &evidencevo.AccessProfile{ActorID: "user-1", EffectiveSubjectID: "user-1", ApplicationPrincipalID: "app", AccountActive: true, Roles: []string{"admin"}, Fingerprint: "fixed"}}
}
func spanHandlerRequest(h *TraceHandler, path string, scope *evidencevo.QueryScope) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if scope != nil {
		r = r.WithContext(context.WithValue(r.Context(), trustedQueryScopeContextKey{}, *scope))
	}
	w := httptest.NewRecorder()
	if !h.GetTraceSubresource(w, r) {
		panic("span route not dispatched")
	}
	return w
}
func spanHandlerFixture(t *testing.T, start, count int) opensearchvo.SearchResult {
	t.Helper()
	buckets := []any{}
	for i := start; i < start+count; i++ {
		id := fmt.Sprintf("s%05d", i)
		buckets = append(buckets, map[string]any{"key": map[string]string{"span_id": id}, "sample": map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_source": map[string]any{"traceId": "trace", "spanId": id, "name": "step", "kind": "INTERNAL", "startTimeUnixNano": "10", "endTimeUnixNano": "20"}}}}}})
	}
	spans := map[string]any{"buckets": buckets}
	if count > 0 {
		spans["after_key"] = map[string]string{"span_id": fmt.Sprintf("s%05d", start+count-1)}
	}
	raw, err := json.Marshal(map[string]any{"timed_out": false, "_shards": map[string]int{"failed": 0}, "aggregations": map[string]any{"missing_span_id": map[string]int{"doc_count": 0}, "spans": spans}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func newSpanHandler(t *testing.T) (*TraceHandler, *handlerSpanPort, *handlerSpanSummary, *handlerSpanOperations) {
	t.Helper()
	p := &handlerSpanPort{result: spanHandlerFixture(t, 0, 0)}
	s := &handlerSpanSummary{page: evidencevo.TraceSummaryPage{Entries: []evidencevo.TraceSummary{{TraceID: "trace"}}}}
	o := &handlerSpanOperations{}
	return NewTraceHandlerWithTechnicalSources(tracesvc.New(p), s, o, []byte("span-handler-test-signing-key")), p, s, o
}

func TestTraceSpanHandlerSignedContinuationAndLastPage(t *testing.T) {
	h, p, s, o := newSpanHandler(t)
	scope := spanHandlerScope()
	p.result = spanHandlerFixture(t, 0, 100)
	w := spanHandlerRequest(h, "/api/agent-observability/v1/traces/trace/spans", &scope)
	if w.Code != 200 {
		t.Fatalf("first: %d %s", w.Code, w.Body.String())
	}
	var first tracesvc.SpanPage
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 100 || !first.HasMore || first.NextCursor == "" || strings.Contains(w.Body.String(), `"after_key"`) || strings.Contains(w.Body.String(), `"after"`) {
		t.Fatalf("unsafe page: %s", w.Body.String())
	}
	if s.deadline.IsZero() || time.Until(s.deadline) > 3*time.Second {
		t.Fatal("public span page did not bound canonical reads")
	}
	if o.calls != 0 || s.scope.View != evidencevo.AccessViewTechnical {
		t.Fatal("matching summary did not authorize first with technical scope")
	}
	p.result = spanHandlerFixture(t, 100, 1)
	w = spanHandlerRequest(h, "/api/agent-observability/v1/traces/trace/spans?cursor="+url.QueryEscape(first.NextCursor), &scope)
	var last tracesvc.SpanPage
	_ = json.Unmarshal(w.Body.Bytes(), &last)
	if w.Code != 200 || last.HasMore || last.NextCursor != "" || len(last.Entries) != 1 || last.Entries[0].SpanID != "s00100" || !strings.Contains(string(p.query), `"after":{"span_id":"s00099"}`) || s.calls != 2 {
		t.Fatalf("last: %d %s query=%s", w.Code, w.Body.String(), p.query)
	}
	// A valid cursor cannot substitute for a fresh canonical visibility decision.
	s.page = evidencevo.TraceSummaryPage{}
	w = spanHandlerRequest(h, "/api/agent-observability/v1/traces/trace/spans?cursor="+url.QueryEscape(first.NextCursor), &scope)
	if w.Code != 404 || p.calls != 2 {
		t.Fatal("cursor authorized a no-longer-visible trace")
	}
	o.entries = []sessionvo.OperationExecution{{Fact: sessionvo.OperationCallFact{TraceID: "trace"}}}
	w = spanHandlerRequest(h, "/api/agent-observability/v1/traces/trace/spans", &scope)
	if w.Code != 200 || p.calls != 3 {
		t.Fatal("authorized operation-only trace lost fallback")
	}
}

func TestTraceSpanHandlerRejectsAuthorizationAndInvalidCursorsBeforeSearch(t *testing.T) {
	now := time.Now()
	base := spanHandlerScope()
	key := []byte("span-handler-test-signing-key")
	good, _ := encodeTraceSpanCursor(key, "trace", "s00099", base, now)
	expired, _ := encodeTraceSpanCursor(key, "trace", "s00099", base, now.Add(-16*time.Minute))
	for _, tc := range []struct {
		name, trace, cursor string
		scope               *evidencevo.QueryScope
		status              int
	}{
		{"untrusted", "trace", "", nil, 401},
		{"cross_trace", "other", good, &base, 400},
		{"tampered", "trace", good + "a", &base, 400},
		{"expired", "trace", expired, &base, 400},
		{"oversized", "trace", strings.Repeat("x", 2049), &base, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, p, _, _ := newSpanHandler(t)
			path := "/api/agent-observability/v1/traces/" + tc.trace + "/spans"
			if tc.cursor != "" {
				path += "?cursor=" + url.QueryEscape(tc.cursor)
			}
			w := spanHandlerRequest(h, path, tc.scope)
			if w.Code != tc.status || p.calls != 0 {
				t.Fatalf("status=%d body=%s calls=%d", w.Code, w.Body.String(), p.calls)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*evidencevo.QueryScope)
		status int
	}{
		{"profile_absent", func(s *evidencevo.QueryScope) { s.AccessProfile = nil }, 403},
		{"owner_only", func(s *evidencevo.QueryScope) { s.AccessProfile.Roles = nil }, 403},
		{"inactive", func(s *evidencevo.QueryScope) { s.AccessProfile.AccountActive = false }, 403},
		{"other_identity", func(s *evidencevo.QueryScope) { s.AccountID = "another" }, 400},
		{"changed_grants_same_fingerprint", func(s *evidencevo.QueryScope) { s.AccessProfile.ManagedKnowledgeNetworkIDs = []string{"new-grant"} }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, p, _, _ := newSpanHandler(t)
			scope := spanHandlerScope()
			tc.mutate(&scope)
			w := spanHandlerRequest(h, "/api/agent-observability/v1/traces/trace/spans?cursor="+url.QueryEscape(good), &scope)
			if w.Code != tc.status || p.calls != 0 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	h, p, s, o := newSpanHandler(t)
	s.page.Entries[0].TraceID = "foreign"
	o.entries = []sessionvo.OperationExecution{{Fact: sessionvo.OperationCallFact{TraceID: "foreign"}}}
	w := spanHandlerRequest(h, "/api/agent-observability/v1/traces/trace/spans", &base)
	if w.Code != 404 || p.calls != 0 {
		t.Fatal("mismatched canonical result granted trace")
	}
	r := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/traces/trace/spans", nil)
	w = httptest.NewRecorder()
	h.GetTraceSubresource(w, r)
	if w.Code != 405 || p.calls != 0 {
		t.Fatal("nonGET was accepted")
	}
}

func TestTraceSpanHandlerMapsBoundedQueryErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{tracesvc.ErrSpanStorageUnsupported, 422}, {tracesvc.ErrSpanPageBudget, 413}, {tracesvc.ErrSpanPageIncomplete, 503}, {tracesvc.ErrSpanPageUnavailable, 503}, {context.DeadlineExceeded, 504}, {errors.New("private backend payload"), 503},
	} {
		t.Run(fmt.Sprint(tc.status, tc.err), func(t *testing.T) {
			h, p, _, _ := newSpanHandler(t)
			p.err = tc.err
			scope := spanHandlerScope()
			w := spanHandlerRequest(h, "/api/agent-observability/v1/traces/trace/spans", &scope)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "private backend payload") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestNonGlobalTraceDetailKeepsAuthorizedFactsWithoutRawSpanSearch(t *testing.T) {
	h, p, s, o := newSpanHandler(t)
	s.page.Entries = []evidencevo.TraceSummary{{TraceID: "trace", SpanCount: 321, SpanCountStatus: "available"}}
	o.entries = []sessionvo.OperationExecution{{Fact: sessionvo.OperationCallFact{TraceID: "trace", OperationID: "authorized-op"}}}
	scope := spanHandlerScope()
	scope.AccessProfile.Roles = nil
	w := spanHandlerRequest(h, "/api/agent-observability/v1/traces/trace", &scope)
	var detail tracesvc.TechnicalTraceDetail
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if w.Code != 200 || p.graphCalls != 0 || p.calls != 0 || len(detail.Operations) != 1 || detail.Graph != nil || !detail.Partial || detail.Summary.SpanCount != 0 || detail.Summary.SpanCountStatus != "unavailable" || detail.SpanDisplay.BrowseAllowed || detail.SpanDisplay.BrowseReason != "global_trace_access_required" {
		t.Fatalf("unsafe non-global detail: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "span_scope_unavailable") || strings.Contains(w.Body.String(), `"span_unavailable"`) {
		t.Fatal("scope restriction misreported as evidence loss")
	}
}

func TestTraceSpanHandlerDoesNotSearchAfterCanonicalDeadline(t *testing.T) {
	h, p, _, _ := newSpanHandler(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	ctx = context.WithValue(ctx, trustedQueryScopeContextKey{}, spanHandlerScope())
	r := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/trace/spans", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.GetTraceSubresource(w, r)
	if w.Code != http.StatusGatewayTimeout || p.calls != 0 {
		t.Fatalf("expired canonical authorization reached ES: %d %s", w.Code, w.Body.String())
	}
}
