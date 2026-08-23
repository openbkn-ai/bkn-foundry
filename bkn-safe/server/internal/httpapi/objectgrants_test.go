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

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
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

// ownerGrantFixture builds the situation the owner path exists for: a builder
// who created a knowledge network (and therefore holds the creator's object
// grant, opAuthorize included) but holds no admin-authz permission at all.
func ownerGrantFixture(t *testing.T) (*gin.Engine, *authz.Enforcer) {
	t.Helper()
	r, e, db, users := newAdminServer(t)
	ctx := t.Context()
	for _, u := range []struct{ id, account string }{
		{"u-owner", "builder"},
		{"u-mate", "teammate"},
		{"u-stranger", "stranger"},
	} {
		if err := users.CreateLocalUser(ctx, &model.User{ID: u.id, Account: u.account, Name: u.account, Enabled: true}, "pw-init0"); err != nil {
			t.Fatal(err)
		}
	}
	// task_manage is registered for the type but deliberately NOT granted below,
	// so a test can ask for an operation that is valid yet unheld.
	seedCatalogOps(t, db, "knowledge_network",
		"view_detail", "modify", "delete", "query_data", "authorize", "task_manage")
	// Exactly what bkn-backend writes on create (COMMON_OPERATIONS).
	for _, op := range []string{"view_detail", "modify", "delete", "query_data", "authorize"} {
		if err := e.GrantObjectPermission("u-owner", "knowledge_network", "kn-mine", op); err != nil {
			t.Fatal(err)
		}
	}
	return r, e
}

// The regression: the creator of a knowledge network can share it, without any
// admin-authz permission. Before, opAuthorize was written on create and then
// never consulted, so this was a flat 403 (bkn-studio#478).
func TestObjectGrantsOwnerMayShareOwnObject(t *testing.T) {
	r, e := ownerGrantFixture(t)

	w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail", "query_data"},
	}, "u-owner")
	if w.Code != http.StatusNoContent {
		t.Fatalf("owner share: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-mate", "knowledge_network", "kn-mine", "view_detail"); !ok {
		t.Error("shared grant did not take effect at enforce time")
	}

	// And can take it back.
	w = tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
	}, "u-owner")
	if w.Code != http.StatusNoContent {
		t.Fatalf("owner revoke: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-mate", "knowledge_network", "kn-mine", "view_detail"); ok {
		t.Error("owner revoke did not remove the grant")
	}
}

// The two limits on a delegate, and the boundary of what it owns.
func TestObjectGrantsOwnerLimits(t *testing.T) {
	r, e := ownerGrantFixture(t)
	// A second network the owner did NOT create.
	if err := e.GrantObjectPermission("u-stranger", "knowledge_network", "kn-theirs", "view_detail"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		body map[string]any
	}{
		{
			// The chain stops at one: a delegate cannot mint another delegate.
			"cannot pass authorize on",
			map[string]any{
				"accessor_id": "u-mate",
				"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
				"operations":  []string{"view_detail", "authorize"},
			},
		},
		{
			// task_manage is registered for the type but the owner does not hold
			// it, so it cannot be handed out.
			"cannot grant an op it does not hold",
			map[string]any{
				"accessor_id": "u-mate",
				"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
				"operations":  []string{"view_detail", "task_manage"},
			},
		},
		{
			// Ownership is per object, not per type.
			"cannot reach another owner's object",
			map[string]any{
				"accessor_id": "u-mate",
				"resource":    map[string]any{"type": "knowledge_network", "id": "kn-theirs"},
				"operations":  []string{"view_detail"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", tc.body, "u-owner"); w.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d (%s)", w.Code, w.Body.String())
			}
		})
	}
	if ok, _ := e.Check("u-mate", "knowledge_network", "kn-mine", "view_detail"); ok {
		t.Error("a rejected request must not write a partial grant")
	}

	// Someone with no stake in the object gets nothing.
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail"},
	}, "u-stranger"); w.Code != http.StatusForbidden {
		t.Fatalf("non-owner: want 403, got %d (%s)", w.Code, w.Body.String())
	}
}

// A role holding type-wide authorize (network_builder does, on
// knowledge_network) may delegate any instance of that type — the seeded policy
// already says so. It is still a delegate: the same two limits apply.
func TestObjectGrantsTypeWideAuthorize(t *testing.T) {
	r, e := ownerGrantFixture(t)
	if err := e.GrantRolePermission("role-builder", "knowledge_network", "*", "authorize"); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantRolePermission("role-builder", "knowledge_network", "*", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole("u-stranger", "role-builder"); err != nil {
		t.Fatal(err)
	}

	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail"},
	}, "u-stranger"); w.Code != http.StatusNoContent {
		t.Fatalf("type-wide authorize holder: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	// modify is not in the role grant, so it cannot be passed on.
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"modify"},
	}, "u-stranger"); w.Code != http.StatusForbidden {
		t.Fatalf("op it does not hold: want 403, got %d (%s)", w.Code, w.Body.String())
	}
}

