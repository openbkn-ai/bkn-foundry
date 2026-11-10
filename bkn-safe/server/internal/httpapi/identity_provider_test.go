package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/federation"
)

func TestTokenHookCommunityAndExtension(t *testing.T) {
	for _, tc := range []struct {
		name, key, header, body string
		status                  int
		modify, fail            bool
	}{
		{name: "community leaves session unchanged", key: "shared-hook-key", header: "shared-hook-key", body: `{"request":{"client_id":"openbkn-studio"}}`, status: 204},
		{name: "missing configured key", body: `{}`, status: 403},
		{name: "wrong caller key", key: "shared-hook-key", header: "bad", body: `{}`, status: 403},
		{name: "body bounded", key: "shared-hook-key", header: "shared-hook-key", body: strings.Repeat("a", (32<<10)+1), status: 400},
		{name: "private extension enriches session", key: "shared-hook-key", header: "shared-hook-key", body: `{}`, status: 200, modify: true},
		{name: "private extension fails closed", key: "shared-hook-key", header: "shared-hook-key", body: `{}`, status: 403, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			var ext *federation.Extension
			if tc.modify || tc.fail {
				ext = &federation.Extension{TokenHook: func(context.Context, []byte) (map[string]any, bool, error) {
					if tc.fail {
						return nil, false, errors.New("rejected")
					}
					return map[string]any{"visitor_type": "realname"}, true, nil
				}}
			}
			registerTokenHook(r, tc.key, ext)
			req := httptest.NewRequest(http.MethodPost, "/internal/sso/token-hook", strings.NewReader(tc.body))
			req.Header.Set("X-SSO-Hook-Key", tc.header)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status=%d, want=%d", w.Code, tc.status)
			}
			if tc.status == 200 && !strings.Contains(w.Body.String(), `"access_token":{"visitor_type":"realname"}`) {
				t.Fatal("missing claims")
			}
		})
	}
}

func TestLoginExtensionAndLocalBypass(t *testing.T) {
	for _, mode := range []string{"community", "choices", "redirect", "local"} {
		t.Run(mode, func(t *testing.T) {
			r := gin.New()
			var ext *federation.Extension
			called := false
			if mode != "community" {
				ext = &federation.Extension{BeforeLogin: func(*gin.Context) (federation.LoginPage, error) {
					called = true
					if mode == "redirect" {
						return federation.LoginPage{Redirect: "/external/start"}, nil
					}
					return federation.LoginPage{Options: []federation.LoginOption{{Name: "Customer IdP", URL: "/external/start"}}}, nil
				}}
			}
			registerAuth(r, nil, nil, nil, "", ext)
			target := "/login?login_challenge=challenge"
			if mode == "local" {
				target += "&local=1"
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
			if mode == "redirect" {
				if w.Code != 302 || w.Header().Get("Location") != "/external/start" {
					t.Fatal("missing extension redirect")
				}
				return
			}
			if w.Code != 200 || !strings.Contains(w.Body.String(), "<form") {
				t.Fatal("local login page unavailable")
			}
			if mode == "choices" && !strings.Contains(w.Body.String(), "Customer IdP") {
				t.Fatal("missing extension choice")
			}
			if mode == "local" && called {
				t.Fatal("local bypass invoked external identity provider")
			}
		})
	}
}

func TestCommunityHasNoPrivateIdentityRoutes(t *testing.T) {
	r := New(Deps{TokenHookKey: "shared-hook-key"})
	for _, path := range []string{"/sso/login/casdoor", "/sso/callback/casdoor", "/api/safe/v1/auth/token-exchange"} {
		method := http.MethodGet
		if strings.Contains(path, "token-exchange") {
			method = http.MethodPost
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != 404 {
			t.Fatalf("community exposes private route %s: %d", path, w.Code)
		}
	}
}
