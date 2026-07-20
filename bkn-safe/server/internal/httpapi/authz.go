// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/model"
)

var threeAdminRoleIDs = []string{
	"d2bd2082-ad03-11e8-aa06-000c29358ad6", // admin
	"d8998f72-ad03-11e8-aa06-000c29358ad6", // security
	"def246f2-ad03-11e8-aa06-000c29358ad6", // audit
}

// resourceRef is the clean { type, id } object reference used across the authz API.
type resourceRef struct {
	Type string `json:"type" binding:"required"`
	ID   string `json:"id"`
}

// registerAuthz mounts bkn-safe's clean authorization API under /api/safe/v1/authz.
// This is a redesign — it deliberately drops ISF's quirks (GET-in-body,
// array-vs-map responses, policy-delete double form, public/private split).
func registerAuthz(r *gin.Engine, e *authz.Enforcer, db *gorm.DB) {
	g := r.Group("/api/safe/v1/authz")

	// POST /check — single decision. { accessor_id, resource{type,id}, operation } -> { allowed }
	g.POST("/check", func(c *gin.Context) {
		var req struct {
			AccessorID string      `json:"accessor_id" binding:"required"`
			Resource   resourceRef `json:"resource" binding:"required"`
			Operation  string      `json:"operation" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		ok, err := e.Check(req.AccessorID, req.Resource.Type, req.Resource.ID, req.Operation)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"allowed": ok})
	})

	// POST /operations — which ops the accessor may perform on a resource.
	// Candidate ops come from the resource type's catalog. -> { operations:[...] }
	g.POST("/operations", func(c *gin.Context) {
		var req struct {
			AccessorID string      `json:"accessor_id" binding:"required"`
			Resource   resourceRef `json:"resource" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		candidates, err := catalogOps(db, req.Resource.Type)
		if err != nil {
			serverError(c, err)
			return
		}
		allowed, err := e.AllowedOps(req.AccessorID, req.Resource.Type, req.Resource.ID, candidates)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"operations": allowed})
	})

	// POST /policies — grant an accessor concrete ops on one resource instance
	// (the create-resource pattern). { accessor_id, resource, operations:[...] }
	g.POST("/policies", func(c *gin.Context) {
		var req struct {
			AccessorID string      `json:"accessor_id" binding:"required"`
			Resource   resourceRef `json:"resource" binding:"required"`
			Operations []string    `json:"operations" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		// This route is tokenless by design (service-to-service, ClusterIP), so
		// input validation is the only thing standing between a caller and an
		// arbitrary policy row. It is staged deliberately — see policyGuard.
		if err := rejectWildcardGrant(req.Resource.Type, req.Operations); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		auditPolicyWriteShape(c, db, "POST", req.AccessorID, req.Resource, req.Operations)
		for _, op := range req.Operations {
			if err := e.GrantObjectPermission(req.AccessorID, req.Resource.Type, req.Resource.ID, op); err != nil {
				serverError(c, err)
				return
			}
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /policies — drop all policies targeting a resource instance
	// (used when the resource is deleted). { resource{type,id} }
	g.DELETE("/policies", func(c *gin.Context) {
		var req struct {
			Resource resourceRef `json:"resource" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		// A wildcard type here would drop every policy in the system — an
		// authorization teardown, not a resource cleanup.
		if err := rejectWildcardGrant(req.Resource.Type, nil); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		auditPolicyWriteShape(c, db, "DELETE", "", req.Resource, nil)
		if err := e.RemoveResourcePolicies(req.Resource.Type, req.Resource.ID); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// GET /resources — enumerate the concrete resource-instance IDs of a type
	// that the accessor may perform op on (incl. role-inherited grants).
	// Query: ?accessor_id=u1&resource_type=agent&operation=use
	// -> { ids:[...] }. Type-wide ("*") grants are excluded; callers handle the
	// is-admin case separately. (Generic accessor→instances enumeration.)
	g.GET("/resources", func(c *gin.Context) {
		accessorID := c.Query("accessor_id")
		rtype := c.Query("resource_type")
		op := c.Query("operation")
		if accessorID == "" || rtype == "" || op == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "accessor_id, resource_type, operation required"})
			return
		}
		ids, err := e.AccessibleResources(accessorID, rtype, op)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ids": ids})
	})

	// GET /policies — list the per-accessor grants on a resource instance.
	// Query: ?resource_type=agent&resource_id=a1
	// -> { entries:[ { accessor_id, resource{type,id}, operations:[...] } ] }
	// Used by DA's ListPolicy/ListPolicyAll (who-can-do-what on a resource).
	g.GET("/policies", func(c *gin.Context) {
		rtype := c.Query("resource_type")
		rid := c.Query("resource_id")
		if rtype == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "resource_type required"})
			return
		}
		policies, err := e.ResourcePolicies(rtype, rid)
		if err != nil {
			serverError(c, err)
			return
		}
		entries := make([]gin.H, 0, len(policies))
		for _, p := range policies {
			entries = append(entries, gin.H{
				"accessor_id": p.AccessorID,
				"resource":    gin.H{"type": rtype, "id": rid},
				"operations":  p.Operations,
			})
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries})
	})

}

