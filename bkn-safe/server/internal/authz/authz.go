// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

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
//
// The matcher also accepts policies whose subject is PublicAccessorID: ISF's
// "grant to the root department = everyone" convention. bkn-safe keeps no
// user→department g rules (membership lives in relational tables only), so a
// root-department grant would otherwise never match any requester; instead the
// matcher treats it as a public grant, scoped as usual by object and act.
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
m = (g(r.sub, p.sub) || p.sub == "` + PublicAccessorID + `") && keyMatch(r.obj, p.obj) && (p.act == "*" || r.act == p.act)
`

// PublicAccessorID is the root-department accessor: a policy granted to this
// subject applies to every requester (see modelConf). The UUID is the ISF root
// department id, written by e.g. execution-factory's CreateIntCompPolicyForAllUsers
// (interfaces.AccessorRootDepartmentID) for built-in toolbox public access.
const PublicAccessorID = "00000000-0000-0000-0000-000000000000"

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

// CanAdmin reports whether the accessor may use the admin API. The seeded
// super-admin (wildcard "*","*" grant) passes via keyMatch; any other role must
// be granted the safe_admin/manage capability explicitly to administer.
func (en *Enforcer) CanAdmin(accessorID string) (bool, error) {
	return en.Check(accessorID, "safe_admin", "console", "manage")
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
	return groupGrantsByObject(rows), nil
}

// groupGrantsByObject collapses raw (sub, obj, act) policy rows into per-object
// grants, de-duplicating acts (the same grant can arrive via several roles).
// Objects follow first appearance; ops within an object follow row order.
func groupGrantsByObject(rows [][]string) []RoleGrant {
	byObj := map[string]map[string]bool{}
	ops := map[string][]string{}
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		o, act := row[1], row[2]
		if _, ok := byObj[o]; !ok {
			order = append(order, o)
			byObj[o] = map[string]bool{}
		}
		if byObj[o][act] {
			continue
		}
		byObj[o][act] = true
		ops[o] = append(ops[o], act)
	}
	out := make([]RoleGrant, 0, len(order))
	for _, o := range order {
		out = append(out, RoleGrant{Object: o, Operations: ops[o]})
	}
	return out
}

// PermQuery narrows an EffectivePermissions read. The zero value returns the
// full effective set; ResourceType scopes to one type and ResourceIDs further
// narrows the instance exception rows (ResourceIDs is only meaningful with a
// ResourceType set).
type PermQuery struct {
	ResourceType string   // "" = all types
	ResourceIDs  []string // empty = all instances of the type
}

// EffectivePermissions returns the accessor's authorization as a COLLAPSED,
// effective set rather than one row per (instance, op) — the payload behind the
// /me/permissions self-service read. It exists to keep that response bounded:
// its size is proportional to (#types + #real exceptions), not to the number of
// resource instances the accessor can see.
//
// Two shapes:
//
//   - Resource wildcard holder: if the accessor holds a bare "*"/"*" grant, the
//     whole set collapses to a single {type:"*", id:"*", ops:["*"]} row (scoped:
//     projected onto the queried type as {type, id:"*", ops:["*"]}). This is
//     gated on the ACTUAL wildcard grant, NOT on CanAdmin/is_admin: an
//     admin-console-only role holds safe_admin:console:manage without the
//     resource wildcard and must not be reported as all-powerful over every
//     resource. hasWildcard reports whether this short-circuit fired.
//
//   - Otherwise, per type: one type-wide row {type, id:"*", typeWideOps} plus an
//     instance row ONLY when that instance grants ops beyond its type-wide set
//     (the row carries just the surplus ops). Instances fully covered by a
//     type-wide grant are dropped. Callers/frontends judge "may do op on
//     (type,id)" as op ∈ typeWide(type) OR op ∈ instance(type,id) — i.e. they
//     union the id:"*" row with the instance row.
//
// Object/op order follows GetImplicitPermissionsForUser; callers treat the
// result as sets.
func (en *Enforcer) EffectivePermissions(accessorID string, q PermQuery) (hasWildcard bool, grants []RoleGrant, err error) {
	rows, err := en.e.GetImplicitPermissionsForUser(accessorID)
	if err != nil {
		return false, nil, err
	}
	grouped := groupGrantsByObject(rows)

	// Wildcard short-circuit — keyed on a real "*"/"*" grant, not is_admin.
	for _, g := range grouped {
		rtype, _ := splitObjectKey(g.Object)
		if rtype == ActAll && hasOp(g.Operations, ActAll) {
			if q.ResourceType == "" {
				return true, []RoleGrant{{Object: ActAll + ":" + ActAll, Operations: []string{ActAll}}}, nil
			}
			return true, []RoleGrant{{Object: q.ResourceType + ":*", Operations: []string{ActAll}}}, nil
		}
	}

	// Type-wide op set per type (the id "*" rows).
	typeWide := map[string]map[string]bool{}
	for _, g := range grouped {
		rtype, rid := splitObjectKey(g.Object)
		if rid == "*" {
			if typeWide[rtype] == nil {
				typeWide[rtype] = map[string]bool{}
			}
			for _, op := range g.Operations {
				typeWide[rtype][op] = true
			}
		}
	}

	idFilter := map[string]bool{}
	for _, id := range q.ResourceIDs {
		idFilter[id] = true
	}

	out := make([]RoleGrant, 0, len(grouped))
	for _, g := range grouped {
		rtype, rid := splitObjectKey(g.Object)
		if q.ResourceType != "" && rtype != q.ResourceType {
			continue
		}
		if rid == "*" {
			// Type-wide row: always kept within scope; the frontend unions on it.
			out = append(out, RoleGrant{Object: g.Object, Operations: g.Operations})
			continue
		}
		// Instance row: keep only ops beyond the type-wide set; drop if fully
		// covered (this is what removes the per-instance fan-out).
		tw := typeWide[rtype]
		extra := make([]string, 0, len(g.Operations))
		for _, op := range g.Operations {
			if tw[op] {
				continue
			}
			extra = append(extra, op)
		}
		if len(extra) == 0 {
			continue
		}
		if len(idFilter) > 0 && !idFilter[rid] {
			continue
		}
		out = append(out, RoleGrant{Object: g.Object, Operations: extra})
	}
	return false, out, nil
}

// hasOp reports whether ops contains want.
func hasOp(ops []string, want string) bool {
	for _, op := range ops {
		if op == want {
			return true
		}
	}
	return false
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

// RemoveRolePermissions purges only the p-lines owned by a role, preserving its
// member bindings. Seed uses this before re-applying the built-in permission
// matrix so removed grants do not linger across upgrades.
func (en *Enforcer) RemoveRolePermissions(roleID string) error {
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

// ObjectGrant is one accessor's grant set on one concrete resource instance:
// the cross-product cell of the object-level authorization matrix (who can do
// what on which specific object). Powers the admin "授权管理" overview.
type ObjectGrant struct {
	AccessorID   string
	ResourceType string
	ResourceID   string
	Operations   []string
}

// ListObjectGrants enumerates concrete per-object accessor grants across all
// resources, grouped by (accessor, resource). Type-wide ("*" id) and bare-"*"
// (super-admin) patterns are excluded — those belong to roles/seed, not the
// object-grant surface. accessorID/resourceType/resourceID are optional filters
// (empty = match any). Subjects are returned verbatim; the caller separates
// user accessors from role subjects (casbin stores both as opaque ids).
func (en *Enforcer) ListObjectGrants(accessorID, resourceType, resourceID string) ([]ObjectGrant, error) {
	var rows [][]string
	var err error
	if accessorID != "" {
		rows, err = en.e.GetFilteredPolicy(0, accessorID)
	} else {
		rows, err = en.e.GetPolicy()
	}
	if err != nil {
		return nil, err
	}
	type key struct{ sub, rtype, rid string }
	ops := map[key][]string{}
	seen := map[key]map[string]bool{}
	order := make([]key, 0, len(rows))
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		sub, o, act := row[0], row[1], row[2]
		rtype, rid := splitObjectKey(o)
		if rid == "" || rid == "*" { // skip type-wide / bare "*" (role/seed grants)
			continue
		}
		if resourceType != "" && rtype != resourceType {
			continue
		}
		if resourceID != "" && rid != resourceID {
			continue
		}
		k := key{sub, rtype, rid}
		if seen[k] == nil {
			order = append(order, k)
			seen[k] = map[string]bool{}
		}
		if seen[k][act] {
			continue
		}
		seen[k][act] = true
		ops[k] = append(ops[k], act)
	}
	out := make([]ObjectGrant, 0, len(order))
	for _, k := range order {
		out = append(out, ObjectGrant{
			AccessorID: k.sub, ResourceType: k.rtype, ResourceID: k.rid, Operations: ops[k],
		})
	}
	return out, nil
}

// SetObjectPermissions replaces an accessor's entire op set on one concrete
// resource instance: it drops every existing (accessor, object) p-line and adds
// one per op. The "edit a grant" write behind the admin object-grant page
// (POST /policies only adds, never prunes). Passing no ops clears the grant.
func (en *Enforcer) SetObjectPermissions(accessorID, resourceType, resourceID string, ops []string) error {
	if err := en.RemoveAccessorResourcePolicies(accessorID, resourceType, resourceID); err != nil {
		return err
	}
	for _, op := range ops {
		if _, err := en.e.AddPolicy(accessorID, obj(resourceType, resourceID), op); err != nil {
			return err
		}
	}
	return nil
}

// RemoveAccessorResourcePolicies drops every op one accessor holds on one
// concrete resource instance (revoke a single grantee's grant), leaving other
// accessors' grants on the same resource intact — unlike RemoveResourcePolicies,
// which wipes the resource for everyone on delete. Idempotent.
func (en *Enforcer) RemoveAccessorResourcePolicies(accessorID, resourceType, resourceID string) error {
	_, err := en.e.RemoveFilteredPolicy(0, accessorID, obj(resourceType, resourceID))
	return err
}

// splitObjectKey splits a casbin object key "type:id" on the FIRST colon (the
// id may itself contain colons). A bare "*" yields type "*", id "".
func splitObjectKey(o string) (rtype, rid string) {
	for i := 0; i < len(o); i++ {
		if o[i] == ':' {
			return o[:i], o[i+1:]
		}
	}
	return o, ""
}
