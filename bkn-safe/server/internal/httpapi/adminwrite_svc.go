// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// adminWriteServices is core's implementation of adminwrite.Services. It is the
// place the rbac_basic write guards live, so they hold regardless of what the
// ee HTTP layer does: a wrong or malicious ee handler still cannot mint a
// system role, escape the built-in immutability, hand out a wildcard grant, or
// confer the admin-console capability. ee owns the HTTP shape; the rules are
// here, next to the engine.
type adminWriteServices struct {
	e  *authz.Enforcer
	db *gorm.DB
}

// newAdminWriteServices builds the core service surface passed to the ee
// write-route mounter.
func newAdminWriteServices(e *authz.Enforcer, db *gorm.DB) adminwrite.Services {
	return &adminWriteServices{e: e, db: db}
}

// RequirePermission hands ee core's RBAC middleware, so a write route keeps the
// same per-caller check its community read sibling uses.
//
// This is the RBAC layer only. The entitlement layer is the socket's — it runs
// ahead of this one and answers 404, because a missing licence has to look like
// a route that does not exist while a missing permission is refused outright.
// ee adds neither; both are core's.
func (s *adminWriteServices) RequirePermission(resourceType, op string) gin.HandlerFunc {
	return RequirePermission(s.e, resourceType, op)
}

// RequireAnyPermission implements adminwrite.AnyPermissionRequirer: the same RBAC
// middleware, for a route that accepts more than one permission point (a renamed
// point plus the one it superseded).
func (s *adminWriteServices) RequireAnyPermission(points ...adminwrite.PermissionPoint) gin.HandlerFunc {
	corePoints := make([]PermissionPoint, 0, len(points))
	for _, p := range points {
		corePoints = append(corePoints, PermissionPoint{ResourceType: p.ResourceType, Op: p.Op})
	}
	return RequireAnyPermission(s.e, corePoints...)
}

// CreateRole creates a custom role. Source is forced to custom — the API can
// never mint a system or business role, whichever id or name is asked for.
func (s *adminWriteServices) CreateRole(ctx context.Context, spec adminwrite.RoleSpec) (string, error) {
	id := spec.ID
	if id == "" {
		id = auth.NewID()
	}
	role := model.Role{
		ID: id, Name: spec.Name, Description: spec.Description,
		Source: model.RoleSourceCustom,
	}
	if err := s.db.WithContext(ctx).Create(&role).Error; err != nil {
		return "", err
	}
	return role.ID, nil
}

// UpdateRole renames/re-describes a custom role. Built-ins are immutable.
func (s *adminWriteServices) UpdateRole(ctx context.Context, id string, patch adminwrite.RolePatch) error {
	role, err := s.loadCustomRole(ctx, id)
	if err != nil {
		return err
	}
	fields := map[string]any{}
	if patch.Name != nil {
		fields["name"] = *patch.Name
	}
	if patch.Description != nil {
		fields["description"] = *patch.Description
	}
	if len(fields) == 0 {
		return adminwrite.ErrNoUpdatableFields
	}
	return s.db.WithContext(ctx).Model(&model.Role{}).Where("id = ?", role.ID).Updates(fields).Error
}

// DeleteRole deletes a custom role and purges its casbin bindings and grants.
func (s *adminWriteServices) DeleteRole(ctx context.Context, id string) error {
	role, err := s.loadCustomRole(ctx, id)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Delete(&model.Role{}, "id = ?", role.ID).Error; err != nil {
		return err
	}
	return s.e.RemoveRoleCompletely(role.ID)
}

// GrantRolePermission grants a custom role an op over a resource pattern. It
// refuses the two wildcard forms that would make the policy match everything,
// and refuses the admin-console capability outright: administrative capability
// comes from a seeded role binding, never from a grant handed out at runtime,
// or this route becomes an admin-promotion route.
func (s *adminWriteServices) GrantRolePermission(ctx context.Context, roleID, resourceType, resourceID, op string) error {
	role, err := s.loadCustomRole(ctx, roleID)
	if err != nil {
		return err
	}
	if err := rejectWildcardGrant(resourceType, []string{op}); err != nil {
		return fmt.Errorf("%w: %s", adminwrite.ErrWildcardGrant, err.Error())
	}
	if err := rejectTypeWideActionExecute(resourceType, resourceID, []string{op}); err != nil {
		return fmt.Errorf("%w: %s", adminwrite.ErrTypeWideActionExecute, err.Error())
	}
	if resourceType == adminConsoleResourceType {
		return adminwrite.ErrAdminConsolePermission
	}
	// A role granted resource_manage gets view_detail with it (#1121): the
	// management routes load their target first, so without it the role holds a
	// verb it can never reach.
	ops, err := impliedOps(s.db.WithContext(ctx), resourceType, []string{op})
	if err != nil {
		return err
	}
	for _, granted := range ops {
		if err := s.e.GrantRolePermission(role.ID, resourceType, resourceID, granted); err != nil {
			return err
		}
	}
	return nil
}