// registerRoleBindings mounts the accessor↔role binding endpoints (bind / list /
// unbind). Admin-only — mounted under the /admin group behind RequireAdmin.
func registerRoleBindings(g *gin.RouterGroup, e *authz.Enforcer, db *gorm.DB) {
	// POST /role-bindings — bind an accessor to a role. { accessor_id, role_id }
	// Both ids must reference existing rows: casbin stores the strings verbatim,
	// so a typo'd accessor (e.g. an account name instead of its ID) would 204
	// into a grant that never matches at enforce time.
	g.POST("/role-bindings", RequirePermission(e, "admin-role", "members"), func(c *gin.Context) {
		var req struct {
			AccessorID string `json:"accessor_id" binding:"required"`
			RoleID     string `json:"role_id" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		ok, err := accessorExists(c, db, req.AccessorID)
		if err != nil {
			serverError(c, err)
			return
		}
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "accessor_id does not match any user, department or group id: " + req.AccessorID})
			return
		}
		var n int64
		if err := db.WithContext(c.Request.Context()).Model(&model.Role{}).
			Where("id = ?", req.RoleID).Count(&n).Error; err != nil {
			serverError(c, err)
			return
		}
		if n == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "role_id does not match any role id: " + req.RoleID})
			return
		}
		// Escalation guards. The permission gate on this route (admin-role:members)
		// is held by `security` as well as `admin`, and neither the three-admin
		// mutual-exclusion check below nor anything else stopped a holder from
		// naming ITSELF as the grantee of a strictly more privileged role. Two
		// narrow blocks close that without touching ordinary role assignment:
		//
		//  1. nobody may bind super_admin unless they already hold it — otherwise
		//     the wildcard grant is one request away for any role-manager;
		//  2. nobody may bind a seeded system role to themselves — self-promotion
		//     always goes through another administrator.
		//
		// Business/custom roles are untouched: assigning those to anyone,
		// including oneself, keeps working exactly as before.
		caller := c.GetString(ctxAccessorID)
		isSuper, err := isSuperAdminRoleID(c, db, req.RoleID)
		if err != nil {
			serverError(c, err)
			return
		}
		if isSuper {
			c.JSON(http.StatusForbidden, gin.H{"error": superAdminSeedOnlyMsg})
			return
		}
		if caller != "" && caller == req.AccessorID {
			role, err := roleByID(c, db, req.RoleID)
			if err != nil {
				serverError(c, err)
				return
			}
			if role != nil && role.Source == model.RoleSourceSystem {
				c.JSON(http.StatusForbidden, gin.H{"error": "a system role cannot be bound to yourself; ask another administrator"})
				return
			}
		}
		if isThreeAdminRoleID(req.RoleID) {
			currentRoleIDs, err := e.RolesForAccessor(req.AccessorID)
			if err != nil {
				serverError(c, err)
				return
			}
			for _, currentRoleID := range currentRoleIDs {
				if currentRoleID != req.RoleID && isThreeAdminRoleID(currentRoleID) {
					c.JSON(http.StatusConflict, gin.H{"error": "three-admin roles are mutually exclusive for one accessor"})
					return
				}
			}
		}
		if err := e.AssignRole(req.AccessorID, req.RoleID); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// GET /role-bindings?accessor_id= — list the role ids bound to an accessor.
	// -> { role_ids:[...] }. Mirrors ISF accessor_roles (roles-of-user read).
	g.GET("/role-bindings", RequirePermission(e, "admin-role", "view"), func(c *gin.Context) {
		accessorID := c.Query("accessor_id")
		if accessorID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "accessor_id required"})
			return
		}
		roleIDs, err := e.RolesForAccessor(accessorID)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"role_ids": roleIDs})
	})

	// DELETE /role-bindings — unbind an accessor from a role (inverse of POST).
	// { accessor_id, role_id }
	g.DELETE("/role-bindings", RequirePermission(e, "admin-role", "members"), func(c *gin.Context) {
		var req struct {
			AccessorID string `json:"accessor_id" binding:"required"`
			RoleID     string `json:"role_id" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		// Unbinding is blocked for the same reason binding is: with no API path
		// to grant the role back, a removal would strip the platform of its only
		// wildcard authority until a restart re-ran the seed.
		isSuper, err := isSuperAdminRoleID(c, db, req.RoleID)
		if err != nil {
			serverError(c, err)
			return
		}
		if isSuper {
			c.JSON(http.StatusForbidden, gin.H{"error": superAdminSeedOnlyMsg})
			return
		}
		if err := e.RemoveRole(req.AccessorID, req.RoleID); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func isThreeAdminRoleID(roleID string) bool {
	return slices.Contains(threeAdminRoleIDs, roleID)
}

// superAdminRoleName is the seeded role holding the platform-wide wildcard
// grant (seed/data/roles.json). It is deliberately NOT part of
// threeAdminRoleIDs: the three-admin rule is a separation-of-duties constraint
// among admin/security/audit, while super_admin is a privilege ceiling. Adding
// it to that list would make it mutually exclusive with the three, which the
// seeded built-in admin (bound to super_admin) relies on not being the case.
const superAdminRoleName = "super_admin"

// policyGuard — why the tokenless /authz/policies validation is staged.
//
// The route writes casbin rows on behalf of every service that creates a
// resource (12 call sites across vega, bkn, the execution factory and the model
// factory), and bkn-safe ships independently of all of them, so there is no
// window in which a stricter contract could be turned on atomically. The
// staging is therefore:
//
//	stage 1 (here)  reject only the shapes no legitimate caller sends — a
//	                wildcard resource type or a wildcard operation, both of
//	                which produce a policy matching every object in the system.
//	                Everything else is recorded, not refused.
//	stage 2         require a service credential, accepted-but-logged at first,
//	                so the un-migrated callers can be enumerated (#333).
//	stage 3         flip both the credential and the shape findings below to
//	                hard failures once the log is quiet.
//
// Deliberately NOT rejected in stage 1, despite looking wrong:
//   - the all-zero "public" accessor — the execution factory grants built-in
//     components to it on purpose (CreateIntCompPolicyForAllUsers);
//   - a resource type absent from the seed catalog — vega owns two local-only
//     types (internal_catalog, internal_resource) that were never registered;
//   - an empty operation list — the execution factory sends one when a request
//     carries no allow set, and it is a harmless no-op today;
//   - a wildcard resource ID — bounded to one type, and cheap to keep working.
func auditPolicyWriteShape(c *gin.Context, db *gorm.DB, verb, accessorID string, resource resourceRef, operations []string) {
	var findings []string
	if accessorID != "" {
		if ok, err := accessorExists(c, db, accessorID); err == nil && !ok && accessorID != authz.PublicAccessorID {
			findings = append(findings, "unknown accessor")
		}
	}
	if resource.ID == "*" {
		findings = append(findings, "wildcard resource id")
	}
	if valid, err := catalogOpSet(db, resource.Type); err == nil {
		if len(valid) == 0 {
			findings = append(findings, "resource type not in catalog")
		} else {
			for _, op := range operations {
				if !valid[op] {
					findings = append(findings, "operation not registered for type: "+op)
				}
			}
		}
	}
	if len(findings) == 0 {
		return
	}
	// Shadow only: this is the inventory that decides when stage 3 can land.
	slog.WarnContext(c.Request.Context(), "authz policy write with a shape that a stricter contract would reject",
		"verb", verb, "accessor_id", accessorID, "resource_type", resource.Type, "resource_id", resource.ID,
		"operations", operations, "findings", findings, "client_ip", c.ClientIP())
}

// adminConsoleResourceType is the resource type whose grant opens the admin
// surface (CanAdmin checks safe_admin:console:manage). It must only ever be
// conferred by binding an administrative role, never by a one-off object grant
// — otherwise the object-grant route becomes an admin-promotion route.
const adminConsoleResourceType = "safe_admin"

// rejectWildcardGrant blocks the two wildcard forms that make a policy match
// everything. Callers that legitimately grant a whole resource type pass a
// concrete type with id "*", which is unaffected.
func rejectWildcardGrant(resourceType string, operations []string) error {
	if resourceType == "*" {
		return errors.New(`resource.type must be a concrete type (not "*")`)
	}
	for _, op := range operations {
		if op == "*" {
			return errors.New(`operation must be a concrete operation (not "*")`)
		}
	}
	return nil
}

// roleByID loads a role without writing an HTTP response; nil means not found.
// (loadRole is the handler-facing variant that answers 404 itself.)
func roleByID(c *gin.Context, db *gorm.DB, id string) (*model.Role, error) {
	var role model.Role
	err := db.WithContext(c.Request.Context()).First(&role, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &role, nil
}

// isSuperAdminRoleID reports whether roleID is the seeded super_admin role.
// Resolved by name against the roles table rather than hardcoded, so it stays
// correct if the seed UUID ever changes.
func isSuperAdminRoleID(c *gin.Context, db *gorm.DB, roleID string) (bool, error) {
	var n int64
	if err := db.WithContext(c.Request.Context()).Model(&model.Role{}).
		Where("id = ? AND name = ?", roleID, superAdminRoleName).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// superAdminSeedOnlyMsg explains why the super_admin membership is closed to
// the API. The role is a singleton held by exactly one accessor, fixed by
// seed/data/role-bindings.json: no API caller may add a holder (not even the
// current one — there is no succession through this surface), and no API caller
// may remove one, because after a removal nothing could put it back and the
// platform would be left with no wildcard authority until a restart re-seeded
// it. Changing the holder is a deliberate, out-of-band operation.
const superAdminSeedOnlyMsg = "super_admin membership is fixed by the seed and cannot be changed through the API"

// registerRoles mounts the role catalog endpoints (admin-only, under /admin).
// Built-in (system/business) roles are read-only — their UUIDs are hardcoded in
// DA/flow-automation and their permission matrix is owned by the seed files.
// Custom roles (source=custom) are fully manageable at runtime.
func registerRoles(g *gin.RouterGroup, e *authz.Enforcer, db *gorm.DB) {
	// GET /roles?source= — list roles, optionally filtered by source.
	// -> { roles:[ {id,name,description,source} ] }
	g.GET("/roles", RequirePermission(e, "admin-role", "view"), func(c *gin.Context) {
		q := db.WithContext(c.Request.Context()).Model(&model.Role{})
		if src := c.Query("source"); src != "" {
			q = q.Where("source = ?", src)
		}
		var roles []model.Role
		if err := q.Order("created_at").Find(&roles).Error; err != nil {
			serverError(c, err)
			return
		}
		out := make([]gin.H, 0, len(roles))
		for _, r := range roles {
			out = append(out, roleJSON(r))
		}
		c.JSON(http.StatusOK, gin.H{"roles": out})
	})

	// GET /roles/:id — role detail with its members and permission grants.
	g.GET("/roles/:id", RequirePermission(e, "admin-role", "view"), func(c *gin.Context) {
		role, err := loadRole(c, db, c.Param("id"))
		if role == nil {
			return // loadRole already wrote the response
		}
		_ = err
		members, err := e.RoleMembers(role.ID)
		if err != nil {
			serverError(c, err)
			return
		}
		grants, err := e.RolePermissions(role.ID)
		if err != nil {
			serverError(c, err)
			return
		}
		body := roleJSON(*role)
		body["members"] = members
		body["permissions"] = grantsJSON(grants)
		c.JSON(http.StatusOK, body)
	})

	// GET /roles/:id/members — accessor ids bound to the role. -> { accessor_ids:[...] }
	g.GET("/roles/:id/members", RequirePermission(e, "admin-role", "view"), func(c *gin.Context) {
		role, _ := loadRole(c, db, c.Param("id"))
		if role == nil {
			return
		}
		members, err := e.RoleMembers(role.ID)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"accessor_ids": members})
	})

	// POST /roles — create a custom role. source is forced to "custom" (the API
	// cannot mint system/business roles). { id?, name, description? } -> { id }
	g.POST("/roles", RequirePermission(e, "admin-role", "create"), func(c *gin.Context) {
		var req struct {
			ID          string `json:"id"`
			Name        string `json:"name" binding:"required"`
			Description string `json:"description"`
		}
		if !bind(c, &req) {
			return
		}
		if req.ID == "" {
			req.ID = auth.NewID()
		}
		role := model.Role{
			ID: req.ID, Name: req.Name, Description: req.Description,
			Source: model.RoleSourceCustom,
		}
		if err := db.WithContext(c.Request.Context()).Create(&role).Error; err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"id": role.ID})
	})

	// PUT /roles/:id — rename / re-describe a CUSTOM role. Built-in roles are
	// rejected with 403. { name?, description? }
	g.PUT("/roles/:id", RequirePermission(e, "admin-role", "edit"), func(c *gin.Context) {
		role, _ := loadRole(c, db, c.Param("id"))
		if role == nil {
			return
		}
		if role.BuiltIn() {
			c.JSON(http.StatusForbidden, gin.H{"error": "built-in role is immutable"})
			return
		}
		var req struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if !bind(c, &req) {
			return
		}
		fields := map[string]any{}
		if req.Name != nil {
			fields["name"] = *req.Name
		}
		if req.Description != nil {
			fields["description"] = *req.Description
		}
		if len(fields) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no updatable fields provided"})
			return
		}
		if err := db.WithContext(c.Request.Context()).Model(&model.Role{}).
			Where("id = ?", role.ID).Updates(fields).Error; err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /roles/:id — delete a CUSTOM role and purge its casbin bindings and
	// permission grants. Built-in roles are rejected with 403.
	g.DELETE("/roles/:id", RequirePermission(e, "admin-role", "delete"), func(c *gin.Context) {
		role, _ := loadRole(c, db, c.Param("id"))
		if role == nil {
			return
		}
		if role.BuiltIn() {
			c.JSON(http.StatusForbidden, gin.H{"error": "built-in role is immutable"})
			return
		}
		if err := db.WithContext(c.Request.Context()).Delete(&model.Role{}, "id = ?", role.ID).Error; err != nil {
			serverError(c, err)
			return
		}
		if err := e.RemoveRoleCompletely(role.ID); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// POST /roles/:id/permissions — grant a CUSTOM role an op over a resource
	// pattern (id "*" = whole type). { resource{type,id}, operations:[...] }
	g.POST("/roles/:id/permissions", RequirePermission(e, "admin-authz", "grant"), func(c *gin.Context) {
		role, _ := loadRole(c, db, c.Param("id"))
		if role == nil {
			return
		}
		if role.BuiltIn() {
			c.JSON(http.StatusForbidden, gin.H{"error": "built-in role permissions are seed-managed"})
			return
		}
		var req struct {
			Resource   resourceRef `json:"resource" binding:"required"`
			Operations []string    `json:"operations" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		// A wildcard resource TYPE (or operation) makes the policy match every
		// object in the system — the same shape as the seeded super_admin grant.
		// Minting that from a custom role turns this route into an escalation
		// path. A wildcard resource ID stays allowed: "whole type" is this
		// route's documented purpose and is what the admin console sends.
		if err := rejectWildcardGrant(req.Resource.Type, req.Operations); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		for _, op := range req.Operations {
			if err := e.GrantRolePermission(role.ID, req.Resource.Type, req.Resource.ID, op); err != nil {
				serverError(c, err)
				return
			}
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /roles/:id/permissions — revoke a CUSTOM role's ops over a resource
	// pattern. { resource{type,id}, operations:[...] }
	g.DELETE("/roles/:id/permissions", RequirePermission(e, "admin-authz", "revoke"), func(c *gin.Context) {
		role, _ := loadRole(c, db, c.Param("id"))
		if role == nil {
			return
		}
		if role.BuiltIn() {
			c.JSON(http.StatusForbidden, gin.H{"error": "built-in role permissions are seed-managed"})
			return
		}
		var req struct {
			Resource   resourceRef `json:"resource" binding:"required"`
			Operations []string    `json:"operations" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		for _, op := range req.Operations {
			if err := e.RevokeRolePermission(role.ID, req.Resource.Type, req.Resource.ID, op); err != nil {
				serverError(c, err)
				return
			}
		}
		c.Status(http.StatusNoContent)
	})
}

// loadRole fetches a role by id, writing a 404 and returning nil when missing
// (the caller returns immediately on nil). The error return is the DB error for
// non-not-found failures (already surfaced as 500).
func loadRole(c *gin.Context, db *gorm.DB, id string) (*model.Role, error) {
	var role model.Role
	err := db.WithContext(c.Request.Context()).First(&role, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return nil, err
	}
	if err != nil {
		serverError(c, err)
		return nil, err
	}
	return &role, nil
}

// roleJSON is the standard role body.
func roleJSON(r model.Role) gin.H {
	return gin.H{
		"id": r.ID, "name": r.Name, "description": r.Description,
		"source": r.Source, "built_in": r.BuiltIn(), "created_at": r.CreatedAt,
	}
}

// grantsJSON splits each role grant's "type:id" object into a resource ref.
func grantsJSON(grants []authz.RoleGrant) []gin.H {
	out := make([]gin.H, 0, len(grants))
	for _, gr := range grants {
		rtype, rid := splitObject(gr.Object)
		out = append(out, gin.H{
			"resource":   gin.H{"type": rtype, "id": rid},
			"operations": gr.Operations,
		})
	}
	return out
}

// splitObject splits a casbin object key "type:id" on the FIRST colon (the id
// may itself contain colons). A bare "*" (super-admin everything) yields type
// "*", id "".
func splitObject(o string) (rtype, rid string) {
	for i := 0; i < len(o); i++ {
		if o[i] == ':' {
			return o[:i], o[i+1:]
		}
	}
	return o, ""
}

// accessorExists reports whether the id is a known binding subject: a user,
// department or group id.
func accessorExists(c *gin.Context, db *gorm.DB, id string) (bool, error) {
	ctx := c.Request.Context()
	for _, m := range []any{&model.User{}, &model.Department{}, &model.Group{}} {
		var n int64
		if err := db.WithContext(ctx).Model(m).Where("id = ?", id).Count(&n).Error; err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}

// catalogOps returns the operation ids registered for a resource type.
func catalogOps(db *gorm.DB, resourceType string) ([]string, error) {
	var ops []model.Operation
	if err := db.Where("resource_type_id = ?", resourceType).Find(&ops).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(ops))
	for _, op := range ops {
		ids = append(ids, op.ID)
	}
	return ids, nil
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return false
	}
	return true
}

func serverError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
