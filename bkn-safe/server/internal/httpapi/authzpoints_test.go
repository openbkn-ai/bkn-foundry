// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// The authorization-management surface is guarded per endpoint action, not by the
// admin-console gate. These tests pin the point-per-endpoint contract: the gate
// alone opens nothing, each write needs its own point, and the read points are
// what an auditor holds.

const objectGrantsPath = "/api/safe/v1/admin/object-grants"

func TestAdminConsoleGateAloneAuthorizesNothing(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	if err := users.CreateLocalUser(t.Context(),
		&model.User{ID: "grantee-1", Account: "grantee-1", Name: "grantee-1", Enabled: true}, "pw-init0"); err != nil {
		t.Fatalf("create grantee: %v", err)
	}
	const (
		roleID = "custom-role"
		caller = "console-only-user"
	)
	if err := db.Create(&model.User{ID: caller, Account: caller, Enabled: true}).Error; err != nil {
		t.Fatalf("create caller: %v", err)
	}
	if err := db.Create(&model.Role{ID: roleID, Name: roleID, Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatalf("create custom role: %v", err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")

	// The caller holds ONLY safe_admin:console:manage: it passes RequireAdmin and
	// must be refused by every endpoint's own point.
	grantAdminSurface(t, e, "role-console-only")
	bindRole(t, e, caller, "role-console-only")

	grantBody := gin.H{
		"accessor_id": "grantee-1",
		"resource":    gin.H{"type": "catalog", "id": "c1"},
		"operations":  []string{"view_detail"},
	}
	revokeBody := gin.H{"accessor_id": "grantee-1", "resource": gin.H{"type": "catalog", "id": "c1"}}
	bindBody := gin.H{"accessor_id": "grantee-1", "role_id": roleID}
	permBody := gin.H{"resource": gin.H{"type": "catalog", "id": "*"}, "operations": []string{"view_detail"}}
	rolePerms := "/api/safe/v1/admin/roles/" + roleID + "/permissions"

	cases := []struct {
		name, method, path string
		body               any
	}{
		{"grant object", http.MethodPost, objectGrantsPath, grantBody},
		{"revoke object", http.MethodDelete, objectGrantsPath, revokeBody},
		{"bind role", http.MethodPost, "/api/safe/v1/admin/role-bindings", bindBody},
		{"unbind role", http.MethodDelete, "/api/safe/v1/admin/role-bindings", bindBody},
		{"configure role permissions", http.MethodPost, rolePerms, permBody},
		{"revoke role permissions", http.MethodDelete, rolePerms, permBody},
		{"list object grants", http.MethodGet, objectGrantsPath, nil},
		{"read policies", http.MethodGet, "/api/safe/v1/admin/policies?resource_type=catalog&resource_id=c1", nil},
		{"read role permissions", http.MethodGet, rolePerms, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if w := tokReq(t, r, c.method, c.path, c.body, caller); w.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// Role-permission writes are guarded by admin-role:permissions. The point they
// used to require — admin-authz:grant/revoke — stays accepted for the
// deprecation window, so a custom role built on the old point survives upgrade.
func TestRolePermissionWritePointSplit(t *testing.T) {
	permBody := gin.H{"resource": gin.H{"type": "catalog", "id": "*"}, "operations": []string{"view_detail"}}

	cases := []struct {
		name              string
		ops               map[string][]string // resourceType -> ops
		wantPost, wantDel int
	}{
		{
			name:     "canonical point",
			ops:      map[string][]string{"admin-role": {"permissions"}},
			wantPost: http.StatusNoContent, wantDel: http.StatusNoContent,
		},
		{
			name:     "legacy authz points still accepted",
			ops:      map[string][]string{"admin-authz": {"grant", "revoke"}},
			wantPost: http.StatusNoContent, wantDel: http.StatusNoContent,
		},
		{
			name:     "legacy grant does not open revoke",
			ops:      map[string][]string{"admin-authz": {"grant"}},
			wantPost: http.StatusNoContent, wantDel: http.StatusForbidden,
		},
		{
			name:     "authz view opens neither",
			ops:      map[string][]string{"admin-authz": {"view"}},
			wantPost: http.StatusForbidden, wantDel: http.StatusForbidden,
		},
		{
			name:     "role members does not open permissions",
			ops:      map[string][]string{"admin-role": {"view", "members"}},
			wantPost: http.StatusForbidden, wantDel: http.StatusForbidden,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, e, db, users := newAdminServer(t)
			if err := db.Create(&model.Role{ID: "custom-role", Name: "custom-role", Source: model.RoleSourceCustom}).Error; err != nil {
				t.Fatalf("create custom role: %v", err)
			}
			seedCatalogOps(t, db, "catalog", "view_detail")
			const caller = "point-user"
			if err := users.CreateLocalUser(t.Context(), &model.User{ID: caller, Account: caller, Enabled: true}, "pw-init0"); err != nil {
				t.Fatalf("create caller: %v", err)
			}
			grantAdminSurface(t, e, "role-point")
			for rtype, ops := range c.ops {
				grantRoleOps(t, e, "role-point", rtype, ops...)
			}
			bindRole(t, e, caller, "role-point")

			path := "/api/safe/v1/admin/roles/custom-role/permissions"
			if w := tokReq(t, r, http.MethodPost, path, permBody, caller); w.Code != c.wantPost {
				t.Fatalf("POST: want %d, got %d: %s", c.wantPost, w.Code, w.Body.String())
			}
			if w := tokReq(t, r, http.MethodDelete, path, permBody, caller); w.Code != c.wantDel {
				t.Fatalf("DELETE: want %d, got %d: %s", c.wantDel, w.Code, w.Body.String())
			}
		})
	}
}

// admin-authz:view is the policy-review point: it opens the read surface an
// auditor needs (per-resource policies, a role's permission set is admin-role:view)
// and nothing that writes.
func TestPolicyReadPoints(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	if err := users.CreateLocalUser(t.Context(),
		&model.User{ID: "grantee-1", Account: "grantee-1", Enabled: true}, "pw-init0"); err != nil {
		t.Fatalf("create grantee: %v", err)
	}
	if err := db.Create(&model.Role{ID: "custom-role", Name: "custom-role", Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatalf("create custom role: %v", err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail")
	if err := e.GrantObjectPermission("grantee-1", "catalog", "c1", "view_detail"); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	if err := e.GrantRolePermission("custom-role", "catalog", "*", "view_detail"); err != nil {
		t.Fatalf("seed role grant: %v", err)
	}

	const reviewer = "reviewer-user"
	if err := users.CreateLocalUser(t.Context(), &model.User{ID: reviewer, Account: reviewer, Enabled: true}, "pw-init0"); err != nil {
		t.Fatalf("create reviewer: %v", err)
	}
	grantAdminSurface(t, e, "role-reviewer")
	grantRoleOps(t, e, "role-reviewer", "admin-authz", "view")
	grantRoleOps(t, e, "role-reviewer", "admin-role", "view")
	bindRole(t, e, reviewer, "role-reviewer")

	w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/policies?resource_type=catalog&resource_id=c1", nil, reviewer)
	if w.Code != http.StatusOK {
		t.Fatalf("policies read: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var policies struct {
		Entries []struct {
			AccessorID string   `json:"accessor_id"`
			Operations []string `json:"operations"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &policies); err != nil {
		t.Fatalf("decode policies: %v", err)
	}
	if len(policies.Entries) != 1 || policies.Entries[0].AccessorID != "grantee-1" {
		t.Fatalf("policies: want the single grantee-1 entry, got %+v", policies.Entries)
	}

	w = tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/roles/custom-role/permissions", nil, reviewer)
	if w.Code != http.StatusOK {
		t.Fatalf("role permissions read: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var perms struct {
		Permissions []struct {
			Resource struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"resource"`
			Operations []string `json:"operations"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &perms); err != nil {
		t.Fatalf("decode role permissions: %v", err)
	}
	if len(perms.Permissions) != 1 || perms.Permissions[0].Resource.Type != "catalog" {
		t.Fatalf("role permissions: want one catalog grant, got %+v", perms.Permissions)
	}

	// resource_type is mandatory: without it the read would mean "every policy".
	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/policies", nil, reviewer); w.Code != http.StatusBadRequest {
		t.Fatalf("policies without resource_type: want 400, got %d", w.Code)
	}

	// The review points must not open the writes.
	if w := tokReq(t, r, http.MethodPost, objectGrantsPath, gin.H{
		"accessor_id": "grantee-1",
		"resource":    gin.H{"type": "catalog", "id": "c1"},
		"operations":  []string{"view_detail"},
	}, reviewer); w.Code != http.StatusForbidden {
		t.Fatalf("reviewer grant: want 403, got %d", w.Code)
	}
}

// Revoke is idempotent for the caller (204 whether or not it matched) but the
// difference is recorded for the auditor: Detail carries _outcome.removed.
func TestObjectGrantRevokeSemantics(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	if err := users.CreateLocalUser(t.Context(),
		&model.User{ID: "grantee-1", Account: "grantee-1", Enabled: true}, "pw-init0"); err != nil {
		t.Fatalf("create grantee: %v", err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")
	for _, op := range []string{"view_detail", "modify"} {
		if err := e.GrantObjectPermission("grantee-1", "catalog", "c1", op); err != nil {
			t.Fatalf("seed grant %s: %v", op, err)
		}
	}

	// An unregistered resource type is a typo, not a no-op revoke.
	if w := adminReq(t, r, http.MethodDelete, objectGrantsPath, gin.H{
		"accessor_id": "grantee-1", "resource": gin.H{"type": "nope", "id": "c1"},
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown type: want 400, got %d: %s", w.Code, w.Body.String())
	}

	clearAuditLog(t, db)
	if w := adminReq(t, r, http.MethodDelete, objectGrantsPath, gin.H{
		"accessor_id": "grantee-1", "resource": gin.H{"type": "catalog", "id": "c1"},
	}); w.Code != http.StatusNoContent {
		t.Fatalf("revoke: want 204, got %d: %s", w.Code, w.Body.String())
	}
	if detail := onlyAuditDetail(t, db); !strings.Contains(detail, `"removed":2`) {
		t.Fatalf("effective revoke: want _outcome.removed=2 in audit detail, got %s", detail)
	}

	// Same request again: still 204, but the audit trail shows it matched nothing.
	clearAuditLog(t, db)
	if w := adminReq(t, r, http.MethodDelete, objectGrantsPath, gin.H{
		"accessor_id": "grantee-1", "resource": gin.H{"type": "catalog", "id": "c1"},
	}); w.Code != http.StatusNoContent {
		t.Fatalf("repeat revoke: want 204, got %d: %s", w.Code, w.Body.String())
	}
	if detail := onlyAuditDetail(t, db); !strings.Contains(detail, `"removed":0`) {
		t.Fatalf("repeat revoke: want _outcome.removed=0 in audit detail, got %s", detail)
	}
}

func clearAuditLog(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Where("1 = 1").Delete(&model.AuditLog{}).Error; err != nil {
		t.Fatalf("clear audit log: %v", err)
	}
}

// onlyAuditDetail returns the Detail column of the single audit row present,
// failing when the count is not exactly one (the caller clears the table first,
// so "one row" is what pins the assertion to the request under test).
func onlyAuditDetail(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var rows []model.AuditLog
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit log: want exactly 1 row, got %d", len(rows))
	}
	return rows[0].Detail
}
