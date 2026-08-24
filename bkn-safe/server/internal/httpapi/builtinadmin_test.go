// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/seed"
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

func TestBusinessProvenanceAppPrincipalProtected(t *testing.T) {
	for name, request := range map[string]struct {
		method string
		suffix string
		body   any
	}{
		"delete":      {method: http.MethodDelete},
		"disable":     {method: http.MethodPut, body: map[string]any{"enabled": false}},
		"change type": {method: http.MethodPut, body: map[string]any{"account_type": "other"}},
		"rename":      {method: http.MethodPut, body: map[string]any{"name": "Customer Service"}},
		"set password": {
			method: http.MethodPut,
			suffix: "/password",
			body:   map[string]any{"password": "must-not-become-a-login"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, _, db, _ := newAdminServer(t)
			if err := db.Create(&model.User{
				ID:          seed.BusinessProvenanceOwnerID,
				Account:     "openbkn-business-provenance",
				Name:        "OpenBKN Business Provenance Service",
				Enabled:     true,
				Source:      model.SourceLocal,
				AccountType: model.AccountTypeApp,
			}).Error; err != nil {
				t.Fatal(err)
			}
			path := "/api/safe/v1/admin/users/" + seed.BusinessProvenanceOwnerID + request.suffix
			if w := adminReq(t, r, request.method, path, request.body); w.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d (%s)", w.Code, w.Body.String())
			}
		})
	}
}
