package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/model"
)

// seedCatalogOps registers operation ids for a resource type so the
// object-grant op-validation has a catalog to check against.
func seedCatalogOps(t *testing.T, db *gorm.DB, resourceType string, ops ...string) {
	t.Helper()
	for _, op := range ops {
		row := model.Operation{ResourceTypeID: resourceType, ID: op, Name: op}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed op %s/%s: %v", resourceType, op, err)
		}
	}
}

type ogEntry struct {
	AccessorID string `json:"accessor_id"`
	Resource   struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"resource"`
	Operations []string `json:"operations"`
}

func listObjectGrants(t *testing.T, r *gin.Engine, query string) []ogEntry {
	t.Helper()
	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/object-grants"+query, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list grants: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var body struct {
		Entries []ogEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode grants: %v", err)
	}
	return body.Entries
}

func TestObjectGrantsSetListRevoke(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	ctx := t.Context()
	if err := users.CreateLocalUser(ctx, &model.User{ID: "u-1", Account: "alice", Name: "Alice", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")

	// set: grant u-1 two ops on catalog c1
	w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-1",
		"resource":    map[string]any{"type": "catalog", "id": "c1"},
		"operations":  []string{"view_detail", "modify"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("grant: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "modify"); !ok {
		t.Fatal("grant did not take effect at enforce time")
	}

	// list (no filter) returns the grant
	entries := listObjectGrants(t, r, "")
	if len(entries) != 1 || entries[0].AccessorID != "u-1" || entries[0].Resource.ID != "c1" || len(entries[0].Operations) != 2 {
		t.Fatalf("unexpected list: %+v", entries)
	}
	// filtered lists
	if got := listObjectGrants(t, r, "?accessor_id=u-1"); len(got) != 1 {
		t.Fatalf("accessor filter: %+v", got)
	}
	if got := listObjectGrants(t, r, "?resource_type=catalog&resource_id=c1"); len(got) != 1 {
		t.Fatalf("resource filter: %+v", got)
	}
	if got := listObjectGrants(t, r, "?resource_id=other"); len(got) != 0 {
		t.Fatalf("resource filter (miss): %+v", got)
	}

	// set again with a smaller op set: replace semantics drop "modify"
	w = adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-1",
		"resource":    map[string]any{"type": "catalog", "id": "c1"},
		"operations":  []string{"view_detail"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("re-grant: want 204, got %d", w.Code)
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "modify"); ok {
		t.Fatal("replace did not prune the dropped op")
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "view_detail"); !ok {
		t.Fatal("replace dropped the kept op")
	}

	// revoke
	w = adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-1",
		"resource":    map[string]any{"type": "catalog", "id": "c1"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "view_detail"); ok {
		t.Fatal("revoke did not remove the grant")
	}
	if got := listObjectGrants(t, r, ""); len(got) != 0 {
		t.Fatalf("list after revoke: %+v", got)
	}
}

func TestObjectGrantsValidation(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	ctx := t.Context()
	if err := users.CreateLocalUser(ctx, &model.User{ID: "u-1", Account: "alice", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Department{ID: "dep-1", Name: "Data"}).Error; err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"department grantee", map[string]any{"accessor_id": "dep-1", "resource": map[string]any{"type": "catalog", "id": "c1"}, "operations": []string{"view_detail"}}},
		{"unknown user", map[string]any{"accessor_id": "ghost", "resource": map[string]any{"type": "catalog", "id": "c1"}, "operations": []string{"view_detail"}}},
		{"wildcard id", map[string]any{"accessor_id": "u-1", "resource": map[string]any{"type": "catalog", "id": "*"}, "operations": []string{"view_detail"}}},
		{"unknown type", map[string]any{"accessor_id": "u-1", "resource": map[string]any{"type": "nope", "id": "c1"}, "operations": []string{"view_detail"}}},
		{"unknown op", map[string]any{"accessor_id": "u-1", "resource": map[string]any{"type": "catalog", "id": "c1"}, "operations": []string{"bogus"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d (%s)", w.Code, w.Body.String())
			}
		})
	}
}

// Role-subject and type-wide grants must not surface on the user object-grant
// listing (that surface is users-on-concrete-objects only).
func TestObjectGrantsExcludesRolesAndWildcards(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	ctx := t.Context()
	if err := users.CreateLocalUser(ctx, &model.User{ID: "u-1", Account: "alice", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Role{ID: "role-x", Name: "x", Source: "custom"}).Error; err != nil {
		t.Fatal(err)
	}
	// a concrete grant to a ROLE (should be excluded)
	_ = e.GrantRolePermission("role-x", "catalog", "c9", "view_detail")
	// a type-wide grant to the user (id "*", should be excluded)
	_ = e.GrantRolePermission("u-1", "catalog", "*", "view_detail")
	// a concrete grant to the USER (should be included)
	_ = e.GrantObjectPermission("u-1", "catalog", "c1", "view_detail")

	entries := listObjectGrants(t, r, "")
	if len(entries) != 1 || entries[0].AccessorID != "u-1" || entries[0].Resource.ID != "c1" {
		t.Fatalf("listing must contain only the user concrete grant, got %+v", entries)
	}
}
