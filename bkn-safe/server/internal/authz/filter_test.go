// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"reflect"
	"testing"
)

const (
	superAdminRole = "7dcfcc9c-ad02-11e8-aa06-000c29358ad6"
	builderRole    = "network-builder-role"
)

var knOps = []string{"view_detail", "create", "modify", "delete", "query_data", "authorize", "task_manage"}

// opsOf indexes a filter result by resource id.
func opsOf(t *testing.T, got []FilteredResource) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, r := range got {
		out[r.ID] = r.Operations
	}
	return out
}

// TestFilterResourceOpsProjection is the case the batch contract exists for:
// visibility is decided by view_detail alone, while the returned operation set
// is the full candidate subset the accessor holds. Before this, a caller could
// only ask one question and got [view_detail] back for everyone.
func TestFilterResourceOpsProjection(t *testing.T) {
	e := newTestEnforcer(t)
	const user = "u-1"

	// Role-derived: view_detail on every knowledge network.
	mustNoErr(t, e.GrantRolePermission(builderRole, "knowledge_network", "*", "view_detail"))
	mustNoErr(t, e.AssignRole(user, builderRole))
	// Direct instance grants: owner-ish ops on kn-1 only.
	mustNoErr(t, e.GrantObjectPermission(user, "knowledge_network", "kn-1", "modify"))
	mustNoErr(t, e.GrantObjectPermission(user, "knowledge_network", "kn-1", "delete"))
	// A resource the accessor cannot see at all.
	mustNoErr(t, e.GrantObjectPermission("someone-else", "knowledge_network", "kn-3", "view_detail"))

	got, err := e.FilterResourceOps(user, []ResourceRef{
		{"knowledge_network", "kn-1"},
		{"knowledge_network", "kn-2"},
	}, []string{"view_detail"}, knOps)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}

	ops := opsOf(t, got)
	if want := []string{"view_detail", "modify", "delete"}; !reflect.DeepEqual(ops["kn-1"], want) {
		t.Errorf("kn-1 ops = %v, want %v", ops["kn-1"], want)
	}
	if want := []string{"view_detail"}; !reflect.DeepEqual(ops["kn-2"], want) {
		t.Errorf("kn-2 ops = %v, want %v", ops["kn-2"], want)
	}
	if len(got) != 2 {
		t.Errorf("got %d resources, want 2 (kn-3 was never requested)", len(got))
	}
}

// TestFilterResourceOpsVisibility covers the filtering axis: resources missing
// any visibility op are dropped entirely, not returned with an empty op set.
func TestFilterResourceOpsVisibility(t *testing.T) {
	e := newTestEnforcer(t)
	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "knowledge_network", "kn-1", "view_detail"))
	mustNoErr(t, e.GrantObjectPermission(user, "knowledge_network", "kn-1", "query_data"))
	mustNoErr(t, e.GrantObjectPermission(user, "knowledge_network", "kn-2", "view_detail"))

	refs := []ResourceRef{
		{"knowledge_network", "kn-1"},
		{"knowledge_network", "kn-2"},
		{"knowledge_network", "kn-3"},
	}

	t.Run("single visibility op", func(t *testing.T) {
		got, err := e.FilterResourceOps(user, refs, []string{"view_detail"}, knOps)
		if err != nil {
			t.Fatalf("filter: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %v, want kn-1 and kn-2 only", got)
		}
	})

	t.Run("multiple visibility ops are conjunctive", func(t *testing.T) {
		got, err := e.FilterResourceOps(user, refs, []string{"view_detail", "query_data"}, knOps)
		if err != nil {
			t.Fatalf("filter: %v", err)
		}
		if len(got) != 1 || got[0].ID != "kn-1" {
			t.Fatalf("got %v, want kn-1 only (kn-2 lacks query_data)", got)
		}
	})

	t.Run("no visibility ops returns every requested resource", func(t *testing.T) {
		got, err := e.FilterResourceOps(user, refs, nil, knOps)
		if err != nil {
			t.Fatalf("filter: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d resources, want 3", len(got))
		}
		ops := opsOf(t, got)
		if len(ops["kn-3"]) != 0 {
			t.Errorf("kn-3 ops = %v, want empty", ops["kn-3"])
		}
	})
}

// TestFilterResourceOpsSuperAdmin pins the wildcard path: the seeded super_admin
// grant is object "*" / act "*", which must yield the complete candidate set on
// every resource of every type — the exact case that regressed to [view_detail].
func TestFilterResourceOpsSuperAdmin(t *testing.T) {
	e := newTestEnforcer(t)
	const admin = "admin-1"
	mustNoErr(t, e.Grant(superAdminRole, "*", "*"))
	mustNoErr(t, e.AssignRole(admin, superAdminRole))

	got, err := e.FilterResourceOps(admin, []ResourceRef{
		{"knowledge_network", "kn-1"},
		{"resource", "r-9"},
	}, []string{"view_detail"}, knOps)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d resources, want 2", len(got))
	}
	for _, r := range got {
		if !reflect.DeepEqual(r.Operations, knOps) {
			t.Errorf("%s:%s ops = %v, want all candidates", r.Type, r.ID, r.Operations)
		}
	}
}

