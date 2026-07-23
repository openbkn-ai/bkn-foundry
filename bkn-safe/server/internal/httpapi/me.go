// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// registerMeReads mounts the READ-ONLY self-service endpoints under
// /api/safe/v1/me (GET "" and GET /permissions). Token-gated by RequireUser:
// the accessor id comes from the verified bearer token, never from the request.
// Frontends call these once after login to drive menu/button visibility; the
// backend still enforces every request via /authz/check.
//
// These two are the endpoints the login burst hits in parallel, so the caller
// mounts them behind the introspection cache. Mutating /me endpoints (profile
// PUT, API-key issue/revoke) are registered separately on an UNCACHED verifier
// (see registerMeProfile / registerMeAPIKeys) so a revoked token cannot mutate
// within the read cache's TTL window.
func registerMeReads(g *gin.RouterGroup, e *authz.Enforcer, db *gorm.DB, dir *directory.Service) {
	// GET "" -> the caller's identity and roles:
	// { id, account, name, email, telephone, account_type, enabled,
	//   departments:[ids], roles:[names], role_ids:[ids], updated_at }
	// Role names resolve via the roles table; a dangling binding (role row
	// deleted) falls back to the raw id rather than being dropped.
	g.GET("", func(c *gin.Context) {
		sub := c.GetString(ctxAccessorID)

		user, err := dir.GetUser(c.Request.Context(), sub)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "no user for token subject: " + sub})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}

		roleIDs, err := e.RolesForAccessor(sub)
		if err != nil {
			serverError(c, err)
			return
		}
		var roleRows []model.Role
		if len(roleIDs) > 0 {
			if err := db.WithContext(c.Request.Context()).
				Where("id IN ?", roleIDs).Find(&roleRows).Error; err != nil {
				serverError(c, err)
				return
			}
		}
		nameByID := map[string]string{}
		for _, r := range roleRows {
			nameByID[r.ID] = r.Name
		}
		roleNames := make([]string, 0, len(roleIDs))
		for _, id := range roleIDs {
			if n, ok := nameByID[id]; ok {
				roleNames = append(roleNames, n)
			} else {
				roleNames = append(roleNames, id)
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"id":           user.ID,
			"account":      user.Account,
			"name":         user.Name,
			"email":        user.Email,
			"telephone":    user.Telephone,
			"account_type": user.AccountType,
			"enabled":      user.Enabled,
			"departments":  user.Departments,
			"roles":        roleNames,
			"role_ids":     roleIDs,
			"updated_at":   user.UpdatedAt,
		})
	})

	// GET /permissions -> { is_admin, permissions:[ { resource{type,id}, operations:[...] } ] }
	// Returns the EFFECTIVE (collapsed) authorization, not one row per instance:
	// a resource-wildcard holder gets a single {type:"*",id:"*",ops:["*"]} row;
	// everyone else gets one type-wide row per type plus an instance row only
	// where that instance's ops exceed the type-wide set (carrying just the
	// surplus). The frontend unions the id:"*" row with instance rows to judge
	// a concrete (type,id). is_admin stays a separate flag (CanAdmin), decoupled
	// from whether permissions is the wildcard single row.
	//
	// Optional scope filters: ?resource_type=<T> narrows to one type;
	// &resource_id=<id1,id2,...> (comma-separated) narrows the instance rows,
	// keeping the type-wide id:"*" row. resource_id requires resource_type.
	g.GET("/permissions", func(c *gin.Context) {
		accessorID := c.GetString(ctxAccessorID)
		isAdmin, err := e.CanAdmin(accessorID)
		if err != nil {
			serverError(c, err)
			return
		}
		resourceType := c.Query("resource_type")
		var resourceIDs []string
		if raw := c.Query("resource_id"); raw != "" {
			for _, id := range strings.Split(raw, ",") {
				if id = strings.TrimSpace(id); id != "" {
					resourceIDs = append(resourceIDs, id)
				}
			}
		}
		if len(resourceIDs) > 0 && resourceType == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "resource_id requires resource_type"})
			return
		}
		_, grants, err := e.EffectivePermissions(accessorID, authz.PermQuery{
			ResourceType: resourceType,
			ResourceIDs:  resourceIDs,
		})
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"is_admin":    isAdmin,
			"permissions": grantsJSON(grants),
		})
	})
}

// registerMeProfile mounts the MUTATING self-service profile endpoint (PUT "")
// under /api/safe/v1/me. It is registered on an UNCACHED verifier (unlike the
// read-only endpoints in registerMeReads) so a revoked token cannot edit the
// profile within the read cache's TTL window.
//
// PUT "" -> self-service profile update. The target is ALWAYS the token subject
// (never an :id from the path/body), so a caller can only edit its own profile —
// no admin grant required. Only name/email/telephone are writable here;
// account_type, enabled, department membership, account, and password stay
// admin-only / have their own endpoints. Only keys present in the body are
// changed; an absent key is left untouched. -> 204.
func registerMeProfile(g *gin.RouterGroup, users *auth.UserStore) {
	if users == nil {
		return
	}
	g.PUT("", func(c *gin.Context) {
		var req struct {
			Name      *string `json:"name"`
			Email     *string `json:"email"`
			Telephone *string `json:"telephone"`
		}
		if !bind(c, &req) {
			return
		}
		fields := map[string]any{}
		if req.Name != nil {
			name := strings.TrimSpace(*req.Name)
			if name == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "name cannot be empty"})
				return
			}
			if len(name) > 255 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "name too long (max 255)"})
				return
			}
			fields["name"] = name
		}
		if req.Email != nil {
			email := strings.TrimSpace(*req.Email)
			// An empty email clears the field; a non-empty one must parse as a
			// single, bare address (no display name / list).
			if email != "" {
				addr, err := mail.ParseAddress(email)
				if err != nil || addr.Name != "" || addr.Address != email {
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email"})
					return
				}
			}
			fields["email"] = email
		}
		if req.Telephone != nil {
			tel := strings.TrimSpace(*req.Telephone)
			if len(tel) > 64 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "telephone too long (max 64)"})
				return
			}
			fields["telephone"] = tel
		}
		if len(fields) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no updatable fields provided"})
			return
		}
		sub := c.GetString(ctxAccessorID)
		err := users.UpdateUser(c.Request.Context(), sub, fields)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "no user for token subject: " + sub})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}
