package authz

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTestEnforcer builds an Enforcer over an in-memory sqlite DB.
func newTestEnforcer(t *testing.T) *Enforcer {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	e, err := New(db)
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	return e
}

// TestRoleGrantAndWildcard covers the core RBAC path and the keyMatch wildcard
// semantics, including the per-object grant that must NOT leak to siblings
// (the keyMatch2 over-match bug this design avoids).
func TestRoleGrantAndWildcard(t *testing.T) {
	e := newTestEnforcer(t)
	const (
		appAdmin = "1572fb82-526f-11f0-bde6-e674ec8dde71"
		user     = "u-1"
		other    = "u-2"
	)
	mustNoErr(t, e.GrantRolePermission(appAdmin, "agent", "*", "use"))
	mustNoErr(t, e.GrantRolePermission(appAdmin, "agent", "*", "mgnt_built_in_agent"))
	mustNoErr(t, e.AssignRole(user, appAdmin))
	mustNoErr(t, e.GrantObjectPermission(user, "pipeline", "p1", "read"))

	cases := []struct {
		sub, typ, id, op string
		want             bool
		why              string
	}{
		{user, "agent", "probe", "use", true, "agent:* covers agent:probe"},
		{user, "agent", "anything", "use", true, "agent:* covers any agent id"},
		{user, "agent", "probe", "delete", false, "no delete op granted"},
		{user, "pipeline", "x", "use", false, "agent:* must not match pipeline type"},
		{other, "agent", "probe", "use", false, "user without the role is denied"},
		{user, "pipeline", "p1", "read", true, "direct grant on pipeline:p1"},
		{user, "pipeline", "p2", "read", false, "per-object grant must not leak to sibling"},
	}
	for _, c := range cases {
		got, err := e.Check(c.sub, c.typ, c.id, c.op)
		if err != nil {
			t.Fatalf("check(%s,%s:%s,%s): %v", c.sub, c.typ, c.id, c.op, err)
		}
		if got != c.want {
			t.Errorf("Check(%s, %s:%s, %s) = %v, want %v — %s", c.sub, c.typ, c.id, c.op, got, c.want, c.why)
		}
	}
}

// TestAllowedOps mirrors ISF resource-operation: returns the allowed subset.
func TestAllowedOps(t *testing.T) {
	e := newTestEnforcer(t)
	const appAdmin, user = "role-app", "u-1"
	mustNoErr(t, e.GrantRolePermission(appAdmin, "agent", "*", "use"))
	mustNoErr(t, e.GrantRolePermission(appAdmin, "agent", "*", "mgnt_built_in_agent"))
	mustNoErr(t, e.AssignRole(user, appAdmin))

	got, err := e.AllowedOps(user, "agent", "probe", []string{"use", "mgnt_built_in_agent", "delete", "publish"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(got, []string{"use", "mgnt_built_in_agent"}) {
		t.Errorf("AllowedOps = %v, want {use, mgnt_built_in_agent}", got)
	}
}

// TestRemoveResourcePolicies drops all grants on a concrete instance.
func TestRemoveResourcePolicies(t *testing.T) {
	e := newTestEnforcer(t)
	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "pipeline", "p1", "read"))
	mustNoErr(t, e.GrantObjectPermission(user, "pipeline", "p1", "update"))

	ok, _ := e.Check(user, "pipeline", "p1", "read")
	if !ok {
		t.Fatal("expected read before removal")
	}
	mustNoErr(t, e.RemoveResourcePolicies("pipeline", "p1"))
	if ok, _ := e.Check(user, "pipeline", "p1", "read"); ok {
		t.Error("read still allowed after RemoveResourcePolicies")
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}
