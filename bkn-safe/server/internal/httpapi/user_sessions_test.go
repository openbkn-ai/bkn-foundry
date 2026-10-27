// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

type sessionRevokerFunc func(context.Context, string) error

func (f sessionRevokerFunc) RevokeUserSessions(ctx context.Context, id string) error {
	return f(ctx, id)
}

func TestDisableRevokesSessionsAndRejectsCachedReads(t *testing.T) {
	calls := 0
	var assertDisabled func(string)
	r, _, db, _ := newAdminServer(t, sessionRevokerFunc(func(_ context.Context, id string) error {
		calls++
		assertDisabled(id)
		if id != "session-user" {
			t.Errorf("revoked %q, want session-user", id)
		}
		return nil
	}))
	assertDisabled = func(id string) {
		var user model.User
		if err := db.First(&user, "id = ?", id).Error; err != nil || user.Enabled {
			t.Fatalf("account must be disabled during cleanup: enabled=%v err=%v", user.Enabled, err)
		}
	}
	if err := db.Create(&model.User{ID: "session-user", Account: "session-user", Name: "Session user", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/safe/v1/me", "/api/safe/v1/me/permissions"} {
		if w := tokReq(t, r, http.MethodGet, path, nil, "session-user"); w.Code != http.StatusOK {
			t.Fatalf("baseline %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, enabled := range []bool{false, false, true} {
		w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/session-user", map[string]any{"enabled": enabled})
		if w.Code != http.StatusNoContent {
			t.Fatalf("enabled=%v: %d %s", enabled, w.Code, w.Body.String())
		}
		var user model.User
		if err := db.First(&user, "id = ?", "session-user").Error; err != nil || user.Enabled != enabled {
			t.Fatalf("account state=%v, err=%v, want %v", user.Enabled, err, enabled)
		}
		if !enabled {
			for _, path := range []string{"/api/safe/v1/me", "/api/safe/v1/me/permissions", "/api/safe/v1/capabilities"} {
				if got := tokReq(t, r, http.MethodGet, path, nil, "session-user"); got.Code != http.StatusUnauthorized {
					t.Fatalf("disabled cached read %s: %d %s", path, got.Code, got.Body.String())
				}
			}
		}
	}
	if calls != 3 {
		t.Fatalf("revocations = %d, want disable, retry, and re-enable", calls)
	}
	var count int64
	if err := db.Model(&model.AuditLog{}).Where("target_id = ?", "session-user").Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("only disable and enable should be audited: count=%d err=%v", count, err)
	}
}

func TestSessionRevocationFailureKeepsAccountDisabled(t *testing.T) {
	r, _, db, _ := newAdminServer(t, sessionRevokerFunc(func(context.Context, string) error {
		return errors.New("hydra unavailable")
	}))
	if err := db.Create(&model.User{ID: "failure-user", Account: "failure-user", Name: "Failure user", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/failure-user", map[string]any{"enabled": enabled})
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("enabled=%v: %d %s", enabled, w.Code, w.Body.String())
		}
		var user model.User
		if err := db.First(&user, "id = ?", "failure-user").Error; err != nil || user.Enabled {
			t.Fatalf("failed cleanup must leave account disabled: enabled=%v err=%v", user.Enabled, err)
		}
	}
	var events []model.AuditLog
	if err := db.Where("target_id = ?", "failure-user").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Status != http.StatusNoContent || events[0].TargetName != "Failure user" {
		t.Fatalf("only the committed disable must be audited: %+v", events)
	}
}

func TestUserDisableRequiresConfiguredRevoker(t *testing.T) {
	r, _, db, _ := newAdminServer(t, nil)
	if err := db.Create(&model.User{ID: "no-revoker", Account: "no-revoker", Name: "No revoker", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/no-revoker", map[string]any{"enabled": false})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing revoker: %d %s", w.Code, w.Body.String())
	}
	if got := tokReq(t, r, http.MethodGet, "/api/safe/v1/me", nil, "no-revoker"); got.Code != http.StatusUnauthorized {
		t.Fatalf("disabled subject must be refused: %d %s", got.Code, got.Body.String())
	}
}

func TestUnauthorizedDisableDoesNotRevokeSessions(t *testing.T) {
	calls := 0
	r, _, db, _ := newAdminServer(t, sessionRevokerFunc(func(context.Context, string) error {
		calls++
		return nil
	}))
	if err := db.Create(&model.User{ID: "ordinary", Account: "ordinary", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	w := tokReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/ordinary", map[string]any{"enabled": false}, "ordinary")
	if w.Code != http.StatusForbidden || calls != 0 {
		t.Fatalf("unauthorized disable: status=%d revocations=%d", w.Code, calls)
	}
	var user model.User
	if err := db.First(&user, "id = ?", "ordinary").Error; err != nil || !user.Enabled {
		t.Fatalf("unauthorized request changed account state: enabled=%v err=%v", user.Enabled, err)
	}
}
