// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
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

var errModelAuthorizationManagedBySystem = fmt.Errorf("%w: model authorization is managed by the platform", adminwrite.ErrForbidden)

type roleNameConflictError struct{ existingID string }

func (e *roleNameConflictError) Error() string          { return adminwrite.ErrRoleNameExisted.Error() }
func (e *roleNameConflictError) Unwrap() error          { return adminwrite.ErrRoleNameExisted }
func (e *roleNameConflictError) ExistingRoleID() string { return e.existingID }

func normalizeRoleName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
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
	name := strings.TrimSpace(spec.Name)
	nameKey := normalizeRoleName(name)
	id := spec.ID
	if id == "" {
		id = auth.NewID()
	}
	role := model.Role{
		ID: id, Name: name, NameKey: &nameKey, Description: spec.Description,
		Source: model.RoleSourceCustom,
	}
	err := s.e.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		if err := rejectRoleNameConflict(ctx, tx.DB(), nameKey, ""); err != nil {
			return err
		}
		if err := tx.DB().Create(&role).Error; err != nil {
			return s.translateRoleNameUniqueError(ctx, nameKey, "", err)
		}
		return enqueueRoleAudit(ctx, tx.DB(), role.ID, role.Name, http.StatusCreated)
	})
	if err != nil {
		return "", err
	}
	markRoleAuditHandled(ctx)
	return role.ID, nil
}

// UpdateRole renames/re-describes a custom role. Built-ins are immutable.
func (s *adminWriteServices) UpdateRole(ctx context.Context, id string, patch adminwrite.RolePatch) error {
	var role *model.Role
	err := s.e.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		var err error
		role, err = loadCustomRole(ctx, tx.DB(), id)
		if err != nil {
			return err
		}
		fields := map[string]any{}
		if patch.Name != nil {
			name := strings.TrimSpace(*patch.Name)
			nameKey := normalizeRoleName(name)
			if err := rejectRoleNameConflict(ctx, tx.DB(), nameKey, role.ID); err != nil {
				return err
			}
			fields["name"] = name
			fields["name_key"] = nameKey
			role.Name = name
		}
		if patch.Description != nil {
			fields["description"] = *patch.Description
		}
		if len(fields) == 0 {
			return adminwrite.ErrNoUpdatableFields
		}
		if err := tx.DB().Model(&model.Role{}).Where("id = ?", role.ID).Updates(fields).Error; err != nil {
			if patch.Name != nil {
				return s.translateRoleNameUniqueError(ctx, normalizeRoleName(*patch.Name), role.ID, err)
			}
			return err
		}
		return enqueueRoleAudit(ctx, tx.DB(), role.ID, role.Name, http.StatusNoContent)
	})
	if err == nil {
		markRoleAuditHandled(ctx)
	}
	return err
}

// rejectRoleNameConflict covers both migrated roles (name_key) and legacy rows
// whose nullable key has not yet been safely backfilled.
func (s *adminWriteServices) rejectRoleNameConflict(ctx context.Context, nameKey, excludeID string) error {
	return rejectRoleNameConflict(ctx, s.db, nameKey, excludeID)
}

