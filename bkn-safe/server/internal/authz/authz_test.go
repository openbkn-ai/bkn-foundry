// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

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

// TestPublicAccessorGrant covers the root-department-as-everyone convention:
// a policy whose subject is PublicAccessorID matches ANY requesting subject,
// scoped strictly to the granted object and op (ISF "grant to root department
// = public" semantics; bkn-safe has no user→department g rules, so this is
// resolved in the matcher instead).
func TestPublicAccessorGrant(t *testing.T) {
	e := newTestEnforcer(t)
	mustNoErr(t, e.GrantObjectPermission(PublicAccessorID, "tool_box", "tb1", "public_access"))
	mustNoErr(t, e.GrantObjectPermission(PublicAccessorID, "tool_box", "tb1", "execute"))

	cases := []struct {
		sub, typ, id, op string
		want             bool
		why              string
	}{
		{"u-anyone", "tool_box", "tb1", "public_access", true, "public grant matches any subject"},
		{"u-anyone", "tool_box", "tb1", "execute", true, "public grant matches any subject"},
		{"u-anyone", "tool_box", "tb1", "delete", false, "ungranted op must stay denied"},
		{"u-anyone", "tool_box", "tb2", "execute", false, "public grant must not leak to sibling object"},
		{"u-anyone", "agent", "tb1", "execute", false, "public grant must not leak to other type"},
		{PublicAccessorID, "tool_box", "tb1", "execute", true, "the public subject itself still matches"},
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

// TestResourcePolicies covers grouping of per-accessor grants on one resource
// instance, used by DA's ListPolicy(All). Grants on sibling/other resources or
// role-level patterns must NOT appear in a concrete-instance listing.
func TestResourcePolicies(t *testing.T) {
	e := newTestEnforcer(t)
	mustNoErr(t, e.GrantObjectPermission("u-1", "agent", "a1", "use"))
	mustNoErr(t, e.GrantObjectPermission("u-1", "agent", "a1", "modify"))
	mustNoErr(t, e.GrantObjectPermission("u-2", "agent", "a1", "use"))
	// noise that must be excluded from agent:a1
	mustNoErr(t, e.GrantObjectPermission("u-1", "agent", "a2", "use"))
	mustNoErr(t, e.GrantRolePermission("role-x", "agent", "*", "use"))

	got, err := e.ResourcePolicies("agent", "a1")
	if err != nil {
		t.Fatalf("ResourcePolicies: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d accessors, want 2: %+v", len(got), got)
	}
	byAcc := map[string][]string{}
	for _, p := range got {
		byAcc[p.AccessorID] = p.Operations
	}
	if ops := byAcc["u-1"]; !sameSet(ops, []string{"use", "modify"}) {
		t.Errorf("u-1 ops = %v, want [use modify]", ops)
	}
	if ops := byAcc["u-2"]; !sameSet(ops, []string{"use"}) {
		t.Errorf("u-2 ops = %v, want [use]", ops)
	}
	if _, ok := byAcc["role-x"]; ok {
		t.Error("role-level agent:* grant must not appear in concrete agent:a1 listing")
	}

	// empty resource -> empty list, no error.
	empty, err := e.ResourcePolicies("agent", "nonexistent")
	if err != nil {
		t.Fatalf("ResourcePolicies(empty): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty resource = %+v, want none", empty)
	}
}

// TestAccessibleResources covers enumeration of concrete instances an accessor
// can act on, including via role, with "*" patterns and opaque id encodings.
func TestAccessibleResources(t *testing.T) {
	e := newTestEnforcer(t)
	const role = "role-data"
	// direct per-instance grants
	mustNoErr(t, e.GrantObjectPermission("u-1", "data_flow", "d1:default", "list"))
	mustNoErr(t, e.GrantObjectPermission("u-1", "data_flow", "d2:default", "list"))
	mustNoErr(t, e.GrantObjectPermission("u-1", "data_flow", "d3:default", "view")) // wrong op
	mustNoErr(t, e.GrantObjectPermission("u-1", "operator", "op1", "list"))         // wrong type
	// role-inherited grant
	mustNoErr(t, e.GrantObjectPermission(role, "data_flow", "d4:default", "list"))
	mustNoErr(t, e.AssignRole("u-1", role))
	// type-wide grant (must be excluded from concrete enumeration)
	mustNoErr(t, e.GrantRolePermission("role-admin", "data_flow", "*", "list"))
	mustNoErr(t, e.AssignRole("u-1", "role-admin"))

	got, err := e.AccessibleResources("u-1", "data_flow", "list")
	if err != nil {
		t.Fatalf("AccessibleResources: %v", err)
	}
	want := []string{"d1:default", "d2:default", "d4:default"}
	if !sameSet(got, want) {
		t.Fatalf("got %v, want %v (no d3/op1/'*')", got, want)
	}

	// wildcard act grants everything: a "*" act on d5 surfaces for any op.
	mustNoErr(t, e.Grant("u-2", "data_flow:d5", ActAll))
	g2, err := e.AccessibleResources("u-2", "data_flow", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(g2, []string{"d5"}) {
		t.Fatalf("wildcard-act enum = %v, want [d5]", g2)
	}
}
