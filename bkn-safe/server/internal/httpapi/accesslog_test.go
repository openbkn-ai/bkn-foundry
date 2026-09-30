// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
	registerLogout(r.Group("/api/safe/v1/me"), recorder, nil)
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
