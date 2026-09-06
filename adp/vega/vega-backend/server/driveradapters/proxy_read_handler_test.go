// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

type fakeProxyAuthorizationService struct {
	err      error
	requests []interfaces.ProxyReadContext
}

func (f *fakeProxyAuthorizationService) Authorize(_ context.Context, request interfaces.ProxyReadContext) error {
	f.requests = append(f.requests, request)
	return f.err
}

type capturedProxyAudit struct {
	events []proxyReadAuditEvent
}

func (a *capturedProxyAudit) RecordProxyRead(_ context.Context, event proxyReadAuditEvent) {
	a.events = append(a.events, event)
}

func setupProxyReadHandlerTest(
	t *testing.T, authorizationErr error,
) (*gin.Engine, *restHandler, *vmock.MockResourceService, *vmock.MockResourceDataService, *fakeProxyAuthorizationService, *capturedProxyAudit) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	rs := vmock.NewMockResourceService(ctrl)
	rds := vmock.NewMockResourceDataService(ctrl)
	pas := &fakeProxyAuthorizationService{err: authorizationErr}
	audit := &capturedProxyAudit{}
	handler := &restHandler{rs: rs, rds: rds, pas: pas, proxyAuditRecorder: audit}
	engine := gin.New()
	engine.Use(gin.Recovery(), handler.TraceContextMiddleware())
	engine.GET("/api/vega-backend/in/v1/proxy/resources/:id/schema", handler.GetResourceSchemaByProxy)
	engine.POST("/api/vega-backend/in/v1/proxy/resources/:id/data", handler.PostResourceDataByProxy)
	return engine, handler, rs, rds, pas, audit
}

func setProxyReadHeaders(req *http.Request, operation, childType string) {
	req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "proxy-1")
	req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, interfaces.ProxyAccountTypeApp)
	req.Header.Set(interfaces.HTTPHeaderBKNCallerID, "caller-1")
	req.Header.Set(interfaces.HTTPHeaderBKNCallerType, "user")
	req.Header.Set(interfaces.HTTPHeaderBKNKnowledgeID, "kn-1")
	req.Header.Set(interfaces.HTTPHeaderBKNChildType, childType)
	req.Header.Set(interfaces.HTTPHeaderBKNChildID, "child-1")
	req.Header.Set(interfaces.HTTPHeaderBKNProxyVersion, "3")
	req.Header.Set(interfaces.HTTPHeaderBKNTargetType, interfaces.ProxyTargetTypeResource)
	req.Header.Set(interfaces.HTTPHeaderBKNTargetID, "resource-1")
	req.Header.Set(interfaces.HTTPHeaderBKNOperation, operation)
	req.Header.Set(interfaces.HTTPHeaderBKNExecutionID, "execution-1")
}

func TestProxyChildMayRead(t *testing.T) {
	for _, test := range []struct {
		name      string
		childType string
		operation string
		allowed   bool
	}{
		{name: "object type can view schema", childType: interfaces.ProxyChildTypeObjectType, operation: interfaces.OPERATION_TYPE_VIEW_DETAIL, allowed: true},
		{name: "object type can query data", childType: interfaces.ProxyChildTypeObjectType, operation: interfaces.OPERATION_TYPE_QUERY_DATA, allowed: true},
		{name: "relation type can query data", childType: interfaces.ProxyChildTypeRelationType, operation: interfaces.OPERATION_TYPE_QUERY_DATA, allowed: true},
		{name: "metric can query data", childType: interfaces.ProxyChildTypeMetric, operation: interfaces.OPERATION_TYPE_QUERY_DATA, allowed: true},
		{name: "relation type cannot view schema", childType: interfaces.ProxyChildTypeRelationType, operation: interfaces.OPERATION_TYPE_VIEW_DETAIL},
		{name: "metric cannot view schema", childType: interfaces.ProxyChildTypeMetric, operation: interfaces.OPERATION_TYPE_VIEW_DETAIL},
		{name: "unknown child cannot query", childType: "unknown", operation: interfaces.OPERATION_TYPE_QUERY_DATA},
		{name: "no child can execute an unlisted operation", childType: interfaces.ProxyChildTypeObjectType, operation: "execute"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.allowed, proxyChildMayRead(test.childType, test.operation))
		})
	}
}