// The self-service read follows the write authority: an owner sees who already
// holds their own network, and nothing else. It is per object by construction —
// there is no way to widen it into a platform view.
func TestObjectGrantsOwnerPolicyRead(t *testing.T) {
	r, _ := ownerGrantFixture(t)
	const base = "/api/safe/v1/me/object-grants?resource_type=knowledge_network"

	if w := tokReq(t, r, http.MethodGet, base+"&resource_id=kn-mine", nil, "u-owner"); w.Code != http.StatusOK {
		t.Fatalf("owner reading its own object: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	if w := tokReq(t, r, http.MethodGet, base+"&resource_id=kn-theirs", nil, "u-owner"); w.Code != http.StatusForbidden {
		t.Fatalf("owner reading another object: want 403, got %d", w.Code)
	}
	// No id at all is rejected as a malformed request rather than answered as a
	// type-wide read — the endpoint has no type-wide mode to fall into.
	if w := tokReq(t, r, http.MethodGet, base, nil, "u-owner"); w.Code != http.StatusBadRequest {
		t.Fatalf("owner reading the whole type: want 400, got %d (%s)", w.Code, w.Body.String())
	}
	// The platform-wide listing stays where it was, administrator-only.
	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/object-grants", nil, "u-owner"); w.Code != http.StatusForbidden {
		t.Fatalf("owner listing every grant: want 403, got %d", w.Code)
	}
	if w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/policies?resource_type=knowledge_network", nil); w.Code != http.StatusOK {
		t.Fatalf("administrator reading the whole type: want 200, got %d", w.Code)
	}
}

// The owner-facing lookups: names on the grant rows, and a candidate picker.
// Both exist because the platform user directory is administrator-only, and both
// are gated on the same authority as writing grants on the object.
func TestObjectGrantsOwnerDirectoryLookups(t *testing.T) {
	r, _ := ownerGrantFixture(t)
	const base = "/api/safe/v1/me"

	w := tokReq(t, r, http.MethodGet, base+"/object-grants?resource_type=knowledge_network&resource_id=kn-mine", nil, "u-owner")
	if w.Code != http.StatusOK {
		t.Fatalf("read grants: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var grants struct {
		Entries []struct {
			AccessorID      string `json:"accessor_id"`
			AccessorAccount string `json:"accessor_account"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &grants); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(grants.Entries) != 1 || grants.Entries[0].AccessorID != "u-owner" {
		t.Fatalf("want the owner's own row, got %+v", grants.Entries)
	}
	if grants.Entries[0].AccessorAccount != "builder" {
		t.Errorf("accessor_account = %q, want \"builder\" — an id alone names nobody", grants.Entries[0].AccessorAccount)
	}

	// The picker is scoped to an object the caller may authorize...
	w = tokReq(t, r, http.MethodGet, base+"/grantable-users?resource_type=knowledge_network&resource_id=kn-mine&search=team", nil, "u-owner")
	if w.Code != http.StatusOK {
		t.Fatalf("picker: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var picker struct {
		Users []struct {
			ID      string `json:"id"`
			Account string `json:"account"`
		} `json:"users"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &picker); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(picker.Users) != 1 || picker.Users[0].Account != "teammate" {
		t.Fatalf("search=team should match only teammate, got %+v", picker.Users)
	}

	// ...and is not a back door into the directory for anyone else.
	if w := tokReq(t, r, http.MethodGet, base+"/grantable-users?resource_type=knowledge_network&resource_id=kn-mine&search=team", nil, "u-stranger"); w.Code != http.StatusForbidden {
		t.Fatalf("non-owner picker: want 403, got %d", w.Code)
	}
	if w := tokReq(t, r, http.MethodGet, base+"/grantable-users?resource_type=knowledge_network&search=team", nil, "u-owner"); w.Code != http.StatusBadRequest {
		t.Fatalf("picker without an object: want 400, got %d", w.Code)
	}
	// Holding authorize on one object is not a licence to page through the
	// directory, so an empty search is refused rather than answered with a page.
	if w := tokReq(t, r, http.MethodGet, base+"/grantable-users?resource_type=knowledge_network&resource_id=kn-mine", nil, "u-owner"); w.Code != http.StatusBadRequest {
		t.Fatalf("picker without a search term: want 400, got %d", w.Code)
	}
}

// A delegate may take back what it shared, but must not strip another holder of
// `authorize` — the creator included. Revoking removes every op the accessor has
// on the object, and a delegate cannot grant `authorize` back, so without this
// rule anyone trusted with one object could take it from the person who made it.
func TestObjectGrantsDelegateCannotStripAuthorizeHolder(t *testing.T) {
	r, e := ownerGrantFixture(t)
	// u-stranger is a network_builder: type-wide authorize on the whole type, no
	// stake in this particular network.
	mustGrantRole(t, e, "role-builder", "knowledge_network", "authorize")
	mustGrantRole(t, e, "role-builder", "knowledge_network", "view_detail")
	if err := e.AssignRole("u-stranger", "role-builder"); err != nil {
		t.Fatal(err)
	}

	// The creator holds authorize on kn-mine and must survive.
	w := tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-owner",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
	}, "u-stranger")
	if w.Code != http.StatusForbidden {
		t.Fatalf("stripping the creator: want 403, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-owner", "knowledge_network", "kn-mine", "authorize"); !ok {
		t.Fatal("the creator lost authorize on its own object")
	}

	// A plain grantee stays revocable — undoing a share is the point.
	if err := e.GrantObjectPermission("u-mate", "knowledge_network", "kn-mine", "view_detail"); err != nil {
		t.Fatal(err)
	}
	w = tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
	}, "u-stranger")
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoking a plain grantee: want 204, got %d (%s)", w.Code, w.Body.String())
	}

	// The grant side erases just as thoroughly: POST replaces the whole op set, so
	// writing view_detail onto the creator would drop its authorize in one move.
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-owner",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail"},
	}, "u-stranger"); w.Code != http.StatusForbidden {
		t.Fatalf("overwriting the creator's grant: want 403, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-owner", "knowledge_network", "kn-mine", "authorize"); !ok {
		t.Fatal("the creator lost authorize through the grant path")
	}

	// An administrator is still able to do both.
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-owner",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
	}); w.Code != http.StatusNoContent {
		t.Fatalf("administrator revoking an authorize holder: want 204, got %d", w.Code)
	}
}

