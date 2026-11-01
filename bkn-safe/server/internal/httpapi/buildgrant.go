// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/buildgrant"
)

// The project coordinator establishes/revokes scopes; KB API only checks them.
// These routes are ClusterIP-only, like the existing proxy lifecycle surface.
func registerBuildGrants(r *gin.Engine, service *buildgrant.Service) {
	group := r.Group("/api/safe/in/v1/build-grants")
	group.Use(func(c *gin.Context) { c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20); c.Next() })
	group.POST("", func(c *gin.Context) {
		var req buildgrant.CreateRequest
		if !bind(c, &req) {
			return
		}
		grant, created, err := service.Create(c.Request.Context(), req)
		if writeBuildGrantError(c, err) {
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		c.JSON(status, grant)
	})
	group.POST("/check", func(c *gin.Context) {
		var req buildgrant.CheckRequest
		if !bind(c, &req) {
			return
		}
		if writeBuildGrantError(c, service.Check(c.Request.Context(), req)) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"allowed": true})
	})
	type scope struct {
		ProjectID string `json:"project_id"`
		BuildID   string `json:"build_id"`
	}
	group.DELETE("/:id", func(c *gin.Context) {
		var req scope
		if !bind(c, &req) {
			return
		}
		if writeBuildGrantError(c, service.Revoke(c.Request.Context(), c.Param("id"), req.ProjectID, req.BuildID)) {
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.POST("/:id/cleanup:complete", func(c *gin.Context) {
		var req scope
		if !bind(c, &req) {
			return
		}
		if writeBuildGrantError(c, service.CompleteCleanup(c.Request.Context(), c.Param("id"), req.ProjectID, req.BuildID)) {
			return
		}
		c.Status(http.StatusNoContent)
	})
}
func writeBuildGrantError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	status := http.StatusServiceUnavailable
	switch {
	case errors.Is(err, buildgrant.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, buildgrant.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, buildgrant.ErrConflict):
		status = http.StatusConflict
	}
	// Do not echo manifest ids, request bodies, dependency responses or credentials.
	replyPublicError(c, status)
	return true
}
