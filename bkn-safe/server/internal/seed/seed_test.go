// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"reflect"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestApplySeedsRolesCatalogGrants verifies the central seed lands roles + the
// catalog and that the network-builder grant makes a real decision pass.
func TestApplySeedsRolesCatalogGrants(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatalf("authz: %v", err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("apply seed: %v", err)
	}

	// 5 Studio roles, with the preserved three-admin UUIDs present.
	var roleCount int64
	db.Model(&model.Role{}).Count(&roleCount)
	if roleCount != 5 {
		t.Errorf("role count = %d, want 5", roleCount)
	}
	for id, name := range map[string]string{
		"7dcfcc9c-ad02-11e8-aa06-000c29358ad6": "super_admin",
		"d2bd2082-ad03-11e8-aa06-000c29358ad6": "admin",
		"d8998f72-ad03-11e8-aa06-000c29358ad6": "security",
		"def246f2-ad03-11e8-aa06-000c29358ad6": "audit",
		"1572fb82-526f-11f0-bde6-e674ec8dde71": "network_builder",
	} {
		var r model.Role
		if err := db.First(&r, "id = ?", id).Error; err != nil {
			t.Errorf("preserved role %s missing: %v", id, err)
		}
		if r.Name != name {
			t.Errorf("role %s name = %q, want %q", id, r.Name, name)
		}
	}
	var networkBuilder model.Role
	if err := db.First(&networkBuilder, "id = ?", "1572fb82-526f-11f0-bde6-e674ec8dde71").Error; err != nil {
		t.Fatal(err)
	}
	if networkBuilder.Description != "负责数据、知识和执行工厂资产的业务网络构建者。" {
		t.Errorf("network_builder description = %q", networkBuilder.Description)
	}

	// Retained Studio admin and Agent resource types are seeded.
	var opCount int64
	db.Model(&model.Operation{}).Where("resource_type_id = ?", "agent").Count(&opCount)
	if opCount != 11 {
		t.Errorf("agent operation count = %d, want 11", opCount)
	}
	db.Model(&model.Operation{}).Where("resource_type_id = ?", "agent_tpl").Count(&opCount)
	if opCount != 3 {
		t.Errorf("agent_tpl operation count = %d, want 3", opCount)
	}
	db.Model(&model.Operation{}).Where("resource_type_id = ?", "admin-user").Count(&opCount)
	if opCount == 0 {
		t.Error("expected admin-user operations seeded")
	}

	// network_builder grant works: a user bound to the role can create business resources.
	const user = "u-1"
	if err := e.AssignRole(user, "1572fb82-526f-11f0-bde6-e674ec8dde71"); err != nil {
		t.Fatal(err)
	}
	ok, err := e.Check(user, "knowledge_network", "kn-1", "create")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("network_builder should be able to create knowledge networks after seed")
	}
}

// TestApplyRemovesWithdrawnResourceTypes proves an upgraded deployment does
// not retain the retired vocabulary or any role/object grant that names it.
func TestApplyRemovesWithdrawnResourceTypes(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]string{"stream_data_pipeline": "view_detail"}
	for resourceType, operation := range legacy {
		if err := db.Create(&model.ResourceType{ID: resourceType, Name: resourceType}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.Operation{ResourceTypeID: resourceType, ID: operation, Name: operation}).Error; err != nil {
			t.Fatal(err)
		}
		if err := e.GrantObjectPermission("legacy-user", resourceType, "old-1", operation); err != nil {
			t.Fatal(err)
		}
		if err := e.GrantRolePermission("legacy-role", resourceType, "*", operation); err != nil {
			t.Fatal(err)
		}
	}

	if err := Apply(db, e); err != nil {
		t.Fatalf("apply upgrade seed: %v", err)
	}
	for resourceType, operation := range legacy {
		var typeCount, operationCount, grantCount int64
		if err := db.Model(&model.ResourceType{}).Where("id = ?", resourceType).Count(&typeCount).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.Operation{}).Where("resource_type_id = ?", resourceType).Count(&operationCount).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AuthorizationGrant{}).Where("object LIKE ?", resourceType+":%").Count(&grantCount).Error; err != nil {
			t.Fatal(err)
		}
		if typeCount != 0 || operationCount != 0 || grantCount != 0 {
			t.Errorf("withdrawn %s remains: types=%d operations=%d grants=%d", resourceType, typeCount, operationCount, grantCount)
		}
		if ok, err := e.Check("legacy-user", resourceType, "old-1", operation); err != nil || ok {
			t.Errorf("direct %s permission remains: allow=%v err=%v", resourceType, ok, err)
		}
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("second apply must remain idempotent: %v", err)
	}
}

