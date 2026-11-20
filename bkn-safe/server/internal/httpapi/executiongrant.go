// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/executiongrant"
)

// registerExecutionGrants mounts issue and revoke for the service that runs a
// background execution, and the introspection its credential is verified with.
// All are ClusterIP-only and tokenless, like /build-grants and the AppKey
// introspection: never route them through the gateway.
func registerExecutionGrants(r *gin.Engine, service *executiongrant.Service) {
	group := r.Group("/api/safe/in/v1/execution-grants")
	group.Use(func(c *gin.Context) { c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10); c.Next() })
	group.POST("", func(c *gin.Context) {
		var req executiongrant.IssueRequest
		if !bind(c, &req) {
			return
		}
		issued, err := service.Issue(c.Request.Context(), req)
		if writeExecutionGrantError(c, err) {
			return
		}
		c.JSON(http.StatusCreated, issued)
	})
	group.DELETE("/:id", func(c *gin.Context) {
		var req struct {
			ProjectID string `json:"project_id"`
			RunID     string `json:"run_id"`
		}
		if !bind(c, &req) {
			return
		}
		if writeExecutionGrantError(c, service.Revoke(c.Request.Context(), c.Param("id"), req.ProjectID, req.RunID)) {
			return
		}
		c.Status(http.StatusNoContent)
	})
	// Mirrors OAuth2 introspection: 200 {active:false} on any failure, with no
	// reason; the delegator and the scope the caller must hold it to on success.
	r.POST("/api/safe/v1/execution-grants/introspect", func(c *gin.Context) {
		var req struct {
			Token string `json:"token" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		v, err := service.Verify(c.Request.Context(), req.Token)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"active": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"active":        true,
			"sub":           v.DelegatorID,
			"subject_name":  v.Name,
			"account_type":  v.AccountType,
			"delegation_id": v.GrantID,
			"project_id":    v.ProjectID,
			"run_id":        v.RunID,
			"kn_id":         v.KnID,
			"agent_id":      v.AgentID,
		})
	})
}

func writeExecutionGrantError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	status := http.StatusServiceUnavailable
	switch {
	case errors.Is(err, executiongrant.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, executiongrant.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, executiongrant.ErrConflict):
		status = http.StatusConflict
	}
	// Never echo the request, the credential or a dependency's answer.
	replyPublicError(c, status)
	return true
}
