// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/oauthorigin"
)

type accessOriginManager interface {
	List(context.Context) ([]oauthorigin.Entry, error)
	Add(context.Context, string, string) (oauthorigin.Entry, error)
	Delete(context.Context, string) (bool, error)
}

func registerOAuthAccessOriginAdmin(g *gin.RouterGroup, manager accessOriginManager, e *authz.Enforcer) {
	g.GET("/oauth/access-origins", RequirePermission(e, "admin-client", "manage"), func(c *gin.Context) {
		entries, err := manager.List(c.Request.Context())
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries, "total": len(entries)})
	})

	g.POST("/oauth/access-origins", RequirePermission(e, "admin-client", "manage"), func(c *gin.Context) {
		var req struct {
			Origin string `json:"origin" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		entry, err := manager.Add(c.Request.Context(), req.Origin, c.GetString(ctxAccessorID))
		if err != nil {
			var duplicate *oauthorigin.DuplicateError
			switch {
			case errors.As(err, &duplicate):
				replyPublicErrorDetails(c, http.StatusConflict, gin.H{"existing_id": duplicate.ExistingID})
			case errors.Is(err, oauthorigin.ErrInvalidOrigin):
				replyPublicError(c, http.StatusBadRequest)
			case errors.Is(err, oauthorigin.ErrOriginLimit):
				replyPublicError(c, http.StatusConflict)
			default:
				serverError(c, err)
			}
			return
		}
		status := http.StatusCreated
		if entry.SyncState != "synced" {
			status = http.StatusAccepted
		}
		c.JSON(status, entry)
	})

	g.DELETE("/oauth/access-origins/:id", RequirePermission(e, "admin-client", "manage"), func(c *gin.Context) {
		synced, err := manager.Delete(c.Request.Context(), c.Param("id"))
		if err != nil {
			switch {
			case errors.Is(err, oauthorigin.ErrNotFound):
				replyPublicError(c, http.StatusNotFound)
			case errors.Is(err, oauthorigin.ErrReadOnly):
				replyPublicError(c, http.StatusForbidden)
			default:
				serverError(c, err)
			}
			return
		}
		if !synced {
			c.JSON(http.StatusAccepted, gin.H{"id": c.Param("id"), "desired_state": "deleting", "sync_state": "error"})
			return
		}
		c.Status(http.StatusNoContent)
	})
}
