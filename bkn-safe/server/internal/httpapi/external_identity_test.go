// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package httpapi

import (
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/identity"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
	"net/http"
	"testing"
)

func TestExternalUserAdminBoundaries(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	user := model.User{ID: "external", Account: "remote", Name: "Remote", Source: model.Source("sso"), Enabled: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		body any
	}{
		{"/users/external/password", map[string]any{"password": "Changed123!"}},
		{"/users/external", map[string]any{"name": "Changed"}},
		{"/users/external", map[string]any{"email": "changed@example.com"}},
		{"/users/external", map[string]any{"telephone": "12345"}},
	} {
		response := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin"+tc.path, tc.body)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", tc.path, response.Code, response.Body.String())
		}
	}
	var stored model.User
	if err := db.First(&stored, "id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PasswordHash != "" || stored.Name != "Remote" || stored.MustChangePassword {
		t.Fatalf("rejected writes mutated user: %+v", stored)
	}
	for _, path := range []string{"/api/safe/v1/admin/users?limit=100", "/api/safe/v1/admin/users/external"} {
		response := adminReq(t, r, http.MethodGet, path, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("read: %d %s", response.Code, response.Body.String())
		}
		var decoded any
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		// JSON serialization must expose the source on both list and detail responses.
		if !containsJSONSource(decoded, "sso") {
			t.Fatalf("source missing: %s", response.Body.String())
		}
	}
	response := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/external", map[string]any{"enabled": false})
	if response.Code != http.StatusNoContent {
		t.Fatalf("disable: %d %s", response.Code, response.Body.String())
	}
	var count int64
	if err := db.Model(&model.AuditLog{}).Where("resource = ? AND status >= ?", "users", http.StatusBadRequest).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected writes produced audit: %d %v", count, err)
	}
}

func containsJSONSource(value any, source string) bool {
	switch v := value.(type) {
	case map[string]any:
		if v["source"] == source {
			return true
		}
		for _, child := range v {
			if containsJSONSource(child, source) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsJSONSource(child, source) {
				return true
			}
		}
	}
	return false
}

func TestSynchronizedDepartmentAdminBoundaries(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	if err := identity.RegisterCleanup(db, identity.Cleanup{DepartmentSources: func(_ *gorm.DB, ids []string) (map[string]string, error) {
		out := map[string]string{}
		for _, id := range ids {
			if id == "synced" {
				out[id] = "sso"
			}
		}
		return out, nil
	}}); err != nil {
		t.Fatal(err)
	}
	for _, dept := range []model.Department{{ID: "root", Name: "Root"}, {ID: "synced", Name: "Synced", ParentID: "root"}, {ID: "other", Name: "Other"}} {
		if err := db.Create(&dept).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, body := range []any{
		map[string]any{"name": "Changed"},
		map[string]any{"name": "Changed", "parent_id": "root"},
		map[string]any{"parent_id": "other"},
	} {
		response := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/departments/synced", body)
		if response.Code != http.StatusForbidden {
			t.Fatalf("synced write: %d %s", response.Code, response.Body.String())
		}
	}
	response := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/departments/synced", map[string]any{"name": "Synced", "parent_id": "root", "remark": "Local note"})
	if response.Code != http.StatusNoContent {
		t.Fatalf("local metadata: %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"/api/safe/v1/admin/departments?limit=100", "/api/safe/v1/admin/departments/synced"} {
		response := adminReq(t, r, http.MethodGet, path, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("read: %d %s", response.Code, response.Body.String())
		}
		var decoded any
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if !containsJSONSource(decoded, "sso") {
			t.Fatalf("department source missing: %s", response.Body.String())
		}
	}
}
