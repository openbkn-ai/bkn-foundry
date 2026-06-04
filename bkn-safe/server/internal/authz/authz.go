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

// RemoveResourcePolicies drops every policy targeting a concrete resource
// instance (used when a resource is deleted).
func (en *Enforcer) RemoveResourcePolicies(resourceType, resourceID string) error {
	_, err := en.e.RemoveFilteredPolicy(1, obj(resourceType, resourceID))
	return err
}
