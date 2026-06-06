// Package authz is bkn-safe's authorization engine: a Casbin RBAC model with
// resource instances, backed by a GORM adapter (policies live in the shared DB).
//
// This is a clean redesign, NOT the ISF authorization contract. Kowell only
// uses the RBAC subset (ISF's deny/condition/obligation/hierarchy/expires are
// unused), so the model is allow-only.
//
// Object format is "type:id" (e.g. "agent:probe", "agent:*"). The matcher uses
// keyMatch — NOT keyMatch2: keyMatch2 treats ":" as a named wildcard, which
// would make a per-object grant "pipeline:p1" over-match "pipeline:p2"
// (privilege escalation). keyMatch treats only "*" as a wildcard.
package authz

import (
	"fmt"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"gorm.io/gorm"
)

// modelConf is the RBAC + resource-instance Casbin model.
//
//	r = sub, obj, act   sub=accessorID, obj="type:id", act=operation
//	g = _, _            user/app -> role (UUID-preserved)
const modelConf = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub) && keyMatch(r.obj, p.obj) && (p.act == "*" || r.act == p.act)
`

// ActAll is the wildcard act: a policy with act "*" grants every operation on
// the matched object (used for the super-admin "do everything" grant).
const ActAll = "*"

// Enforcer wraps a Casbin enforcer with the bkn-safe object convention.
type Enforcer struct {
	e *casbin.Enforcer
}

// New builds an Enforcer using a GORM-backed policy store on the given db.
func New(db *gorm.DB) (*Enforcer, error) {
	adapter, err := gormadapter.NewAdapterByDB(db)
	if err != nil {
		return nil, fmt.Errorf("new gorm adapter: %w", err)
	}
	m, err := model.NewModelFromString(modelConf)
	if err != nil {
		return nil, fmt.Errorf("parse casbin model: %w", err)
	}
	e, err := casbin.NewEnforcer(m, adapter)
	if err != nil {
		return nil, fmt.Errorf("new enforcer: %w", err)
	}
	if err := e.LoadPolicy(); err != nil {
		return nil, fmt.Errorf("load policy: %w", err)
	}
	return &Enforcer{e: e}, nil
}

// obj builds the "type:id" object key.
func obj(resourceType, id string) string { return resourceType + ":" + id }

// Check reports whether accessor may perform op on the given resource instance.
func (en *Enforcer) Check(accessorID, resourceType, resourceID, op string) (bool, error) {
	return en.e.Enforce(accessorID, obj(resourceType, resourceID), op)
}

// AllowedOps returns, from the candidate ops, those the accessor may perform on
// the resource. Mirrors ISF resource-operation (allow_operation): the result is
// a set; callers must not depend on order.
func (en *Enforcer) AllowedOps(accessorID, resourceType, resourceID string, candidates []string) ([]string, error) {
	out := make([]string, 0, len(candidates))
	for _, op := range candidates {
		ok, err := en.e.Enforce(accessorID, obj(resourceType, resourceID), op)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, op)
		}
	}
	return out, nil
}

// GrantRolePermission grants a role an op over a resource-type instance pattern
// (id may be "*" for the whole type). Idempotent.
func (en *Enforcer) GrantRolePermission(roleID, resourceType, idPattern, op string) error {
	_, err := en.e.AddPolicy(roleID, obj(resourceType, idPattern), op)
	return err
}

// RevokeRolePermission removes a role's op over a resource-type instance
// pattern (the inverse of GrantRolePermission). Idempotent.
func (en *Enforcer) RevokeRolePermission(roleID, resourceType, idPattern, op string) error {
	_, err := en.e.RemovePolicy(roleID, obj(resourceType, idPattern), op)
	return err
}

// Grant adds a raw (sub, obj, act) policy. obj is the full object pattern
// (e.g. "agent:*" or "*" for everything); act may be ActAll ("*"). Used by the
// seed for the super-admin wildcard. Idempotent.
func (en *Enforcer) Grant(sub, obj, act string) error {
	_, err := en.e.AddPolicy(sub, obj, act)
	return err
}

// GrantObjectPermission grants an accessor an op over one concrete resource
// instance (the CreateResources pattern). Idempotent.
func (en *Enforcer) GrantObjectPermission(accessorID, resourceType, resourceID, op string) error {
	_, err := en.e.AddPolicy(accessorID, obj(resourceType, resourceID), op)
	return err
}

// RevokeObjectPermission removes a concrete per-object grant.
func (en *Enforcer) RevokeObjectPermission(accessorID, resourceType, resourceID, op string) error {
	_, err := en.e.RemovePolicy(accessorID, obj(resourceType, resourceID), op)
	return err
}

// AssignRole binds an accessor (user/app) to a role. Idempotent.
func (en *Enforcer) AssignRole(accessorID, roleID string) error {
	_, err := en.e.AddGroupingPolicy(accessorID, roleID)
	return err
}

// RemoveRole unbinds an accessor from a role (the inverse of AssignRole).
// Idempotent: removing a binding that isn't there is a no-op.
func (en *Enforcer) RemoveRole(accessorID, roleID string) error {
	_, err := en.e.RemoveGroupingPolicy(accessorID, roleID)
	return err
}

// RolesForAccessor lists the role ids directly bound to an accessor (the
// grouping g-lines with sub=accessor). Mirrors ISF accessor_roles.
func (en *Enforcer) RolesForAccessor(accessorID string) ([]string, error) {
	return en.e.GetRolesForUser(accessorID)
}

// RoleMembers lists the accessor ids bound to a role (the grouping g-lines with
// role=roleID). Mirrors ISF role-members.
func (en *Enforcer) RoleMembers(roleID string) ([]string, error) {
	return en.e.GetUsersForRole(roleID)
}

// RoleGrant is one resource-object grant held by a role: the object pattern
// ("type:id", id may be "*") and the operations allowed on it.
type RoleGrant struct {
	Object     string
	Operations []string
}

// RolePermissions lists the policy grants whose subject is the role, grouped by
// object pattern. Read-only view of a role's seeded permission matrix.
func (en *Enforcer) RolePermissions(roleID string) ([]RoleGrant, error) {
	rows, err := en.e.GetFilteredPolicy(0, roleID)
	if err != nil {
		return nil, err
	}
	byObj := map[string][]string{}
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		o, act := row[1], row[2]
		if _, ok := byObj[o]; !ok {
			order = append(order, o)
		}
		byObj[o] = append(byObj[o], act)
	}
	out := make([]RoleGrant, 0, len(order))
	for _, o := range order {
		out = append(out, RoleGrant{Object: o, Operations: byObj[o]})
	}
	return out, nil
}

// RemoveRoleCompletely purges every casbin trace of a role: its bindings
// (grouping g-lines with role=roleID) and its own permission grants (p-lines
// with sub=roleID). Called when a custom role is deleted. Idempotent.
func (en *Enforcer) RemoveRoleCompletely(roleID string) error {
	if _, err := en.e.RemoveFilteredGroupingPolicy(1, roleID); err != nil {
		return err
	}
	_, err := en.e.RemoveFilteredPolicy(0, roleID)
	return err
}

// RemoveAccessor purges every casbin trace of an accessor: its role bindings
// (grouping g-lines with sub=accessor) and any concrete object policies granted
// directly to it (p-lines with sub=accessor). Called when a user is deleted so
// no orphaned grants linger. Idempotent.
func (en *Enforcer) RemoveAccessor(accessorID string) error {
	if _, err := en.e.RemoveFilteredGroupingPolicy(0, accessorID); err != nil {
		return err
	}
	_, err := en.e.RemoveFilteredPolicy(0, accessorID)
	return err
}

// RemoveResourcePolicies drops every policy targeting a concrete resource
// instance (used when a resource is deleted).
func (en *Enforcer) RemoveResourcePolicies(resourceType, resourceID string) error {
	_, err := en.e.RemoveFilteredPolicy(1, obj(resourceType, resourceID))
	return err
}

// AccessibleResources lists the concrete resource-instance IDs of a given type
// that the accessor may perform op on, INCLUDING grants inherited via roles.
// The "*" id-pattern (type-wide grants, e.g. super-admin / data-admin) is
// excluded — this enumerates concrete instances only; callers handle the
// type-wide case separately (an "is-admin" short-circuit).
//
// IDs are returned verbatim (bkn-safe is opaque to any caller-side id encoding,
// e.g. "dagID:subtype"), de-duplicated, in first-appearance order. Mirrors ISF
// resource-list for one (accessor, type, op).
func (en *Enforcer) AccessibleResources(accessorID, resourceType, op string) ([]string, error) {
	perms, err := en.e.GetImplicitPermissionsForUser(accessorID)
	if err != nil {
		return nil, err
	}
	prefix := resourceType + ":"
	seen := map[string]bool{}
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		if len(p) < 3 {
			continue
		}
		o, act := p[1], p[2]
		if act != op && act != ActAll {
			continue
		}
		if len(o) <= len(prefix) || o[:len(prefix)] != prefix {
			continue
		}
		id := o[len(prefix):] // split on first ":" only; id may itself contain ":"
		if id == "*" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// ResourcePolicy is one accessor's grant set on a single resource instance.
type ResourcePolicy struct {
	AccessorID string
	Operations []string
}

// ResourcePolicies lists the per-accessor grants on a concrete resource
// instance, grouping the raw (sub, obj, act) rows by accessor. Order of
// accessors follows first appearance; ops within an accessor follow row order.
// Mirrors ISF list-policy for one resource (bkn-safe has no expiry/condition,
// so callers treat entries as never-expiring allow-only).
func (en *Enforcer) ResourcePolicies(resourceType, resourceID string) ([]ResourcePolicy, error) {
	rows, err := en.e.GetFilteredPolicy(1, obj(resourceType, resourceID))
	if err != nil {
		return nil, err
	}
	bySub := map[string][]string{}
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		sub, act := row[0], row[2]
		if _, ok := bySub[sub]; !ok {
			order = append(order, sub)
		}
		bySub[sub] = append(bySub[sub], act)
	}
	out := make([]ResourcePolicy, 0, len(order))
	for _, sub := range order {
		out = append(out, ResourcePolicy{AccessorID: sub, Operations: bySub[sub]})
	}
	return out, nil
}
