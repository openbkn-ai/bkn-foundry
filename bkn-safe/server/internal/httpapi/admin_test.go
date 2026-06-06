package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/database"
	"bkn-safe/internal/directory"
	"bkn-safe/internal/model"
)

// newAdminServer builds a full server including the user-admin surface (Users +
// Enforcer), which newTestServer omits.
func newAdminServer(t *testing.T) (*gin.Engine, *authz.Enforcer, *gorm.DB, *auth.UserStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	e, err := authz.New(db)
	if err != nil {
		t.Fatalf("authz: %v", err)
	}
	users := auth.NewUserStore(db)
	r := New(Deps{Enforcer: e, DB: db, Directory: directory.New(db), Users: users})
	return r, e, db, users
}

func TestUserUpdateAndDelete(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	ctx := t.Context()
	u := &model.User{ID: "u-1", Account: "bob", Name: "Bob", Enabled: true}
	if err := users.CreateLocalUser(ctx, u, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	// bind a role + grant a direct policy so delete must purge casbin.
	_ = e.AssignRole("u-1", "role-x")
	_ = e.GrantObjectPermission("u-1", "agent", "a1", "use")

	// update name + disable
	w := do(t, r, http.MethodPut, "/api/safe/v1/directory/users/u-1",
		map[string]any{"name": "Bobby", "enabled": false})
	if w.Code != http.StatusNoContent {
		t.Fatalf("update: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	var got model.User
	db.First(&got, "id = ?", "u-1")
	if got.Name != "Bobby" || got.Enabled {
		t.Errorf("update not applied: %+v", got)
	}

	// update unknown -> 404
	w = do(t, r, http.MethodPut, "/api/safe/v1/directory/users/ghost", map[string]any{"name": "x"})
	if w.Code != http.StatusNotFound {
		t.Errorf("update ghost: want 404, got %d", w.Code)
	}

	// delete -> 204, row gone, casbin purged
	w = do(t, r, http.MethodDelete, "/api/safe/v1/directory/users/u-1", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	var n int64
	db.Model(&model.User{}).Where("id = ?", "u-1").Count(&n)
	if n != 0 {
		t.Error("user row not deleted")
	}
	roles, _ := e.RolesForAccessor("u-1")
	if len(roles) != 0 {
		t.Errorf("role binding not purged: %v", roles)
	}
	ok, _ := e.Check("u-1", "agent", "a1", "use")
	if ok {
		t.Error("direct grant not purged")
	}

	// delete again -> 404
	w = do(t, r, http.MethodDelete, "/api/safe/v1/directory/users/u-1", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("delete twice: want 404, got %d", w.Code)
	}
}

func TestDepartmentCRUD(t *testing.T) {
	r, _, db, _ := newAdminServer(t)

	// create root
	w := do(t, r, http.MethodPost, "/api/safe/v1/directory/departments",
		map[string]any{"id": "d-root", "name": "Root"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", w.Code, w.Body.String())
	}
	// create child
	w = do(t, r, http.MethodPost, "/api/safe/v1/directory/departments",
		map[string]any{"id": "d-child", "name": "Child", "parent_id": "d-root"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create child: %d", w.Code)
	}

	// rename child
	w = do(t, r, http.MethodPut, "/api/safe/v1/directory/departments/d-child",
		map[string]any{"name": "Kid"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("update: %d", w.Code)
	}
	var d model.Department
	db.First(&d, "id = ?", "d-child")
	if d.Name != "Kid" {
		t.Errorf("rename not applied: %q", d.Name)
	}

	// delete non-empty root -> 409
	w = do(t, r, http.MethodDelete, "/api/safe/v1/directory/departments/d-root", nil)
	if w.Code != http.StatusConflict {
		t.Errorf("delete non-empty: want 409, got %d", w.Code)
	}

	// delete empty child -> 204
	w = do(t, r, http.MethodDelete, "/api/safe/v1/directory/departments/d-child", nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("delete child: want 204, got %d", w.Code)
	}
	// now root is empty -> 204
	w = do(t, r, http.MethodDelete, "/api/safe/v1/directory/departments/d-root", nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("delete root: want 204, got %d", w.Code)
	}
	// delete unknown -> 404
	w = do(t, r, http.MethodDelete, "/api/safe/v1/directory/departments/ghost", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("delete ghost: want 404, got %d", w.Code)
	}

	// member guard: dept with a member user can't be deleted
	db.Create(&model.Department{ID: "d-hr", Name: "HR"})
	db.Create(&model.UserDepartment{UserID: "u-x", DepartmentID: "d-hr"})
	w = do(t, r, http.MethodDelete, "/api/safe/v1/directory/departments/d-hr", nil)
	if w.Code != http.StatusConflict {
		t.Errorf("delete dept-with-member: want 409, got %d", w.Code)
	}
}

func TestRoleCRUDAndBuiltInProtection(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	// seed a built-in role
	db.Create(&model.Role{ID: "sys-1", Name: "超级管理员", Source: model.RoleSourceSystem})

	// create custom role
	w := do(t, r, http.MethodPost, "/api/safe/v1/authz/roles",
		map[string]any{"id": "c-1", "name": "Auditors", "description": "read logs"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create role: %d (%s)", w.Code, w.Body.String())
	}

	// list -> both, custom flagged built_in=false
	w = do(t, r, http.MethodGet, "/api/safe/v1/authz/roles", nil)
	var list struct {
		Roles []struct {
			ID      string `json:"id"`
			Source  string `json:"source"`
			BuiltIn bool   `json:"built_in"`
		} `json:"roles"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Roles) != 2 {
		t.Fatalf("list: want 2 roles, got %d", len(list.Roles))
	}

	// update custom -> 204
	w = do(t, r, http.MethodPut, "/api/safe/v1/authz/roles/c-1", map[string]any{"name": "Audit Team"})
	if w.Code != http.StatusNoContent {
		t.Errorf("update custom: want 204, got %d", w.Code)
	}
	// update built-in -> 403
	w = do(t, r, http.MethodPut, "/api/safe/v1/authz/roles/sys-1", map[string]any{"name": "hax"})
	if w.Code != http.StatusForbidden {
		t.Errorf("update built-in: want 403, got %d", w.Code)
	}
	// delete built-in -> 403
	w = do(t, r, http.MethodDelete, "/api/safe/v1/authz/roles/sys-1", nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("delete built-in: want 403, got %d", w.Code)
	}

	// grant the custom role a permission, then it shows in GET /roles/:id
	w = do(t, r, http.MethodPost, "/api/safe/v1/authz/roles/c-1/permissions",
		map[string]any{"resource": map[string]string{"type": "audit", "id": "*"}, "operations": []string{"list"}})
	if w.Code != http.StatusNoContent {
		t.Fatalf("grant role perm: %d (%s)", w.Code, w.Body.String())
	}
	// bind a member and read it back
	_ = e.AssignRole("u-9", "c-1")
	w = do(t, r, http.MethodGet, "/api/safe/v1/authz/roles/c-1", nil)
	var detail struct {
		Members     []string `json:"members"`
		Permissions []struct {
			Resource   map[string]string `json:"resource"`
			Operations []string          `json:"operations"`
		} `json:"permissions"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if len(detail.Members) != 1 || detail.Members[0] != "u-9" {
		t.Errorf("members = %v", detail.Members)
	}
	if len(detail.Permissions) != 1 || detail.Permissions[0].Resource["type"] != "audit" {
		t.Errorf("permissions = %v", detail.Permissions)
	}

	// delete custom role -> 204 + casbin purged (member binding gone)
	w = do(t, r, http.MethodDelete, "/api/safe/v1/authz/roles/c-1", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete custom: %d", w.Code)
	}
	members, _ := e.RoleMembers("c-1")
	if len(members) != 0 {
		t.Errorf("role binding not purged on delete: %v", members)
	}

	// get unknown -> 404
	w = do(t, r, http.MethodGet, "/api/safe/v1/authz/roles/ghost", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("get ghost role: want 404, got %d", w.Code)
	}
}

func TestRoleBindingsListAndUnbind(t *testing.T) {
	r, e, _, _ := newAdminServer(t)

	// bind via API
	w := do(t, r, http.MethodPost, "/api/safe/v1/authz/role-bindings",
		map[string]any{"accessor_id": "u-1", "role_id": "r-a"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("bind: %d", w.Code)
	}
	_ = e.AssignRole("u-1", "r-b")

	// list roles of accessor
	w = do(t, r, http.MethodGet, "/api/safe/v1/authz/role-bindings?accessor_id=u-1", nil)
	var resp struct {
		RoleIDs []string `json:"role_ids"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.RoleIDs) != 2 {
		t.Fatalf("role_ids = %v", resp.RoleIDs)
	}

	// unbind one
	w = do(t, r, http.MethodDelete, "/api/safe/v1/authz/role-bindings",
		map[string]any{"accessor_id": "u-1", "role_id": "r-a"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("unbind: %d", w.Code)
	}
	w = do(t, r, http.MethodGet, "/api/safe/v1/authz/role-bindings?accessor_id=u-1", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.RoleIDs) != 1 || resp.RoleIDs[0] != "r-b" {
		t.Errorf("after unbind role_ids = %v", resp.RoleIDs)
	}

	// missing accessor_id -> 400
	w = do(t, r, http.MethodGet, "/api/safe/v1/authz/role-bindings", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing accessor_id: want 400, got %d", w.Code)
	}
}
