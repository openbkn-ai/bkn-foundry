// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"

	"bkn-backend/common/bkntrace"
	"bkn-backend/common/operationaudit"
)

type operationAuditQueryStoreStub struct {
	filter operationaudit.Filter
	scope  operationaudit.Scope
	found  bool
}

func (s *operationAuditQueryStoreStub) List(_ context.Context, filter operationaudit.Filter) (operationaudit.Page, error) {
	s.filter = filter
	return operationaudit.Page{Entries: []operationaudit.Entry{{EventID: "evt-a", EventTime: time.Now().UTC()}}}, nil
}

func (s *operationAuditQueryStoreStub) Get(_ context.Context, _ string, scope operationaudit.Scope) (operationaudit.Entry, bool, error) {
	s.scope = scope
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
