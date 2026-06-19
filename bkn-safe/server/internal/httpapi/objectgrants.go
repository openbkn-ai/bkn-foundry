package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/authz"
	"bkn-safe/internal/model"
)

// registerObjectGrants mounts the object-level authorization management API
// under /admin (admin-only). It manages the "grant a specific object to a
// specific user" matrix that sits ON TOP of role-based RBAC: each grant binds
// one user accessor to concrete ops on one concrete resource instance
// (catalog/operator/model/knowledge_network/…).
//
// This is the gateway-exposed, audited management surface for object grants.
// The internal /api/safe/v1/authz/policies endpoints stay for service-to-service
// "grant the creator access on resource create" calls; here every write is
// validated (known user, concrete resource, catalog-registered ops) so the UI
// can't mint dead policies.
//
// Grantees are USERS only. Departments are intentionally unsupported: casbin
// holds no user→department membership rules, so a department grant would be a
// dead policy that never matches at enforce time (see RolePermissions path for
// the role-based alternative).
func registerObjectGrants(g *gin.RouterGroup, e *authz.Enforcer, db *gorm.DB) {
	// GET /object-grants?accessor_id=&resource_type=&resource_id=
	// -> { entries:[ { accessor_id, resource{type,id}, operations:[...] } ] }
	// All three query params are optional filters. Role-subject and public
	// grants are excluded — this surface lists user object grants only.
	g.GET("/object-grants", func(c *gin.Context) {
		grants, err := e.ListObjectGrants(
			c.Query("accessor_id"), c.Query("resource_type"), c.Query("resource_id"))
		if err != nil {
			serverError(c, err)
			return
		}
		roleIDs, err := roleIDSet(c, db)
		if err != nil {
			serverError(c, err)
			return
		}
		entries := make([]gin.H, 0, len(grants))
		for _, gr := range grants {
			if roleIDs[gr.AccessorID] || gr.AccessorID == authz.PublicAccessorID {
				continue // not a user object grant
			}
			entries = append(entries, gin.H{
				"accessor_id": gr.AccessorID,
				"resource":    gin.H{"type": gr.ResourceType, "id": gr.ResourceID},
				"operations":  gr.Operations,
			})
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries})
	})

	// POST /object-grants — set (replace) a user's exact op set on one concrete
	// resource instance. { accessor_id, resource{type,id}, operations:[...] }
	// Upsert semantics: the grant's ops become exactly `operations`. An empty
	// list is rejected (use DELETE to revoke) so an accidental empty body can't
	// silently wipe a grant.
	g.POST("/object-grants", func(c *gin.Context) {
		var req struct {
			AccessorID string      `json:"accessor_id" binding:"required"`
			Resource   resourceRef `json:"resource" binding:"required"`
			Operations []string    `json:"operations" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if req.Resource.ID == "" || req.Resource.ID == "*" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "object grants target a concrete resource id (not \"*\")"})
			return
		}
		if len(req.Operations) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "operations required; use DELETE to revoke a grant"})
			return
		}
		// Grantee must be a user (apps are user rows too). Departments/groups are
		// rejected: their grants never match at enforce time.
		ok, err := isUserAccessor(c, db, req.AccessorID)
		if err != nil {
			serverError(c, err)
			return
		}
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "accessor_id must be a known user (object grants support user grantees only)"})
			return
		}
		// Ops must be registered for the resource type — blocks typos that would
		// create policies no /check can ever satisfy.
		valid, err := catalogOpSet(db, req.Resource.Type)
		if err != nil {
			serverError(c, err)
			return
		}
		if len(valid) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown resource type: " + req.Resource.Type})
			return
		}
		for _, op := range req.Operations {
			if !valid[op] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "operation not registered for " + req.Resource.Type + ": " + op})
				return
			}
		}
		if err := e.SetObjectPermissions(req.AccessorID, req.Resource.Type, req.Resource.ID, req.Operations); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /object-grants — revoke one user's grant on one concrete resource
	// instance, leaving other grantees on the same resource untouched.
	// { accessor_id, resource{type,id} }
	g.DELETE("/object-grants", func(c *gin.Context) {
		var req struct {
			AccessorID string      `json:"accessor_id" binding:"required"`
			Resource   resourceRef `json:"resource" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if req.Resource.ID == "" || req.Resource.ID == "*" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "object grants target a concrete resource id (not \"*\")"})
			return
		}
		if err := e.RemoveAccessorResourcePolicies(req.AccessorID, req.Resource.Type, req.Resource.ID); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// isUserAccessor reports whether id is a known user row (real user or app
// account; both are model.User distinguished by account_type).
func isUserAccessor(c *gin.Context, db *gorm.DB, id string) (bool, error) {
	var n int64
	if err := db.WithContext(c.Request.Context()).Model(&model.User{}).
		Where("id = ?", id).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// roleIDSet loads every role id into a lookup set, used to exclude role subjects
// from the user object-grant listing.
func roleIDSet(c *gin.Context, db *gorm.DB) (map[string]bool, error) {
	var ids []string
	if err := db.WithContext(c.Request.Context()).Model(&model.Role{}).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
}

// catalogOpSet returns the resource type's registered operation ids as a set
// (membership-test form of catalogOps).
func catalogOpSet(db *gorm.DB, resourceType string) (map[string]bool, error) {
	ops, err := catalogOps(db, resourceType)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ops))
	for _, op := range ops {
		set[op] = true
	}
	return set, nil
}
