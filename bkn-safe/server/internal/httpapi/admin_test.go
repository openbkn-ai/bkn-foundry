// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"bkn-safe/internal/audit"
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
		Audit:         audit.New(db),
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

func TestUserListSearchAndFindByAccount(t *testing.T) {
	r, _, _, users := newAdminServer(t)
	ctx := t.Context()
	for _, u := range []*model.User{
		{ID: "u-a", Account: "alice", Name: "Alice", Enabled: true},
		{ID: "u-b", Account: "bob", Name: "Bobby", Enabled: true},
		{ID: "u-c", Account: "carol", Name: "Alicia", Enabled: true},
	} {
		if err := users.CreateLocalUser(ctx, u, "pw-init0"); err != nil {
			t.Fatal(err)
		}
	}

	// list all
	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/users", nil)
	var list struct {
		Users []directory.UserSummary `json:"users"`
		Total int                     `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if list.Total != 3 || len(list.Users) != 3 {
		t.Fatalf("list all: total=%d len=%d", list.Total, len(list.Users))
	}

	// search "ali" matches alice(account) + Alicia(name)
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/users?search=ali", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if list.Total != 2 {
		t.Errorf("search ali: want 2, got %d", list.Total)
	}

	// exact account lookup
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/users?account=bob", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Users) != 1 || list.Users[0].ID != "u-b" {
		t.Errorf("account=bob: %v", list.Users)
	}

	// account miss -> empty, not 404
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/users?account=ghost", nil)
	if w.Code != http.StatusOK {
		t.Errorf("account miss: want 200, got %d", w.Code)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Users) != 0 {
		t.Errorf("account miss: want empty, got %v", list.Users)
	}
}

func TestDepartmentDetailAndMembers(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	db.Create(&model.Department{ID: "d-eng", Name: "Engineering"})
	db.Create(&model.Department{ID: "d-eng-be", Name: "Backend", ParentID: "d-eng"})
	db.Create(&model.User{ID: "u-1", Account: "alice", Name: "Alice", Enabled: true})
	db.Create(&model.User{ID: "u-2", Account: "bob", Name: "Bob", Enabled: true})
	db.Create(&model.UserDepartment{UserID: "u-1", DepartmentID: "d-eng"})
	db.Create(&model.UserDepartment{UserID: "u-2", DepartmentID: "d-eng"})

	// single detail
	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments/d-eng", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("dept detail: %d", w.Code)
	}
	var d model.Department
	_ = json.Unmarshal(w.Body.Bytes(), &d)
	if d.Name != "Engineering" {
		t.Errorf("dept detail name = %q", d.Name)
	}
	if w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments/ghost", nil); w.Code != http.StatusNotFound {
		t.Errorf("dept detail ghost: want 404, got %d", w.Code)
	}

	// flat list (no parent_id) -> both depts
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments", nil)
	var flat struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &flat)
	if flat.Total != 2 {
		t.Errorf("flat list total = %d, want 2", flat.Total)
	}

	// scoped (parent_id=d-eng) -> only the child
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments?parent_id=d-eng", nil)
	var scoped struct {
		Departments []model.Department `json:"departments"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &scoped)
	if len(scoped.Departments) != 1 || scoped.Departments[0].ID != "d-eng-be" {
		t.Errorf("scoped children = %v", scoped.Departments)
	}

	// members
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments/d-eng/members", nil)
	var mem struct {
		Users []directory.UserSummary `json:"users"`
		Total int                     `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &mem)
	if mem.Total != 2 {
		t.Errorf("dept members total = %d, want 2", mem.Total)
	}
}

func TestRoleBindingsListAndUnbind(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	db.Create(&model.User{ID: "u-1", Account: "u1", Name: "U1", Enabled: true})
	db.Create(&model.Role{ID: "r-a", Name: "RoleA", Source: model.RoleSourceCustom})

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

// Binding must reference an existing accessor (user/department/group) and an
// existing role — a typo'd accessor (e.g. an account NAME instead of its ID)
// must fail loudly, not 204 into a grant that never matches at enforce time.
func TestRoleBindingValidatesAccessorAndRole(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	db.Create(&model.User{ID: "u-real", Account: "real", Name: "Real", Enabled: true})
	db.Create(&model.Role{ID: "r-real", Name: "RoleReal", Source: model.RoleSourceCustom})

	// account name passed where the user ID belongs -> 400
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/role-bindings",
		map[string]any{"accessor_id": "real", "role_id": "r-real"}); w.Code != http.StatusBadRequest {
		t.Errorf("unknown accessor: want 400, got %d (%s)", w.Code, w.Body.String())
	}
	// unknown role -> 400
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/role-bindings",
		map[string]any{"accessor_id": "u-real", "role_id": "r-ghost"}); w.Code != http.StatusBadRequest {
		t.Errorf("unknown role: want 400, got %d (%s)", w.Code, w.Body.String())
	}
	// both valid -> 204
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/role-bindings",
		map[string]any{"accessor_id": "u-real", "role_id": "r-real"}); w.Code != http.StatusNoContent {
		t.Errorf("valid bind: want 204, got %d (%s)", w.Code, w.Body.String())
	}

	// department and group accessors are also valid binding subjects
	db.Create(&model.Department{ID: "d-1", Name: "Dept"})
	db.Create(&model.Group{ID: "g-1", Name: "Group"})
	for _, id := range []string{"d-1", "g-1"} {
		if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/role-bindings",
			map[string]any{"accessor_id": id, "role_id": "r-real"}); w.Code != http.StatusNoContent {
			t.Errorf("bind %s: want 204, got %d (%s)", id, w.Code, w.Body.String())
		}
	}
}

func TestDepartmentMembershipWrite(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	ctx := t.Context()
	db.Create(&model.Department{ID: "d-eng", Name: "Engineering"})
	for _, u := range []*model.User{
		{ID: "u-1", Account: "alice", Name: "Alice", Enabled: true},
		{ID: "u-2", Account: "bob", Name: "Bob", Enabled: true},
	} {
		if err := users.CreateLocalUser(ctx, u, "pw-init0"); err != nil {
			t.Fatal(err)
		}
	}

	// assign two members
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments/d-eng/members",
		map[string]any{"user_ids": []string{"u-1", "u-2"}}); w.Code != http.StatusNoContent {
		t.Fatalf("add members: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	// idempotent re-add -> still 204, no duplicate row
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments/d-eng/members",
		map[string]any{"user_ids": []string{"u-1"}}); w.Code != http.StatusNoContent {
		t.Fatalf("re-add: want 204, got %d", w.Code)
	}
	var n int64
	db.Model(&model.UserDepartment{}).Where("department_id = ?", "d-eng").Count(&n)
	if n != 2 {
		t.Fatalf("membership rows = %d, want 2 (idempotent)", n)
	}

	// GET members reflects the writes
	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments/d-eng/members", nil)
	var mem struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &mem)
	if mem.Total != 2 {
		t.Errorf("members total = %d, want 2", mem.Total)
	}

	// user detail shows the department
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/users/u-1", nil)
	var detail struct {
		Departments []string `json:"departments"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if len(detail.Departments) != 1 || detail.Departments[0] != "d-eng" {
		t.Errorf("user departments = %v", detail.Departments)
	}

	// unknown user -> 400 (nothing written)
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments/d-eng/members",
		map[string]any{"user_ids": []string{"ghost"}}); w.Code != http.StatusBadRequest {
		t.Errorf("unknown user: want 400, got %d (%s)", w.Code, w.Body.String())
	}
	// unknown department -> 404
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments/ghost/members",
		map[string]any{"user_ids": []string{"u-1"}}); w.Code != http.StatusNotFound {
		t.Errorf("unknown dept: want 404, got %d", w.Code)
	}

	// remove one member (idempotent: a non-member id is ignored)
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/departments/d-eng/members",
		map[string]any{"user_ids": []string{"u-1", "never-was"}}); w.Code != http.StatusNoContent {
		t.Fatalf("remove member: want 204, got %d", w.Code)
	}
	db.Model(&model.UserDepartment{}).Where("department_id = ?", "d-eng").Count(&n)
	if n != 1 {
		t.Errorf("after remove rows = %d, want 1", n)
	}

	// still one member -> department delete remains blocked (409)
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/departments/d-eng", nil); w.Code != http.StatusConflict {
		t.Errorf("delete dept with member: want 409, got %d", w.Code)
	}
}

