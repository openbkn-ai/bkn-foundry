// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
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
		// The data surface defaults to nothing (#513); capabilities are unchanged.
		{"normal-user cannot query knowledge by default", normalUser, "knowledge_network", "kn1", "query_data", false},
		{"normal-user cannot view a catalog by default", normalUser, "catalog", "c1", "view_detail", false},
		{"normal-user cannot view a data resource by default", normalUser, "resource", "r1", "view_detail", false},
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
		"b5f9ac3e-992c-4bbd-8126-95e87e51c46e", // normal_user
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
		{"large model display", "large_model", "display", true},
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
	// Assert the rebuild half with a grant the role actually holds: the data
	// surface is gone (#513), so using catalog here would test the revocation
	// instead, which is another test's job.
	if ok, err := e.Check(user, "skill", "s1", "execute"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("normal_user desired grant was not restored after reconcile")
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

// TestSeedRevokesNormalUserDataGrants pins #513: the ordinary role holds no
// data grant at all, so an object grant is finally the thing that decides.
//
// The point is not tidiness. In an allow-only engine a type-wide allow cannot be
// narrowed by an object grant, so as long as normal_user held resource:* every
// per-object configuration an administrator made was dead on arrival.
//
// The capability surface stays: tools, models and agents leak no data through a
// type-wide grant, and revoking those would only leave a platform where a signed-in
// user cannot invoke a model.
func TestSeedRevokesNormalUserDataGrants(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatal(err)
	}

	const (
		normalUser     = "b5f9ac3e-992c-4bbd-8126-95e87e51c46e"
		networkBuilder = "1572fb82-526f-11f0-bde6-e674ec8dde71"
	)
	user := "u-normal"
	if err := e.AssignRole(user, normalUser); err != nil {
		t.Fatal(err)
	}

	// The data surface: not one grant should be left.
	for _, tc := range []struct{ rtype, op string }{
		{"catalog", "view_detail"},
		{"resource", "view_detail"},
		{"resource", "query_data"},
		{"knowledge_network", "view_detail"},
		{"knowledge_network", "query_data"},
	} {
		allowed, err := e.Check(user, tc.rtype, "x-1", tc.op)
		if err != nil {
			t.Fatal(err)
		}
		if allowed {
			t.Errorf("normal_user still holds %s/%s by default — object grants stay overridden by it", tc.rtype, tc.op)
		}
	}

	// Granted explicitly, it must be visible — otherwise revoking the wildcard
	// leaves no working path to see anything at all.
	if err := e.GrantObjectPermission(user, "catalog", "c-1", "view_detail"); err != nil {
		t.Fatal(err)
	}
	allowed, err := e.Check(user, "catalog", "c-1", "view_detail")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Error("an explicit catalog grant still does not grant access — the convergence would leave no usable path")
	}

	// The capability surface is untouched: revoking it buys no safety and leaves a
	// signed-in user unable to invoke a model.
	for _, tc := range []struct{ rtype, op string }{
		{"skill", "execute"},
		{"tool_box", "execute"},
		{"large_model", "execute"},
		{"small_model", "execute"},
		{"agent", "use"},
	} {
		allowed, err := e.Check(user, tc.rtype, "x-1", tc.op)
		if err != nil {
			t.Fatal(err)
		}
		if !allowed {
			t.Errorf("normal_user lost the capability %s/%s — that is not convergence, it is an unusable platform", tc.rtype, tc.op)
		}
	}

	// The builder is unaffected. Creating a table is judged on the target catalog
	// now (#801): a table has to be created inside one, so "may create a table"
	// and "may act on this catalog" were always the same question — and the old
	// resource:*/create could not answer which catalog it would land in.
	builder := "u-builder"
	if err := e.AssignRole(builder, networkBuilder); err != nil {
		t.Fatal(err)
	}
	allowed, err = e.Check(builder, "catalog", "c-1", "resource_manage")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Error("network_builder lost catalog resource_manage — creating a data table would 403 after upgrade")
	}
}

// network_builder manages every knowledge network but may not hand one out.
//
// Sharing a network is the creator's call: bkn-backend writes `authorize` to
// whoever created it, and that object grant is what the owner surface reads. A
// type-wide `authorize` would instead let every member of the role give any
// network to anyone — and since `authorize` can only be taken back by a platform
// administrator, that power could not be walked back from inside the role.
//
// The rest of the row stays: the role is still the business-plane administrator.
func TestNetworkBuilderCannotShareNetworksItDidNotCreate(t *testing.T) {
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

	if ok, err := e.Check(builder, "knowledge_network", network, "authorize"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("network_builder must not hold type-wide authorize on knowledge networks")
	}

	for _, op := range []string{"view_detail", "create", "modify", "delete", "query_data", "task_manage"} {
		ok, err := e.Check(builder, "knowledge_network", network, op)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("network_builder lost %q — it is still the business-plane administrator", op)
		}
	}

	// The creator's own grant is untouched: that is where sharing comes from.
	const creator = "u-creator"
	if err := e.GrantObjectPermission(creator, "knowledge_network", network, "authorize"); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.Check(creator, "knowledge_network", network, "authorize"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Error("the creator's object-level authorize must still decide sharing")
	}
}

// The same rule as knowledge networks, arrived at the same way: a type-wide
// `authorize` on catalog let every network_builder hand out a catalog somebody
// else created, because casbin's keyMatch makes `catalog:*` match every id.
// Observed on a live deployment — an account whose only role is network_builder
// saw the share control on catalogs created by the administrator, and the write
// behind it succeeded.
//
// The creator is unaffected: vega writes COMMON_OPERATIONS, `authorize`
// included, as a `catalog:<id>` object grant at create time, and the owner
// surface reads exactly that.
func TestNetworkBuilderCannotShareCatalogsItDidNotCreate(t *testing.T) {
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

	if ok, err := e.Check(builder, "catalog", catalog, "authorize"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("network_builder must not hold type-wide authorize on catalogs")
	}

	// Everything else the role needs to run the business plane stays.
	for _, op := range []string{"view_detail", "create", "modify", "delete", "task_manage", "resource_manage", "query_data"} {
		ok, err := e.Check(builder, "catalog", catalog, op)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("network_builder lost %q on catalog", op)
		}
	}

	// A catalog the builder created carries the grant on the instance instead.
	if err := e.GrantObjectPermission(builder, "catalog", "cat-mine", "authorize"); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.Check(builder, "catalog", "cat-mine", "authorize"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Error("the creator's own object grant must still authorize sharing")
	}
}
