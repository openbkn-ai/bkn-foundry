// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package authz is bkn-safe's authorization engine: a Casbin RBAC model with
// resource instances, backed by a GORM adapter (policies live in the shared DB).
//
// This is a clean redesign, NOT the ISF authorization contract. Kowell only
// uses the RBAC subset plus an explicit deny effect. Deny overrides ordinary
// direct, role-derived, public and inherited allows. The seeded super-admin
// role is handled as a recovery-path bypass before policy evaluation.
//
// Object format is "type:id" (e.g. "agent:probe", "agent:*"). The matcher uses
// keyMatch — NOT keyMatch2: keyMatch2 treats ":" as a named wildcard, which
// would make a per-object grant "pipeline:p1" over-match "pipeline:p2"
// (privilege escalation). keyMatch treats only "*" as a wildcard.
package authz

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"gorm.io/gorm"

	safemodel "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// modelConf is the RBAC + resource-instance Casbin model.
//
//	r = sub, obj, act   sub=accessorID, obj="type:id", act=operation
//	p = sub, obj, act, eft, policy_source, authority_source
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
p = sub, obj, act, eft, policy_source, authority_source

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow)) && !some(where (p.eft == deny))

[matchers]
m = (g(r.sub, p.sub) || p.sub == "` + PublicAccessorID + `") && keyMatch(r.obj, p.obj) && (p.act == "*" || r.act == p.act)
`

// PublicAccessorID is the root-department accessor: a policy granted to this
// subject applies to every requester (see modelConf). The UUID is the ISF root
// department id, written by e.g. execution-factory's CreateIntCompPolicyForAllUsers
// (interfaces.AccessorRootDepartmentID) for built-in toolbox public access.
const PublicAccessorID = "00000000-0000-0000-0000-000000000000"

// SuperAdminRoleID is the immutable seeded recovery role. Explicit deny rules
// never constrain its members, so an administrator can always repair a broken
// authorization configuration.
const SuperAdminRoleID = "7dcfcc9c-ad02-11e8-aa06-000c29358ad6"

const (
	EffectAllow = "allow"
	EffectDeny  = "deny"
)

// ActAll is the wildcard act: a policy with act "*" grants every operation on
// the matched object (used for the super-admin "do everything" grant).
const ActAll = "*"

// ErrManagedProxyPolicies tells a resource-deletion caller to remove the
// corresponding proxy-grant sources before performing generic policy cleanup.
var ErrManagedProxyPolicies = errors.New("resource still has managed proxy policies")

// Enforcer wraps a Casbin enforcer with the bkn-safe object convention.
type Enforcer struct {
	e casbin.IEnforcer
	// db reads the resource hierarchy (which catalog a table belongs to) that
	// casbin cannot express: policies are keyed by an opaque "type:id", so
	// inheritance is resolved around the matcher, not inside it. Nil disables
	// inheritance entirely, which is the pre-#800 behaviour.
	db *gorm.DB
	// transactionMu serializes every policy write and must be acquired before
	// reading the adapter pointer. The gorm adapter temporarily replaces that
	// pointer while a transaction is in flight, so adapter-local locking alone
	// is too late for concurrent callers.
	transactionMu sync.Mutex
}

// New builds an Enforcer using a GORM-backed policy store on the given db.
func New(db *gorm.DB) (*Enforcer, error) {
	adapter, err := gormadapter.NewAdapterByDB(db)
	if err != nil {
		return nil, fmt.Errorf("new gorm adapter: %w", err)
	}
	// The old three-column model left v3 empty. Normalize those rows before the
	// four-column model loads them; this is idempotent and keeps every historical
	// grant effective as an explicit allow.
	if err := db.Table("casbin_rule").Where("ptype = ? AND (v3 IS NULL OR v3 = '')", "p").
		Update("v3", EffectAllow).Error; err != nil {
		return nil, fmt.Errorf("normalize legacy casbin policies: %w", err)
	}
	// Provenance classification and stable grant IDs belong to the coordinated
	// offline migration. Refuse to load an unclassified or partially projected
	// store instead of guessing ownership during startup.
	if err := (&Enforcer{db: db}).validateGrantProjection(); err != nil {
		if errors.Is(err, ErrPolicySourceMigrationRequired) {
			return nil, err
		}
		return nil, fmt.Errorf("validate authorization grant projection: %w", err)
	}
	m, err := model.NewModelFromString(modelConf)
	if err != nil {
		return nil, fmt.Errorf("parse casbin model: %w", err)
	}
	e, err := casbin.NewSyncedEnforcer(m, adapter)
	if err != nil {
		return nil, fmt.Errorf("new enforcer: %w", err)
	}
	if err := e.LoadPolicy(); err != nil {
		return nil, fmt.Errorf("load policy: %w", err)
	}
	return &Enforcer{e: e, db: db}, nil
}

// obj builds the "type:id" object key.
func obj(resourceType, id string) string { return resourceType + ":" + id }

// Check reports whether accessor may perform op on the given resource instance.
//
// A grant on the resource itself is answered by casbin alone. Only when that
// fails does the resource's hierarchy come into play: a grant on an ancestor
// (the catalog a table sits in) counts, translated through the operation
// mapping. With no ownership row recorded the second step finds nothing, which
// is exactly the pre-#800 decision (#800).
func (en *Enforcer) Check(accessorID, resourceType, resourceID, op string) (bool, error) {
	ok, err := en.checkPolicy(accessorID, resourceType, resourceID, op)
	if err != nil || !ok || en.db == nil {
		return ok, err
	}
	managed, err := en.isManagedProxy(accessorID)
	if err != nil {
		return false, err
	}
	if !managed {
		return true, nil
	}
	return en.hasCurrentProxySource(accessorID, resourceType, resourceID, op)
}

func (en *Enforcer) isManagedProxy(accessorID string) (bool, error) {
	var managed int64
	if err := en.db.Model(&safemodel.ManagedProxyAccount{}).
		Where("proxy_account_id = ?", accessorID).Count(&managed).Error; err != nil {
		return false, err
	}
	return managed > 0, nil
}

// checkPolicy evaluates the current direct, role and hierarchy policy without
// applying managed-proxy provenance. Delegator validation must use this raw
// path so a source can never recursively justify itself.
func (en *Enforcer) checkPolicy(accessorID, resourceType, resourceID, op string) (bool, error) {
	idx, err := en.grantIndex(accessorID)
	if err != nil {
		return false, err
	}
	if idx.superAdmin {
		return true, nil
	}
	direct := idx.decide(ResourceRef{Type: resourceType, ID: resourceID}, []string{op})
	if direct[op] == EffectDeny {
		return false, nil
	}
	if direct[op] == EffectAllow {
		return true, nil
	}
	inherited, err := en.inheritedOpsWithIndex(idx, resourceType, resourceID, []string{op})
	if err != nil {
		return false, err
	}
	return inherited[op], nil
}

// hasCurrentProxySource makes the source ledger part of every managed-proxy
// decision. A historical Casbin Allow is necessary but not sufficient: KN
// binding sources also depend on their recorded delegator still holding the
// exact downstream operation. Manual and administrator sources follow their
// own explicit lifecycle and remain valid while active.
func (en *Enforcer) hasCurrentProxySource(proxyID, resourceType, resourceID, op string) (bool, error) {
	var sources []safemodel.ProxyGrantSource
	if err := en.db.Where(
		"proxy_account_id = ? AND resource_type = ? AND resource_id = ? AND operation = ? AND lifecycle_status = ?",
		proxyID, resourceType, resourceID, op, safemodel.ProxyGrantSourceStatusActive,
	).Find(&sources).Error; err != nil {
		return false, err
	}
	for _, source := range sources {
		switch source.SourceType {
		case safemodel.ProxyGrantSourceTypeManual, safemodel.ProxyGrantSourceTypeAdmin:
			return true, nil
		case safemodel.ProxyGrantSourceTypeKNBinding:
			var registered int64
			if err := en.db.Model(&safemodel.Operation{}).
				Where("resource_type_id = ? AND id = ?", resourceType, op).Count(&registered).Error; err != nil {
				return false, err
			}
			if registered == 0 {
				continue
			}
			var active int64
			if err := en.db.Model(&safemodel.User{}).
				Where("id = ? AND enabled = ?", source.GrantedBy, true).Count(&active).Error; err != nil {
				return false, err
			}
			if active == 0 {
				continue
			}
			allowed, err := en.checkPolicy(source.GrantedBy, resourceType, resourceID, op)
			if err != nil {
				return false, err
			}
			if allowed {
				return true, nil
			}
		}
	}
	return false, nil
}

// AllowedOps returns, from the candidate ops, those the accessor may perform on
// the resource. Mirrors ISF resource-operation (allow_operation): the result is
// a set; callers must not depend on order.
func (en *Enforcer) AllowedOps(accessorID, resourceType, resourceID string, candidates []string) ([]string, error) {
	idx, err := en.grantIndex(accessorID)
	if err != nil {
		return nil, err
	}
	if idx.superAdmin {
		return append([]string(nil), candidates...), nil
	}
	out := make([]string, 0, len(candidates))
	missing := make([]string, 0, len(candidates))
	direct := idx.decide(ResourceRef{Type: resourceType, ID: resourceID}, candidates)
	for _, op := range candidates {
		if direct[op] == EffectAllow {
			out = append(out, op)
			continue
		}
		if direct[op] != EffectDeny {
			missing = append(missing, op)
		}
	}
	// One climb for everything that missed, rather than one per operation: the
	// ancestor chain and its operation mapping are the same for all of them.
	inherited, err := en.inheritedOpsWithIndex(idx, resourceType, resourceID, missing)
	if err != nil {
		return nil, err
	}
	for _, op := range missing {
		if inherited[op] {
			out = append(out, op)
		}
	}
	if len(out) == 0 || en.db == nil {
		return out, nil
	}
	managed, err := en.isManagedProxy(accessorID)
	if err != nil || !managed {
		return out, err
	}
	currentPermissions, err := en.currentProxyPermissions(accessorID)
	if err != nil {
		return nil, err
	}
	current := out[:0]
	for _, op := range out {
		if currentPermissions[proxyPermission{ResourceType: resourceType, ResourceID: resourceID, Operation: op}] {
			current = append(current, op)
		}
	}
	return current, nil
}

// GrantRolePermission grants a role an op over a resource-type instance pattern
// (id may be "*" for the whole type). Idempotent.
func (en *Enforcer) GrantRolePermission(roleID, resourceType, idPattern, op string) error {
	return en.addDefaultGrant(context.Background(), roleID, obj(resourceType, idPattern), op, EffectAllow,
		PolicySourceRolePermission, AuthoritySourceAdminAuthz)
}

// RevokeRolePermission removes a role's op over a resource-type instance
// pattern (the inverse of GrantRolePermission). Idempotent.
func (en *Enforcer) RevokeRolePermission(roleID, resourceType, idPattern, op string) error {
	return en.removeDefaultGrant(context.Background(), roleID, obj(resourceType, idPattern), op, EffectAllow,
		PolicySourceRolePermission, AuthoritySourceAdminAuthz)
}

// Grant adds a raw (sub, obj, act) policy. obj is the full object pattern
// (e.g. "agent:*" or "*" for everything); act may be ActAll ("*"). Used by the
// seed for the super-admin wildcard. Idempotent.
func (en *Enforcer) Grant(sub, obj, act string) error {
	return en.addDefaultGrant(context.Background(), sub, obj, act, EffectAllow,
		PolicySourceRolePermission, AuthoritySourceSystem)
}

// GrantObjectPermission is the compatibility writer for call sites that
// predate trusted provenance. It marks their rows legacy/migration so it never
// invents a Community bundle. New lifecycle and management flows use the
// source-specific writers in policy_source.go. Idempotent.
func (en *Enforcer) GrantObjectPermission(accessorID, resourceType, resourceID, op string) error {
	return en.addDefaultGrant(context.Background(), accessorID, obj(resourceType, resourceID), op, EffectAllow,
		PolicySourceLegacy, AuthoritySourceMigration)
}

// DenyObjectPermission adds an explicit per-object exception. Deny overrides
// every ordinary allow source; only membership in SuperAdminRoleID bypasses it.
func (en *Enforcer) DenyObjectPermission(accessorID, resourceType, resourceID, op string) error {
	return en.addDefaultGrant(context.Background(), accessorID, obj(resourceType, resourceID), op, EffectDeny,
		PolicySourceLegacy, AuthoritySourceMigration)
}

// RevokeObjectPermission removes every allow source for one concrete tuple.
// It is retained for lifecycle reconciliation code written before policies had
// provenance. New management flows that mean "one source" use RevokePolicy.
func (en *Enforcer) RevokeObjectPermission(accessorID, resourceType, resourceID, op string) error {
	return en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		_, err := tx.enforcer.removePolicyGrants(PolicyFilter{
			AccessorID: accessorID, Object: obj(resourceType, resourceID), Operation: op, Effect: EffectAllow,
		})
		return err
	})
}

// CanAdmin reports whether the accessor may use the admin API. The seeded
// super-admin (wildcard "*","*" grant) passes via keyMatch; any other role must
// be granted the safe_admin/manage capability explicitly to administer.
func (en *Enforcer) CanAdmin(accessorID string) (bool, error) {
	return en.Check(accessorID, "safe_admin", "console", "manage")
}

// AssignRole binds an accessor (user/app) to a role. Idempotent.
func (en *Enforcer) AssignRole(accessorID, roleID string) error {
	en.transactionMu.Lock()
	defer en.transactionMu.Unlock()
	_, err := en.e.AddGroupingPolicy(accessorID, roleID)
	return err
}

// RemoveRole unbinds an accessor from a role (the inverse of AssignRole).
// Idempotent: removing a binding that isn't there is a no-op.
func (en *Enforcer) RemoveRole(accessorID, roleID string) error {
	en.transactionMu.Lock()
	defer en.transactionMu.Unlock()
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
//
// InstanceOperations is set only by EffectivePermissions under TypeWideOnly, on
// a type row ("type:*"): it carries the operations the accessor holds on at
// least one INSTANCE of the type but not type-wide. It answers "is this type
// reachable at all", which is what menu/navigation callers ask; it deliberately
// does not say which instance, so it must never gate a concrete (type,id) — use
// an unscoped or resource_id-scoped read for that.
type RoleGrant struct {
	Object             string
	Operations         []string
	InstanceOperations []string
	DeniedOperations   []string
}

// RolePermissions lists the policy grants whose subject is the role, grouped by
// object pattern. Read-only view of a role's seeded permission matrix.
func (en *Enforcer) RolePermissions(roleID string) ([]RoleGrant, error) {
	rows, err := en.e.GetFilteredPolicy(0, roleID)
	if err != nil {
		return nil, err
	}
	return groupGrantsByObject(projectCommunityBundleRows(activePolicyRows(rows), false)), nil
}

// groupGrantsByObject collapses raw (sub, obj, act) policy rows into per-object
// grants, de-duplicating acts (the same grant can arrive via several roles).
// Objects follow first appearance; ops within an object follow row order.
func groupGrantsByObject(rows [][]string) []RoleGrant {
	byObj := map[string]map[string]bool{}
	ops := map[string][]string{}
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) < 3 || (len(row) >= 4 && row[3] == EffectDeny) {
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
// ResourceType set). TypeWideOnly emits no instance exception rows — for
// callers that only render type-level UI (menus/navigation) and would discard
// per-instance rows anyway; with per-object grants those rows dominate the
// payload (#353). Their operations are not lost: they fold into the type row's
// InstanceOperations, so "granted only on objects" still reports the type.
type PermQuery struct {
	ResourceType string   // "" = all types
	ResourceIDs  []string // empty = all instances of the type
	TypeWideOnly bool     // true = only id:"*" rows (and the wildcard row)
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
//   - Under TypeWideOnly the instance rows collapse instead of appearing: their
//     surplus ops move to the type row's InstanceOperations, and a type reached
//     only through instances gets a row with empty Operations. A caller asking
//     "may this accessor touch this type at all" unions Operations with
//     InstanceOperations; a caller asking about a concrete instance must not use
//     this shape at all, since it no longer says which instance.
//
// Object/op order follows GetImplicitPermissionsForUser; callers treat the
// result as sets.
func (en *Enforcer) EffectivePermissions(accessorID string, q PermQuery) (hasWildcard bool, grants []RoleGrant, err error) {
	rows, err := en.e.GetImplicitPermissionsForUser(accessorID)
	if err != nil {
		return false, nil, err
	}
	rows = activePolicyRows(rows)
	rows = projectCommunityBundleRows(rows, true)
	grouped := groupGrantsByObject(rows)
	superAdmin, err := en.hasSuperAdminRole(accessorID)
	if err != nil {
		return false, nil, err
	}

	hasDeny := false
	for _, row := range rows {
		if policyEffect(row) == EffectDeny {
			hasDeny = true
			break
		}
	}
	// Wildcard short-circuit — keyed on a real "*"/"*" grant, not is_admin.
	// An ordinary wildcard holder with exceptions cannot collapse to one row;
	// super-admin remains the only principal whose deny rows are intentionally
	// ignored by the runtime decision.
	for _, g := range grouped {
		rtype, _ := splitObjectKey(g.Object)
		if rtype == ActAll && hasOp(g.Operations, ActAll) && (!hasDeny || superAdmin) {
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
	// TypeWideOnly still emits no instance ROWS, but their operations are folded
	// into the type row's InstanceOperations rather than discarded: an accessor
	// whose only grants are per-object would otherwise come back with nothing at
	// all, and a caller rendering navigation from this read would hide the entry
	// to a resource the accessor was explicitly granted (bkn-studio#478).
	instanceExtra := map[string]map[string]bool{}
	typeRowIndex := map[string]int{}
	for _, g := range grouped {
		rtype, rid := splitObjectKey(g.Object)
		if q.ResourceType != "" && rtype != q.ResourceType {
			continue
		}
		if rid == "*" {
			// Type-wide row: always kept within scope; the frontend unions on it.
			typeRowIndex[rtype] = len(out)
			out = append(out, RoleGrant{Object: g.Object, Operations: g.Operations})
			continue
		}
		// Instance row: keep only ops beyond the type-wide set; drop if fully
		// covered (this is what removes the per-instance fan-out). A type-wide
		// ActAll ("*") grant covers every op on the type, so the instance row is
		// always redundant — handle it explicitly rather than relying on literal
		// op matching. (rejectWildcardGrant keeps a "type:*"/"*" grant off the
		// write paths today, so this branch is defensive.)
		tw := typeWide[rtype]
		if tw[ActAll] {
			continue
		}
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
		if q.TypeWideOnly {
			if instanceExtra[rtype] == nil {
				instanceExtra[rtype] = map[string]bool{}
			}
			for _, op := range extra {
				instanceExtra[rtype][op] = true
			}
			continue
		}
		if len(idFilter) > 0 && !idFilter[rid] {
			continue
		}
		out = append(out, RoleGrant{Object: g.Object, Operations: extra})
	}

	// Attach the folded instance operations. Sorted throughout so a payload the
	// frontend caches after login does not churn between two identical reads.
	foldedTypes := make([]string, 0, len(instanceExtra))
	for rtype := range instanceExtra {
		foldedTypes = append(foldedTypes, rtype)
	}
	sort.Strings(foldedTypes)
	for _, rtype := range foldedTypes {
		ops := make([]string, 0, len(instanceExtra[rtype]))
		for op := range instanceExtra[rtype] {
			ops = append(ops, op)
		}
		sort.Strings(ops)
		if i, ok := typeRowIndex[rtype]; ok {
			out[i].InstanceOperations = ops
			continue
		}
		// The accessor holds nothing type-wide on this type, only instances, so
		// there is no row to attach to. Emit one whose Operations is empty: the
		// id keeps the "*" shape every caller unions on, and an empty type-wide
		// set plus a non-empty instance set reads exactly as what it is — "you
		// may reach this type, but only through specific objects".
		out = append(out, RoleGrant{Object: rtype + ":*", Operations: []string{}, InstanceOperations: ops})
	}

	// Deny exceptions are additive to the legacy response shape. Keep the allow
	// rows unchanged for old clients, while newer administration clients can
	// explain why a concrete operation is absent from Check/operations/filter.
	if !superAdmin && !q.TypeWideOnly {
		byObject := make(map[string]int, len(out))
		for i := range out {
			byObject[out[i].Object] = i
		}
		for _, row := range rows {
			if policyEffect(row) != EffectDeny || len(row) < 3 {
				continue
			}
			rtype, rid := splitObjectKey(row[1])
			if q.ResourceType != "" && rtype != q.ResourceType {
				continue
			}
			if len(idFilter) > 0 && rid != "*" && !idFilter[rid] {
				continue
			}
			i, ok := byObject[row[1]]
			if !ok {
				i = len(out)
				byObject[row[1]] = i
				out = append(out, RoleGrant{Object: row[1], Operations: []string{}})
			}
			if !hasOp(out[i].DeniedOperations, row[2]) {
				out[i].DeniedOperations = append(out[i].DeniedOperations, row[2])
			}
		}
	}
	return false, out, nil
}

func (en *Enforcer) hasSuperAdminRole(accessorID string) (bool, error) {
	roles, err := en.e.GetImplicitRolesForUser(accessorID)
	if err != nil {
		return false, err
	}
	for _, role := range roles {
		if role == SuperAdminRoleID {
			return true, nil
		}
	}
	return false, nil
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
	return en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		if _, err := tx.enforcer.e.RemoveFilteredGroupingPolicy(1, roleID); err != nil {
			return err
		}
		_, err := tx.enforcer.removePolicyGrants(PolicyFilter{AccessorID: roleID})
		return err
	})
}

// RenameOperation rewrites every policy row on a resource type that grants the
// old operation so that it grants the new one instead. Returns how many rows
// moved, so a caller can log an upgrade that actually did something.
//
// Renaming an operation in the seeded vocabulary only fixes the ROLE grants —
// those are wiped and rebuilt from the seed on every start. Grants written per
// object by an administrator carry the old string in the policy store and would
// silently stop matching, which reads as "the permission I granted disappeared".
// This is the migration for those, and it is idempotent: once no row holds the
// old spelling, it does nothing.
func (en *Enforcer) RenameOperation(resourceType, oldOp, newOp string) (int, error) {
	moved := 0
	err := en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		var rows []safemodel.AuthorizationGrant
		if err := tx.db.Where("operation = ? AND object LIKE ?", oldOp, resourceType+":%").
			Order("grant_id").Find(&rows).Error; err != nil {
			return err
		}
		prefix := resourceType + ":"
		for _, row := range rows {
			if len(row.Object) <= len(prefix) || row.Object[:len(prefix)] != prefix {
				continue
			}
			source, authority := PolicySource(row.PolicySource), AuthoritySource(row.AuthoritySource)
			derivedID := row.GrantID == deterministicGrantID(row.AccessorID, row.Object, oldOp, row.Effect, source, authority)
			createdAt := row.CreatedAt
			if _, _, err := tx.enforcer.revokePolicyGrant(row.GrantID); err != nil {
				return err
			}
			grant := policyGrant(row)
			grant.Operation = newOp
			if derivedID {
				grant.GrantID = deterministicGrantID(row.AccessorID, row.Object, newOp, row.Effect, source, authority)
			}
			created, err := tx.enforcer.addPolicyGrant(grant)
			if err != nil {
				return err
			}
			if created {
				if err := tx.db.Model(&safemodel.AuthorizationGrant{}).Where("grant_id = ?", grant.GrantID).
					Update("created_at", createdAt).Error; err != nil {
					return err
				}
			}
			moved++
		}
		return nil
	})
	return moved, err
}

// BackfilledGrant names one policy row that gained an implied operation, so the
// caller can record each repair in the audit trail rather than only counting it.
type BackfilledGrant struct {
	AccessorID string
	ResourceID string
}

// BackfillImpliedOperation adds impliedOp to every policy row on a resource type
// that already grants holderOp and does not yet grant the implied one. Returns
// the rows that gained the operation, so an upgrade that did something is both
// visible in the log and recordable per grant.
//
// The rule it repairs is enforced when a grant is WRITTEN (see the grant paths
// in httpapi), which leaves grants written before the rule existed unrepaired —
// and for catalog.resource_manage that is not a cosmetic gap: a grant carrying
// only the management verb reaches nothing, because every management route
// loads its target first and that load is a view_detail judgement. Without this
// backfill the fix would apply to new grants only, and an administrator would
// have to re-save each old one without ever being told to.
//
// Idempotent: once every holder row carries the implied operation it adds
// nothing, at the cost of one filtered read per declared implication per start.
func (en *Enforcer) BackfillImpliedOperation(resourceType, holderOp, impliedOp string) ([]BackfilledGrant, error) {
	var added []BackfilledGrant
	err := en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		var rows []safemodel.AuthorizationGrant
		if err := tx.db.Where("operation = ? AND object LIKE ?", holderOp, resourceType+":%").
			Order("grant_id").Find(&rows).Error; err != nil {
			return err
		}
		prefix := resourceType + ":"
		for _, row := range rows {
			if row.Effect == EffectDeny || len(row.Object) <= len(prefix) || row.Object[:len(prefix)] != prefix {
				continue
			}
			source := PolicySource(row.PolicySource)
			// Compatibility and logical-bundle rows are immutable snapshots of their
			// own contracts. Runtime requires (#1429) handles reachability without
			// expanding legacy, and bundle expansion belongs only in the grant index.
			if source == PolicySourceLegacy || source == PolicySourceCommunityBundle {
				continue
			}
			grant := policyGrant(row)
			grant.GrantID = deterministicGrantID(row.GrantID, row.Object, impliedOp, EffectAllow,
				source, AuthoritySource(row.AuthoritySource))
			grant.Operation = impliedOp
			grant.Effect = EffectAllow
			created, err := tx.enforcer.addPolicyGrant(grant)
			if err != nil {
				return err
			}
			if created {
				added = append(added, BackfilledGrant{AccessorID: row.AccessorID, ResourceID: row.Object[len(prefix):]})
			}
		}
		return nil
	})
	return added, err
}

// RemoveRolePermissions purges only the role_permission p-lines owned by a
// role, preserving both its member bindings and independently managed policy
// sources. Seed uses this before re-applying the built-in permission matrix so
// removed seeded grants do not linger across upgrades without erasing a
// Community bundle or another resource grant assigned to the same role.
func (en *Enforcer) RemoveRolePermissions(roleID string) error {
	return en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		_, err := tx.enforcer.removePolicyGrants(PolicyFilter{
			AccessorID: roleID, PolicySource: PolicySourceRolePermission,
		})
		return err
	})
}

// RemoveAccessor purges every casbin trace of an accessor: its role bindings
// (grouping g-lines with sub=accessor) and any concrete object policies granted
// directly to it (p-lines with sub=accessor). Called when a user is deleted so
// no orphaned grants linger. Idempotent.
func (en *Enforcer) RemoveAccessor(accessorID string) error {
	return en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		if _, err := tx.enforcer.e.RemoveFilteredGroupingPolicy(0, accessorID); err != nil {
			return err
		}
		_, err := tx.enforcer.removePolicyGrants(PolicyFilter{AccessorID: accessorID})
		return err
	})
}

// RemoveResourcePolicies drops policies targeting a concrete resource instance
// after ensuring no managed proxy policy remains. Proxy policies belong
// exclusively to proxy-grant sources, and the KN deletion flow must remove
// those sources before generic resource policy cleanup.
func (en *Enforcer) RemoveResourcePolicies(resourceType, resourceID string) error {
	return en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		var activeSources int64
		if err := tx.db.Model(&safemodel.ProxyGrantSource{}).
			Where("resource_type = ? AND resource_id = ? AND lifecycle_status = ?", resourceType, resourceID, "active").
			Count(&activeSources).Error; err != nil {
			return err
		}
		if activeSources > 0 {
			return ErrManagedProxyPolicies
		}
		_, err := tx.enforcer.removePolicyGrants(PolicyFilter{Object: obj(resourceType, resourceID)})
		return err
	})
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
//
// Instances reached only through an ancestor are included too (#800). Without
// that, this read would silently under-report the moment inheritance carries a
// grant: an inherited resource holds no policy row of its own, so a list page
// built on this endpoint would lose rows that Check allows — and every caller
// keeps working unchanged instead of having to learn about the hierarchy.
func (en *Enforcer) AccessibleResources(accessorID, resourceType, op string) ([]string, error) {
	ids, err := en.accessibleResources(accessorID, resourceType, op, map[string]bool{})
	if err != nil {
		return nil, err
	}
	resources := make([]ResourceRef, 0, len(ids))
	for _, id := range ids {
		resources = append(resources, ResourceRef{Type: resourceType, ID: id})
	}
	// The candidate set is collected above from direct and inherited grants,
	// then filtered in one deny-aware batch. Calling Check for every id here
	// would turn a resource-list request into N hierarchy/proxy lookups.
	filtered, err := en.FilterResourceOps(accessorID, resources, []string{op}, nil)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(filtered))
	for _, resource := range filtered {
		out = append(out, resource.ID)
	}
	return out, nil
}

// accessibleResources is AccessibleResources plus the visited-type set that
// keeps the ancestor recursion finite.
func (en *Enforcer) accessibleResources(accessorID, resourceType, op string, visitedTypes map[string]bool) ([]string, error) {
	perms, err := en.e.GetImplicitPermissionsForUser(accessorID)
	if err != nil {
		return nil, err
	}
	perms = activePolicyRows(perms)
	prefix := resourceType + ":"
	seen := map[string]bool{}
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		if len(p) < 3 || (len(p) >= 4 && p[3] == EffectDeny) {
			continue
		}
		o, act := p[1], p[2]
		if act != op && act != ActAll && !isCommunityBundleRow(p) {
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
	inherited, err := en.inheritedResources(accessorID, resourceType, op, visitedTypes)
	if err != nil {
		return nil, err
	}
	for _, id := range inherited {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// ResourcePolicy is one accessor's grant set on a single resource instance.
type ResourcePolicy struct {
	AccessorID       string
	Operations       []string
	DeniedOperations []string
}

// ResourcePolicies lists the per-accessor grants on a concrete resource
// instance, grouping the raw (sub, obj, act) rows by accessor. Order of
// accessors follows first appearance; ops within an accessor follow row order.
// Mirrors ISF list-policy for one resource (bkn-safe has no expiry/condition).
// Allows retain the legacy Operations field; deny exceptions are additive.
func (en *Enforcer) ResourcePolicies(resourceType, resourceID string) ([]ResourcePolicy, error) {
	rows, err := en.e.GetFilteredPolicy(1, obj(resourceType, resourceID))
	if err != nil {
		return nil, err
	}
	rows = activePolicyRows(rows)
	rows = projectCommunityBundleRows(rows, false)
	bySub := map[string][]string{}
	deniedBySub := map[string][]string{}
	seenSub := map[string]bool{}
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		sub, act := row[0], row[2]
		if !seenSub[sub] {
			order = append(order, sub)
			seenSub[sub] = true
		}
		if len(row) >= 4 && row[3] == EffectDeny {
			deniedBySub[sub] = append(deniedBySub[sub], act)
		} else {
			bySub[sub] = append(bySub[sub], act)
		}
	}
	out := make([]ResourcePolicy, 0, len(order))
	for _, sub := range order {
		out = append(out, ResourcePolicy{AccessorID: sub, Operations: bySub[sub], DeniedOperations: deniedBySub[sub]})
	}
	return out, nil
}

// ObjectGrant is one accessor's grant set on one concrete resource instance:
// the cross-product cell of the object-level authorization matrix (who can do
// what on which specific object). Powers the admin authorization overview.
type ObjectGrant struct {
	AccessorID       string
	ResourceType     string
	ResourceID       string
	Operations       []string
	DeniedOperations []string
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
	rows = activePolicyRows(rows)
	rows = projectCommunityBundleRows(rows, false)
	type key struct{ sub, rtype, rid string }
	ops := map[key][]string{}
	deniedOps := map[key][]string{}
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
		effectKey := act + "\x00" + policyEffect(row)
		if seen[k][effectKey] {
			continue
		}
		seen[k][effectKey] = true
		if policyEffect(row) == EffectDeny {
			deniedOps[k] = append(deniedOps[k], act)
		} else {
			ops[k] = append(ops[k], act)
		}
	}
	out := make([]ObjectGrant, 0, len(order))
	for _, k := range order {
		out = append(out, ObjectGrant{
			AccessorID: k.sub, ResourceType: k.rtype, ResourceID: k.rid,
			Operations: ops[k], DeniedOperations: deniedOps[k],
		})
	}
	return out, nil
}

// SetObjectPermissions replaces an accessor's allow operation set on one
// concrete resource instance. Deny exceptions are managed independently by
// SetObjectPermissionsForEffect. Passing no ops clears the allow set.
func (en *Enforcer) SetObjectPermissions(accessorID, resourceType, resourceID string, ops []string) error {
	return en.SetObjectPermissionsForEffect(accessorID, resourceType, resourceID, ops, EffectAllow)
}

// SetObjectPermissionsForEffect is the compatibility whole-set writer. It only
// replaces the legacy/migration slice, preserving every independently owned
// source. The edition-aware management API migrates to
// SetProfessionalObjectPermissions in #1430.
func (en *Enforcer) SetObjectPermissionsForEffect(accessorID, resourceType, resourceID string, ops []string, effect string) error {
	if effect != EffectAllow && effect != EffectDeny {
		return fmt.Errorf("invalid policy effect %q", effect)
	}
	return en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		filter := PolicyFilter{
			AccessorID: accessorID, Object: obj(resourceType, resourceID), Effect: effect,
			PolicySource: PolicySourceLegacy, AuthoritySource: AuthoritySourceMigration,
		}
		desired := make([]PolicyGrant, 0, len(ops))
		for _, op := range ops {
			desired = append(desired, deterministicPolicyGrant(accessorID, obj(resourceType, resourceID), op,
				effect, PolicySourceLegacy, AuthoritySourceMigration))
		}
		return tx.enforcer.replacePolicyGrantSlice(filter, desired)
	})
}

func policyEffect(row []string) string {
	if len(row) >= 4 && row[3] == EffectDeny {
		return EffectDeny
	}
	return EffectAllow
}

// RemoveAccessorResourcePolicies drops every op one accessor holds on one
// concrete resource instance (revoke a single grantee's grant), leaving other
// accessors' grants on the same resource intact — unlike RemoveResourcePolicies,
// which wipes the resource for everyone on delete. Idempotent.
//
// Returns how many p-lines were removed, so a caller can tell an effective
// revoke from a no-op one (a request naming a grant that does not exist).
// Nothing about the outcome changes with the count — the operation is idempotent
// either way — but the audit trail needs the distinction: "revoked 3 ops" and
// "matched nothing" are different administrative facts.
func (en *Enforcer) RemoveAccessorResourcePolicies(accessorID, resourceType, resourceID string) (int, error) {
	removed := 0
	err := en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		var err error
		removed, err = tx.enforcer.removePolicyGrants(PolicyFilter{
			AccessorID: accessorID, Object: obj(resourceType, resourceID),
		})
		return err
	})
	return removed, err
}

// RemoveAccessorResourcePoliciesForEffect removes only allow or deny rows,
// allowing an administrator to clear an exception without disturbing grants.
func (en *Enforcer) RemoveAccessorResourcePoliciesForEffect(accessorID, resourceType, resourceID, effect string) (int, error) {
	if effect != EffectAllow && effect != EffectDeny {
		return 0, fmt.Errorf("invalid policy effect %q", effect)
	}
	removed := 0
	err := en.Transaction(context.Background(), func(tx *PolicyTransaction) error {
		var err error
		removed, err = tx.enforcer.removePolicyGrants(PolicyFilter{
			AccessorID: accessorID, Object: obj(resourceType, resourceID), Effect: effect,
		})
		return err
	})
	return removed, err
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
