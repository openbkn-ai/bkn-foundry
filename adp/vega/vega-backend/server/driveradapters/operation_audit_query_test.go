// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common/operationaudit"
	verrors "vega-backend/errors"
	vmock "vega-backend/interfaces/mock"
)

type operationAuditQueryStoreStub struct {
	found   bool
	listErr error
	getErr  error
}

func (s *operationAuditQueryStoreStub) List(_ context.Context, _ operationaudit.Filter) (operationaudit.Page, error) {
	if s.listErr != nil {
		return operationaudit.Page{}, s.listErr
	}
	return operationaudit.Page{}, nil
}

func (s *operationAuditQueryStoreStub) Get(_ context.Context, _ string, _, _ string) (operationaudit.Entry, bool, error) {
	if s.getErr != nil {
		return operationaudit.Entry{}, false, s.getErr
	}
	return operationaudit.Entry{}, s.found, nil
}

func TestOperationAuditErrorsAreLocalized(t *testing.T) {
	restoreGinMode := setGinMode()
	defer restoreGinMode()

	tests := []struct {
		name            string
		language        string
		path            string
		store           operationAuditQueryStore
		roles           []string
		wantStatus      int
		wantCode        string
		wantDescription string
	}{
		{
			name: "Chinese invalid range", language: "zh-CN",
			path:  "/api/vega-backend/v1/operation-audits?from=invalid&to=2026-08-08T00:00:00Z",
			store: &operationAuditQueryStoreStub{}, roles: []string{"audit"}, wantStatus: http.StatusBadRequest,
			wantCode: verrors.VegaBackend_OperationAudit_InvalidRange, wantDescription: "操作审计查询时间范围无效。",
		},
		{
			name: "English invalid range", language: "en-US",
			path:  "/api/vega-backend/v1/operation-audits?from=invalid&to=2026-08-08T00:00:00Z",
			store: &operationAuditQueryStoreStub{}, roles: []string{"audit"}, wantStatus: http.StatusBadRequest,
			wantCode: verrors.VegaBackend_OperationAudit_InvalidRange, wantDescription: "The operation-audit time range is invalid.",
		},
		{
			name: "English source unavailable", language: "en-US",
			path:  "/api/vega-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z",
			roles: []string{"audit"}, wantStatus: http.StatusServiceUnavailable,
			wantCode: verrors.VegaBackend_OperationAudit_ServiceUnavailable, wantDescription: "The operation-audit service is unavailable.",
		},
		{
			name: "Chinese source unavailable", language: "zh-CN",
			path:  "/api/vega-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z",
			roles: []string{"audit"}, wantStatus: http.StatusServiceUnavailable,
			wantCode: verrors.VegaBackend_OperationAudit_ServiceUnavailable, wantDescription: "操作审计服务暂不可用。",
		},
		{
			name: "Chinese invalid cursor", language: "zh-CN",
			path:  "/api/vega-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z&before_time=invalid",
			store: &operationAuditQueryStoreStub{}, roles: []string{"audit"}, wantStatus: http.StatusBadRequest,
			wantCode: verrors.VegaBackend_OperationAudit_InvalidBeforeTime, wantDescription: "操作审计游标时间无效。",
		},
		{
			name: "English invalid cursor", language: "en-US",
			path:  "/api/vega-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z&before_time=invalid",
			store: &operationAuditQueryStoreStub{}, roles: []string{"audit"}, wantStatus: http.StatusBadRequest,
			wantCode: verrors.VegaBackend_OperationAudit_InvalidBeforeTime, wantDescription: "The operation-audit cursor time is invalid.",
		},
		{
			name: "English query failure", language: "en-US",
			path:  "/api/vega-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z",
			store: &operationAuditQueryStoreStub{listErr: errors.New("store unavailable")}, roles: []string{"audit"}, wantStatus: http.StatusInternalServerError,
			wantCode: verrors.VegaBackend_OperationAudit_QueryFailed, wantDescription: "The operation-audit query failed.",
		},
		{
			name: "Chinese query failure", language: "zh-CN",
			path:  "/api/vega-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z",
			store: &operationAuditQueryStoreStub{listErr: errors.New("store unavailable")}, roles: []string{"audit"}, wantStatus: http.StatusInternalServerError,
			wantCode: verrors.VegaBackend_OperationAudit_QueryFailed, wantDescription: "操作审计查询失败。",
		},
		{
			name: "Chinese not found", language: "zh-CN", path: "/api/vega-backend/v1/operation-audits/event-missing",
			store: &operationAuditQueryStoreStub{}, roles: []string{"audit"}, wantStatus: http.StatusNotFound,
			wantCode: verrors.VegaBackend_OperationAudit_NotFound, wantDescription: "未找到操作审计事件。",
		},
		{
			name: "English not found", language: "en-US", path: "/api/vega-backend/v1/operation-audits/event-missing",
			store: &operationAuditQueryStoreStub{}, roles: []string{"audit"}, wantStatus: http.StatusNotFound,
			wantCode: verrors.VegaBackend_OperationAudit_NotFound, wantDescription: "The operation-audit event was not found.",
		},
		{
			name: "English access denied", language: "en-US",
			path:  "/api/vega-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z",
			store: &operationAuditQueryStoreStub{}, roles: []string{"user"}, wantStatus: http.StatusForbidden,
			wantCode: verrors.VegaBackend_OperationAudit_AccessDenied, wantDescription: "You are not permitted to view operation-audit records.",
		},
		{
			name: "Chinese access denied", language: "zh-CN",
			path:  "/api/vega-backend/v1/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z",
			store: &operationAuditQueryStoreStub{}, roles: []string{"user"}, wantStatus: http.StatusForbidden,
			wantCode: verrors.VegaBackend_OperationAudit_AccessDenied, wantDescription: "您没有查看操作审计记录的权限。",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, safeServer := newOperationAuditQueryHandler(t, test.store, test.roles)
			defer safeServer.Close()
			engine := gin.New()
			engine.Use(rest.LanguageMiddleware(), rest.PrivateNoCacheMiddleware())
			engine.GET("/api/vega-backend/v1/operation-audits", handler.ListOperationAudits)
			engine.GET("/api/vega-backend/v1/operation-audits/:event_id", handler.GetOperationAudit)

			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", "Bearer token")
			request.Header.Set("x-tenant-id", "tenant-a")
			request.Header.Set("x-business-domain", "domain-a")
			request.Header.Set(rest.AcceptLanguageHeader, test.language)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			require.Equal(t, test.wantStatus, response.Code, response.Body.String())
			require.Equal(t, test.language, response.Header().Get(rest.ContentLanguageHeader))
			require.Equal(t, "private, no-cache", response.Header().Get("Cache-Control"))
			var body struct {
				ErrorCode    string `json:"error_code"`
				Description  string `json:"description"`
				ErrorDetails any    `json:"error_details"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Equal(t, test.wantCode, body.ErrorCode)
			require.Equal(t, test.wantDescription, body.Description)
			if test.wantCode == verrors.VegaBackend_OperationAudit_InvalidRange {
				details, ok := body.ErrorDetails.(map[string]any)
				require.True(t, ok)
				require.Equal(t, "RFC3339", details["format"])
				require.Equal(t, "720h0m0s", details["max_duration"])
			}
		})
	}
}

func newOperationAuditQueryHandler(t *testing.T, store operationAuditQueryStore, roles []string) (*restHandler, *httptest.Server) {
	t.Helper()
	controller := gomock.NewController(t)
	t.Cleanup(controller.Finish)
	authService := vmock.NewMockAuthService(controller)
	authService.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{ID: "user-a", Type: hydra.VisitorType_User}, nil)
	safeServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/api/safe/v1/me", request.URL.Path)
		require.Equal(t, "Bearer token", request.Header.Get("Authorization"))
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ID":"user-a","Enabled":true,"Roles":` + mustJSON(t, roles) + `}`))
	}))
	t.Setenv("BKN_SAFE_URL", safeServer.URL)
	return &restHandler{as: authService, auditQueryStore: store}, safeServer
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}
