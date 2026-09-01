// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestAuthorizationDecisionsRequireActiveAccounts(t *testing.T) {
	r, e, db := newTestServer(t)
	if err := db.Create(&[]model.User{
		{ID: "active", Account: "active", Enabled: true},
		{ID: "disabled", Account: "disabled", Enabled: false},
	}).Error; err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "agent", "use")
	for _, accessor := range []string{"active", "disabled", "missing"} {
		if err := e.GrantObjectPermission(accessor, "agent", "a-1", "use"); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		accessor string
		allowed  bool
	}{
		{"active", true},
		{"disabled", false},
		{"missing", false},
	} {
		t.Run(tc.accessor, func(t *testing.T) {
			check := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", map[string]any{
				"accessor_id": tc.accessor,
				"resource":    map[string]string{"type": "agent", "id": "a-1"},
				"operation":   "use",
			})
			var checkBody struct {
				Allowed bool `json:"allowed"`
			}
			if check.Code != http.StatusOK || json.Unmarshal(check.Body.Bytes(), &checkBody) != nil || checkBody.Allowed != tc.allowed {
				t.Fatalf("check = %d %s, want allowed=%v", check.Code, check.Body.String(), tc.allowed)
			}

			operations := do(t, r, http.MethodPost, "/api/safe/v1/authz/operations", map[string]any{
				"accessor_id": tc.accessor,
				"resource":    map[string]string{"type": "agent", "id": "a-1"},
			})
			var operationsBody struct {
				Operations []string `json:"operations"`
			}
			if operations.Code != http.StatusOK || json.Unmarshal(operations.Body.Bytes(), &operationsBody) != nil {
				t.Fatalf("operations = %d %s", operations.Code, operations.Body.String())
			}
			if got := len(operationsBody.Operations) > 0; got != tc.allowed {
				t.Errorf("operations = %v, want non-empty=%v", operationsBody.Operations, tc.allowed)
			}

			filter := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
				"accessor_id":          tc.accessor,
				"resources":            []map[string]string{{"type": "agent", "id": "a-1"}},
				"candidate_operations": []string{"use"},
			})
			var filterBody struct {
				Resources []filterEntry `json:"resources"`
			}
			if filter.Code != http.StatusOK || json.Unmarshal(filter.Body.Bytes(), &filterBody) != nil {
				t.Fatalf("resource-filter = %d %s", filter.Code, filter.Body.String())
			}
			if got := len(filterBody.Resources) > 0; got != tc.allowed {
				t.Errorf("resource-filter = %v, want non-empty=%v", filterBody.Resources, tc.allowed)
			}

			resources := do(t, r, http.MethodGet,
				"/api/safe/v1/authz/resources?accessor_id="+tc.accessor+"&resource_type=agent&operation=use", nil)
			var resourcesBody struct {
				IDs []string `json:"ids"`
			}
			if resources.Code != http.StatusOK || json.Unmarshal(resources.Body.Bytes(), &resourcesBody) != nil {
				t.Fatalf("resources = %d %s", resources.Code, resources.Body.String())
			}
			if got := len(resourcesBody.IDs) > 0; got != tc.allowed {
				t.Errorf("resources = %v, want non-empty=%v", resourcesBody.IDs, tc.allowed)
			}
		})
	}
}

func TestDisabledManagementActorAndGranteeAreRejected(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	if err := db.Create(&model.User{ID: "disabled-grantee", Account: "disabled-grantee", Enabled: false}).Error; err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail")
	grant := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "disabled-grantee",
		"resource":    map[string]string{"type": "catalog", "id": "c-1"},
		"operations":  []string{"view_detail"},
	})
	if grant.Code != http.StatusBadRequest {
		t.Fatalf("disabled grantee = %d, want 400: %s", grant.Code, grant.Body.String())
	}

	if err := db.Model(&model.User{}).Where("id = ?", adminSub).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if got := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/roles", nil); got.Code != http.StatusForbidden {
		t.Fatalf("disabled admin = %d, want 403: %s", got.Code, got.Body.String())
	}
}

func TestAuthorizationAccountStoreFailureIsUnavailable(t *testing.T) {
	r, _, db := newTestServer(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	w := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", map[string]any{
		"accessor_id": "u-1",
		"resource":    map[string]string{"type": "agent", "id": "a-1"},
		"operation":   "use",
	})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("account store failure = %d, want 503: %s", w.Code, w.Body.String())
	}
}
