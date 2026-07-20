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

func grantAdminSurface(t *testing.T, e *authz.Enforcer, roleID string) {
	t.Helper()
	if err := e.Grant(roleID, "safe_admin:console", "manage"); err != nil {
		t.Fatalf("grant admin surface: %v", err)
	}
}

func grantRoleOps(t *testing.T, e *authz.Enforcer, roleID, resourceType string, ops ...string) {
	t.Helper()
	for _, op := range ops {
		if err := e.Grant(roleID, resourceType+":*", op); err != nil {
			t.Fatalf("grant %s:%s to %s: %v", resourceType, op, roleID, err)
		}
	}
}

func bindRole(t *testing.T, e *authz.Enforcer, accessorID, roleID string) {
	t.Helper()
	if err := e.AssignRole(accessorID, roleID); err != nil {
		t.Fatalf("bind role: %v", err)
	}
}

func TestThreeAdminRolesUseEndpointLevelPermissions(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	ctx := t.Context()

	const (
		adminRole    = "role-admin"
		securityRole = "role-security"
		auditRole    = "role-audit"
		adminUser    = "u-admin-role"
		securityUser = "u-security-role"
		auditUser    = "u-audit-role"
	)
	for _, roleID := range []string{adminRole, securityRole, auditRole} {
		grantAdminSurface(t, e, roleID)
	}
	grantRoleOps(t, e, adminRole, "admin-user", "view", "create", "edit", "delete", "toggle", "reset-password")
	grantRoleOps(t, e, adminRole, "admin-dept", "view", "create", "edit", "delete", "members")
	grantRoleOps(t, e, securityRole, "admin-user", "view", "toggle", "reset-password")
	grantRoleOps(t, e, securityRole, "admin-role", "view", "create", "edit", "delete", "members")
	grantRoleOps(t, e, securityRole, "admin-authz", "view", "grant", "revoke")
	grantRoleOps(t, e, auditRole, "admin-user", "view")
	grantRoleOps(t, e, auditRole, "admin-role", "view")
	grantRoleOps(t, e, auditRole, "admin-authz", "view")
	grantRoleOps(t, e, auditRole, "admin-audit", "view")

	for _, userID := range []string{adminUser, securityUser, auditUser, "target-user"} {
		if err := users.CreateLocalUser(ctx, &model.User{ID: userID, Account: userID, Name: userID, Enabled: true}, "pw-init0"); err != nil {
			t.Fatalf("create user %s: %v", userID, err)
		}
	}
	if err := db.Create(&model.Role{ID: "target-role", Name: "target-role", Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatalf("create target role: %v", err)
	}
	bindRole(t, e, adminUser, adminRole)
	bindRole(t, e, securityUser, securityRole)
	bindRole(t, e, auditUser, auditRole)

	createUser := func(account string) gin.H {
		return gin.H{"account": account, "name": account, "password": "pw-init0"}
	}
	createRole := func(name string) gin.H {
		return gin.H{"id": name, "name": name}
	}
	bindTarget := gin.H{"accessor_id": "target-user", "role_id": "target-role"}

	cases := []struct {
		name, token, method, path string
		body                      any
		want                      int
	}{
		{"admin creates user", adminUser, http.MethodPost, "/api/safe/v1/admin/users", createUser("admin-created"), http.StatusCreated},
		{"admin cannot create role", adminUser, http.MethodPost, "/api/safe/v1/admin/roles", createRole("admin-role-created"), http.StatusForbidden},
		{"admin cannot bind role", adminUser, http.MethodPost, "/api/safe/v1/admin/role-bindings", bindTarget, http.StatusForbidden},
		{"admin cannot read audit", adminUser, http.MethodGet, "/api/safe/v1/admin/audit-logs", nil, http.StatusForbidden},
		{"security cannot create user", securityUser, http.MethodPost, "/api/safe/v1/admin/users", createUser("security-created-user"), http.StatusForbidden},
		{"security toggles user", securityUser, http.MethodPut, "/api/safe/v1/admin/users/target-user", gin.H{"enabled": false}, http.StatusNoContent},
		{"security cannot edit user profile", securityUser, http.MethodPut, "/api/safe/v1/admin/users/target-user", gin.H{"name": "changed"}, http.StatusForbidden},
		{"security creates role", securityUser, http.MethodPost, "/api/safe/v1/admin/roles", createRole("security-role-created"), http.StatusCreated},
		{"security binds role", securityUser, http.MethodPost, "/api/safe/v1/admin/role-bindings", bindTarget, http.StatusNoContent},
		{"security cannot read audit", securityUser, http.MethodGet, "/api/safe/v1/admin/audit-logs", nil, http.StatusForbidden},
		{"audit cannot create user", auditUser, http.MethodPost, "/api/safe/v1/admin/users", createUser("audit-created-user"), http.StatusForbidden},
		{"audit cannot create role", auditUser, http.MethodPost, "/api/safe/v1/admin/roles", createRole("audit-role-created"), http.StatusForbidden},
		{"audit cannot bind role", auditUser, http.MethodPost, "/api/safe/v1/admin/role-bindings", bindTarget, http.StatusForbidden},
		{"audit reads audit", auditUser, http.MethodGet, "/api/safe/v1/admin/audit-logs", nil, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if w := tokReq(t, r, c.method, c.path, c.body, c.token); w.Code != c.want {
				t.Fatalf("want %d, got %d: %s", c.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestThreeAdminRoleBindingsAreMutuallyExclusive(t *testing.T) {
	r, _, _, users := newAdminServer(t)
	ctx := t.Context()
	const accessorID = "three-admin-target"
	if err := users.CreateLocalUser(ctx, &model.User{ID: accessorID, Account: accessorID, Name: accessorID, Enabled: true}, "pw-init0"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	for _, roleID := range threeAdminRoleIDs {
		if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/roles", gin.H{
			"id": roleID, "name": roleID,
		}); w.Code != http.StatusCreated {
			t.Fatalf("create role %s: want 201, got %d: %s", roleID, w.Code, w.Body.String())
		}
	}
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/role-bindings", gin.H{
		"accessor_id": accessorID,
		"role_id":     threeAdminRoleIDs[0],
	}); w.Code != http.StatusNoContent {
		t.Fatalf("bind first three-admin role: want 204, got %d: %s", w.Code, w.Body.String())
	}
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/role-bindings", gin.H{
		"accessor_id": accessorID,
		"role_id":     threeAdminRoleIDs[1],
	}); w.Code != http.StatusConflict {
		t.Fatalf("bind second three-admin role: want 409, got %d: %s", w.Code, w.Body.String())
	}
}

// TestRoleBindingEscalationGuards covers the three ways a role-manager (the
// `security` admin holds admin-role:members) could previously promote itself to
// platform administrator without any other administrator's involvement:
// binding super_admin, binding a system role to itself, and — via the two
// sibling routes — minting a wildcard policy or grabbing safe_admin directly.
func TestRoleBindingEscalationGuards(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	ctx := t.Context()

	const (
		securityUser   = "u-security-esc"
		securityRole   = "role-security-esc"
		superRoleID    = "role-super-esc"
		businessRoleID = "role-business-esc"
		victim         = "u-victim-esc"
	)
	for _, id := range []string{securityUser, victim} {
		if err := users.CreateLocalUser(ctx, &model.User{ID: id, Account: id, Name: id, Enabled: true}, "pw-init0"); err != nil {
			t.Fatalf("create user %s: %v", id, err)
		}
	}
	// The seeded super_admin role is identified by NAME, so the row must carry it.
	if err := db.Create(&model.Role{ID: superRoleID, Name: superAdminRoleName, Source: model.RoleSourceSystem}).Error; err != nil {
		t.Fatalf("create super role: %v", err)
	}
	if err := db.Create(&model.Role{ID: businessRoleID, Name: "builder", Source: model.RoleSourceBusiness}).Error; err != nil {
		t.Fatalf("create business role: %v", err)
	}
	if err := e.Grant(superRoleID, "*", "*"); err != nil {
		t.Fatalf("grant super role: %v", err)
	}
	// security: may administer (safe_admin) and may manage role membership and
	// authorization — exactly the seeded grant set.
	grantRoleOps(t, e, securityRole, "safe_admin", "manage")
	grantRoleOps(t, e, securityRole, "admin-role", "members")
	grantRoleOps(t, e, securityRole, "admin-authz", "grant")
	bindRole(t, e, securityUser, securityRole)

	// super_admin is a seed-fixed singleton: no caller may add a holder, and a
	// holder may not appoint a successor either — there is no succession through
	// this API at all.
	t.Run("role-manager cannot bind super_admin", func(t *testing.T) {
		if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/admin/role-bindings", gin.H{
			"accessor_id": victim, "role_id": superRoleID,
		}, securityUser); w.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("super_admin cannot appoint another super_admin", func(t *testing.T) {
		if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/role-bindings", gin.H{
			"accessor_id": victim, "role_id": superRoleID,
		}); w.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("super_admin binding cannot be removed either", func(t *testing.T) {
		if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/role-bindings", gin.H{
			"accessor_id": adminSub, "role_id": superRoleID,
		}); w.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("cannot self-bind a system role", func(t *testing.T) {
		sysRole := "role-system-esc"
		if err := db.Create(&model.Role{ID: sysRole, Name: "ops", Source: model.RoleSourceSystem}).Error; err != nil {
			t.Fatal(err)
		}
		if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/admin/role-bindings", gin.H{
			"accessor_id": securityUser, "role_id": sysRole,
		}, securityUser); w.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	// Ordinary role assignment — including to oneself — must keep working.
	t.Run("self-binding a business role still allowed", func(t *testing.T) {
		if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/admin/role-bindings", gin.H{
			"accessor_id": securityUser, "role_id": businessRoleID,
		}, securityUser); w.Code != http.StatusNoContent {
			t.Fatalf("want 204, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("custom role cannot be granted a wildcard type or operation", func(t *testing.T) {
		if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/roles", gin.H{
			"id": "role-custom-esc", "name": "custom-esc",
		}); w.Code != http.StatusCreated {
			t.Fatalf("create custom role: %d %s", w.Code, w.Body.String())
		}
		for _, body := range []gin.H{
			{"resource": gin.H{"type": "*", "id": "*"}, "operations": []string{"view_detail"}},
			{"resource": gin.H{"type": "agent", "id": "*"}, "operations": []string{"*"}},
		} {
			if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/admin/roles/role-custom-esc/permissions", body, securityUser); w.Code != http.StatusBadRequest {
				t.Fatalf("want 400 for %v, got %d: %s", body, w.Code, w.Body.String())
			}
		}
		// Whole-type grants (concrete type, id "*") remain the documented use.
		if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/admin/roles/role-custom-esc/permissions", gin.H{
			"resource": gin.H{"type": "agent", "id": "*"}, "operations": []string{"use"},
		}, securityUser); w.Code != http.StatusNoContent {
			t.Fatalf("whole-type grant should still work: %d %s", w.Code, w.Body.String())
		}
	})

	t.Run("admin console cannot be object-granted", func(t *testing.T) {
		if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", gin.H{
			"accessor_id": securityUser,
			"resource":    gin.H{"type": adminConsoleResourceType, "id": "console"},
			"operations":  []string{"manage"},
		}, securityUser); w.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
		}
	})
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
			ID        string `json:"id"`
			CreatedAt string `json:"created_at"`
		} `json:"roles"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Roles) != 2 {
		t.Fatalf("list: want 2 roles, got %d", len(list.Roles))
	}
	if list.Roles[0].CreatedAt == "" || list.Roles[1].CreatedAt == "" {
		t.Fatalf("list role created_at should be returned: %+v", list.Roles)
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
		CreatedAt   string   `json:"created_at"`
		Members     []string `json:"members"`
		Permissions []struct {
			Resource map[string]string `json:"resource"`
		} `json:"permissions"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if detail.CreatedAt == "" {
		t.Error("detail role created_at should be returned")
	}
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
	adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/departments/d-1", nil)
	adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/users/ghost", nil)
	// a GET must NOT be audited (no feedback loop on the read path)
	adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments", nil)

	var total int64
	db.Model(&model.AuditLog{}).Count(&total)
	if total != 4 {
		t.Fatalf("audit rows = %d, want 4 (GET excluded)", total)
	}

	type logRow struct {
		ActorID    string `json:"actor_id"`
		Method     string `json:"method"`
		Resource   string `json:"resource"`
		Action     string `json:"action"`
		TargetID   string `json:"target_id"`
		TargetName string `json:"target_name"`
		Status     int    `json:"status"`
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

	// filter by resource=departments -> the create + rename + delete
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/audit-logs?resource=departments", nil)
	var depts listResp
	_ = json.Unmarshal(w.Body.Bytes(), &depts)
	if depts.Total != 3 {
		t.Errorf("resource=departments: total=%d, want 3", depts.Total)
	}
	var deleteDept *logRow
	for i := range depts.Logs {
		if depts.Logs[i].Method == http.MethodDelete {
			deleteDept = &depts.Logs[i]
			break
		}
	}
	if deleteDept == nil || deleteDept.TargetName != "Renamed" {
		t.Errorf("delete department target snapshot = %+v, want target_name Renamed", deleteDept)
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
