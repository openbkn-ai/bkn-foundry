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
// catalog and that the app-admin grant makes a real decision pass.
func TestApplySeedsRolesCatalogGrants(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatalf("authz: %v", err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("apply seed: %v", err)
	}

	// 9 roles, with the preserved business UUIDs present.
	var roleCount int64
	db.Model(&model.Role{}).Count(&roleCount)
	if roleCount != 9 {
		t.Errorf("role count = %d, want 9", roleCount)
	}
	for _, id := range []string{
		"1572fb82-526f-11f0-bde6-e674ec8dde71", // 应用管理员
		"00990824-4bf7-11f0-8fa7-865d5643e61f", // 数据管理员
		"3fb94948-5169-11f0-b662-3a7bdba2913f", // AI管理员
	} {
		var r model.Role
		if err := db.First(&r, "id = ?", id).Error; err != nil {
			t.Errorf("preserved role %s missing: %v", id, err)
		}
	}

	// agent resource type + its operations seeded.
	var opCount int64
	db.Model(&model.Operation{}).Where("resource_type_id = ?", "agent").Count(&opCount)
	if opCount == 0 {
		t.Error("expected agent operations seeded")
	}

	// app-admin grant works: a user bound to the role can use an agent.
	const user = "u-1"
	if err := e.AssignRole(user, "1572fb82-526f-11f0-bde6-e674ec8dde71"); err != nil {
		t.Fatal(err)
	}
	ok, err := e.Check(user, "agent", "any-agent-id", "use")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("app-admin should be able to use agent:* after seed")
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
		appAdmin   = "1572fb82-526f-11f0-bde6-e674ec8dde71"
		dataAdmin  = "00990824-4bf7-11f0-8fa7-865d5643e61f"
		aiAdmin    = "3fb94948-5169-11f0-b662-3a7bdba2913f"
		superAdmin = "7dcfcc9c-ad02-11e8-aa06-000c29358ad6"
	)
	cases := []struct {
		name, role, typ, id, op string
		want                    bool
	}{
		{"app-admin uses agent", appAdmin, "agent", "x", "use", true},
		{"app-admin not catalog", appAdmin, "catalog", "x", "create", false},
		{"data-admin manages catalog", dataAdmin, "catalog", "x", "create", true},
		{"data-admin manages knowledge_network", dataAdmin, "knowledge_network", "kn1", "data_query", true},
		{"data-admin manages data_flow", dataAdmin, "data_flow", "f1", "manual_exec", true},
		{"data-admin not operator", dataAdmin, "operator", "o1", "execute", false},
		{"ai-admin manages operator", aiAdmin, "operator", "o1", "execute", true},
		{"ai-admin manages skill", aiAdmin, "skill", "s1", "publish", true},
		{"ai-admin not catalog", aiAdmin, "catalog", "x", "create", false},
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
	if roleCount != 9 {
		t.Errorf("role count after re-seed = %d, want 9", roleCount)
	}
}