func rejectRoleNameConflict(ctx context.Context, db *gorm.DB, nameKey, excludeID string) error {
	var role model.Role
	query := db.WithContext(ctx).Where("(name_key = ? OR (name_key IS NULL AND LOWER(TRIM(name)) = ?))", nameKey, nameKey)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	err := query.First(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return &roleNameConflictError{existingID: role.ID}
}

func (s *adminWriteServices) translateRoleNameUniqueError(ctx context.Context, nameKey, excludeID string, err error) error {
	if err == nil {
		return nil
	}
	// The unique index is the concurrency guarantee. Re-querying also returns
	// the winning role ID without exposing a database-specific duplicate error.
	if conflict := s.rejectRoleNameConflict(ctx, nameKey, excludeID); conflict != nil {
		return conflict
	}
	return err
}

// DeleteRole deletes a custom role and purges its casbin bindings and grants.
func (s *adminWriteServices) DeleteRole(ctx context.Context, id string) error {
	err := s.e.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		role, err := loadCustomRole(ctx, tx.DB(), id)
		if err != nil {
			return err
		}
		if err := tx.DB().Delete(&model.Role{}, "id = ?", role.ID).Error; err != nil {
			return err
		}
		if err := tx.RemoveRoleCompletely(role.ID); err != nil {
			return err
		}
		return enqueueRoleAudit(ctx, tx.DB(), role.ID, role.Name, http.StatusNoContent)
	})
	if err == nil {
		markRoleAuditHandled(ctx)
	}
	return err
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
	if isModelAuthorizationResourceType(resourceType) {
		return errModelAuthorizationManagedBySystem
	}
	// A role granted resource_manage gets its required view_detail (#1121): the
	// management routes load their target first, so without it the role holds a
	// verb it can never reach.
	ops, err := s.e.NormalizeOperations(ctx, resourceType, []string{op})
	if err != nil {
		return err
	}
	if err := s.e.ValidateGrantableOperations(ctx, resourceType, ops); err != nil {
		if errors.Is(err, authz.ErrOperationNotGrantable) {
			return fmt.Errorf("%w: operation is not grantable", adminwrite.ErrInvalid)
		}
		return err
	}
	err = s.e.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		if err := tx.GrantNormalizedRolePermissions(role.ID, resourceType, resourceID, ops); err != nil {
			return err
		}
		return enqueueRoleAudit(ctx, tx.DB(), role.ID, role.Name, http.StatusNoContent)
	})
	if err == nil {
		markRoleAuditHandled(ctx)
	}
	return err
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
// An operation stays if something the role is LEFT WITH requires it (#1121):
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
// the next revoke is honoured because nothing left requires view_detail.
func (s *adminWriteServices) RevokeRolePermissions(ctx context.Context,
	roleID, resourceType, resourceID string, ops []string) error {
	if len(ops) == 0 {
		return nil
	}
	err := s.e.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		role, err := loadCustomRole(ctx, tx.DB(), roleID)
		if err != nil {
			return err
		}
		held, err := roleOperationsOn(tx, role.ID, resourceType, resourceID)
		if err != nil {
			return err
		}
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
		requiringByOperation, err := tx.RequiringOperationsByRequirement(ctx, resourceType, ops)
		if err != nil {
			return err
		}
		for _, op := range ops {
			retainedBy := ""
			for _, other := range requiringByOperation[op] {
				if remaining[other] {
					retainedBy = other
					break
				}
			}
			if retainedBy != "" {
				slog.Info("kept an operation another retained role operation requires",
					"role_id", role.ID, "resource_type", resourceType, "resource_id", resourceID,
					"operation", op, "required_by", retainedBy)
				continue
			}
			if err := tx.RevokeRolePermission(role.ID, resourceType, resourceID, op); err != nil {
				return err
			}
		}
		return enqueueRoleAudit(ctx, tx.DB(), role.ID, role.Name, http.StatusNoContent)
	})
	if err == nil {
		markRoleAuditHandled(ctx)
	}
	return err
}

// roleOperationsOn returns the operations the role holds on exactly one
// resource pattern, as a set.
func (s *adminWriteServices) roleOperationsOn(roleID, resourceType, resourceID string) (map[string]bool, error) {
	grants, err := s.e.RolePermissions(roleID)
	return roleOperationsFromGrants(grants, err, resourceType, resourceID)
}

func roleOperationsOn(tx *authz.PolicyTransaction, roleID, resourceType, resourceID string) (map[string]bool, error) {
	grants, err := tx.RolePermissions(roleID)
	return roleOperationsFromGrants(grants, err, resourceType, resourceID)
}

func roleOperationsFromGrants(grants []authz.RoleGrant, err error, resourceType, resourceID string) (map[string]bool, error) {
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
	role, err := loadCustomRole(ctx, s.db, id)
	return role, err
}

func loadCustomRole(ctx context.Context, db *gorm.DB, id string) (*model.Role, error) {
	var role model.Role
	err := db.WithContext(ctx).First(&role, "id = ?", id).Error
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

func enqueueRoleAudit(ctx context.Context, tx *gorm.DB, targetID, targetName string, status int) error {
	operation, ok := audit.RequestOperationFromContext(ctx)
	if !ok {
		return nil
	}
	return operation.Enqueue(tx, targetID, targetName, status)
}

func markRoleAuditHandled(ctx context.Context) {
	if operation, ok := audit.RequestOperationFromContext(ctx); ok {
		operation.MarkHandled()
	}
}