func TestAuditTrail(t *testing.T) {
	r, _, db, _ := newAdminServer(t)

	// three mutations: create dept (POST, no :id), rename it (PUT, :id), and
	// delete a ghost user (DELETE, :id -> 404 but still audited).
	adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments",
		map[string]any{"id": "d-1", "name": "Root"})
	adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/departments/d-1",
		map[string]any{"name": "Renamed"})
	adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/users/ghost", nil)
	// a GET must NOT be audited (no feedback loop on the read path)
	adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments", nil)

	var total int64
	db.Model(&model.AuditLog{}).Count(&total)
	if total != 3 {
		t.Fatalf("audit rows = %d, want 3 (GET excluded)", total)
	}

	type logRow struct {
		ActorID  string `json:"actor_id"`
		Method   string `json:"method"`
		Resource string `json:"resource"`
		Action   string `json:"action"`
		TargetID string `json:"target_id"`
		Status   int    `json:"status"`
	}
	type listResp struct {
		Logs  []logRow `json:"logs"`
		Total int      `json:"total"`
	}

	// filter by resource=users -> the single ghost-delete, recorded with its 404
	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/audit-logs?resource=users", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list audit: %d (%s)", w.Code, w.Body.String())
	}
	var users listResp
	_ = json.Unmarshal(w.Body.Bytes(), &users)
	if users.Total != 1 || len(users.Logs) != 1 {
		t.Fatalf("resource=users: total=%d len=%d", users.Total, len(users.Logs))
	}
	got := users.Logs[0]
	if got.ActorID != adminSub || got.Method != http.MethodDelete || got.Action != "users" ||
		got.TargetID != "ghost" || got.Status != http.StatusNotFound {
		t.Errorf("ghost-delete entry = %+v", got)
	}

	// filter by resource=departments -> the create + rename
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/audit-logs?resource=departments", nil)
	var depts listResp
	_ = json.Unmarshal(w.Body.Bytes(), &depts)
	if depts.Total != 2 {
		t.Errorf("resource=departments: total=%d, want 2", depts.Total)
	}
}

