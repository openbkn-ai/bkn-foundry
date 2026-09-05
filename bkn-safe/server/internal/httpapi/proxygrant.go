// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/proxygrant"
)

// registerProxyGrantSources mounts the ClusterIP-only provenance and policy
// synchronization surface used by the BKN publishing coordinator.
func registerProxyGrantSources(r *gin.Engine, service *proxygrant.Service) {
	group := r.Group("/api/safe/in/v1/proxy-grant-sources")

	group.POST("", func(c *gin.Context) {
		var req proxygrant.GrantRequest
		if !bind(c, &req) {
			return
		}
		source, changed, err := service.Grant(c.Request.Context(), req)
		if writeProxyGrantError(c, err) {
			return
		}
		status := http.StatusOK
		if changed {
			status = http.StatusCreated
		}
		c.JSON(status, source)
	})

	group.DELETE("/:id", func(c *gin.Context) {
		var req proxygrant.RevokeRequest
		if !bind(c, &req) {
			return
		}
		source, _, err := service.Revoke(c.Request.Context(), c.Param("id"), req)
		if writeProxyGrantError(c, err) {
			return
		}
		c.JSON(http.StatusOK, source)
	})

	group.POST("/check", func(c *gin.Context) {
		var req proxygrant.GrantRequest
		if !bind(c, &req) {
			return
		}
		result, err := service.Check(c.Request.Context(), req)
		if writeProxyGrantError(c, err) {
			return
		}
		c.JSON(http.StatusOK, result)
	})

	group.POST("/sync", func(c *gin.Context) {
		var req proxygrant.SyncRequest
		if !bind(c, &req) {
			return
		}
		result, err := service.Sync(c.Request.Context(), req)
		if writeProxyGrantError(c, err) {
			return
		}
		c.JSON(http.StatusOK, result)
	})

	group.POST("/reconcile", func(c *gin.Context) {
		var req proxygrant.ReconcileRequest
		if !bind(c, &req) {
			return
		}
		result, err := service.Reconcile(c.Request.Context(), req)
		if writeProxyGrantError(c, err) {
			return
		}
		c.JSON(http.StatusOK, result)
	})
}

// writeProxyGrantError writes err and reports whether the caller must stop.
func writeProxyGrantError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, proxygrant.ErrInvalidRequest):
		replyPublicError(c, http.StatusBadRequest)
	case errors.Is(err, proxygrant.ErrForbidden):
		replyPublicError(c, http.StatusForbidden)
	case errors.Is(err, proxygrant.ErrNotFound):
		replyPublicError(c, http.StatusNotFound)
	case errors.Is(err, proxygrant.ErrProxyInactive):
		replyPublicError(c, http.StatusConflict)
	default:
		serverError(c, err)
	}
	return true
}
