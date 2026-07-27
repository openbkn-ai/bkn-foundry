// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
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
	body := listObjectGrantsBody(t, r, query)
	return body.Entries
}

func listObjectGrantsBody(t *testing.T, r *gin.Engine, query string) struct {
	Entries []ogEntry `json:"entries"`
	Total   int       `json:"total"`
	Summary *struct {
		Grants   int `json:"grants"`
		Objects  int `json:"objects"`
		Grantees int `json:"grantees"`
	} `json:"summary"`
} {
	t.Helper()
	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/object-grants"+query, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list grants: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var body struct {
		Entries []ogEntry `json:"entries"`
		Total   int       `json:"total"`
		Summary *struct {
			Grants   int `json:"grants"`
			Objects  int `json:"objects"`
			Grantees int `json:"grantees"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode grants: %v", err)
	}
	return body
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

func TestObjectGrantsPaginationAndSearch(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	ctx := t.Context()
	for _, u := range []model.User{
		{ID: "u-1", Account: "alice", Name: "Alice", Enabled: true},
		{ID: "u-2", Account: "bob", Name: "Bob", Enabled: true},
	} {
		if err := users.CreateLocalUser(ctx, &u, "pw-init0"); err != nil {
			t.Fatal(err)
		}
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")

	grant := func(accessorID, id string) {
		t.Helper()
		w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
			"accessor_id": accessorID,
			"resource":    map[string]any{"type": "catalog", "id": id},
			"operations":  []string{"view_detail"},
		})
		if w.Code != http.StatusNoContent {
			t.Fatalf("grant %s/%s: want 204, got %d (%s)", accessorID, id, w.Code, w.Body.String())
		}
	}
	grant("u-1", "c1")
	grant("u-1", "c2")
	grant("u-2", "c3")

	body := listObjectGrantsBody(t, r, "?limit=1&offset=0&include_summary=true")
	if body.Total != 3 || len(body.Entries) != 1 {
		t.Fatalf("pagination page 1: total=%d entries=%d", body.Total, len(body.Entries))
	}
	if body.Summary == nil || body.Summary.Grants != 3 || body.Summary.Objects != 3 || body.Summary.Grantees != 2 {
		t.Fatalf("unexpected summary: %+v", body.Summary)
	}
	body = listObjectGrantsBody(t, r, "?limit=1&offset=2")
	if body.Total != 3 || len(body.Entries) != 1 {
		t.Fatalf("pagination page 3: total=%d entries=%d", body.Total, len(body.Entries))
	}

	if got := listObjectGrants(t, r, "?search=alice"); len(got) != 2 {
		t.Fatalf("search by user: %+v", got)
	}
	if got := listObjectGrants(t, r, "?search=c3"); len(got) != 1 || got[0].Resource.ID != "c3" {
		t.Fatalf("search by resource id: %+v", got)
	}
	if got := listObjectGrants(t, r, "?obj_type=catalog&obj_id=c1"); len(got) != 1 {
		t.Fatalf("obj_* aliases: %+v", got)
	}
}

func TestObjectGrantsGroupedViews(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	ctx := t.Context()
	for _, u := range []model.User{
		{ID: "u-1", Account: "alice", Name: "Alice", Enabled: true},
		{ID: "u-2", Account: "bob", Name: "Bob", Enabled: true},
	} {
		if err := users.CreateLocalUser(ctx, &u, "pw-init0"); err != nil {
			t.Fatal(err)
		}
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")

	grant := func(accessorID, id string, ops ...string) {
		t.Helper()
		w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
			"accessor_id": accessorID,
			"resource":    map[string]any{"type": "catalog", "id": id},
			"operations":  ops,
		})
		if w.Code != http.StatusNoContent {
			t.Fatalf("grant %s/%s: %d (%s)", accessorID, id, w.Code, w.Body.String())
		}
	}
	// c1: granted to both u-1 and u-2; c2: only u-1.
	grant("u-1", "c1", "view_detail", "modify")
	grant("u-2", "c1", "view_detail")
	grant("u-1", "c2", "view_detail")

	decode := func(query string) struct {
		Groups []map[string]any `json:"groups"`
		Total  int              `json:"total"`
	} {
		t.Helper()
		w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/object-grants"+query, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("grouped list %s: %d (%s)", query, w.Code, w.Body.String())
		}
		var body struct {
			Groups []map[string]any `json:"groups"`
			Total  int              `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return body
	}

	// group_by=object: 2 distinct objects (c1, c2); c1 has 2 grantees, c2 has 1.
	byObj := decode("?group_by=object")
	if byObj.Total != 2 || len(byObj.Groups) != 2 {
		t.Fatalf("group_by=object: total=%d groups=%d", byObj.Total, len(byObj.Groups))
	}
	for _, g := range byObj.Groups {
		obj := g["object"].(map[string]any)
		want := 1.0
		if obj["id"] == "c1" {
			want = 2.0
		}
		if g["grantee_count"].(float64) != want {
			t.Fatalf("object %v grantee_count = %v, want %v", obj["id"], g["grantee_count"], want)
		}
	}

	// group_by=grantee: 2 distinct grantees; u-1 on 2 objects, u-2 on 1.
	byGrantee := decode("?group_by=grantee")
	if byGrantee.Total != 2 || len(byGrantee.Groups) != 2 {
		t.Fatalf("group_by=grantee: total=%d groups=%d", byGrantee.Total, len(byGrantee.Groups))
	}
	for _, g := range byGrantee.Groups {
		want := 1.0
		if g["accessor_id"] == "u-1" {
			want = 2.0
		}
		if g["object_count"].(float64) != want {
			t.Fatalf("grantee %v object_count = %v, want %v", g["accessor_id"], g["object_count"], want)
		}
	}

	// grouped pagination: 1 object per page, total still 2.
	page := decode("?group_by=object&limit=1&offset=0")
	if page.Total != 2 || len(page.Groups) != 1 {
		t.Fatalf("grouped pagination: total=%d groups=%d", page.Total, len(page.Groups))
	}
}
