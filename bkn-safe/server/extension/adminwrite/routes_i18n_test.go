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
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/httperrors"
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
			description: map[string]string{"zh-CN": "管理写入请求无效。请检查请求的角色或权限变更。", "en-US": "The admin write request is invalid. Check the requested role or permission change."},
		},
		{
			name: "not found", err: ErrNotFound,
			status: http.StatusNotFound, code: httperrors.NotFound,
			description: map[string]string{"zh-CN": "请求的资源不存在。请检查资源标识是否正确。", "en-US": "The requested resource was not found. Check that the resource identifier is correct."},
		},
		{
			name: "role name existed", err: errors.Join(ErrRoleNameExisted, errors.New("duplicate detail")),
			status: http.StatusConflict, code: httperrors.ResourceExisted,
			description: map[string]string{"zh-CN": "角色标识已存在。请使用其他角色标识。", "en-US": "The role identifier already exists. Use a different role identifier."},
		},
		{
			name: "forbidden", err: ErrForbidden,
			status: http.StatusForbidden, code: httperrors.Forbidden,
			description: map[string]string{"zh-CN": "没有执行此操作的权限。请联系管理员授予所需权限。", "en-US": "You are not permitted to perform this operation. Contact an administrator to obtain the required permission."},
		},
		{
			name: "immutable", err: ErrImmutable,
			status: http.StatusForbidden, code: httperrors.AdminWriteImmutable,
			description: map[string]string{"zh-CN": "内置对象不能修改。请改为创建或修改自定义对象。", "en-US": "Built-in objects cannot be modified. Create or modify a custom object instead."},
		},
		{
			name: "no updatable fields", err: ErrNoUpdatableFields,
			status: http.StatusBadRequest, code: httperrors.AdminWriteNoUpdatableFields,
			description: map[string]string{"zh-CN": "未提供可更新字段。请至少提供一个可更新字段。", "en-US": "No updatable fields were provided. Provide at least one field that can be updated."},
		},
		{
			name: "wildcard grant", err: errors.Join(ErrWildcardGrant, errors.New("wildcard detail")),
			status: http.StatusBadRequest, code: httperrors.AdminWriteWildcardGrantForbidden,
			description: map[string]string{"zh-CN": "不能授予通配资源类型或操作。请指定具体的资源类型和操作。", "en-US": "Wildcard resource types and operations cannot be granted. Specify a concrete resource type and operation."},
		},
		{
			name: "admin console permission", err: ErrAdminConsolePermission,
			status: http.StatusForbidden, code: httperrors.AdminWriteAdminConsolePermissionForbidden,
			description: map[string]string{"zh-CN": "不能通过角色权限授予管理控制台能力。请通过受管的管理员角色绑定授予此能力。", "en-US": "The admin console capability cannot be granted through role permissions. Use the managed administrator role binding for this capability."},
		},
		{
			name: "internal", err: errors.New("database detail"),
			status: http.StatusInternalServerError, code: httperrors.InternalError,
			description: map[string]string{"zh-CN": "服务内部错误。请稍后重试；如问题持续，请联系管理员。", "en-US": "An internal service error occurred. Try again later; contact an administrator if the problem persists."},
		},
	}

	for _, testCase := range testCases {
		for _, language := range []string{"zh-CN", "en-US"} {
			t.Run(testCase.name+"/"+language, func(t *testing.T) {
				router := gin.New()
				router.Use(rest.LanguageMiddleware())
				router.GET("/error", func(c *gin.Context) { writeErr(c, testCase.err) })

				request := httptest.NewRequest(http.MethodGet, "/error", nil)
				request.Header.Set(i18n.AcceptLanguageHeader, language)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)

				if response.Code != testCase.status {
					t.Fatalf("status = %d, want %d", response.Code, testCase.status)
				}
				if got := response.Header().Get(i18n.ContentLanguageHeader); got != language {
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
