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
