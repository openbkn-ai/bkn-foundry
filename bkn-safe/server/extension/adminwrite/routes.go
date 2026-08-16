// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package adminwrite

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/httperrors"
)

// Routes mounts the rbac_basic write endpoints onto g, each behind core's RBAC
// permission check (via svc.RequirePermission). It is a Mounter — the socket
// wraps it with its own licence group so that layer sits in front of RBAC; the
// community build never registers any mounter, so these routes do not exist
// there (404). Both refusals are the same 404, which is the point.
//
// The handlers are deliberately thin: bind JSON, call the guarded Services
// operation, map the sentinel error to a status. Every security rule
// (source=custom, built-in immutability, wildcard and admin-console refusal)
// lives in the Services implementation in core, next to the engine — not here —
// so it holds no matter which build wires these routes.
//
// Note on physical placement: unlike the perm_object_level socket, whose
// implementation carries real product logic and lives wholly in the private ee
// repository, these rbac_basic handlers are thin CRUD with no algorithmic
// secret. They sit in this public core package so both the ee build and core's
// own tests can reuse one implementation. The community binary still never
// serves them — it registers no mounter — so the two-binary contract's
// user-visible guarantee (404, not 403) holds; only the dead handler bytes,
// which reveal nothing, remain compiled in. #278 core decision two's physical-
// absence rule is kept where it matters (perm_object_level) and relaxed here
// where there is nothing to hide.
func Routes(g *gin.RouterGroup, svc Services) {
	// POST /roles — create a custom role. { id?, name, description? } -> { id }
	g.POST("/roles", svc.RequirePermission("admin-role", "create"), func(c *gin.Context) {
		var req struct {
			ID          string `json:"id"`
			Name        string `json:"name" binding:"required"`
			Description string `json:"description"`
		}
		if !bindJSON(c, &req) {
			return
		}
		id, err := svc.CreateRole(c.Request.Context(), RoleSpec{ID: req.ID, Name: req.Name, Description: req.Description})
		if err != nil {
			writeErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"id": id})
	})

	// PUT /roles/:id — rename/re-describe a custom role. { name?, description? }
	g.PUT("/roles/:id", svc.RequirePermission("admin-role", "edit"), func(c *gin.Context) {
		var req struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if !bindJSON(c, &req) {
			return
		}
		if err := svc.UpdateRole(c.Request.Context(), c.Param("id"), RolePatch{Name: req.Name, Description: req.Description}); err != nil {
			writeErr(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /roles/:id — delete a custom role, purging its bindings and grants.
	g.DELETE("/roles/:id", svc.RequirePermission("admin-role", "delete"), func(c *gin.Context) {
		if err := svc.DeleteRole(c.Request.Context(), c.Param("id")); err != nil {
			writeErr(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// POST /roles/:id/permissions — grant a custom role an op over a resource
	// pattern (id "*" = whole type). { resource{type,id}, operations:[...] }
	//
	// Guarded by admin-role:permissions: shaping a ROLE's permission set is role
	// management, distinct from admin-authz:grant, which hands one concrete object
	// to one concrete user (object-grants). Separating them lets a deployment give
	// out "may shape roles" and "may grant objects" independently. admin-authz:grant
	// stays accepted for the deprecation window because it is what this route
	// required before the split — custom roles carrying only the old point keep
	// working, and each such call is logged (see RequireAnyPermission).
	g.POST("/roles/:id/permissions", requireAnyPermission(svc,
		PermissionPoint{ResourceType: "admin-role", Op: "permissions"},
		PermissionPoint{ResourceType: "admin-authz", Op: "grant"},
	), func(c *gin.Context) {
		req, ok := bindPermReq(c)
		if !ok {
			return
		}
		for _, op := range req.Operations {
			if err := svc.GrantRolePermission(c.Request.Context(), c.Param("id"), req.Resource.Type, req.Resource.ID, op); err != nil {
				writeErr(c, err)
				return
			}
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /roles/:id/permissions — revoke a custom role's ops over a pattern.
	// Same point split as the POST above; admin-authz:revoke stays accepted for
	// the deprecation window.
	g.DELETE("/roles/:id/permissions", requireAnyPermission(svc,
		PermissionPoint{ResourceType: "admin-role", Op: "permissions"},
		PermissionPoint{ResourceType: "admin-authz", Op: "revoke"},
	), func(c *gin.Context) {
		req, ok := bindPermReq(c)
		if !ok {
			return
		}
		for _, op := range req.Operations {
			if err := svc.RevokeRolePermission(c.Request.Context(), c.Param("id"), req.Resource.Type, req.Resource.ID, op); err != nil {
				writeErr(c, err)
				return
			}
		}
		c.Status(http.StatusNoContent)
	})
}

type permReq struct {
	Resource struct {
		Type string `json:"type" binding:"required"`
		ID   string `json:"id"`
	} `json:"resource" binding:"required"`
	Operations []string `json:"operations" binding:"required"`
}

func bindPermReq(c *gin.Context) (permReq, bool) {
	var req permReq
	if !bindJSON(c, &req) {
		return permReq{}, false
	}
	return req, true
}

// bindJSON binds and validates the request body, answering 400 on failure.
func bindJSON(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		httperrors.Write(c, http.StatusBadRequest, nil)
		return false
	}
	return true
}

// writeErr maps a Services sentinel error to an HTTP status. An unrecognised
// error is a 500 — the message is not leaked.
func writeErr(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	code := httperrors.InternalError
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
		code = httperrors.NotFound
	case errors.Is(err, ErrImmutable):
		status = http.StatusForbidden
		code = httperrors.AdminWriteImmutable
	case errors.Is(err, ErrNoUpdatableFields):
		status = http.StatusBadRequest
		code = httperrors.AdminWriteNoUpdatableFields
	case errors.Is(err, ErrWildcardGrant):
		status = http.StatusBadRequest
		code = httperrors.AdminWriteWildcardGrantForbidden
	case errors.Is(err, ErrAdminConsolePermission):
		status = http.StatusForbidden
		code = httperrors.AdminWriteAdminConsolePermissionForbidden
	case errors.Is(err, ErrForbidden):
		status = http.StatusForbidden
		code = httperrors.Forbidden
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
		code = httperrors.AdminWriteInvalid
	}
	httperrors.WriteCode(c, status, code, nil)
}
