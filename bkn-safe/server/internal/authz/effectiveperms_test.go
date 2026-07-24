// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"sort"
	"testing"
)

// byObject indexes collapsed grants by "type:id" -> sorted ops, for set compare.
func byObject(grants []RoleGrant) map[string][]string {
	out := map[string][]string{}
	for _, g := range grants {
		ops := append([]string(nil), g.Operations...)
		sort.Strings(ops)
		out[g.Object] = ops
	}
	return out
}

func eqOps(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	sort.Strings(want)
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// A resource-wildcard holder collapses to a single {*:*, [*]} row; scoped, the
// wildcard projects onto the queried type.
func TestEffectivePermissionsWildcard(t *testing.T) {
	e := newTestEnforcer(t)
	const super = "u-super"
	mustNoErr(t, e.Grant(super, "*", "*")) // bare super-admin grant

	// Even with extra concrete grants, the wildcard supersedes everything.
	mustNoErr(t, e.GrantObjectPermission(super, "resource", "r1", "view_detail"))

	has, grants, err := e.EffectivePermissions(super, PermQuery{})
	mustNoErr(t, err)
	if !has {
		t.Fatal("hasWildcard: want true")
	}
	if len(grants) != 1 || grants[0].Object != "*:*" || !eqOps(grants[0].Operations, "*") {
		t.Fatalf("wildcard set = %+v, want single *:* [*]", grants)
	}

	// Scoped: project onto the queried type.
	has, grants, err = e.EffectivePermissions(super, PermQuery{ResourceType: "large_model"})
	mustNoErr(t, err)
	if !has || len(grants) != 1 || grants[0].Object != "large_model:*" || !eqOps(grants[0].Operations, "*") {
		t.Fatalf("scoped wildcard = %+v, want large_model:* [*]", grants)
	}
}

// An admin-console capability (safe_admin:console:manage) makes CanAdmin true but
// is NOT a resource wildcard: EffectivePermissions must return the real grants,
// never the {*:*} row. This is the over-report guard.
func TestEffectivePermissionsAdminConsoleIsNotWildcard(t *testing.T) {
	e := newTestEnforcer(t)
	const (
		role = "r-user-admin"
		user = "u-admin"
	)
	mustNoErr(t, e.GrantRolePermission(role, "safe_admin", "console", "manage"))
	mustNoErr(t, e.GrantRolePermission(role, "admin-user", "*", "view"))
	mustNoErr(t, e.AssignRole(user, role))

	// Sanity: this user IS an admin by the console check.
	admin, err := e.CanAdmin(user)
	mustNoErr(t, err)
	if !admin {
		t.Fatal("CanAdmin: want true for admin-console role")
	}

	has, grants, err := e.EffectivePermissions(user, PermQuery{})
	mustNoErr(t, err)
	if has {
		t.Fatal("hasWildcard: want false — admin-console is not a resource wildcard")
	}
	idx := byObject(grants)
	if _, ok := idx["*:*"]; ok {
		t.Fatalf("must not emit *:* for admin-console-only user: %+v", grants)
	}
	if !eqOps(idx["safe_admin:console"], "manage") || !eqOps(idx["admin-user:*"], "view") {
		t.Fatalf("real grants not preserved: %+v", grants)
	}
}

// An instance grant fully covered by its type-wide grant is dropped.
func TestEffectivePermissionsInstanceCoveredByTypeWide(t *testing.T) {
	e := newTestEnforcer(t)
	const (
		role = "r-a"
		user = "u1"
	)
	mustNoErr(t, e.GrantRolePermission(role, "agent", "*", "use"))
	mustNoErr(t, e.AssignRole(user, role))
	mustNoErr(t, e.GrantObjectPermission(user, "agent", "a1", "use")) // redundant

	has, grants, err := e.EffectivePermissions(user, PermQuery{})
	mustNoErr(t, err)
	if has {
		t.Fatal("hasWildcard: want false")
	}
	idx := byObject(grants)
	if _, ok := idx["agent:a1"]; ok {
		t.Fatalf("agent:a1 should be dropped (covered by agent:*): %+v", grants)
	}
	if !eqOps(idx["agent:*"], "use") {
		t.Fatalf("agent:* = %v, want [use]", idx["agent:*"])
	}
}

// An instance that grants ops beyond its type-wide set keeps only the surplus.
func TestEffectivePermissionsInstanceExceedsTypeWide(t *testing.T) {
	e := newTestEnforcer(t)
	const (
		role = "r-a"
		user = "u1"
	)
	mustNoErr(t, e.GrantRolePermission(role, "agent", "*", "view"))
	mustNoErr(t, e.AssignRole(user, role))
	mustNoErr(t, e.GrantObjectPermission(user, "agent", "a1", "view")) // covered
	mustNoErr(t, e.GrantObjectPermission(user, "agent", "a1", "edit")) // surplus

	_, grants, err := e.EffectivePermissions(user, PermQuery{})
	mustNoErr(t, err)
	idx := byObject(grants)
	if !eqOps(idx["agent:*"], "view") {
		t.Fatalf("agent:* = %v, want [view]", idx["agent:*"])
	}
	if !eqOps(idx["agent:a1"], "edit") {
		t.Fatalf("agent:a1 = %v, want [edit] (surplus only)", idx["agent:a1"])
	}
}

// A type-wide ActAll ("*") grant covers every op on the type, so instance rows
// are dropped whatever their ops — even ops not literally in the type-wide set.
// (Defensive: rejectWildcardGrant keeps such a grant off the HTTP write paths,
// but the fold must not silently fail if one ever exists.)
func TestEffectivePermissionsTypeWideActAllCoversInstances(t *testing.T) {
	e := newTestEnforcer(t)
	const (
		role = "r-a"
		user = "u1"
	)
	mustNoErr(t, e.GrantRolePermission(role, "agent", "*", "*")) // type-wide ActAll
	mustNoErr(t, e.AssignRole(user, role))
	mustNoErr(t, e.GrantObjectPermission(user, "agent", "a1", "use"))
	mustNoErr(t, e.GrantObjectPermission(user, "agent", "a2", "publish"))

	has, grants, err := e.EffectivePermissions(user, PermQuery{})
	mustNoErr(t, err)
	if has {
		t.Fatal("hasWildcard: want false — this is a type-scoped ActAll, not a bare */*")
	}
	idx := byObject(grants)
	if _, ok := idx["agent:a1"]; ok {
		t.Errorf("agent:a1 must be dropped under type-wide agent:*/[*]: %+v", grants)
	}
	if _, ok := idx["agent:a2"]; ok {
		t.Errorf("agent:a2 must be dropped under type-wide agent:*/[*]: %+v", grants)
	}
	if !eqOps(idx["agent:*"], "*") {
		t.Errorf("agent:* = %v, want [*]", idx["agent:*"])
	}
}

// A pure instance grant with no type-wide grant survives in full.
func TestEffectivePermissionsPureInstance(t *testing.T) {
	e := newTestEnforcer(t)
	const user = "u1"
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "r1", "view_detail"))

	_, grants, err := e.EffectivePermissions(user, PermQuery{})
	mustNoErr(t, err)
	idx := byObject(grants)
	if !eqOps(idx["resource:r1"], "view_detail") {
		t.Fatalf("resource:r1 = %v, want [view_detail]", idx["resource:r1"])
	}
}

