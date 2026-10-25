// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

type handlerRootServiceSource struct {
	staticProjectionSource
	calls int
	query iprojectionsource.Query
	err   error
}

func (s *handlerRootServiceSource) ScanTraceSummaryProjection(ctx context.Context, q iprojectionsource.Query, visit func(iprojectionsource.Result) error) error {
	s.calls++
	s.query = q
	trace := evidencevo.NormalizedTrace{TraceID: "trace-a", RequestID: "request-a", AccountID: q.Scope.AccountID, AccountType: q.Scope.AccountType, Events: []evidencevo.EvidenceEvent{{EventID: "event-a", EventType: "retrieval.completed", TraceID: "trace-a", RequestID: "request-a", Producer: "Canonical-Service", OperationName: "tool-a", ObservedAt: "2026-10-24T01:00:00Z", EmittedAt: "2026-10-24T01:00:00Z", Payload: map[string]any{"status": "completed"}}}}
	if err := visit(iprojectionsource.Result{Traces: []evidencevo.NormalizedTrace{trace}, CanonicalRoots: map[string]string{"trace-a": "tool-a"}}); err != nil {
		return err
	}
	return s.err
}
func TestRootServiceHandlerPreservesFiltersAndTrustedScope(t *testing.T) {
	source := &handlerRootServiceSource{}
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New(), evidencesvc.WithProjectionSource(source)))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/trace-root-services?q=CANON&service=ignore-self&trace_id=trace-a&tool=tool-a&from=2026-10-24T00:00:00Z&to=2026-10-25T00:00:00Z&limit=1", nil)
	scope := evidencevo.QueryScope{AccountID: "trusted-owner", AccountType: "app"}
	req = req.WithContext(context.WithValue(req.Context(), trustedQueryScopeContextKey{}, scope))
	response := httptest.NewRecorder()
	handler.ListTraceRootServices(response, req)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"entries":["Canonical-Service"]`) || !strings.Contains(response.Body.String(), `"has_more":false`) {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	if source.calls != 1 || source.query.TraceID != "trace-a" || source.query.Tool != "tool-a" || source.query.Scope.AccountID != "trusted-owner" || source.query.Scope.View != evidencevo.AccessViewTechnical || !source.query.SummaryOnly || !source.query.OmitSummaryTerminalContent {
		t.Fatalf("handoff %+v", source.query)
	}
}
func TestRootServiceHandlerRejectsInvalidQueriesBeforeScan(t *testing.T) {
	for _, suffix := range []string{"limit=101", "limit=0", "limit=200", "q=a&q=b", "page=1", "knowledge_network=secret", "owner=other", "from=bad", "from=2026-10-25T00:00:00Z&to=2026-10-24T00:00:00Z", "q=%ZZ", "q=" + url.QueryEscape(strings.Repeat("字", 257))} {
		t.Run(suffix[:min(len(suffix), 36)], func(t *testing.T) {
			source := &handlerRootServiceSource{}
			handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New(), evidencesvc.WithProjectionSource(source)))
			response := httptest.NewRecorder()
			handler.ListTraceRootServices(response, authenticatedQueryRequest(http.MethodGet, "/?"+suffix, nil))
			if response.Code != 400 || source.calls != 0 {
				t.Fatalf("%d %s scans=%d", response.Code, response.Body.String(), source.calls)
			}
		})
	}
	source := &handlerRootServiceSource{}
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New(), evidencesvc.WithProjectionSource(source)))
	response := httptest.NewRecorder()
	handler.ListTraceRootServices(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != 401 || source.calls != 0 {
		t.Fatalf("untrusted discovery: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ListTraceRootServices(response, authenticatedQueryRequest(http.MethodPost, "/", nil))
	if response.Code != 405 || source.calls != 0 {
		t.Fatal("non-GET discovery was scanned")
	}
}
func TestRootServiceHandlerFailsWithoutPartialCatalog(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{context.DeadlineExceeded, 503, "SUMMARY_QUERY_TIMEOUT"}, {iprojectionsource.ErrSummaryScanBudgetExceeded, 422, "SUMMARY_QUERY_BUDGET_EXCEEDED"}} {
		source := &handlerRootServiceSource{err: tc.err}
		handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New(), evidencesvc.WithProjectionSource(source)))
		response := httptest.NewRecorder()
		handler.ListTraceRootServices(response, authenticatedQueryRequest(http.MethodGet, "/", nil))
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.code) || strings.Contains(response.Body.String(), "Canonical-Service") {
			t.Fatalf("%d %s", response.Code, response.Body.String())
		}
	}
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	response := httptest.NewRecorder()
	handler.ListTraceRootServices(response, authenticatedQueryRequest(http.MethodGet, "/", nil))
	if response.Code != 503 || !strings.Contains(response.Body.String(), "ROOT_SERVICE_OPTIONS_UNAVAILABLE") {
		t.Fatalf("unavailable capability fabricated an empty catalog: %d %s", response.Code, response.Body.String())
	}
}
