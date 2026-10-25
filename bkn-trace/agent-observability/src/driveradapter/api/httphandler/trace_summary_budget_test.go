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
