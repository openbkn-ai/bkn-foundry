// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package adminwrite

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/httperrors"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestWriteErrUsesStableLocalizedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		err         error
		status      int
		code        string
		description map[string]string
	}{
		{
			name: "invalid", err: errors.Join(ErrInvalid, errors.New("unsafe detail")),
			status: http.StatusBadRequest, code: httperrors.AdminWriteInvalid,
			description: map[string]string{"zh-CN": "管理写入请求无效。", "en-US": "The admin write request is invalid."},
		},
		{
			name: "not found", err: ErrNotFound,
			status: http.StatusNotFound, code: httperrors.NotFound,
			description: map[string]string{"zh-CN": "请求的资源不存在。", "en-US": "The requested resource was not found."},
		},
		{
			name: "forbidden", err: ErrForbidden,
			status: http.StatusForbidden, code: httperrors.Forbidden,
			description: map[string]string{"zh-CN": "没有执行此操作的权限。", "en-US": "You are not permitted to perform this operation."},
		},
		{
			name: "immutable", err: ErrImmutable,
			status: http.StatusForbidden, code: httperrors.AdminWriteImmutable,
			description: map[string]string{"zh-CN": "内置对象不能修改。", "en-US": "Built-in objects cannot be modified."},
		},
		{
			name: "no updatable fields", err: ErrNoUpdatableFields,
			status: http.StatusBadRequest, code: httperrors.AdminWriteNoUpdatableFields,
			description: map[string]string{"zh-CN": "未提供可更新字段。", "en-US": "No updatable fields were provided."},
		},
		{
			name: "wildcard grant", err: errors.Join(ErrWildcardGrant, errors.New("wildcard detail")),
			status: http.StatusBadRequest, code: httperrors.AdminWriteWildcardGrantForbidden,
			description: map[string]string{"zh-CN": "不能授予通配资源类型或操作。", "en-US": "Wildcard resource types and operations cannot be granted."},
		},
		{
			name: "admin console permission", err: ErrAdminConsolePermission,
			status: http.StatusForbidden, code: httperrors.AdminWriteAdminConsolePermissionForbidden,
			description: map[string]string{"zh-CN": "不能通过角色权限授予管理控制台能力。", "en-US": "The admin console capability cannot be granted through role permissions."},
		},
		{
			name: "internal", err: errors.New("database detail"),
			status: http.StatusInternalServerError, code: httperrors.InternalError,
			description: map[string]string{"zh-CN": "服务内部错误。", "en-US": "An internal service error occurred."},
		},
	}

	for _, testCase := range testCases {
		for _, language := range []string{"zh-CN", "en-US"} {
			t.Run(testCase.name+"/"+language, func(t *testing.T) {
				router := gin.New()
				router.Use(sharedrest.LanguageMiddleware())
				router.GET("/error", func(c *gin.Context) { writeErr(c, testCase.err) })

				request := httptest.NewRequest(http.MethodGet, "/error", nil)
				request.Header.Set(sharedrest.AcceptLanguageHeader, language)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)

				if response.Code != testCase.status {
					t.Fatalf("status = %d, want %d", response.Code, testCase.status)
				}
				if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != language {
					t.Fatalf("Content-Language = %q, want %q", got, language)
				}
				var body map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if got := body["error_code"]; got != testCase.code {
					t.Errorf("error_code = %q, want %q", got, testCase.code)
				}
				if got := body["description"]; got != testCase.description[language] {
					t.Errorf("description = %q, want %q", got, testCase.description[language])
				}
				if got := body["error"]; got != testCase.description[language] {
					t.Errorf("legacy error = %q, want localized description", got)
				}
				for _, internalDetail := range []string{"unsafe detail", "wildcard detail", "database detail"} {
					if strings.Contains(response.Body.String(), internalDetail) {
						t.Errorf("internal error detail %q leaked into the response", internalDetail)
					}
				}
			})
		}
	}
}
