// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/oauthorigin"
)

// ClientManager is the slice of hydra's OAuth2 client admin that bkn-safe exposes:
// reading and editing a login client's redirect_uris. *auth.HydraAdmin implements
// it (production); tests inject an in-memory stub so no live hydra is needed.
type ClientManager interface {
	GetClientRedirectURIs(ctx context.Context, clientID string) ([]string, error)
	AddClientRedirectURI(ctx context.Context, clientID, uri string) ([]string, error)
	RemoveClientRedirectURI(ctx context.Context, clientID, uri string) ([]string, error)
}

// StudioOriginManager backs the legacy openbkn-studio redirect-URI endpoint
// with the same durable origin store as the dedicated admin API.
type StudioOriginManager interface {
	Callbacks(context.Context) ([]string, error)
	AddCallback(context.Context, string, string) ([]string, error)
	RemoveCallback(context.Context, string) ([]string, error)
}

// manageableClients are the first-party login clients whose redirect_uris an admin
// may edit here. Restricting to the platform's own seeded clients (see
// charts/bkn-safe client-seed-job) keeps this from being a generic hydra client
// editor. Kept as its own list — not aliased to firstPartyClients — so loosening
// what is editable never silently loosens what skips the consent screen.
var manageableClients = map[string]bool{
	"openbkn-studio": true,
	"openbkn-cli":    true,
	"openbkn-sdk":    true,
}

// registerClientAdmin mounts redirect-uri management for the platform's login
// clients under the admin group (RequireAdmin + audited). Studio mutations are
// routed through the durable access-origin service; the CLI and SDK retain the
// legacy direct-Hydra behavior for backward compatibility.
func registerClientAdmin(g *gin.RouterGroup, mgr ClientManager, origins StudioOriginManager, e *authz.Enforcer) {
	// GET /clients/:id/redirect-uris -> { "redirect_uris": [...] }
	g.GET("/clients/:id/redirect-uris", RequirePermission(e, "admin-client", "manage"), func(c *gin.Context) {
		id := c.Param("id")
		if !manageableClients[id] {
			replyPublicError(c, http.StatusForbidden)
			return
		}
		var uris []string
		var err error
		if id == "openbkn-studio" && origins != nil {
			uris, err = origins.Callbacks(c.Request.Context())
		} else {
			uris, err = mgr.GetClientRedirectURIs(c.Request.Context(), id)
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"redirect_uris": uris})
	})

	// POST /clients/:id/redirect-uris { "redirect_uri": "..." } -> { "redirect_uris" }
	// Idempotent: adding an already-registered uri returns the unchanged list.
	g.POST("/clients/:id/redirect-uris", RequirePermission(e, "admin-client", "manage"), func(c *gin.Context) {
		id := c.Param("id")
		if !manageableClients[id] {
			replyPublicError(c, http.StatusForbidden)
			return
		}
		var req struct {
			RedirectURI string `json:"redirect_uri" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if !validRedirectURI(req.RedirectURI) {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		var uris []string
		var err error
		if id == "openbkn-studio" && origins != nil {
			uris, err = origins.AddCallback(c.Request.Context(), req.RedirectURI, c.GetString(ctxAccessorID))
		} else {
			uris, err = mgr.AddClientRedirectURI(c.Request.Context(), id, req.RedirectURI)
		}
		if err != nil {
			if errors.Is(err, oauthorigin.ErrInvalidOrigin) {
				replyPublicError(c, http.StatusBadRequest)
				return
			}
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"redirect_uris": uris})
	})

	// DELETE /clients/:id/redirect-uris { "redirect_uri": "..." } -> { "redirect_uris" }
	g.DELETE("/clients/:id/redirect-uris", RequirePermission(e, "admin-client", "manage"), func(c *gin.Context) {
		id := c.Param("id")
		if !manageableClients[id] {
			replyPublicError(c, http.StatusForbidden)
			return
		}
		var req struct {
			RedirectURI string `json:"redirect_uri" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		var uris []string
		var err error
		if id == "openbkn-studio" && origins != nil {
			uris, err = origins.RemoveCallback(c.Request.Context(), req.RedirectURI)
		} else {
			uris, err = mgr.RemoveClientRedirectURI(c.Request.Context(), id, req.RedirectURI)
		}
		if err != nil {
			if errors.Is(err, oauthorigin.ErrInvalidOrigin) {
				replyPublicError(c, http.StatusBadRequest)
				return
			}
			if errors.Is(err, oauthorigin.ErrReadOnly) {
				replyPublicError(c, http.StatusForbidden)
				return
			}
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"redirect_uris": uris})
	})
}

// validRedirectURI accepts only an absolute http/https URL with a host and no
// fragment or wildcard. hydra itself rejects wildcards and fragments; validating
// up front turns a vague hydra 4xx into a clear 400.
func validRedirectURI(raw string) bool {
	if strings.ContainsAny(raw, "*") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != "" && u.Fragment == ""
}