// TestApplyRestoresAgentTypesWithoutDeletingExistingGrants guards the upgrade
// path from #1458: Agent types are active again, so startup must preserve any
// compatible grant that predates the restored directory and fill in the full
// reviewed operation vocabulary around it.
func TestApplyRestoresAgentTypesWithoutDeletingExistingGrants(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	for resourceType, operation := range map[string]string{
		"agent": "use", "agent_tpl": "publish",
	} {
		if err := db.Create(&model.ResourceType{ID: resourceType, Name: "legacy " + resourceType}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.Operation{ResourceTypeID: resourceType, ID: operation, Name: operation}).Error; err != nil {
			t.Fatal(err)
		}
		if err := e.GrantObjectPermission("legacy-user", resourceType, "old-1", operation); err != nil {
			t.Fatal(err)
		}
	}

	if err := Apply(db, e); err != nil {
		t.Fatalf("apply upgrade seed: %v", err)
	}

	wantCounts := map[string]int64{"agent": 11, "agent_tpl": 3}
	for resourceType, operation := range map[string]string{
		"agent": "use", "agent_tpl": "publish",
	} {
		var resourceTypeRow model.ResourceType
		if err := db.First(&resourceTypeRow, "id = ?", resourceType).Error; err != nil {
			t.Fatalf("load restored type %s: %v", resourceType, err)
		}
		if resourceTypeRow.ParentTypeID != "" {
			t.Errorf("%s parent = %q, want standalone root", resourceType, resourceTypeRow.ParentTypeID)
		}
		var operationCount int64
		if err := db.Model(&model.Operation{}).Where("resource_type_id = ?", resourceType).
			Count(&operationCount).Error; err != nil {
			t.Fatal(err)
		}
		if operationCount != wantCounts[resourceType] {
			t.Errorf("%s operation count = %d, want %d", resourceType, operationCount, wantCounts[resourceType])
		}
		if ok, err := e.Check("legacy-user", resourceType, "old-1", operation); err != nil || !ok {
			t.Errorf("compatible %s grant was not preserved: allow=%v err=%v", resourceType, ok, err)
		}
	}
}

// TestSeededRoleGrants verifies the business-admin domains and the super-admin
// wildcard land correctly after seeding (a user bound to each role).
func TestSeededRoleGrants(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	const (
		superAdmin     = "7dcfcc9c-ad02-11e8-aa06-000c29358ad6"
		admin          = "d2bd2082-ad03-11e8-aa06-000c29358ad6"
		security       = "d8998f72-ad03-11e8-aa06-000c29358ad6"
		audit          = "def246f2-ad03-11e8-aa06-000c29358ad6"
		networkBuilder = "1572fb82-526f-11f0-bde6-e674ec8dde71"
	)
	cases := []struct {
		name, role, typ, id, op string
		want                    bool
	}{
		{"admin manages users", admin, "admin-user", "x", "create", true},
		{"admin views audit logs", admin, "admin-audit", "x", "view", true},
		{"admin views role catalog for user management", admin, "admin-role", "x", "view", true},
		{"admin not role grant", admin, "admin-authz", "x", "grant", false},
		{"security manages roles", security, "admin-role", "x", "create", true},
		{"security configures role permissions", security, "admin-role", "x", "permissions", true},
		{"security can reset password", security, "admin-user", "x", "reset-password", true},
		{"admin not role permissions", admin, "admin-role", "x", "permissions", false},
		{"audit not role permissions", audit, "admin-role", "x", "permissions", false},
		{"audit reviews policies", audit, "admin-authz", "x", "view", true},
		{"security not audit", security, "admin-audit", "x", "view", false},
		{"audit views audit logs", audit, "admin-audit", "x", "view", true},
		{"audit not user edit", audit, "admin-user", "x", "edit", false},
		{"network-builder manages catalog", networkBuilder, "catalog", "x", "create", true},
		{"network-builder manages skill", networkBuilder, "skill", "s1", "publish", true},
		{"network-builder not system users", networkBuilder, "admin-user", "x", "create", false},
		{"super-admin manages agents", superAdmin, "agent", "x", "use", true},
		{"super-admin does anything (any type/op)", superAdmin, "whatever", "z", "some_random_op", true},
	}
	for _, c := range cases {
		u := "u-" + c.name
		if err := e.AssignRole(u, c.role); err != nil {
			t.Fatal(err)
		}
		got, err := e.Check(u, c.typ, c.id, c.op)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s: Check(%s, %s:%s, %s) = %v, want %v", c.name, c.role, c.typ, c.id, c.op, got, c.want)
		}
	}
}