// RevokeRolePermission revokes one operation.
//
// Deprecated: use RevokeRolePermissions. One operation at a time cannot see the
// rest of the caller's set, so the outcome of a multi-operation revoke depends
// on the order — see RevokeRolePermissions.
func (s *adminWriteServices) RevokeRolePermission(ctx context.Context, roleID, resourceType, resourceID, op string) error {
	return s.RevokeRolePermissions(ctx, roleID, resourceType, resourceID, []string{op})
}

// RevokeRolePermissions revokes a set of operations from a custom role over one
// resource pattern.
//
// An operation stays if something the role is LEFT WITH implies it (#1121):
// dropping view_detail while resource_manage remains would leave a verb whose
// every route answers 403, the grant this rule exists to prevent. Retaining is
// also what the whole-set object-grant surface does for the same edit, so the
// two surfaces agree.
//
// "Left with" is the whole point, and it is why this takes a set. Judging one
// operation at a time against the live state makes the answer depend on the
// order the caller listed them — revoking [view_detail, resource_manage] would
// keep view_detail because resource_manage was still there when view_detail was
// considered, while the reverse order would drop both. The remainder is
// computed once, before anything is removed, so both orders drop both.
//
// Dropping view_detail alone stays reachable: revoke resource_manage first, and
// the next revoke is honoured because nothing left implies view_detail.
func (s *adminWriteServices) RevokeRolePermissions(ctx context.Context,
	roleID, resourceType, resourceID string, ops []string) error {

	role, err := s.loadCustomRole(ctx, roleID)
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return nil
	}
	held, err := s.roleOperationsOn(role.ID, resourceType, resourceID)
	if err != nil {
		return err
	}
	// What the role keeps once this request is applied — computed before the
	// first removal so no operation's verdict can depend on another's.
	remaining := map[string]bool{}
	requested := make(map[string]bool, len(ops))
	for _, op := range ops {
		requested[op] = true
	}
	for op := range held {
		if !requested[op] {
			remaining[op] = true
		}
	}

	for _, op := range ops {
		implying, err := impliedBy(s.db.WithContext(ctx), resourceType, []string{op})
		if err != nil {
			return err
		}
		retainedBy := ""
		for _, other := range implying {
			if other != op && remaining[other] {
				retainedBy = other
				break
			}
		}
		if retainedBy != "" {
			slog.Info("kept an operation the role still implies",
				"role_id", role.ID, "resource_type", resourceType, "resource_id", resourceID,
				"operation", op, "implied_by", retainedBy)
			continue
		}
		if err := s.e.RevokeRolePermission(role.ID, resourceType, resourceID, op); err != nil {
			return err
		}
	}
	return nil
}

// roleOperationsOn returns the operations the role holds on exactly one
// resource pattern, as a set.
func (s *adminWriteServices) roleOperationsOn(roleID, resourceType, resourceID string) (map[string]bool, error) {
	grants, err := s.e.RolePermissions(roleID)
	if err != nil {
		return nil, err
	}
	want := resourceType + ":" + resourceID
	out := map[string]bool{}
	for _, g := range grants {
		if g.Object != want {
			continue
		}
		for _, op := range g.Operations {
			out[op] = true
		}
	}
	return out, nil
}

// loadCustomRole fetches a role and rejects built-ins. It maps to the
// adminwrite sentinels so ee need not know the model.
func (s *adminWriteServices) loadCustomRole(ctx context.Context, id string) (*model.Role, error) {
	var role model.Role
	err := s.db.WithContext(ctx).First(&role, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, adminwrite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if role.BuiltIn() {
		return nil, adminwrite.ErrImmutable
	}
	return &role, nil
}
