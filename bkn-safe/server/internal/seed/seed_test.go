// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"bkn-safe/internal/authz"
	"bkn-safe/internal/database"
	"bkn-safe/internal/model"
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

	// 6 Studio roles, with the preserved three-admin UUIDs present.
	var roleCount int64
	db.Model(&model.Role{}).Count(&roleCount)
	if roleCount != 6 {
		t.Errorf("role count = %d, want 6", roleCount)
	}
	for id, name := range map[string]string{
		"7dcfcc9c-ad02-11e8-aa06-000c29358ad6": "super_admin",
		"d2bd2082-ad03-11e8-aa06-000c29358ad6": "admin",
		"d8998f72-ad03-11e8-aa06-000c29358ad6": "security",
		"def246f2-ad03-11e8-aa06-000c29358ad6": "audit",
		"1572fb82-526f-11f0-bde6-e674ec8dde71": "network_builder",
		"b5f9ac3e-992c-4bbd-8126-95e87e51c46e": "normal_user",
	} {
		var r model.Role
		if err := db.First(&r, "id = ?", id).Error; err != nil {
			t.Errorf("preserved role %s missing: %v", id, err)
		}
		if r.Name != name {
			t.Errorf("role %s name = %q, want %q", id, r.Name, name)
		}
	}

	// agent and Studio admin resource types + their operations seeded.
	var opCount int64
	db.Model(&model.Operation{}).Where("resource_type_id = ?", "agent").Count(&opCount)
	if opCount == 0 {
		t.Error("expected agent operations seeded")
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
		normalUser     = "b5f9ac3e-992c-4bbd-8126-95e87e51c46e"
	)
	cases := []struct {
		name, role, typ, id, op string
		want                    bool
	}{
		{"admin manages users", admin, "admin-user", "x", "create", true},
		{"admin not role grant", admin, "admin-authz", "x", "grant", false},
		{"security manages roles", security, "admin-role", "x", "create", true},
		{"security can reset password", security, "admin-user", "x", "reset-password", true},
		{"security not audit", security, "admin-audit", "x", "view", false},
		{"audit views audit logs", audit, "admin-audit", "x", "view", true},
		{"audit not user edit", audit, "admin-user", "x", "edit", false},
		{"network-builder manages catalog", networkBuilder, "catalog", "x", "create", true},
		{"network-builder manages skill", networkBuilder, "skill", "s1", "publish", true},
		{"network-builder not system users", networkBuilder, "admin-user", "x", "create", false},
		{"normal-user can query knowledge", normalUser, "knowledge_network", "kn1", "data_query", true},
		{"normal-user can execute skill", normalUser, "skill", "s1", "execute", true},
		{"normal-user can use agent", normalUser, "agent", "a1", "use", true},
		{"normal-user cannot create catalog", normalUser, "catalog", "x", "create", false},
		{"normal-user cannot publish skill", normalUser, "skill", "s1", "publish", false},
		{"super-admin does anything (agent)", superAdmin, "agent", "x", "use", true},
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
	if !admin.MustChangePassword {
		t.Error("seeded admin must have MustChangePassword=true")
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
	if roleCount != 6 {
		t.Errorf("role count after re-seed = %d, want 6", roleCount)
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

func TestApplyReconcilesCurrentSeedRoleGrants(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}

	const (
		normalUserRole = "b5f9ac3e-992c-4bbd-8126-95e87e51c46e"
		user           = "u-stale-grant"
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
		t.Fatal("test setup failed: stale grant did not take effect")
	}

	if err := Apply(db, e); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if ok, err := e.Check(user, "admin-user", "u1", "create"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("stale current-role grant still allows admin-user create")
	}
	if ok, err := e.Check(user, "catalog", "c1", "view_detail"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("normal_user desired grant was not restored after reconcile")
	}
}
