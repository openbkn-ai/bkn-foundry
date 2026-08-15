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
	infra "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/common/operationaudit"
)

type executionAuditQueryStoreStub struct {
	found   bool
	listErr error
	getErr  error
}

func (s *executionAuditQueryStoreStub) List(_ context.Context, _ operationaudit.Filter) (operationaudit.Page, error) {
	if s.listErr != nil {
		return operationaudit.Page{}, s.listErr
	}
	return operationaudit.Page{}, nil
}

func (s *executionAuditQueryStoreStub) Get(_ context.Context, _, _, _ string) (operationaudit.Entry, bool, error) {
	if s.getErr != nil {
		return operationaudit.Entry{}, false, s.getErr
	}
	return operationaudit.Entry{}, s.found, nil
}

type executionAuditUserManagementStub struct {
	user *interfaces.UserInfo
	err  error
}

func (s executionAuditUserManagementStub) GetAppInfo(context.Context, string) (*interfaces.AppInfo, error) {
	return nil, nil
}

func (s executionAuditUserManagementStub) GetUserInfo(context.Context, string, ...string) (*interfaces.UserInfo, error) {
	return s.user, s.err
}

func (s executionAuditUserManagementStub) GetUsersInfo(context.Context, []string, []string) ([]*interfaces.UserInfo, error) {
	return nil, nil
}

func (s executionAuditUserManagementStub) GetUsersName(context.Context, []string) (map[string]string, error) {
	return nil, nil
}

func TestExecutionOperationAuditErrorsAreLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name            string
		language        string
		path            string
		authenticated   bool
		roles           []string
		store           operationAuditQueryStore
		wantStatus      int
		wantCode        string
		wantDescription string
	}{
		{
			name: "Chinese authentication required", language: "zh-CN",
			path: "/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z", wantStatus: http.StatusUnauthorized,
			wantCode: infraerrors.ErrExtOperationAuditAuthenticationRequired.String(), wantDescription: "查看操作审计记录需要身份认证。",
		},
		{
			name: "English authentication required", language: "en-US",
			path: "/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z", wantStatus: http.StatusUnauthorized,
			wantCode: infraerrors.ErrExtOperationAuditAuthenticationRequired.String(), wantDescription: "Authentication is required to view operation-audit records.",
		},
		{
			name: "Chinese access denied", language: "zh-CN", authenticated: true, roles: []string{"user"},
			path: "/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z", wantStatus: http.StatusForbidden,
			wantCode: infraerrors.ErrExtOperationAuditAccessDenied.String(), wantDescription: "您没有查看操作审计记录的权限。",
		},
		{
			name: "English access denied", language: "en-US", authenticated: true, roles: []string{"user"},
			path: "/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z", wantStatus: http.StatusForbidden,
			wantCode: infraerrors.ErrExtOperationAuditAccessDenied.String(), wantDescription: "You are not permitted to view operation-audit records.",
		},
		{
			name: "Chinese invalid range", language: "zh-CN", authenticated: true, roles: []string{"audit"}, store: &executionAuditQueryStoreStub{},
			path: "/operation-audits?from=invalid&to=2026-08-08T00:00:00Z", wantStatus: http.StatusBadRequest,
			wantCode: infraerrors.ErrExtOperationAuditInvalidRange.String(), wantDescription: "操作审计查询时间范围无效。",
		},
		{
			name: "English invalid range", language: "en-US", authenticated: true, roles: []string{"audit"}, store: &executionAuditQueryStoreStub{},
			path: "/operation-audits?from=invalid&to=2026-08-08T00:00:00Z", wantStatus: http.StatusBadRequest,
			wantCode: infraerrors.ErrExtOperationAuditInvalidRange.String(), wantDescription: "The operation-audit time range is invalid.",
		},
		{
			name: "Chinese query failed", language: "zh-CN", authenticated: true, roles: []string{"audit"}, store: &executionAuditQueryStoreStub{listErr: errors.New("store unavailable")},
			path: "/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z", wantStatus: http.StatusInternalServerError,
			wantCode: infraerrors.ErrExtOperationAuditQueryFailed.String(), wantDescription: "操作审计查询失败。",
		},
		{
			name: "English query failed", language: "en-US", authenticated: true, roles: []string{"audit"}, store: &executionAuditQueryStoreStub{listErr: errors.New("store unavailable")},
			path: "/operation-audits?from=2026-08-01T00:00:00Z&to=2026-08-08T00:00:00Z", wantStatus: http.StatusInternalServerError,
			wantCode: infraerrors.ErrExtOperationAuditQueryFailed.String(), wantDescription: "The operation-audit query failed.",
		},
		{
			name: "Chinese not found", language: "zh-CN", authenticated: true, roles: []string{"audit"}, store: &executionAuditQueryStoreStub{},
			path: "/operation-audits/event-missing", wantStatus: http.StatusNotFound,
			wantCode: infraerrors.ErrExtOperationAuditNotFound.String(), wantDescription: "未找到操作审计事件。",
		},
		{
			name: "English not found", language: "en-US", authenticated: true, roles: []string{"audit"}, store: &executionAuditQueryStoreStub{},
			path: "/operation-audits/event-missing", wantStatus: http.StatusNotFound,
			wantCode: infraerrors.ErrExtOperationAuditNotFound.String(), wantDescription: "The operation-audit event was not found.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := gin.New()
			engine.Use(sharedrest.LanguageMiddleware(), sharedrest.PrivateNoCacheMiddleware())
			if test.authenticated {
				engine.Use(func(c *gin.Context) {
					c.Request = c.Request.WithContext(infra.SetAccountAuthContextToCtx(c.Request.Context(), &interfaces.AccountAuthContext{AccountID: "user-a"}))
					c.Next()
				})
			}
			handler := &restPublicHandler{
				auditQueryStore:     test.store,
				auditUserManagement: executionAuditUserManagementStub{user: &interfaces.UserInfo{UserID: "user-a", Roles: test.roles}},
			}
			engine.GET("/operation-audits", handler.ListOperationAudits)
			engine.GET("/operation-audits/:event_id", handler.GetOperationAudit)

			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set(sharedrest.AcceptLanguageHeader, test.language)
			request.Header.Set("x-tenant-id", "tenant-a")
			request.Header.Set(string(interfaces.HeaderXBusinessDomain), "domain-a")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != test.language {
				t.Fatalf("Content-Language = %q, want %q", got, test.language)
			}
			if got := response.Header().Get("Cache-Control"); got != "private, no-cache" {
				t.Fatalf("Cache-Control = %q, want private, no-cache", got)
			}
			var body struct {
				Code        string `json:"code"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != expectedExecutionAuditErrorCode(test.wantStatus, test.wantCode) {
				t.Errorf("code = %q, want %q", body.Code, expectedExecutionAuditErrorCode(test.wantStatus, test.wantCode))
			}
			if body.Description != test.wantDescription {
				t.Errorf("description = %q, want %q", body.Description, test.wantDescription)
			}
		})
	}
}

func expectedExecutionAuditErrorCode(status int, code string) string {
	statusCode := map[int]string{
		http.StatusBadRequest:          "BadRequest",
		http.StatusUnauthorized:        "Unauthorized",
		http.StatusForbidden:           "Forbidden",
		http.StatusNotFound:            "NotFound",
		http.StatusInternalServerError: "InternalServerError",
	}[status]
	return "AgentOperatorIntegration." + statusCode + "." + code
}