// Scope filters: resource_type narrows to one type; resource_id narrows instance
// rows while always keeping the type-wide id:"*" row.
func TestEffectivePermissionsScope(t *testing.T) {
	e := newTestEnforcer(t)
	const (
		role = "r-a"
		user = "u1"
	)
	mustNoErr(t, e.GrantRolePermission(role, "resource", "*", "view_detail"))
	mustNoErr(t, e.AssignRole(user, role))
	// Two instances with a surplus op beyond the type-wide view_detail.
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "r1", "modify"))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "r2", "modify"))
	// A different type that must be filtered out.
	mustNoErr(t, e.GrantObjectPermission(user, "agent", "a1", "use"))

	// resource_type only: drops agent, keeps resource:* + both instances.
	_, grants, err := e.EffectivePermissions(user, PermQuery{ResourceType: "resource"})
	mustNoErr(t, err)
	idx := byObject(grants)
	if _, ok := idx["agent:a1"]; ok {
		t.Fatalf("agent:a1 must be filtered by resource_type: %+v", grants)
	}
	if !eqOps(idx["resource:*"], "view_detail") {
		t.Fatalf("resource:* = %v", idx["resource:*"])
	}
	if !eqOps(idx["resource:r1"], "modify") || !eqOps(idx["resource:r2"], "modify") {
		t.Fatalf("both instances expected: %+v", grants)
	}

	// resource_id=r1: narrows instances to r1, still keeps resource:* row.
	_, grants, err = e.EffectivePermissions(user, PermQuery{ResourceType: "resource", ResourceIDs: []string{"r1"}})
	mustNoErr(t, err)
	idx = byObject(grants)
	if _, ok := idx["resource:r2"]; ok {
		t.Fatalf("resource:r2 must be narrowed out: %+v", grants)
	}
	if !eqOps(idx["resource:*"], "view_detail") {
		t.Fatalf("type-wide row must remain under resource_id filter: %+v", grants)
	}
	if !eqOps(idx["resource:r1"], "modify") {
		t.Fatalf("resource:r1 = %v", idx["resource:r1"])
	}
}

