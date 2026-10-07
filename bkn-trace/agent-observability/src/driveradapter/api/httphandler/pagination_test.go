// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
)

func TestTraceAndLogPaginationAcceptsPagesAbove100(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	for _, page := range []int{100, 101, 181, 201} {
		t.Run(strconv.Itoa(page), func(t *testing.T) {
			request := authenticatedQueryRequest(http.MethodGet, "/?page="+strconv.Itoa(page)+"&page_size=20", nil)
			response := httptest.NewRecorder()
			options, ok := handler.summaryQueryOptionsFromRequest(response, request)
			if !ok || options.Page != page || options.Limit != 20 {
				t.Fatalf("expected trace page=%d, page_size=20: options=%+v response=%s", page, options, response.Body.String())
			}
			query, err := parseLogQuery(request)
			if err != nil || query.Page != page || query.Limit != 20 {
				t.Fatalf("expected log page=%d, page_size=20: query=%+v err=%v", page, query, err)
			}
			handler.ListTraceExecutions(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("expected trace list to accept page %d: %d %s", page, response.Code, response.Body.String())
			}
		})
	}
}

func TestTraceAndLogPaginationRejectsInvalidPages(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	for _, page := range []string{"0", "-1", "1.5", "abc", "999999999999999999999999999999"} {
		t.Run(page, func(t *testing.T) {
			request := authenticatedQueryRequest(http.MethodGet, "/?page="+page, nil)
			response := httptest.NewRecorder()
			handler.ListTraceExecutions(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected invalid trace page rejection, got %d", response.Code)
			}
			if _, err := parseLogQuery(request); err == nil {
				t.Fatal("expected invalid log page rejection")
			}
		})
	}
}

func TestTraceAndLogPaginationRetainsPageSizeLimit(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	request := authenticatedQueryRequest(http.MethodGet, "/?page=1&page_size=201", nil)
	response := httptest.NewRecorder()
	handler.ListTraceExecutions(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected trace page size above 200 to be rejected, got %d", response.Code)
	}
	if _, err := parseLogQuery(request); err == nil {
		t.Fatal("expected log page size above 200 to be rejected")
	}
}

func TestLogPaginationRequiresAtLeast20Records(t *testing.T) {
	for _, name := range []string{"page_size", "limit"} {
		for _, size := range []int{1, 10, 19} {
			request := httptest.NewRequest(http.MethodGet, "/?page=1000000&"+name+"="+strconv.Itoa(size), nil)
			if _, err := parseLogQuery(request); err == nil {
				t.Errorf("expected %s=%d to be rejected", name, size)
			}
		}
	}
}

func TestLogNumberedPaginationUnsupportedReturnsActionableError(t *testing.T) {
	profile := evidencevo.AccessProfile{EffectiveSubjectID: "admin-a", Roles: []string{"admin"}, AccountActive: true}
	handler := newTestLogHandler(profile, nil)
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs?page=1000000&page_size=20", nil)
	setLogTestIdentity(request, "admin-a")
	response := httptest.NewRecorder()
	handler.ListLogs(response, request)
	if response.Code != http.StatusBadRequest || !containsJSONCode(response.Body.Bytes(), "pagination_not_supported") {
		t.Fatalf("expected unsupported numbered pagination error, got %d: %s", response.Code, response.Body.String())
	}
}
