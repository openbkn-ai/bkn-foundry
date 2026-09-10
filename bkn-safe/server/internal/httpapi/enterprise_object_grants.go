// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permobject"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
)

func registerEnterpriseObjectGrants(g *gin.RouterGroup, e *authz.Enforcer) {
	g.GET("/enterprise-object-grants", RequirePermission(e, "admin-authz", "view"), func(c *gin.Context) {
		entries, err := permobject.Inventory(c.Request.Context(), time.Now().UTC())
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries, "total": len(entries)})
	})

	g.DELETE("/enterprise-object-grants", RequirePermission(e, "admin-authz", "revoke"), func(c *gin.Context) {
		var req struct {
			GrantID string `json:"grant_id" binding:"required"`
			Reason  string `json:"reason" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		req.GrantID = strings.TrimSpace(req.GrantID)
		req.Reason = strings.TrimSpace(req.Reason)
		if req.GrantID == "" || len(req.GrantID) > 64 || req.Reason == "" || len(req.Reason) > 512 {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		if err := permobject.Revoke(c.Request.Context(), req.GrantID,
			c.GetString(ctxAccessorID), req.Reason, time.Now().UTC()); err != nil {
			serverError(c, err)
			return
		}
		setAuditOutcome(c, map[string]any{"grant_id": req.GrantID, "removed": true})
		c.Status(http.StatusNoContent)
	})
}
