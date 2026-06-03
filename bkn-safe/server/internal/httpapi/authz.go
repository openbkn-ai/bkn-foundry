package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/authz"
	"bkn-safe/internal/model"
)

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
		if err := e.RemoveResourcePolicies(req.Resource.Type, req.Resource.ID); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// POST /role-bindings — bind an accessor to a role. { accessor_id, role_id }
	g.POST("/role-bindings", func(c *gin.Context) {
		var req struct {
			AccessorID string `json:"accessor_id" binding:"required"`
			RoleID     string `json:"role_id" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if err := e.AssignRole(req.AccessorID, req.RoleID); err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
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
