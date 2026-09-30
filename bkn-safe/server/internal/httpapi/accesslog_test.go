// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/accesslog"
)

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