func mustGrantRole(t *testing.T, e *authz.Enforcer, roleID, resourceType, op string) {
	t.Helper()
	if err := e.GrantRolePermission(roleID, resourceType, "*", op); err != nil {
		t.Fatal(err)
	}
}

// A delegate must not be able to write a wildcard grant, or to un-publish a
// built-in by deleting the public-access row. Both are shapes that look like an
// ordinary per-object write but reach far past one object.
func TestObjectGrantsDelegateCannotWriteWildcardOrTouchPublic(t *testing.T) {
	r, e := ownerGrantFixture(t)

	// keyMatch treats "*" anywhere as a wildcard, so an id like "kn-*" would be
	// stored verbatim and then match every network with that prefix — a grant no
	// console screen could show or revoke.
	for _, id := range []string{"kn-*", "*-mine", "*"} {
		if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
			"accessor_id": "u-mate",
			"resource":    map[string]any{"type": "knowledge_network", "id": id},
			"operations":  []string{"view_detail"},
		}, "u-owner"); w.Code != http.StatusBadRequest {
			t.Errorf("granting on id %q: want 400, got %d (%s)", id, w.Code, w.Body.String())
		}
		if w := tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
			"accessor_id": "u-mate",
			"resource":    map[string]any{"type": "knowledge_network", "id": id},
		}, "u-owner"); w.Code != http.StatusBadRequest {
			t.Errorf("revoking on id %q: want 400, got %d (%s)", id, w.Code, w.Body.String())
		}
	}
	// An administrator gets the same answer: these endpoints write one instance.
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-*"},
		"operations":  []string{"view_detail"},
	}); w.Code != http.StatusBadRequest {
		t.Errorf("administrator granting on a wildcard id: want 400, got %d", w.Code)
	}

	// The public-access row publishes a built-in to everyone. It carries no
	// `authorize`, so the holder check alone would let a delegate delete it.
	if err := e.GrantObjectPermission(authz.PublicAccessorID, "knowledge_network", "kn-mine", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if w := tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": authz.PublicAccessorID,
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
	}, "u-owner"); w.Code != http.StatusForbidden {
		t.Fatalf("delegate deleting the public row: want 403, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check(authz.PublicAccessorID, "knowledge_network", "kn-mine", "view_detail"); !ok {
		t.Error("the public-access row was removed by a delegate")
	}
	// An administrator may still remove it.
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": authz.PublicAccessorID,
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
	}); w.Code != http.StatusNoContent {
		t.Errorf("administrator removing the public row: want 204, got %d", w.Code)
	}
}
