package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"bkn-safe/internal/model"
)

func TestMeAuthGate(t *testing.T) {
	r, _, _, _ := newAdminServer(t)
	const path = "/api/safe/v1/me/permissions"

	// no token -> 401
	if w := tokReq(t, r, http.MethodGet, path, nil, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: want 401, got %d", w.Code)
	}
	// invalid token -> 401
	if w := tokReq(t, r, http.MethodGet, path, nil, "bad"); w.Code != http.StatusUnauthorized {
		t.Errorf("bad token: want 401, got %d", w.Code)
	}
	// any authenticated subject -> 200, even with zero grants
	if w := tokReq(t, r, http.MethodGet, path, nil, "nobody"); w.Code != http.StatusOK {
		t.Errorf("plain user: want 200, got %d (%s)", w.Code, w.Body.String())
	}
}

// GET /me returns the caller's own identity and role names; token-gated but
// not admin-gated.
func TestMeIdentity(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	const path = "/api/safe/v1/me"

	db.Create(&model.User{ID: "u-me", Account: "me", Name: "Me", Email: "me@x.io", Enabled: true})
	db.Create(&model.Role{ID: "r-data", Name: "数据管理员", Source: model.RoleSourceSystem})
	db.Create(&model.Department{ID: "d-9", Name: "Dept9"})
	db.Create(&model.UserDepartment{UserID: "u-me", DepartmentID: "d-9"})
	if err := e.AssignRole("u-me", "r-data"); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole("u-me", "r-dangling"); err != nil { // no role row
		t.Fatal(err)
	}

	// no token -> 401; subject without a user row -> 404
	if w := tokReq(t, r, http.MethodGet, path, nil, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: want 401, got %d", w.Code)
	}
	if w := tokReq(t, r, http.MethodGet, path, nil, "ghost"); w.Code != http.StatusNotFound {
		t.Errorf("ghost subject: want 404, got %d", w.Code)
	}

	w := tokReq(t, r, http.MethodGet, path, nil, "u-me")
	if w.Code != http.StatusOK {
		t.Fatalf("me: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		ID          string   `json:"id"`
		Account     string   `json:"account"`
		AccountType string   `json:"account_type"`
		Departments []string `json:"departments"`
		Roles       []string `json:"roles"`
		RoleIDs     []string `json:"role_ids"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != "u-me" || resp.Account != "me" {
		t.Errorf("identity = %+v", resp)
	}
	if len(resp.Departments) != 1 || resp.Departments[0] != "d-9" {
		t.Errorf("departments = %v", resp.Departments)
	}
	// casbin does not guarantee role order — compare as a set
	roleSet := map[string]bool{}
	for _, n := range resp.Roles {
		roleSet[n] = true
	}
	if len(resp.Roles) != 2 || !roleSet["数据管理员"] || !roleSet["r-dangling"] {
		t.Errorf("roles = %v, want {数据管理员, r-dangling} (dangling id kept verbatim)", resp.Roles)
	}
	if len(resp.RoleIDs) != 2 {
		t.Errorf("role_ids = %v", resp.RoleIDs)
	}
}

func TestMePermissions(t *testing.T) {
	r, e, _, _ := newAdminServer(t)
	const path = "/api/safe/v1/me/permissions"

	// u1: one role grant (type-wide), one direct per-object grant, and the same
	// op again via a second role (must de-duplicate).
	if err := e.GrantRolePermission("role-a", "agent", "*", "use"); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantRolePermission("role-b", "agent", "*", "use"); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole("u1", "role-a"); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole("u1", "role-b"); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantObjectPermission("u1", "kn", "kn-1", "view"); err != nil {
		t.Fatal(err)
	}

	w := tokReq(t, r, http.MethodGet, path, nil, "u1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		IsAdmin     bool `json:"is_admin"`
		Permissions []struct {
			Resource struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"resource"`
			Operations []string `json:"operations"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IsAdmin {
		t.Error("u1 must not be admin")
	}
	if len(resp.Permissions) != 2 {
		t.Fatalf("want 2 grants, got %d: %s", len(resp.Permissions), w.Body.String())
	}
	byKey := map[string][]string{}
	for _, p := range resp.Permissions {
		byKey[p.Resource.Type+"/"+p.Resource.ID] = p.Operations
	}
	if ops := byKey["agent/*"]; len(ops) != 1 || ops[0] != "use" {
		t.Errorf("agent/* ops: want [use] (deduped across roles), got %v", ops)
	}
	if ops := byKey["kn/kn-1"]; len(ops) != 1 || ops[0] != "view" {
		t.Errorf("kn/kn-1 ops: want [view], got %v", ops)
	}

	// super-admin: is_admin true, wildcard grant surfaces as type "*".
	w = adminReq(t, r, http.MethodGet, path, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin: want 200, got %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode admin: %v", err)
	}
	if !resp.IsAdmin {
		t.Error("admin: is_admin must be true")
	}
}
