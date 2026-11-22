// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/accesslog"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

type accessActorLookup struct{ user *model.User }

func (s accessActorLookup) ByID(context.Context, string) (*model.User, error) { return s.user, nil }
func (s accessActorLookup) SetPassword(context.Context, string, string) error { return nil }
func (s accessActorLookup) ByAccount(_ context.Context, account string) (*model.User, error) {
	if s.user != nil && s.user.Account == account {
		return s.user, nil
	}
	return nil, errors.New("not found")
}

func TestAccessLogReadEndpointIsRetired(t *testing.T) {
	r, _, _, _ := newAdminServer(t)

	response := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/access-logs", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("retired access-log endpoint status = %d, want %d: %s", response.Code, http.StatusNotFound, response.Body.String())
	}
}

type recordingAccessRecorder struct {
	entries []accesslog.Entry
	err     error
}

func (r *recordingAccessRecorder) Record(_ context.Context, entry accesslog.Entry) error {
	r.entries = append(r.entries, entry)
	return r.err
}

func newLogoutTestRouter(recorder accesslog.Recorder, actorID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(requestIDMiddleware())
	r.Use(func(c *gin.Context) {
		c.Set(ctxAccessorID, actorID)
		c.Next()
	})
	registerLogout(r.Group("/api/safe/v1/me"), logoutFunc(func(context.Context, string, string, string) (string, error) { return actorID, nil }), recorder, nil)
	return r
}

func TestVoluntaryLogoutPublishesAnAccessFact(t *testing.T) {
	recorder := &recordingAccessRecorder{}
	r := newLogoutTestRouter(recorder, adminSub)

	response := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/logout", nil, adminSub)
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if len(recorder.entries) != 1 {
		t.Fatalf("logout access facts = %d, want 1", len(recorder.entries))
	}
	entry := recorder.entries[0]
	if entry.ActorID != adminSub || entry.Action != "logout" || entry.Outcome != "success" || entry.AuthMethod != "oauth" {
		t.Fatalf("logout access fact = %#v, want oauth/logout/success", entry)
	}
	if entry.RequestID == "" || entry.RequestID != response.Header().Get("x-request-id") {
		t.Fatalf("logout request id = %q, response = %q", entry.RequestID, response.Header().Get("x-request-id"))
	}
}

func TestVoluntaryLogoutFailsOpenWhenAuditTransportRejectsFact(t *testing.T) {
	recorder := &recordingAccessRecorder{err: errors.New("queue full")}
	r := newLogoutTestRouter(recorder, "disabled-logout-user")

	response := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/logout", nil, "disabled-logout-user")
	if response.Code != http.StatusNoContent {
		t.Fatalf("fail-open logout status = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if len(recorder.entries) != 1 {
		t.Fatalf("fail-open logout access facts = %d, want 1", len(recorder.entries))
	}
}

func TestCredentialFailureUsesResolvedAccountOrProducesNoFact(t *testing.T) {
	known := &model.User{ID: "user-a", Account: "a", Name: "用户 A"}
	provider := auth.NewProvider(nil, nil, accessActorLookup{user: known})
	recorder := &recordingAccessRecorder{}
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/login", nil)

	recordLogin(context, provider, recorder, nil, "a", "failure", "invalid_credentials")
	if len(recorder.entries) != 1 {
		t.Fatalf("known credential failure facts = %d, want 1", len(recorder.entries))
	}
	entry := recorder.entries[0]
	if entry.ActorID != "user-a" || entry.ActorNameSnapshot != "用户 A" {
		t.Fatalf("known credential failure actor = %#v", entry)
	}

	recordLogin(context, provider, recorder, nil, "unknown", "failure", "invalid_credentials")
	if len(recorder.entries) != 1 {
		t.Fatalf("unknown credential attempt entered business stream: %#v", recorder.entries)
	}
}

// Logout identity is resolved by the revoker even when a previous attempt
// already revoked the bearer; unrelated auth middleware must not prevent retries.
type logoutFunc func(context.Context, string, string, string) (string, error)

func (f logoutFunc) Logout(ctx context.Context, access, refresh, idToken string) (string, error) {
	return f(ctx, access, refresh, idToken)
}

func TestLogoutFailureDoesNotPublishSuccess(t *testing.T) {
	for _, scenario := range []string{"unavailable", "invalid", "inactive", "no-recorder", "not-configured", "bad-json"} {
		t.Run(scenario, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			recorder := &recordingAccessRecorder{}
			var store accesslog.Recorder = recorder
			var revoker StudioSessionLogout = logoutFunc(func(_ context.Context, access, refresh, idToken string) (string, error) {
				if access != "old-access" || refresh != "current-refresh" {
					t.Error("credentials were not forwarded")
				}
				switch scenario {
				case "unavailable":
					return "", errors.New("unavailable")
				case "invalid":
					return "", auth.ErrLogoutToken
				case "inactive":
					return "", nil
				}
				return "user-1", nil
			})
			if scenario == "not-configured" {
				revoker = nil
			}
			if scenario == "no-recorder" {
				store = nil
			}
			registerLogout(r.Group("/api/safe/v1/me"), revoker, store, nil)
			payload := `{"refresh_token":"current-refresh"}`
			if scenario == "bad-json" {
				payload = `{`
			}
			request := httptest.NewRequest(http.MethodPost, "/api/safe/v1/me/logout", strings.NewReader(payload))
			request.Header.Set("Authorization", "Bearer old-access")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			r.ServeHTTP(response, request)
			want := http.StatusNoContent
			switch scenario {
			case "unavailable", "not-configured":
				want = http.StatusServiceUnavailable
			case "invalid":
				want = http.StatusUnauthorized
			case "bad-json":
				want = http.StatusBadRequest
			}
			if response.Code != want {
				t.Fatalf("status %d, want %d: %s", response.Code, want, response.Body)
			}
			if len(recorder.entries) != 0 {
				t.Fatalf("failed/inactive logout published success: %v", recorder.entries)
			}
		})
	}
}
