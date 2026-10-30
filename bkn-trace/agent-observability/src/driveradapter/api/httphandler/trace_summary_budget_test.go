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
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

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
