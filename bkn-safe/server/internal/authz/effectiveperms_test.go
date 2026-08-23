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

// instanceOpsOf returns the folded instance operations on one collapsed row.
func instanceOpsOf(grants []RoleGrant, object string) []string {
	for _, g := range grants {
		if g.Object == object {
			return g.InstanceOperations
		}
	}
	return nil
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

// TypeWideOnly emits no instance exception row — the surplus ops fold into the
// type row's InstanceOperations instead — and composes with ResourceType.
func TestEffectivePermissionsTypeWideOnly(t *testing.T) {
	e := newTestEnforcer(t)
	const user = "u-tw"
	mustNoErr(t, e.GrantRolePermission("role-tw", "large_model", "*", "view"))
	mustNoErr(t, e.AssignRole(user, "role-tw"))
	mustNoErr(t, e.GrantObjectPermission(user, "large_model", "m1", "modify")) // surplus over type-wide
	mustNoErr(t, e.GrantObjectPermission(user, "large_model", "m2", "view"))   // covered, contributes nothing

	has, grants, err := e.EffectivePermissions(user, PermQuery{TypeWideOnly: true})
	mustNoErr(t, err)
	if has {
		t.Fatal("hasWildcard: want false")
	}
	idx := byObject(grants)
	if !eqOps(idx["large_model:*"], "view") {
		t.Errorf("large_model:* = %v, want [view]", idx["large_model:*"])
	}
	if _, ok := idx["large_model:m1"]; ok {
		t.Errorf("surplus instance row must not appear as a row: %+v", grants)
	}
	if !eqOps(instanceOpsOf(grants, "large_model:*"), "modify") {
		t.Errorf("large_model:* instance_operations = %v, want [modify]",
			instanceOpsOf(grants, "large_model:*"))
	}

	// Composes with ResourceType: single type-wide row of the queried type.
	_, grants, err = e.EffectivePermissions(user, PermQuery{ResourceType: "large_model", TypeWideOnly: true})
	mustNoErr(t, err)
	idx = byObject(grants)
	if len(idx) != 1 || !eqOps(idx["large_model:*"], "view") {
		t.Errorf("scoped type-wide = %+v, want only large_model:* [view]", grants)
	}
}

// The regression this fold exists for: an accessor holding NOTHING type-wide on
// a type, only a grant on one object. Before, scope=type returned no row for the
// type and the console hid the entry to a resource the accessor was explicitly
// granted (bkn-studio#478). Now the type reports with empty Operations and the
// instance ops on the side.
func TestEffectivePermissionsTypeWideOnlyInstanceOnlyType(t *testing.T) {
	e := newTestEnforcer(t)
	const user = "u-obj"
	// Mirrors the shipped normal_user role: capability types type-wide, no data
	// grants at all — data types are reachable only through object grants.
	mustNoErr(t, e.GrantRolePermission("role-normal", "tool_box", "*", "view"))
	mustNoErr(t, e.AssignRole(user, "role-normal"))
	mustNoErr(t, e.GrantObjectPermission(user, "knowledge_network", "kn-1", "view_detail"))
	mustNoErr(t, e.GrantObjectPermission(user, "knowledge_network", "kn-2", "query_data"))

	_, grants, err := e.EffectivePermissions(user, PermQuery{TypeWideOnly: true})
	mustNoErr(t, err)

	idx := byObject(grants)
	row, ok := idx["knowledge_network:*"]
	if !ok {
		t.Fatalf("knowledge_network must report despite having no type-wide grant: %+v", grants)
	}
	if len(row) != 0 {
		t.Errorf("knowledge_network:* operations = %v, want empty — nothing is granted type-wide", row)
	}
	// Union across both objects: the caller learns the type is reachable, not
	// which network carries which op.
	if !eqOps(instanceOpsOf(grants, "knowledge_network:*"), "query_data", "view_detail") {
		t.Errorf("knowledge_network:* instance_operations = %v, want [query_data view_detail]",
			instanceOpsOf(grants, "knowledge_network:*"))
	}
	// The role-held type-wide grant is untouched and carries no instance ops.
	if !eqOps(idx["tool_box:*"], "view") {
		t.Errorf("tool_box:* = %v, want [view]", idx["tool_box:*"])
	}
	if got := instanceOpsOf(grants, "tool_box:*"); len(got) != 0 {
		t.Errorf("tool_box:* instance_operations = %v, want empty", got)
	}

	// Unscoped reads are unchanged: real instance rows, no fold.
	_, grants, err = e.EffectivePermissions(user, PermQuery{})
	mustNoErr(t, err)
	idx = byObject(grants)
	if !eqOps(idx["knowledge_network:kn-1"], "view_detail") {
		t.Errorf("kn-1 = %v, want [view_detail]", idx["knowledge_network:kn-1"])
	}
	if _, ok := idx["knowledge_network:*"]; ok {
		t.Errorf("no synthetic type row without TypeWideOnly: %+v", grants)
	}
}