// TestAdminRoleCombinationGrantsRoleCatalogRead verifies the role combination
// used by the user-management regression: the admin role supplies the system
// role-catalog read point while business roles retain model display access.
// It must not gain role-management write permissions.
func TestAdminRoleCombinationGrantsRoleCatalogRead(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	const owner = "owner-regression"
	for _, roleID := range []string{
		"d2bd2082-ad03-11e8-aa06-000c29358ad6", // admin
		"1572fb82-526f-11f0-bde6-e674ec8dde71", // network_builder
	} {
		if err := e.AssignRole(owner, roleID); err != nil {
			t.Fatal(err)
		}
	}

	checks := []struct {
		name, resourceType, operation string
		want                          bool
	}{
		{"role catalog read", "admin-role", "view", true},
		{"user list read", "admin-user", "view", true},
		{"role creation remains restricted", "admin-role", "create", false},
		{"role permission changes remain restricted", "admin-role", "permissions", false},
	}
	for _, tc := range checks {
		got, err := e.Check(owner, tc.resourceType, "*", tc.operation)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s: Check(%s, %s:*, %s) = %v, want %v", tc.name, owner, tc.resourceType, tc.operation, got, tc.want)
		}
	}
}

// TestSeedsAdminUser verifies the built-in admin is created bound to super-admin
// with the forced-change flag, and that a re-seed never overwrites a changed
// password or cleared flag.
func TestSeedsAdminUser(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var admin model.User
	if err := db.First(&admin, "id = ?", adminUserID).Error; err != nil {
		t.Fatalf("admin user not seeded: %v", err)
	}
	if admin.Account != adminAccount {
		t.Errorf("admin account = %q, want %q", admin.Account, adminAccount)
	}
	if !admin.Enabled || admin.Source != model.SourceLocal || admin.PasswordHash == "" {
		t.Errorf("admin row malformed: %+v", admin)
	}
	// The built-in admin is seeded WITHOUT a forced password change: #254 turned
	// it off because unattended install logs in as admin immediately after the
	// seed and a forced change blocks it. The assertion is pinned to that
	// behaviour so the two cannot drift apart again — this test had been failing
	// on main since #254 landed, unnoticed because CI did not run it.
	// Whether to reintroduce a forced change (and how, without breaking
	// unattended install) is an open decision tracked in #328.
	if admin.MustChangePassword {
		t.Error("seeded admin must not force a password change (would block unattended install, see #254)")
	}
	// Super-admin wildcard reaches the admin via the seeded role binding.
	ok, err := e.Check(adminUserID, "catalog", "adp_bkn_catalog", "view_detail")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("seeded admin should have super-admin (view_detail on catalog)")
	}

	// Simulate the operator changing the password + clearing the flag, then
	// re-seed: the row must be left untouched (no reset to the initial password).
	if err := db.Model(&model.User{}).Where("id = ?", adminUserID).
		Updates(map[string]any{"password_hash": "changed-hash", "must_change_password": false}).Error; err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	var after model.User
	if err := db.First(&after, "id = ?", adminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if after.PasswordHash != "changed-hash" || after.MustChangePassword {
		t.Errorf("re-seed overwrote changed admin: hash=%q must_change=%v", after.PasswordHash, after.MustChangePassword)
	}
}

func TestApplySeedsBusinessProvenanceAppPrincipal(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var principal model.User
	if err := db.First(&principal, "id = ?", "bdd59f76-19c3-58b0-bf5f-082c4c3cbddb").Error; err != nil {
		t.Fatalf("business provenance app principal not seeded: %v", err)
	}
	if principal.Account != "openbkn-business-provenance" {
		t.Errorf("account = %q", principal.Account)
	}
	if principal.Name != "OpenBKN Business Provenance Service" {
		t.Errorf("name = %q", principal.Name)
	}
	if principal.AccountType != model.AccountTypeApp {
		t.Errorf("account type = %q, want app", principal.AccountType)
	}
	if !principal.Enabled {
		t.Error("principal must be enabled")
	}
	if principal.PasswordHash != "" {
		t.Error("app principal must not have a login password")
	}
}