// TestFilterResourceOpsPublicGrant covers the root-department ("public") policy
// subject. Those rows are not part of GetImplicitPermissionsForUser, so an index
// built from implicit permissions alone would deny what Check allows.
func TestFilterResourceOpsPublicGrant(t *testing.T) {
	e := newTestEnforcer(t)
	const stranger = "u-nobody"
	mustNoErr(t, e.GrantObjectPermission(PublicAccessorID, "toolbox", "built-in", "use"))

	got, err := e.FilterResourceOps(stranger, []ResourceRef{
		{"toolbox", "built-in"},
		{"toolbox", "private"},
	}, []string{"use"}, []string{"use", "modify"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 1 || got[0].ID != "built-in" {
		t.Fatalf("got %v, want built-in only", got)
	}
	if want := []string{"use"}; !reflect.DeepEqual(got[0].Operations, want) {
		t.Errorf("ops = %v, want %v", got[0].Operations, want)
	}
}

// TestFilterResourceOpsMixedTypes checks one batch carrying several resource
// types: grants must not leak across types (the keyMatch, not keyMatch2, rule).
func TestFilterResourceOpsMixedTypes(t *testing.T) {
	e := newTestEnforcer(t)
	const user = "u-1"
	mustNoErr(t, e.GrantRolePermission(builderRole, "knowledge_network", "*", "view_detail"))
	mustNoErr(t, e.AssignRole(user, builderRole))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "r-1", "view_detail"))

	got, err := e.FilterResourceOps(user, []ResourceRef{
		{"knowledge_network", "kn-1"},
		{"resource", "r-1"},
		{"resource", "r-2"},
		{"internal_resource", "i-1"},
	}, []string{"view_detail"}, []string{"view_detail", "modify"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want kn-1 and r-1", got)
	}
	for _, r := range got {
		if r.Type == "resource" && r.ID != "r-1" {
			t.Errorf("unexpected resource %s", r.ID)
		}
	}
}

// TestFilterResourceOpsEmptyInputs guards the degenerate calls a list page makes
// on an empty page or an unauthenticated-but-routed request.
func TestFilterResourceOpsEmptyInputs(t *testing.T) {
	e := newTestEnforcer(t)
	got, err := e.FilterResourceOps("u-1", nil, []string{"view_detail"}, knOps)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}

	// Unknown accessor: no grants, nothing visible, no error.
	got, err = e.FilterResourceOps("", []ResourceRef{{"knowledge_network", "kn-1"}}, []string{"view_detail"}, knOps)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// TestFilterResourceOpsMatchesCheck is the parity guard. The batch path resolves
// grants once and projects them in memory instead of calling Enforce per
// (resource, op); this asserts the two agree on a matrix spanning direct grants,
// role inheritance, type-wide wildcards, act "*", public grants and misses.
func TestFilterResourceOpsMatchesCheck(t *testing.T) {
	e := newTestEnforcer(t)
	const (
		user     = "u-1"
		admin    = "admin-1"
		stranger = "u-nobody"
		subRole  = "sub-role"
	)
	mustNoErr(t, e.Grant(superAdminRole, "*", "*"))
	mustNoErr(t, e.AssignRole(admin, superAdminRole))
	mustNoErr(t, e.GrantRolePermission(builderRole, "knowledge_network", "*", "view_detail"))
	mustNoErr(t, e.GrantRolePermission(subRole, "knowledge_network", "kn-2", "modify"))
	mustNoErr(t, e.AssignRole(user, builderRole))
	// Role -> role: inherited transitively by g().
	mustNoErr(t, e.AssignRole(builderRole, subRole))
	mustNoErr(t, e.GrantObjectPermission(user, "knowledge_network", "kn-1", "delete"))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "r-1", "*"))
	mustNoErr(t, e.GrantObjectPermission(PublicAccessorID, "toolbox", "built-in", "use"))

	refs := []ResourceRef{
		{"knowledge_network", "kn-1"},
		{"knowledge_network", "kn-2"},
		{"resource", "r-1"},
		{"resource", "r-2"},
		{"toolbox", "built-in"},
		{"toolbox", "private"},
	}
	ops := append(append([]string{}, knOps...), "use")

	for _, accessor := range []string{user, admin, stranger} {
		got, err := e.FilterResourceOps(accessor, refs, nil, ops)
		if err != nil {
			t.Fatalf("filter(%s): %v", accessor, err)
		}
		batch := map[string]map[string]bool{}
		for _, r := range got {
			key := r.Type + ":" + r.ID
			batch[key] = map[string]bool{}
			for _, op := range r.Operations {
				batch[key][op] = true
			}
		}
		for _, ref := range refs {
			for _, op := range ops {
				want, err := e.Check(accessor, ref.Type, ref.ID, op)
				if err != nil {
					t.Fatalf("check: %v", err)
				}
				if got := batch[ref.Type+":"+ref.ID][op]; got != want {
					t.Errorf("accessor=%s %s:%s op=%s: batch=%v check=%v",
						accessor, ref.Type, ref.ID, op, got, want)
				}
			}
		}
	}
}