func TestRestHandlerGetResourceSchemaByProxy(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	engine, _, rs, _, pas, audit := setupProxyReadHandlerTest(t, nil)
	resource := &interfaces.Resource{
		ID: "resource-1", Name: "secret-name",
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
	}
	rs.EXPECT().InternalGetByID(gomock.Any(), gomock.Nil(), "resource-1").
		DoAndReturn(func(ctx context.Context, _ any, _ string) (*interfaces.Resource, error) {
			account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
			require.True(t, ok)
			assert.Equal(t, interfaces.AccountInfo{ID: "proxy-1", Type: "app"}, account)
			return resource, nil
		})

	req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/in/v1/proxy/resources/resource-1/schema", nil)
	setProxyReadHeaders(req, interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.ProxyChildTypeObjectType)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"schema_definition"`)
	assert.NotContains(t, w.Body.String(), "secret-name")
	require.Len(t, pas.requests, 1)
	require.Len(t, audit.events, 1)
	assert.Equal(t, "allow", audit.events[0].Decision)
	assert.Equal(t, "caller-1", audit.events[0].CallerID)
	assert.Equal(t, "kn-1", audit.events[0].KnowledgeID)
	assert.Equal(t, "child-1", audit.events[0].ChildID)
	assert.Equal(t, "proxy-1", audit.events[0].ProxyAccountID)
	assert.Equal(t, uint64(3), audit.events[0].ProxyVersion)
	assert.Equal(t, "resource-1", audit.events[0].ResourceID)
	assert.Equal(t, interfaces.OPERATION_TYPE_VIEW_DETAIL, audit.events[0].Operation)
}

func TestRestHandlerPostResourceDataByProxy(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	t.Run("queries data after final proxy PEP", func(t *testing.T) {
		engine, _, rs, rds, pas, audit := setupProxyReadHandlerTest(t, nil)
		resource := sampleDatasetResource()
		resource.ID = "resource-1"
		rs.EXPECT().InternalGetByID(gomock.Any(), gomock.Nil(), "resource-1").Return(resource, nil)
		rds.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).
			DoAndReturn(func(ctx context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				account := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
				assert.Equal(t, "proxy-1", account.ID)
				assert.Equal(t, 2, params.Limit)
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": "row-1"}}, TotalCount: 1}, nil
			})

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/proxy/resources/resource-1/data", strings.NewReader(`{"paging":{"limit":2},"need_total":true}`))
		setProxyReadHeaders(req, interfaces.OPERATION_TYPE_QUERY_DATA, interfaces.ProxyChildTypeMetric)
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"id":"row-1"`)
		require.Len(t, pas.requests, 1)
		require.Len(t, audit.events, 1)
		assert.Equal(t, "allow", audit.events[0].Decision)
	})

	t.Run("rejects a target mismatch before authorization or physical read", func(t *testing.T) {
		engine, _, _, _, pas, audit := setupProxyReadHandlerTest(t, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/proxy/resources/resource-2/data", strings.NewReader(`{}`))
		setProxyReadHeaders(req, interfaces.OPERATION_TYPE_QUERY_DATA, interfaces.ProxyChildTypeObjectType)
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusForbidden, w.Code)
		assert.Empty(t, pas.requests)
		require.Len(t, audit.events, 1)
		assert.Equal(t, "deny", audit.events[0].Decision)
		assert.Equal(t, "invalid_trusted_context", audit.events[0].Reason)
	})

	t.Run("cannot dispatch a write override", func(t *testing.T) {
		engine, _, _, _, pas, audit := setupProxyReadHandlerTest(t, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/proxy/resources/resource-1/data", strings.NewReader(`[]`))
		setProxyReadHeaders(req, interfaces.OPERATION_TYPE_QUERY_DATA, interfaces.ProxyChildTypeObjectType)
		req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodPost)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		require.Equal(t, http.StatusForbidden, w.Code)
		assert.Empty(t, pas.requests)
		require.Len(t, audit.events, 1)
		assert.Equal(t, "deny", audit.events[0].Decision)
	})

	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantReason string
	}{
		{name: "proxy or policy denied", err: interfaces.ErrProxyAuthorizationDenied, wantStatus: http.StatusForbidden, wantReason: "proxy_or_policy_denied"},
		{name: "bkn-safe unavailable", err: interfaces.ErrProxyAuthorizationUnavailable, wantStatus: http.StatusServiceUnavailable, wantReason: "authorization_unavailable"},
		{name: "unexpected authorization failure", err: errors.New("network failed"), wantStatus: http.StatusServiceUnavailable, wantReason: "authorization_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, _, _, _, pas, audit := setupProxyReadHandlerTest(t, test.err)
			req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/proxy/resources/resource-1/data", strings.NewReader(`{}`))
			setProxyReadHeaders(req, interfaces.OPERATION_TYPE_QUERY_DATA, interfaces.ProxyChildTypeRelationType)
			req.Header.Set(interfaces.HTTP_HEADER_METHOD_OVERRIDE, http.MethodGet)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			require.Equal(t, test.wantStatus, w.Code)
			require.Len(t, pas.requests, 1)
			require.Len(t, audit.events, 1)
			assert.Equal(t, "deny", audit.events[0].Decision)
			assert.Equal(t, test.wantReason, audit.events[0].Reason)
		})
	}
}

func TestRestHandlerProxyRouteWhitelist(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	engine := gin.New()
	(&restHandler{}).RegisterPublic(engine)

	routes := map[string]bool{}
	for _, route := range engine.Routes() {
		if strings.Contains(route.Path, "/in/v1/proxy/") {
			routes[route.Method+" "+route.Path] = true
		}
	}
	assert.Equal(t, map[string]bool{
		"GET /api/vega-backend/in/v1/proxy/resources/:id/schema": true,
		"POST /api/vega-backend/in/v1/proxy/resources/:id/data":  true,
	}, routes)
}

func TestRestHandlerPublicAPIRejectsForgedProxyIdentity(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	as := vmock.NewMockAuthService(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	handler := &restHandler{as: as, rs: rs}
	engine := gin.New()
	public := engine.Group("/api/vega-backend/v1", handler.stripProxyInternalHeaders())
	public.GET("/resources/:id", handler.GetResourcesByEx)

	as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).
		Return(hydra.Visitor{ID: "caller-1", Type: hydra.VisitorType_User}, nil)
	rs.EXPECT().GetByIDs(gomock.Any(), []string{"resource-1"}).
		DoAndReturn(func(ctx context.Context, _ []string) ([]*interfaces.Resource, error) {
			account := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
			assert.Equal(t, interfaces.AccountInfo{ID: "caller-1", Type: "user"}, account)
			return []*interfaces.Resource{{ID: "resource-1"}}, nil
		})

	req := httptest.NewRequest(http.MethodGet, "/api/vega-backend/v1/resources/resource-1", nil)
	setProxyReadHeaders(req, interfaces.OPERATION_TYPE_QUERY_DATA, interfaces.ProxyChildTypeObjectType)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	for _, header := range proxyInternalHeaders {
		assert.Empty(t, req.Header.Get(header), header)
	}
}