func TestUserDepartmentInlineSet(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	db.Create(&model.Department{ID: "d-1", Name: "D1"})
	db.Create(&model.Department{ID: "d-2", Name: "D2"})

	// create with department_ids
	w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/users",
		map[string]any{"id": "u-1", "account": "alice", "name": "Alice",
			"department_ids": []string{"d-1", "d-2"}})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", w.Code, w.Body.String())
	}
	var n int64
	db.Model(&model.UserDepartment{}).Where("user_id = ?", "u-1").Count(&n)
	if n != 2 {
		t.Fatalf("memberships after create = %d, want 2", n)
	}

	// create with an unknown dept -> 400 and NO orphan user
	w = adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/users",
		map[string]any{"id": "u-bad", "account": "bad", "department_ids": []string{"ghost"}})
	if w.Code != http.StatusBadRequest {
		t.Errorf("create bad dept: want 400, got %d (%s)", w.Code, w.Body.String())
	}
	db.Model(&model.User{}).Where("id = ?", "u-bad").Count(&n)
	if n != 0 {
		t.Errorf("orphan user created on bad dept: %d", n)
	}

	// update REPLACES the set: u-1 -> only d-2
	if w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/u-1",
		map[string]any{"department_ids": []string{"d-2"}}); w.Code != http.StatusNoContent {
		t.Fatalf("replace depts: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	var got []model.UserDepartment
	db.Where("user_id = ?", "u-1").Find(&got)
	if len(got) != 1 || got[0].DepartmentID != "d-2" {
		t.Errorf("after replace = %v", got)
	}

	// empty array CLEARS memberships
	if w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/u-1",
		map[string]any{"department_ids": []string{}}); w.Code != http.StatusNoContent {
		t.Fatalf("clear depts: want 204, got %d", w.Code)
	}
	db.Model(&model.UserDepartment{}).Where("user_id = ?", "u-1").Count(&n)
	if n != 0 {
		t.Errorf("memberships after clear = %d, want 0", n)
	}

	// profile-only update (no department_ids key) is still accepted
	if w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/u-1",
		map[string]any{"name": "Alicia"}); w.Code != http.StatusNoContent {
		t.Errorf("profile-only update: want 204, got %d", w.Code)
	}

	// update with an unknown dept -> 400, prior set left untouched
	db.Create(&model.UserDepartment{UserID: "u-1", DepartmentID: "d-1"})
	if w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/u-1",
		map[string]any{"department_ids": []string{"d-1", "ghost"}}); w.Code != http.StatusBadRequest {
		t.Errorf("update bad dept: want 400, got %d", w.Code)
	}
	db.Model(&model.UserDepartment{}).Where("user_id = ?", "u-1").Count(&n)
	if n != 1 {
		t.Errorf("memberships after failed update = %d, want 1 (unchanged)", n)
	}

	// department_ids-only update on an unknown user -> 404
	if w := adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/ghost",
		map[string]any{"department_ids": []string{"d-1"}}); w.Code != http.StatusNotFound {
		t.Errorf("depts update ghost user: want 404, got %d", w.Code)
	}
}

func TestAuditDetailCapture(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	if err := users.CreateLocalUser(t.Context(), &model.User{ID: "u-1", Account: "alice", Name: "Alice", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}

	// a create carries its body into Detail (so "新建部门" shows the name)
	adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments",
		map[string]any{"id": "d-1", "name": "研发部"})
	// a password reset must be recorded with the password MASKED
	adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/users/u-1/password",
		map[string]any{"password": "s3cr3t-should-not-appear"})

	detailFor := func(action string) string {
		w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/audit-logs?action="+action, nil)
		var resp struct {
			Logs []struct {
				Detail string `json:"detail"`
			} `json:"logs"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if len(resp.Logs) != 1 {
			t.Fatalf("action=%s: want 1 log, got %d", action, len(resp.Logs))
		}
		return resp.Logs[0].Detail
	}

	if d := detailFor("departments"); !strings.Contains(d, "研发部") {
		t.Errorf("create-dept detail missing name: %q", d)
	}
	pw := detailFor("users.password")
	if strings.Contains(pw, "s3cr3t-should-not-appear") {
		t.Fatalf("password leaked into audit detail: %q", pw)
	}
	if !strings.Contains(pw, "***") {
		t.Errorf("password not masked in audit detail: %q", pw)
	}

	// sanity: the password is also not anywhere in the raw stored column
	var n int64
	db.Model(&model.AuditLog{}).Where("detail LIKE ?", "%s3cr3t%").Count(&n)
	if n != 0 {
		t.Errorf("password substring found in %d audit rows", n)
	}
}
