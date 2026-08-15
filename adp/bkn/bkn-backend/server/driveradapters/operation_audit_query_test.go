// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/common/bkntrace"
	"bkn-backend/common/operationaudit"
	berrors "bkn-backend/errors"
)

type operationAuditQueryStoreStub struct {
	filter  operationaudit.Filter
	scope   operationaudit.Scope
	found   bool
	listErr error
	getErr  error
}

func (s *operationAuditQueryStoreStub) List(_ context.Context, filter operationaudit.Filter) (operationaudit.Page, error) {
	s.filter = filter
	if s.listErr != nil {
		return operationaudit.Page{}, s.listErr
	}
	return operationaudit.Page{Entries: []operationaudit.Entry{{EventID: "evt-a", EventTime: time.Now().UTC()}}}, nil
}

func (s *operationAuditQueryStoreStub) Get(_ context.Context, _ string, scope operationaudit.Scope) (operationaudit.Entry, bool, error) {
	s.scope = scope
	if s.getErr != nil {
		return operationaudit.Entry{}, false, s.getErr
	}
	return operationaudit.Entry{EventID: "evt-a"}, s.found, nil
}

func TestListOperationAuditsAppliesServerSideRoleScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		profile        bkntrace.OperationAuditProfile
		wantActor      string
		wantNetworkIDs []string
	}{
		{name: "admin tenant scope", profile: bkntrace.OperationAuditProfile{ActorID: "admin-a", Roles: []string{"admin"}}},
		{name: "network builder direct networks", profile: bkntrace.OperationAuditProfile{ActorID: "builder-a", Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn-b", "kn-a"}}, wantNetworkIDs: []string{"kn-b", "kn-a"}},
		{name: "normal user own facts", profile: bkntrace.OperationAuditProfile{ActorID: "user-a", Roles: []string{"normal_user"}}, wantActor: "user-a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &operationAuditQueryStoreStub{}
			handler := &restHandler{
				as:              traceOutboxAuthStub{visitor: hydra.Visitor{ID: test.profile.ActorID, Type: hydra.VisitorType("user")}},
				auditQueryStore: store,
				auditAccessResolver: func(context.Context, string, string) (bkntrace.OperationAuditProfile, error) {
					return test.profile, nil
				},
			}
			engine := gin.New()
			engine.Use(rest.LanguageMiddleware(), rest.PrivateNoCacheMiddleware())
			engine.GET("/api/bkn-backend/v1/operation-audits", handler.ListOperationAudits)
			request := httptest.NewRequest(http.MethodGet, "/api/bkn-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z", nil)
			request.Header.Set("Authorization", "Bearer token")
			request.Header.Set("x-tenant-id", "tenant-a")
			request.Header.Set("x-business-domain", "domain-a")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if store.filter.TenantID != "tenant-a" || store.filter.BusinessDomainID != "domain-a" || store.filter.ActorID != test.wantActor || !sameStrings(store.filter.KnowledgeNetworkIDs, test.wantNetworkIDs) {
				t.Fatalf("filter = %#v", store.filter)
			}
		})
	}
}

func TestOperationAuditQueryRejectsUnboundedRangeAndHidesUnauthorizedDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &operationAuditQueryStoreStub{found: false}
	profile := bkntrace.OperationAuditProfile{ActorID: "builder-a", Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn-a"}}
	handler := &restHandler{
		as:                  traceOutboxAuthStub{visitor: hydra.Visitor{ID: "builder-a", Type: hydra.VisitorType("user")}},
		auditQueryStore:     store,
		auditAccessResolver: func(context.Context, string, string) (bkntrace.OperationAuditProfile, error) { return profile, nil },
	}
	engine := gin.New()
	engine.Use(rest.LanguageMiddleware(), rest.PrivateNoCacheMiddleware())
	engine.GET("/api/bkn-backend/v1/operation-audits", handler.ListOperationAudits)
	engine.GET("/api/bkn-backend/v1/operation-audits/:event_id", handler.GetOperationAudit)

	unbounded := httptest.NewRequest(http.MethodGet, "/api/bkn-backend/v1/operation-audits?from=2026-01-01T00:00:00Z&to=2026-08-08T00:00:00Z", nil)
	unbounded.Header.Set("Authorization", "Bearer token")
	unbounded.Header.Set("x-tenant-id", "tenant-a")
	unbounded.Header.Set("x-business-domain", "domain-a")
	unboundedResponse := httptest.NewRecorder()
	engine.ServeHTTP(unboundedResponse, unbounded)
	if unboundedResponse.Code != http.StatusBadRequest {
		t.Fatalf("unbounded status = %d", unboundedResponse.Code)
	}

	detail := httptest.NewRequest(http.MethodGet, "/api/bkn-backend/v1/operation-audits/evt-secret", nil)
	detail.Header.Set("Authorization", "Bearer token")
	detail.Header.Set("x-tenant-id", "tenant-a")
	detail.Header.Set("x-business-domain", "domain-a")
	detailResponse := httptest.NewRecorder()
	engine.ServeHTTP(detailResponse, detail)
	if detailResponse.Code != http.StatusNotFound || !sameStrings(store.scope.KnowledgeNetworkIDs, []string{"kn-a"}) {
		t.Fatalf("detail status = %d, scope = %#v", detailResponse.Code, store.scope)
	}
}

