// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestAuthPagesUseRequestLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	pages := []struct {
		path string
		want map[string][]string
	}{
		{
			path: "/login?login_challenge=test",
			want: map[string][]string{
				"zh-CN": {`placeholder="账号"`, `>登录</button>`},
				"en-US": {`placeholder="Account"`, `>Sign in</button>`},
			},
		},
		{
			path: "/change-password?login_challenge=test&account=user",
			want: map[string][]string{
				"zh-CN": {">修改密码</h3>", "首次登录请设置新密码", `placeholder="当前密码"`},
				"en-US": {">Change password</h3>", "Set a new password for your first sign-in", `placeholder="Current password"`},
			},
		},
		{
			path: "/device?device_challenge=test",
			want: map[string][]string{
				"zh-CN": {">设备授权</h3>", `placeholder="输入设备码"`, "仅当你正从该设备发起登录"},
				"en-US": {">Device authorization</h3>", `placeholder="Enter device code"`, "Continue only if you started sign-in on this device"},
			},
		},
		{
			path: "/device/success",
			want: map[string][]string{
				"zh-CN": {">登录成功</h3>", "设备已授权"},
				"en-US": {">Sign-in successful</h3>", "The device is authorized"},
			},
		},
		{
			path: "/consent-preview",
			want: map[string][]string{
				"zh-CN": {"授权 Example App", "该应用将获得以下权限", "基础登录", ">同意授权</button>"},
				"en-US": {"Authorize Example App", "This application will receive the following permissions", "Basic sign-in", ">Authorize</button>"},
			},
		},
	}

	for _, language := range []string{"zh-CN", "en-US"} {
		t.Run(language, func(t *testing.T) {
			router := gin.New()
			router.Use(sharedrest.LanguageMiddleware())
			router.GET("/login", showLogin)
			router.GET("/change-password", showChangePassword)
			router.GET("/device", showDevice)
			router.GET("/device/success", showDeviceSuccess)
			router.GET("/consent-preview", func(c *gin.Context) {
				data := localizedAuthPageData(c)
				data.Challenge = "test"
				data.ClientName = "Example App"
				renderHTML(c, consentPage, data)
			})

			for _, page := range pages {
				request := httptest.NewRequest(http.MethodGet, page.path, nil)
				request.Header.Set(sharedrest.AcceptLanguageHeader, language)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)

				if response.Code != http.StatusOK {
					t.Fatalf("GET %s status = %d, want %d", page.path, response.Code, http.StatusOK)
				}
				if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != language {
					t.Fatalf("GET %s Content-Language = %q, want %q", page.path, got, language)
				}
				body := response.Body.String()
				for _, expected := range page.want[language] {
					if !strings.Contains(body, expected) {
						t.Errorf("GET %s body does not contain %q", page.path, expected)
					}
				}
			}
		})
	}
}

func TestAuthValidationMessagesUseRequestLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, testCase := range []struct {
		language string
		message  string
	}{
		{language: "zh-CN", message: "两次输入的新密码不一致或为空"},
		{language: "en-US", message: "The new passwords do not match or are empty."},
	} {
		t.Run(testCase.language, func(t *testing.T) {
			router := gin.New()
			router.Use(sharedrest.LanguageMiddleware())
			router.POST("/change-password", func(c *gin.Context) { doChangePassword(c, nil, nil) })

			form := url.Values{
				"login_challenge":  {"test"},
				"account":          {"user"},
				"old_password":     {"old"},
				"new_password":     {"new"},
				"confirm_password": {"different"},
			}
			request := httptest.NewRequest(http.MethodPost, "/change-password", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set(sharedrest.AcceptLanguageHeader, testCase.language)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != testCase.language {
				t.Fatalf("Content-Language = %q, want %q", got, testCase.language)
			}
			if !strings.Contains(response.Body.String(), testCase.message) {
				t.Errorf("body does not contain localized validation message %q", testCase.message)
			}
		})
	}
}

func TestAuthInvalidRequestTextUsesRequestLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, testCase := range []struct {
		language string
		message  string
	}{
		{language: "zh-CN", message: "请求参数无效。"},
		{language: "en-US", message: "The request parameters are invalid."},
	} {
		t.Run(testCase.language, func(t *testing.T) {
			router := gin.New()
			router.Use(sharedrest.LanguageMiddleware())
			router.GET("/login", showLogin)

			request := httptest.NewRequest(http.MethodGet, "/login", nil)
			request.Header.Set(sharedrest.AcceptLanguageHeader, testCase.language)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
			if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != testCase.language {
				t.Fatalf("Content-Language = %q, want %q", got, testCase.language)
			}
			if got := response.Body.String(); got != testCase.message {
				t.Errorf("body = %q, want %q", got, testCase.message)
			}
		})
	}
}