// TypeWideOnly collapses to one row per type: no instance rows, but the ops
// held only on instances are summarised in InstanceOperations rather than lost.
func TestEffectivePermissionsTypeWideOnly(t *testing.T) {
	e := newTestEnforcer(t)
	const user = "u-tw"
	mustNoErr(t, e.GrantRolePermission("role-tw", "large_model", "*", "view"))
	mustNoErr(t, e.AssignRole(user, "role-tw"))
	mustNoErr(t, e.GrantObjectPermission(user, "large_model", "m1", "modify")) // surplus over type-wide
	mustNoErr(t, e.GrantObjectPermission(user, "large_model", "m2", "modify")) // same op, must not duplicate
	mustNoErr(t, e.GrantObjectPermission(user, "large_model", "m3", "view"))   // already type-wide, not surplus
	mustNoErr(t, e.GrantObjectPermission(user, "agent", "a1", "use"))          // instance-only, no type-wide row

	has, grants, err := e.EffectivePermissions(user, PermQuery{TypeWideOnly: true})
	mustNoErr(t, err)
	if has {
		t.Fatal("hasWildcard: want false")
	}
	idx := byObject(grants)
	instIdx := map[string][]string{}
	for _, g := range grants {
		ops := append([]string(nil), g.InstanceOperations...)
		sort.Strings(ops)
		instIdx[g.Object] = ops
	}
	if len(grants) != 2 {
		t.Fatalf("want exactly one row per type, got %d: %+v", len(grants), grants)
	}
	if _, ok := idx["large_model:m1"]; ok {
		t.Errorf("no instance row may survive: %+v", grants)
	}
	if !eqOps(idx["large_model:*"], "view") {
		t.Errorf("large_model:* ops = %v, want [view]", idx["large_model:*"])
	}
	// modify appears once despite two instances holding it; view is type-wide so
	// it is not repeated as an instance op.
	if !eqOps(instIdx["large_model:*"], "modify") {
		t.Errorf("large_model:* instance ops = %v, want [modify]", instIdx["large_model:*"])
	}
	// The object-only accessor: no type-wide grant, yet the type must still be
	// reported or its entry points vanish.
	if len(idx["agent:*"]) != 0 {
		t.Errorf("agent:* ops = %v, want none", idx["agent:*"])
	}
	if !eqOps(instIdx["agent:*"], "use") {
		t.Errorf("agent:* instance ops = %v, want [use]", instIdx["agent:*"])
	}

	// Composes with ResourceType: only the queried type's row.
	_, grants, err = e.EffectivePermissions(user, PermQuery{ResourceType: "large_model", TypeWideOnly: true})
	mustNoErr(t, err)
	if len(grants) != 1 || grants[0].Object != "large_model:*" {
		t.Errorf("scoped type-wide = %+v, want only large_model:*", grants)
	}
}

// A wildcard holder short-circuits before the type-wide collapse, so scope=type
// still reports the single all-powerful row (never an empty permission set).
func TestEffectivePermissionsTypeWideOnlyWildcard(t *testing.T) {
	e := newTestEnforcer(t)
	const super = "u-super-tw"
	mustNoErr(t, e.Grant(super, "*", "*"))

	has, grants, err := e.EffectivePermissions(super, PermQuery{TypeWideOnly: true})
	mustNoErr(t, err)
	if !has || len(grants) != 1 || grants[0].Object != "*:*" || !eqOps(grants[0].Operations, "*") {
		t.Fatalf("wildcard under scope=type = %+v, want single *:* [*]", grants)
	}
}
