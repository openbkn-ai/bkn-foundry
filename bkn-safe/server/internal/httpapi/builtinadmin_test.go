package httpapi

import (
	"net/http"
	"testing"

	"bkn-safe/internal/model"
	"bkn-safe/internal/seed"
)

// TestBuiltInAdminUserProtected verifies the user-admin API refuses to delete or
// disable the built-in admin (defense in depth — deleting the only super-admin
// would lock everyone out), while still allowing harmless edits like rename.
func TestBuiltInAdminUserProtected(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	ctx := t.Context()
	if err := users.CreateLocalUser(ctx,
		&model.User{ID: seed.AdminUserID, Account: "admin", Name: "Administrator", Enabled: true},
		"pw-init0"); err != nil {
		t.Fatal(err)
	}
	path := "/api/safe/v1/admin/users/" + seed.AdminUserID

	if w := adminReq(t, r, http.MethodDelete, path, nil); w.Code != http.StatusForbidden {
		t.Fatalf("delete built-in admin: want 403, got %d (%s)", w.Code, w.Body.String())
	}
	if w := adminReq(t, r, http.MethodPut, path, map[string]any{"enabled": false}); w.Code != http.StatusForbidden {
		t.Fatalf("disable built-in admin: want 403, got %d (%s)", w.Code, w.Body.String())
	}
	// Non-disable edits are still allowed.
	if w := adminReq(t, r, http.MethodPut, path, map[string]any{"name": "Boss"}); w.Code != http.StatusNoContent {
		t.Fatalf("rename built-in admin: want 204, got %d (%s)", w.Code, w.Body.String())
	}

	var got model.User
	if err := db.First(&got, "id = ?", seed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Name != "Boss" {
		t.Errorf("admin state wrong after guarded edits: %+v", got)
	}
}
