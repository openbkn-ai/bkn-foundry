// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestOpenBKNLogoAssetIsCacheable(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	hydra := auth.NewHydraAdmin("http://hydra.invalid")
	registerAuth(router, auth.NewProvider(nil, hydra, nil), hydra, nil)

	request := httptest.NewRequest(http.MethodGet, openBKNLogoPath, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "image/svg+xml; charset=utf-8" {
		t.Errorf("Content-Type = %q, want image/svg+xml; charset=utf-8", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want long-lived immutable caching", got)
	}
	if got := response.Header().Get("ETag"); got != openBKNLogoETag {
		t.Errorf("ETag = %q, want %q", got, openBKNLogoETag)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(openBKNLogoSVG))
	if openBKNLogoETag != `"`+digest+`"` {
		t.Errorf("logo ETag does not match embedded SVG digest %q", digest)
	}
	if !strings.Contains(openBKNLogoPath, digest[:12]) {
		t.Errorf("logo path %q does not contain content fingerprint %q", openBKNLogoPath, digest[:12])
	}
	if !bytes.Equal(response.Body.Bytes(), openBKNLogoSVG) {
		t.Error("response body does not match the embedded OpenBKN logo")
	}
	for _, forbidden := range []string{"<script", "<image", "data:image", "foreignObject"} {
		if bytes.Contains(response.Body.Bytes(), []byte(forbidden)) {
			t.Errorf("SVG contains forbidden content %q", forbidden)
		}
	}

	request = httptest.NewRequest(http.MethodGet, openBKNLogoPath, nil)
	request.Header.Set("If-None-Match", openBKNLogoETag)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotModified {
		t.Fatalf("conditional request status = %d, want %d", response.Code, http.StatusNotModified)
	}
	if response.Body.Len() != 0 {
		t.Errorf("304 response body size = %d, want 0", response.Body.Len())
	}
}

func TestLoginBackgroundAssetIsCacheable(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	hydra := auth.NewHydraAdmin("http://hydra.invalid")
	registerAuth(router, auth.NewProvider(nil, hydra, nil), hydra, nil)

	request := httptest.NewRequest(http.MethodGet, loginBackgroundPath, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want long-lived immutable caching", got)
	}
	if got := response.Header().Get("ETag"); got != loginBackgroundETag {
		t.Errorf("ETag = %q, want %q", got, loginBackgroundETag)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(loginBackgroundJPG))
	if loginBackgroundETag != `"`+digest+`"` {
		t.Errorf("background ETag does not match embedded JPEG digest %q", digest)
	}
	if !strings.Contains(loginBackgroundPath, digest[:12]) {
		t.Errorf("background path %q does not contain content fingerprint %q", loginBackgroundPath, digest[:12])
	}
	if !bytes.Equal(response.Body.Bytes(), loginBackgroundJPG) {
		t.Error("response body does not match the embedded login background")
	}

	request = httptest.NewRequest(http.MethodGet, loginBackgroundPath, nil)
	request.Header.Set("If-None-Match", loginBackgroundETag)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotModified {
		t.Fatalf("conditional request status = %d, want %d", response.Code, http.StatusNotModified)
	}
	if response.Body.Len() != 0 {
		t.Errorf("304 response body size = %d, want 0", response.Body.Len())
	}
}

func TestLoginPageReferencesExternalAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(sharedrest.LanguageMiddleware())
	router.GET("/login", func(c *gin.Context) { showLogin(c, nil) })

	request := httptest.NewRequest(http.MethodGet, "/login?login_challenge=test", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, expected := range []string{
		`url("` + loginBackgroundPath + `")`,
		`src="` + openBKNLogoPath + `"`,
		`width="244"`,
		`height="84"`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
	if strings.Contains(body, "data:image") {
		t.Error("login page still embeds the logo as a data URI")
	}
	if response.Body.Len() >= 32*1024 {
		t.Errorf("login page size = %d bytes, want less than 32 KiB", response.Body.Len())
	}
}

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
			router.GET("/login", func(c *gin.Context) { showLogin(c, nil) })
			router.GET("/change-password", showChangePassword)
			router.GET("/device", showDevice)
			router.GET("/device/success", showDeviceSuccess)
			router.GET("/consent-preview", func(c *gin.Context) {
				applyAuthLocale(c, "")
				data := consentAuthPageData(c, "test")
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
				assertLocalizedAuthHeaders(t, response, language)
				body := response.Body.String()
				if !strings.Contains(body, `<html lang="`+language+`">`) {
					t.Errorf("GET %s html lang does not match %q", page.path, language)
				}
				for _, expected := range page.want[language] {
					if !strings.Contains(body, expected) {
						t.Errorf("GET %s body does not contain %q", page.path, expected)
					}
				}
			}
		})
	}
}