func TestApplyRejectsConflictingBusinessProvenancePrincipal(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{
		ID:          "bdd59f76-19c3-58b0-bf5f-082c4c3cbddb",
		Account:     "customer-owned-account",
		Name:        "Customer Account",
		Enabled:     true,
		Source:      model.SourceLocal,
		AccountType: model.AccountTypeOther,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := Apply(db, e); err == nil {
		t.Fatal("expected conflicting fixed principal to fail seed")
	}
}

func TestApplyReportsBusinessProvenanceAccountCollision(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{
		ID:          "customer-created-id",
		Account:     BusinessProvenanceOwnerAccount,
		Name:        "Customer Account",
		Enabled:     true,
		Source:      model.SourceLocal,
		AccountType: model.AccountTypeApp,
	}).Error; err != nil {
		t.Fatal(err)
	}

	err = Apply(db, e)
	if err == nil || !strings.Contains(err.Error(), "conflicts with the deployment contract") {
		t.Fatalf("expected actionable fixed principal conflict, got %v", err)
	}
}

// TestApplyIdempotent runs the seed twice; the second run must not error or
// duplicate roles.
func TestApplyIdempotent(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	var roleCount int64
	db.Model(&model.Role{}).Count(&roleCount)
	if roleCount != 5 {
		t.Errorf("role count after re-seed = %d, want 5", roleCount)
	}
}

func TestApplyReconcilesDeprecatedSeedRoles(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}

	const (
		deprecatedDataAdmin = "00990824-4bf7-11f0-8fa7-865d5643e61f"
		user                = "u-legacy"
	)
	if err := db.Create(&model.Role{
		ID:          deprecatedDataAdmin,
		Name:        "数据管理员",
		Description: "legacy seeded role",
		Source:      model.RoleSourceBusiness,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole(user, deprecatedDataAdmin); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantRolePermission(deprecatedDataAdmin, "catalog", "*", "create"); err != nil {
		t.Fatal(err)
	}

	if err := Apply(db, e); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var count int64
	if err := db.Model(&model.Role{}).Where("id = ?", deprecatedDataAdmin).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("deprecated role still exists after seed reconcile")
	}
	if ok, err := e.Check(user, "catalog", "c1", "create"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("deprecated role binding/grant still allows catalog create")
	}
}

func TestReconcileWithdrawnNormalUserRole(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}

	const (
		normalUserRole = normalUserRoleID
		user           = "u-withdrawn-role"
	)
	if err := e.AssignRole(user, normalUserRole); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantRolePermission(normalUserRole, "admin-user", "*", "create"); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.Check(user, "admin-user", "u1", "create"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("test setup failed: withdrawn role grant did not take effect")
	}

	if err := ReconcileWithdrawnNormalUserRole(db, e); err != nil {
		t.Fatalf("reconcile withdrawn normal user role: %v", err)
	}

	if ok, err := e.Check(user, "admin-user", "u1", "create"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("withdrawn role policy or binding still allows admin-user create")
	}
	var count int64
	if err := db.Model(&model.Role{}).Where("id = ?", normalUserRole).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("withdrawn normal_user role still exists after reconcile")
	}
}

// TestCatalogResourceOperationSplit pins where each verb lives once #801 has
// converged: management on the catalog, reading on the table.
//
// The earlier revision of this test asserted the opposite — that the management
// verbs were STILL declared on the resource — because Apply wipes every seeded
// role's p-lines and rebuilds them from grants.json, so removing them before
// vega judged the catalog would have revoked network_builder's ability to create
// a table on upgrade. vega has switched, so the assertion inverts.
func TestCatalogResourceOperationSplit(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	ops := func(resourceType string) map[string]bool {
		var rows []model.Operation
		if err := db.Where("resource_type_id = ?", resourceType).Find(&rows).Error; err != nil {
			t.Fatalf("load operations for %s: %v", resourceType, err)
		}
		got := make(map[string]bool, len(rows))
		for _, r := range rows {
			got[r.ID] = true
		}
		return got
	}

	catalogOps := ops("catalog")
	for _, op := range []string{"view_detail", "create", "modify", "delete", "authorize", "task_manage", "resource_manage", "query_data"} {
		if !catalogOps[op] {
			t.Errorf("catalog is missing operation %q", op)
		}
	}

	resourceOps := ops("resource")
	for _, op := range []string{"view_detail", "query_data"} {
		if !resourceOps[op] {
			t.Errorf("resource is missing read operation %q", op)
		}
	}
	// The management verbs are gone from the table. Putting one back would give
	// the vocabulary two answers to "who may change this table" — the catalog's
	// resource_manage and a table-level verb — and only the first is the one vega
	// asks. create is the clearest case: a table is always created inside a
	// catalog, so a verb on the table could never say which catalog it lands in.
	for _, op := range []string{"create", "modify", "delete", "authorize", "task_manage"} {
		if resourceOps[op] {
			t.Errorf("resource still declares %q — management is judged on the owning catalog now (#801)", op)
		}
	}
	if len(resourceOps) != 2 {
		t.Errorf("resource declares %d operations, want exactly view_detail and query_data", len(resourceOps))
	}
}

