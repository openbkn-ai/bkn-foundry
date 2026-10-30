// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

func TestSummaryStorageRejectionIsRetryableAndLogsOnlySafeDiagnostics(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	for _, status := range []int{429, 503} {
		logs.Reset()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/traces?error_keyword=private-keyword", nil)
		request.Header.Set("x-trace-id", "0123456789abcdef0123456789abcdef")
		writeSummaryQueryError(response, request, fmt.Errorf("query: %w", &opensearch.StatusError{Operation: "receipt snapshot", StatusCode: status, Type: "circuit_breaking_exception", Body: "private-storage-reason"}))
		var body map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != 503 || string(body["error_code"]) != `"SUMMARY_DEPENDENCY_UNAVAILABLE"` || body["total"] != nil || body["entries"] != nil || !strings.Contains(string(body["trace_id"]), "0123456789abcdef0123456789abcdef") {
			t.Fatalf("storage rejection became generic failure/partial success: %d %s", response.Code, response.Body.String())
		}
		if !strings.Contains(logs.String(), "circuit_breaking_exception") || !strings.Contains(logs.String(), fmt.Sprint(status)) || !strings.Contains(logs.String(), "0123456789abcdef0123456789abcdef") || strings.Contains(logs.String(), "private") || strings.Contains(response.Body.String(), "private") {
			t.Fatalf("missing safe diagnostic or leaked request/body: log=%s response=%s", logs.String(), response.Body.String())
		}
	}
}

func TestSummaryBudgetErrorsDistinguishDeterministicRejectionFromTimeout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"deterministic", fmt.Errorf("candidate or page limit: %w", iprojectionsource.ErrSummaryScanBudgetExceeded), http.StatusUnprocessableEntity, "SUMMARY_QUERY_BUDGET_EXCEEDED"},
		{"deadline", errors.Join(iprojectionsource.ErrSummaryScanBudgetExceeded, context.DeadlineExceeded), http.StatusServiceUnavailable, "SUMMARY_QUERY_TIMEOUT"},
		{"deadline-after-last-batch", context.DeadlineExceeded, http.StatusServiceUnavailable, "SUMMARY_QUERY_TIMEOUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/traces?status=error", nil)
			writeSummaryQueryError(response, request, tc.err)
			var body struct {
				ErrorCode string `json:"error_code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.status || body.ErrorCode != tc.code {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestTraceKeywordHandlerReportsIncompleteSearchAsResourceError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source iprojectionsource.ProjectionSourcePort
		status int
		code   string
	}{
		{"compatibility-window", staticProjectionSource{result: iprojectionsource.Result{Truncated: true}}, http.StatusUnprocessableEntity, "SUMMARY_QUERY_BUDGET_EXCEEDED"},
		{"scan-budget", &handlerRootServiceSource{err: iprojectionsource.ErrSummaryScanBudgetExceeded}, http.StatusUnprocessableEntity, "SUMMARY_QUERY_BUDGET_EXCEEDED"},
		{"scan-timeout", &handlerRootServiceSource{err: context.DeadlineExceeded}, http.StatusServiceUnavailable, "SUMMARY_QUERY_TIMEOUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New(), evidencesvc.WithProjectionSource(tc.source)))
			response := httptest.NewRecorder()
			handler.ListTraceExecutions(response, authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces?keyword=trace&limit=1", nil))
			var body map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			var code string
			if err := json.Unmarshal(body["error_code"], &code); err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.status || code != tc.code || body["total"] != nil || body["entries"] != nil || len(body["trace_id"]) == 0 {
				t.Fatalf("incomplete search returned a count or unclassified error: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