func TestChangePasswordLocaleLinksDoNotExposeAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(sharedrest.LanguageMiddleware())
	router.GET("/change-password", showChangePassword)

	request := httptest.NewRequest(http.MethodGet, "/change-password?login_challenge=test&account=user@example.com", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, expected := range []string{
		`href="/change-password?lang=zh-CN&amp;login_challenge=test"`,
		`href="/change-password?lang=en-US&amp;login_challenge=test"`,
		`<input type="hidden" name="account" value="user@example.com">`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
	if strings.Contains(body, `account=user`) || strings.Contains(body, `account%40`) {
		t.Error("change-password locale links expose the account in the query string")
	}
}

func TestShowLoginUsesOIDCUILocalesAndAllowsExplicitOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)

	hydraServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/auth/requests/login" {
			t.Errorf("hydra path = %q, want login request path", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("login_challenge"); got != "test-challenge" {
			t.Errorf("login_challenge = %q, want test-challenge", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"challenge": "test-challenge",
			"client": map[string]any{
				"client_id": "openbkn-studio",
			},
			"request_url":                     "https://openbkn.example/oauth2/auth?client_id=openbkn-studio&ui_locales=en-US%20zh-CN",
			"requested_access_token_audience": []string{},
			"requested_scope":                 []string{"openid"},
			"skip":                            false,
			"subject":                         "",
		})
	}))
	defer hydraServer.Close()

	router := gin.New()
	router.Use(sharedrest.LanguageMiddleware())
	router.GET("/login", func(c *gin.Context) { showLogin(c, auth.NewHydraAdmin(hydraServer.URL)) })

	for _, testCase := range []struct {
		name         string
		path         string
		wantLanguage string
		wantText     string
	}{
		{
			name:         "OIDC locale overrides request header",
			path:         "/login?login_challenge=test-challenge",
			wantLanguage: "en-US",
			wantText:     `placeholder="Account"`,
		},
		{
			name:         "explicit selector overrides OIDC locale",
			path:         "/login?login_challenge=test-challenge&lang=zh-CN",
			wantLanguage: "zh-CN",
			wantText:     `placeholder="账号"`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			request.Header.Set(sharedrest.AcceptLanguageHeader, "zh-CN")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
			}
			assertLocalizedAuthHeaders(t, response, testCase.wantLanguage)
			body := response.Body.String()
			for _, expected := range []string{
				`<html lang="` + testCase.wantLanguage + `">`,
				`name="lang" value="` + testCase.wantLanguage + `"`,
				testCase.wantText,
				`href="/login?lang=zh-CN&amp;login_challenge=test-challenge"`,
				`href="/login?lang=en-US&amp;login_challenge=test-challenge"`,
			} {
				if !strings.Contains(body, expected) {
					t.Errorf("body does not contain %q", expected)
				}
			}
			assertAuthLocaleCookie(t, response, testCase.wantLanguage)
		})
	}
}

func TestShowLoginUsesSharedLocaleCookieBeforeAcceptLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(sharedrest.LanguageMiddleware())
	router.GET("/login", func(c *gin.Context) { showLogin(c, nil) })

	request := httptest.NewRequest(http.MethodGet, "/login?login_challenge=test", nil)
	request.Header.Set(sharedrest.AcceptLanguageHeader, "zh-CN")
	request.AddCookie(&http.Cookie{Name: authLocaleCookieName, Value: "en-US"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	assertLocalizedAuthHeaders(t, response, "en-US")
	if !strings.Contains(response.Body.String(), `placeholder="Account"`) {
		t.Error("locale cookie did not select the English login page")
	}

	request = httptest.NewRequest(http.MethodGet, "/login?login_challenge=test&lang=fr-FR", nil)
	request.Header.Set(sharedrest.AcceptLanguageHeader, "zh-CN")
	request.AddCookie(&http.Cookie{Name: authLocaleCookieName, Value: "en-US"})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("invalid locale status = %d, want %d", response.Code, http.StatusOK)
	}
	assertLocalizedAuthHeaders(t, response, "en-US")
	if !strings.Contains(response.Body.String(), `placeholder="Account"`) {
		t.Error("invalid explicit locale did not safely fall back to the locale cookie")
	}
}

func TestShowLoginFallsBackWhenHydraLocaleLookupFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	hydraServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer hydraServer.Close()

	router := gin.New()
	router.Use(sharedrest.LanguageMiddleware())
	router.GET("/login", func(c *gin.Context) { showLogin(c, auth.NewHydraAdmin(hydraServer.URL)) })

	request := httptest.NewRequest(http.MethodGet, "/login?login_challenge=test", nil)
	request.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	assertLocalizedAuthHeaders(t, response, "en-US")
	if !strings.Contains(response.Body.String(), `placeholder="Account"`) {
		t.Error("Hydra lookup failure blocked the Accept-Language fallback")
	}
}