func TestKnowledgeNetworkDeclaresExecuteOperation(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	var count int64
	if err := db.Model(&model.Operation{}).
		Where("resource_type_id = ? AND id = ?", "knowledge_network", "execute").
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("knowledge_network execute operation count = %d, want 1", count)
	}
}

func TestNetworkBuilderManagesKnowledgeNetworksTypeWide(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}
	const (
		builder = "u-builder"
		network = "kn-someone-elses"
	)
	if err := e.AssignRole(builder, "1572fb82-526f-11f0-bde6-e674ec8dde71"); err != nil {
		t.Fatal(err)
	}

	for _, op := range []string{"view_detail", "create", "modify", "delete", "query_data", "authorize", "execute"} {
		ok, err := e.Check(builder, "knowledge_network", network, op)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("network_builder lost type-wide knowledge_network/%s", op)
		}
	}
}

func TestNetworkBuilderManagesCatalogsTypeWide(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}
	const (
		builder = "u-builder"
		catalog = "cat-someone-elses"
	)
	if err := e.AssignRole(builder, "1572fb82-526f-11f0-bde6-e674ec8dde71"); err != nil {
		t.Fatal(err)
	}

	for _, op := range []string{"view_detail", "create", "modify", "delete", "authorize", "task_manage", "resource_manage", "query_data"} {
		ok, err := e.Check(builder, "catalog", catalog, op)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("network_builder lost type-wide catalog/%s", op)
		}
	}
}

func TestNetworkBuilderPermissionMatrixMatchesBusinessBuilderRole(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	grants, err := e.RolePermissions("1572fb82-526f-11f0-bde6-e674ec8dde71")
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string][]string, len(grants))
	for _, grant := range grants {
		got[grant.Object] = grant.Operations
	}
	want := map[string][]string{
		"catalog:*":           {"view_detail", "create", "modify", "delete", "authorize", "task_manage", "resource_manage", "query_data"},
		"knowledge_network:*": {"view_detail", "create", "modify", "delete", "query_data", "authorize", "execute"},
		"operator:*":          {"create", "modify", "delete", "view", "publish", "unpublish", "authorize", "public_access", "execute"},
		"tool_box:*":          {"create", "modify", "delete", "view", "publish", "unpublish", "authorize", "public_access", "execute"},
		"skill:*":             {"create", "modify", "delete", "view", "publish", "unpublish", "authorize", "public_access", "execute"},
		"mcp:*":               {"create", "modify", "delete", "view", "publish", "unpublish", "authorize", "public_access", "execute"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("network_builder grants = %#v, want %#v", got, want)
	}
}

func TestSeedReconciliationPreservesCommunityBundleAssignedToBuiltInRole(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	const roleID = "1572fb82-526f-11f0-bde6-e674ec8dde71"
	if err := e.GrantCommunityBundle(roleID, "knowledge_network", "kn-role-bundle", authz.AuthoritySourceAdminAuthz); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantRolePermission(roleID, "knowledge_network", "kn-stale", "obsolete_seed_operation"); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	records, err := e.PolicyRecords(authz.PolicyFilter{
		AccessorID:   roleID,
		Object:       "knowledge_network:kn-role-bundle",
		PolicySource: authz.PolicySourceCommunityBundle,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Operation != authz.ActFullBusinessAccess {
		t.Fatalf("bundle after seed reconciliation = %+v; want one logical bundle", records)
	}
	stale, err := e.PolicyRecords(authz.PolicyFilter{
		AccessorID:   roleID,
		Object:       "knowledge_network:kn-stale",
		Operation:    "obsolete_seed_operation",
		PolicySource: authz.PolicySourceRolePermission,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("obsolete seeded role grant survived reconciliation: %+v", stale)
	}
}
