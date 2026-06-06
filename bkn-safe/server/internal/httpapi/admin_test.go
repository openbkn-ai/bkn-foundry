package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

// stubVerifier maps a bearer token straight to its subject: the token string IS
// the accessor id. The literal token "bad" is treated as invalid/inactive.
type stubVerifier struct{}

func (stubVerifier) VerifyToken(_ context.Context, token string) (string, error) {
	if token == "" || token == "bad" {
		return "", errors.New("inactive")
	}
	return token, nil
}

const adminSub = "admin-1" // seeded as super-admin in newAdminServer

// newAdminServer builds a full server with the admin API mounted: a stub token
// verifier (token==subject) and adminSub seeded as super-admin (wildcard grant)
// so RequireAdmin passes for Bearer adminSub.
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
	if err := e.Grant(adminSub, "*", "*"); err != nil { // make adminSub a super-admin
		t.Fatalf("grant super-admin: %v", err)
	}
	users := auth.NewUserStore(db)
	r := New(Deps{
		Enforcer: e, DB: db, Directory: directory.New(db), Users: users,
		TokenVerifier: stubVerifier{},
	})
	return r, e, db, users
}

// adminReq issues a request authenticated as the seeded super-admin.
func adminReq(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return tokReq(t, r, method, path, body, adminSub)
}

// tokReq issues a request with an explicit bearer token ("" = no header).
func tokReq(t *testing.T, r *gin.Engine, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAdminAuthGate(t *testing.T) {
	r, _, _, _ := newAdminServer(t)
	const path = "/api/safe/v1/admin/roles"

	// no token -> 401
	if w := tokReq(t, r, http.MethodGet, path, nil, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: want 401, got %d", w.Code)
	}
	// invalid token -> 401
	if w := tokReq(t, r, http.MethodGet, path, nil, "bad"); w.Code != http.StatusUnauthorized {
		t.Errorf("bad token: want 401, got %d", w.Code)
	}
	// valid token, non-admin subject -> 403
	if w := tokReq(t, r, http.MethodGet, path, nil, "random-user"); w.Code != http.StatusForbidden {
		t.Errorf("non-admin: want 403, got %d", w.Code)
	}
	// valid super-admin -> 200
	if w := adminReq(t, r, http.MethodGet, path, nil); w.Code != http.StatusOK {
		t.Errorf("admin: want 200, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestUserUpdateAndDelete(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	ctx := t.Context()
	u := &model.User{ID: "u-1", Account: "bob", Name: "Bob", Enabled: true}
	if err := users.CreateLocalUser(ctx, u, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	_ = e.AssignRole("u-1", "role-x")
	_ = e.GrantObjectPermission("u-1", "agent", "a1", "use")

	w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/u-1",
		map[string]any{"name": "Bobby", "enabled": false})
	if w.Code != http.StatusNoContent {
		t.Fatalf("update: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	var got model.User
	db.First(&got, "id = ?", "u-1")
	if got.Name != "Bobby" || got.Enabled {
		t.Errorf("update not applied: %+v", got)
	}

	if w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/ghost", map[string]any{"name": "x"}); w.Code != http.StatusNotFound {
		t.Errorf("update ghost: want 404, got %d", w.Code)
	}

	w = adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/users/u-1", nil)
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
	if ok, _ := e.Check("u-1", "agent", "a1", "use"); ok {
		t.Error("direct grant not purged")
	}
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/users/u-1", nil); w.Code != http.StatusNotFound {
		t.Errorf("delete twice: want 404, got %d", w.Code)
	}
}

func TestDepartmentCRUD(t *testing.T) {
	r, _, db, _ := newAdminServer(t)

	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments",
		map[string]any{"id": "d-root", "name": "Root"}); w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", w.Code, w.Body.String())
	}
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments",
		map[string]any{"id": "d-child", "name": "Child", "parent_id": "d-root"}); w.Code != http.StatusCreated {
		t.Fatalf("create child: %d", w.Code)
	}

	if w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/departments/d-child",
		map[string]any{"name": "Kid"}); w.Code != http.StatusNoContent {
		t.Fatalf("update: %d", w.Code)
	}
	var d model.Department
	db.First(&d, "id = ?", "d-child")
	if d.Name != "Kid" {
		t.Errorf("rename not applied: %q", d.Name)
	}

	// delete non-empty root -> 409
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/departments/d-root", nil); w.Code != http.StatusConflict {
		t.Errorf("delete non-empty: want 409, got %d", w.Code)
	}
	// delete empty child -> 204, then empty root -> 204
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/departments/d-child", nil); w.Code != http.StatusNoContent {
		t.Errorf("delete child: want 204, got %d", w.Code)
	}
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/departments/d-root", nil); w.Code != http.StatusNoContent {
		t.Errorf("delete root: want 204, got %d", w.Code)
	}
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/departments/ghost", nil); w.Code != http.StatusNotFound {
		t.Errorf("delete ghost: want 404, got %d", w.Code)
	}

	// member guard
	db.Create(&model.Department{ID: "d-hr", Name: "HR"})
	db.Create(&model.UserDepartment{UserID: "u-x", DepartmentID: "d-hr"})
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/departments/d-hr", nil); w.Code != http.StatusConflict {
		t.Errorf("delete dept-with-member: want 409, got %d", w.Code)
	}
}