func TestOperationAuditQueryDistinguishesDeniedFromUnavailableAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "denied", err: bkntrace.ErrAccessProfileDenied, wantStatus: http.StatusForbidden},
		{name: "unavailable", err: errors.New("BKN Safe unavailable"), wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := &restHandler{
				as: traceOutboxAuthStub{visitor: hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")}},
				auditAccessResolver: func(context.Context, string, string) (bkntrace.OperationAuditProfile, error) {
					return bkntrace.OperationAuditProfile{}, test.err
				},
			}
			engine := gin.New()
			engine.Use(rest.LanguageMiddleware(), rest.PrivateNoCacheMiddleware())
			engine.GET("/api/bkn-backend/v1/operation-audits", handler.ListOperationAudits)
			request := httptest.NewRequest(http.MethodGet, "/api/bkn-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z", nil)
			request.Header.Set("Authorization", "Bearer token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestOperationAuditErrorsAreLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	profile := bkntrace.OperationAuditProfile{ActorID: "user-a", Roles: []string{"normal_user"}}

	tests := []struct {
		name            string
		language        string
		path            string
		store           *operationAuditQueryStoreStub
		resolverErr     error
		wantStatus      int
		wantCode        string
		wantDescription string
	}{
		{
			name: "Chinese invalid range", language: "zh-CN",
			path:  "/api/bkn-backend/v1/operation-audits?from=invalid&to=2026-08-08T00:00:00Z",
			store: &operationAuditQueryStoreStub{}, wantStatus: http.StatusBadRequest,
			wantCode: berrors.BknBackend_OperationAudit_InvalidRange, wantDescription: "操作审计查询时间范围无效。",
		},
		{
			name: "English invalid range", language: "en-US",
			path:  "/api/bkn-backend/v1/operation-audits?from=invalid&to=2026-08-08T00:00:00Z",
			store: &operationAuditQueryStoreStub{}, wantStatus: http.StatusBadRequest,
			wantCode: berrors.BknBackend_OperationAudit_InvalidRange, wantDescription: "The operation-audit time range is invalid.",
		},
		{
			name: "English query failure", language: "en-US",
			path:  "/api/bkn-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z",
			store: &operationAuditQueryStoreStub{listErr: errors.New("store unavailable")}, wantStatus: http.StatusInternalServerError,
			wantCode: berrors.BknBackend_OperationAudit_QueryFailed, wantDescription: "The operation-audit query failed.",
		},
		{
			name: "English access denied", language: "en-US",
			path:  "/api/bkn-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z",
			store: &operationAuditQueryStoreStub{}, resolverErr: bkntrace.ErrAccessProfileDenied, wantStatus: http.StatusForbidden,
			wantCode: berrors.BknBackend_OperationAudit_AccessDenied, wantDescription: "You are not permitted to view operation-audit records.",
		},
		{
			name: "English not found", language: "en-US", path: "/api/bkn-backend/v1/operation-audits/evt-missing",
			store: &operationAuditQueryStoreStub{found: false}, wantStatus: http.StatusNotFound,
			wantCode: berrors.BknBackend_OperationAudit_NotFound, wantDescription: "The operation-audit event was not found.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := func(context.Context, string, string) (bkntrace.OperationAuditProfile, error) {
				if test.resolverErr != nil {
					return bkntrace.OperationAuditProfile{}, test.resolverErr
				}
				return profile, nil
			}
			handler := &restHandler{
				as:                  traceOutboxAuthStub{visitor: hydra.Visitor{ID: "user-a", Type: hydra.VisitorType("user")}},
				auditQueryStore:     test.store,
				auditAccessResolver: resolver,
			}
			engine := gin.New()
			engine.Use(rest.LanguageMiddleware(), rest.PrivateNoCacheMiddleware())
			engine.GET("/api/bkn-backend/v1/operation-audits", handler.ListOperationAudits)
			engine.GET("/api/bkn-backend/v1/operation-audits/:event_id", handler.GetOperationAudit)

			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", "Bearer token")
			request.Header.Set("x-tenant-id", "tenant-a")
			request.Header.Set("x-business-domain", "domain-a")
			request.Header.Set(rest.AcceptLanguageHeader, test.language)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if got := response.Header().Get(rest.ContentLanguageHeader); got != test.language {
				t.Fatalf("Content-Language = %q, want %q", got, test.language)
			}
			if got := response.Header().Get("Cache-Control"); got != "private, no-cache" {
				t.Fatalf("Cache-Control = %q, want private, no-cache", got)
			}
			var body struct {
				ErrorCode    string `json:"error_code"`
				Description  string `json:"description"`
				ErrorDetails any    `json:"error_details"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if body.ErrorCode != test.wantCode || body.Description != test.wantDescription {
				t.Errorf("error = %#v, want code=%q description=%q", body, test.wantCode, test.wantDescription)
			}
			if test.wantCode == berrors.BknBackend_OperationAudit_InvalidRange {
				details, ok := body.ErrorDetails.(map[string]any)
				if !ok || details["format"] != "RFC3339" || details["max_duration"] != "720h0m0s" {
					t.Errorf("error_details = %#v, want stable range details", body.ErrorDetails)
				}
			}
		})
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
