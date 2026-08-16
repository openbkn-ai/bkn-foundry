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
			status: http.StatusBadRequest, code: "BknSafe.InvalidRequest",
			description: map[string]string{"zh-CN": "请求参数无效。", "en-US": "The request parameters are invalid."},
		},
		{
			name: "not found", err: ErrNotFound,
			status: http.StatusNotFound, code: "BknSafe.NotFound",
			description: map[string]string{"zh-CN": "请求的资源不存在。", "en-US": "The requested resource was not found."},
		},
		{
			name: "forbidden", err: ErrForbidden,
			status: http.StatusForbidden, code: "BknSafe.Forbidden",
			description: map[string]string{"zh-CN": "没有执行此操作的权限。", "en-US": "You are not permitted to perform this operation."},
		},
		{
			name: "internal", err: errors.New("database detail"),
			status: http.StatusInternalServerError, code: "BknSafe.InternalError",
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
				if strings.Contains(response.Body.String(), "unsafe detail") || strings.Contains(response.Body.String(), "database detail") {
					t.Error("internal error detail leaked into the response")
				}
			})
		}
	}
}