func TestRoleCRUDAndBuiltInProtection(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	db.Create(&model.Role{ID: "sys-1", Name: "超级管理员", Source: model.RoleSourceSystem})

	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/roles",
		map[string]any{"id": "c-1", "name": "Auditors", "description": "read logs"}); w.Code != http.StatusCreated {
		t.Fatalf("create role: %d (%s)", w.Code, w.Body.String())
	}

	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/roles", nil)
	var list struct {
		Roles []struct {
			ID string `json:"id"`
		} `json:"roles"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Roles) != 2 {
		t.Fatalf("list: want 2 roles, got %d", len(list.Roles))
	}

	if w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/roles/c-1", map[string]any{"name": "Audit Team"}); w.Code != http.StatusNoContent {
		t.Errorf("update custom: want 204, got %d", w.Code)
	}
	if w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/roles/sys-1", map[string]any{"name": "hax"}); w.Code != http.StatusForbidden {
		t.Errorf("update built-in: want 403, got %d", w.Code)
	}
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/roles/sys-1", nil); w.Code != http.StatusForbidden {
		t.Errorf("delete built-in: want 403, got %d", w.Code)
	}

	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/roles/c-1/permissions",
		map[string]any{"resource": map[string]string{"type": "audit", "id": "*"}, "operations": []string{"list"}}); w.Code != http.StatusNoContent {
		t.Fatalf("grant role perm: %d (%s)", w.Code, w.Body.String())
	}
	_ = e.AssignRole("u-9", "c-1")
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/roles/c-1", nil)
	var detail struct {
		Members     []string `json:"members"`
		Permissions []struct {
			Resource map[string]string `json:"resource"`
		} `json:"permissions"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if len(detail.Members) != 1 || detail.Members[0] != "u-9" {
		t.Errorf("members = %v", detail.Members)
	}
	if len(detail.Permissions) != 1 || detail.Permissions[0].Resource["type"] != "audit" {
		t.Errorf("permissions = %v", detail.Permissions)
	}

	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/roles/c-1", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete custom: %d", w.Code)
	}
	if members, _ := e.RoleMembers("c-1"); len(members) != 0 {
		t.Errorf("role binding not purged on delete: %v", members)
	}
	if w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/roles/ghost", nil); w.Code != http.StatusNotFound {
		t.Errorf("get ghost role: want 404, got %d", w.Code)
	}
}

func TestRoleBindingsListAndUnbind(t *testing.T) {
	r, e, _, _ := newAdminServer(t)

	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/role-bindings",
		map[string]any{"accessor_id": "u-1", "role_id": "r-a"}); w.Code != http.StatusNoContent {
		t.Fatalf("bind: %d", w.Code)
	}
	_ = e.AssignRole("u-1", "r-b")

	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/role-bindings?accessor_id=u-1", nil)
	var resp struct {
		RoleIDs []string `json:"role_ids"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.RoleIDs) != 2 {
		t.Fatalf("role_ids = %v", resp.RoleIDs)
	}

	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/role-bindings",
		map[string]any{"accessor_id": "u-1", "role_id": "r-a"}); w.Code != http.StatusNoContent {
		t.Fatalf("unbind: %d", w.Code)
	}
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/role-bindings?accessor_id=u-1", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.RoleIDs) != 1 || resp.RoleIDs[0] != "r-b" {
		t.Errorf("after unbind role_ids = %v", resp.RoleIDs)
	}

	if w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/role-bindings", nil); w.Code != http.StatusBadRequest {
		t.Errorf("missing accessor_id: want 400, got %d", w.Code)
	}
}