func TestShowConsentRendersProviderScopes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	hydraServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/auth/requests/consent" {
			t.Errorf("hydra path = %q, want consent request path", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("consent_challenge"); got != "test-challenge" {
			t.Errorf("consent_challenge = %q, want test-challenge", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"challenge":       "test-challenge",
			"requested_scope": []string{"openid", "profile:read"},
			"subject":         "user-1",
			"client": map[string]any{
				"client_id": "third-party-client",
			},
		})
	}))
	defer hydraServer.Close()

	provider := auth.NewProvider(nil, auth.NewHydraAdmin(hydraServer.URL), nil)
	router := gin.New()
	router.Use(sharedrest.LanguageMiddleware())
	router.GET("/consent", func(c *gin.Context) { showConsent(c, provider) })

	request := httptest.NewRequest(http.MethodGet, "/consent?consent_challenge=test-challenge", nil)
	request.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	assertLocalizedAuthHeaders(t, response, "en-US")
	body := response.Body.String()
	for _, expected := range []string{"Authorize third-party-client", "openid", "profile:read"} {
		if !strings.Contains(body, expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
	if strings.Contains(body, "Basic sign-in") {
		t.Error("non-empty provider scopes rendered the empty-scope fallback")
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
				"lang":             {testCase.language},
				"account":          {"user"},
				"old_password":     {"old"},
				"new_password":     {"new"},
				"confirm_password": {"different"},
			}
			request := httptest.NewRequest(http.MethodPost, "/change-password", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set(sharedrest.AcceptLanguageHeader, oppositeAuthLanguage(testCase.language))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			assertLocalizedAuthHeaders(t, response, testCase.language)
			if !strings.Contains(response.Body.String(), testCase.message) {
				t.Errorf("body does not contain localized validation message %q", testCase.message)
			}
			if !strings.Contains(response.Body.String(), `name="lang" value="`+testCase.language+`"`) {
				t.Errorf("body does not preserve form language %q", testCase.language)
			}
		})
	}
}

func TestAuthMissingParameterTextUsesRequestLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name     string
		method   string
		path     string
		form     url.Values
		messages map[string]string
	}{
		{
			name: "login challenge", method: http.MethodGet, path: "/login",
			messages: map[string]string{"zh-CN": "缺少 login_challenge 参数。", "en-US": "The login_challenge parameter is required."},
		},
		{
			name: "consent challenge", method: http.MethodGet, path: "/consent",
			messages: map[string]string{"zh-CN": "缺少 consent_challenge 参数。", "en-US": "The consent_challenge parameter is required."},
		},
		{
			name: "device challenge", method: http.MethodPost, path: "/device", form: url.Values{"user_code": {"code"}},
			messages: map[string]string{"zh-CN": "缺少 device_challenge 参数。", "en-US": "The device_challenge parameter is required."},
		},
		{
			name: "user code", method: http.MethodPost, path: "/device", form: url.Values{"device_challenge": {"challenge"}},
			messages: map[string]string{"zh-CN": "缺少 user_code 参数。", "en-US": "The user_code parameter is required."},
		},
	}

	for _, testCase := range testCases {
		for _, language := range []string{"zh-CN", "en-US"} {
			t.Run(testCase.name+"/"+language, func(t *testing.T) {
				router := gin.New()
				router.Use(sharedrest.LanguageMiddleware())
				router.GET("/login", func(c *gin.Context) { showLogin(c, nil) })
				router.GET("/consent", func(c *gin.Context) { showConsent(c, nil) })
				router.POST("/device", func(c *gin.Context) { doDevice(c, nil) })

				body := strings.NewReader("")
				if testCase.form != nil {
					body = strings.NewReader(testCase.form.Encode())
				}
				request := httptest.NewRequest(testCase.method, testCase.path, body)
				if testCase.form != nil {
					request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				request.Header.Set(sharedrest.AcceptLanguageHeader, language)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)

				if response.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
				}
				assertLocalizedAuthHeaders(t, response, language)
				if got := response.Body.String(); got != testCase.messages[language] {
					t.Errorf("body = %q, want %q", got, testCase.messages[language])
				}
			})
		}
	}
}

func assertLocalizedAuthHeaders(t *testing.T, response *httptest.ResponseRecorder, language string) {
	t.Helper()
	if got := response.Header().Get(sharedrest.ContentLanguageHeader); got != language {
		t.Fatalf("Content-Language = %q, want %q", got, language)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if vary := strings.Join(response.Header().Values("Vary"), ","); !strings.Contains(vary, sharedrest.AcceptLanguageHeader) {
		t.Fatalf("Vary = %q, want %s", vary, sharedrest.AcceptLanguageHeader)
	}
}

func assertAuthLocaleCookie(t *testing.T, response *httptest.ResponseRecorder, language string) {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name != authLocaleCookieName {
			continue
		}
		if cookie.Value != language {
			t.Fatalf("%s cookie = %q, want %q", authLocaleCookieName, cookie.Value, language)
		}
		if cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode || cookie.HttpOnly {
			t.Fatalf("unexpected locale cookie attributes: %#v", cookie)
		}
		return
	}
	t.Fatalf("response did not set %s cookie", authLocaleCookieName)
}

func oppositeAuthLanguage(language string) string {
	if language == sharedrest.SimplifiedChinese {
		return sharedrest.AmericanEnglish
	}
	return sharedrest.SimplifiedChinese
}
