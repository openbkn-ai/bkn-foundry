// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestRelativeHydraRedirect(t *testing.T) {
	tests := []struct {
		name   string
		target string
		base   string
		want   string
	}{
		{
			name:   "canonical origin becomes relative",
			target: "https://public.example/oauth2/auth?login_verifier=a%2Fb",
			base:   "https://PUBLIC.example:443",
			want:   "/oauth2/auth?login_verifier=a%2Fb",
		},
		{
			name:   "alternate origin remains absolute",
			target: "https://identity.example/oauth2/auth?x=1",
			base:   "https://public.example",
			want:   "https://identity.example/oauth2/auth?x=1",
		},
		{
			name:   "different port remains absolute",
			target: "https://public.example:8443/oauth2/auth",
			base:   "https://public.example",
			want:   "https://public.example:8443/oauth2/auth",
		},
		{
			name:   "relative input unchanged",
			target: "/oauth2/auth?x=1",
			base:   "https://public.example",
			want:   "/oauth2/auth?x=1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := relativeHydraRedirect(test.target, test.base); got != test.want {
				t.Fatalf("relativeHydraRedirect() = %q, want %q", got, test.want)
			}
		})
	}
}

func hydraAcceptLoginError(t *testing.T, body string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	_, err := auth.NewHydraAdmin(srv.URL).AcceptLogin(context.Background(), "challenge", "subject", false)
	if err == nil {
		t.Fatal("AcceptLogin error = nil, want Hydra error")
	}
	return err
}

func TestIsExpiredLoginRequestRecognizesHydraExpiredChallenge(t *testing.T) {
	err := hydraAcceptLoginError(t, `{"error":"request_unauthorized","reason":"The login request has expired, please try again.","status_code":401}`)
	if !isExpiredLoginRequest(err) {
		t.Fatal("expired Hydra login request must be rendered as a restart-login prompt")
	}
}

func TestIsExpiredLoginRequestRejectsOtherHydraUnauthorizedResponses(t *testing.T) {
	err := hydraAcceptLoginError(t, `{"error":"request_unauthorized","reason":"The login request was already used.","status_code":401}`)
	if isExpiredLoginRequest(err) {
		t.Fatal("non-expired Hydra login request must not be rendered as a restart-login prompt")
	}
}

type fixedAuthenticator struct{ user *model.User }

func (a fixedAuthenticator) Verify(context.Context, string, string) (*model.User, error) {
	return a.user, nil
}

type passwordRecorder struct{ password string }

func (s *passwordRecorder) ByID(context.Context, string) (*model.User, error) { return nil, nil }
func (s *passwordRecorder) SetPassword(_ context.Context, _ string, password string) error {
	s.password = password
	return nil
}

func TestDisabledLoginMessageRequiresCorrectPassword(t *testing.T) {
	_, _, _, users := newAdminServer(t)
	if err := users.CreateLocalUser(t.Context(), &model.User{
		ID: "disabled-login", Account: "disabled-login", Enabled: false,
	}, "correct-password"); err != nil {
		t.Fatal(err)
	}
	provider := auth.NewProvider(users, nil, users)
	router := gin.New()
	router.Use(sharedrest.LanguageMiddleware())
	router.POST("/login", func(c *gin.Context) { doLogin(c, provider, nil) })
	for _, tc := range []struct{ locale, password, want string }{
		{"zh-CN", "correct-password", "账号已停用，请联系管理员"},
		{"en-US", "correct-password", "Your account has been disabled."},
		{"zh-CN", "wrong-password", "账号或密码错误"},
		{"en-US", "wrong-password", "The account or password is incorrect."},
	} {
		form := url.Values{"login_challenge": {"challenge"}, "account": {"disabled-login"}, "password": {tc.password}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept-Language", tc.locale)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), tc.want) {
			t.Fatalf("%s password=%s: %d %s", tc.locale, tc.password, response.Code, response.Body.String())
		}
		if tc.password != "correct-password" && strings.Contains(response.Body.String(), "账号已停用") {
			t.Fatal("wrong password must not reveal disabled status")
		}
	}
}

func TestChangePasswordExpiredChallengeShowsTerminalSuccessPage(t *testing.T) {
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"request_unauthorized","reason":"The login request has expired, please try again.","status_code":401}`))
	}))
	t.Cleanup(hydra.Close)

	store := &passwordRecorder{}
	provider := auth.NewProvider(fixedAuthenticator{user: &model.User{ID: "u-1", Account: "alice", Enabled: true}}, auth.NewHydraAdmin(hydra.URL), store)
	router := gin.New()
	router.Use(sharedrest.LanguageMiddleware())
	router.POST("/change-password", func(c *gin.Context) { doChangePassword(c, provider, nil) })

	form := url.Values{
		"login_challenge": {"expired"}, "account": {"alice"}, "old_password": {"old"},
		"new_password": {"new"}, "confirm_password": {"new"}, "lang": {"en-US"},
	}
	request := httptest.NewRequest(http.MethodPost, "/change-password", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if store.password != "new" {
		t.Fatalf("stored password = %q, want new", store.password)
	}
	if strings.Contains(response.Body.String(), "<form") {
		t.Fatal("expired challenge after a password change must not render a retry form")
	}
	if !strings.Contains(response.Body.String(), "Your password was updated") {
		t.Fatalf("body = %q, want terminal password-updated message", response.Body.String())
	}
}
