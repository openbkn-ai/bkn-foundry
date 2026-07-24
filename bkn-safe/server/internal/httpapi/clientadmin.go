// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
)

// ClientManager is the slice of hydra's OAuth2 client admin that bkn-safe exposes:
// reading and editing a login client's redirect_uris. *auth.HydraAdmin implements
// it (production); tests inject an in-memory stub so no live hydra is needed.
type ClientManager interface {
	GetClientRedirectURIs(ctx context.Context, clientID string) ([]string, error)
	AddClientRedirectURI(ctx context.Context, clientID, uri string) ([]string, error)
	RemoveClientRedirectURI(ctx context.Context, clientID, uri string) ([]string, error)
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
// clients under the admin group (RequireAdmin + audited). This is a runtime
// convenience: a helm upgrade re-seeds clients from chart values, so durable
// redirect_uris still belong in clientSeed.extraWebRedirectUris.
func registerClientAdmin(g *gin.RouterGroup, mgr ClientManager, e *authz.Enforcer) {
	// GET /clients/:id/redirect-uris -> { "redirect_uris": [...] }
	g.GET("/clients/:id/redirect-uris", RequirePermission(e, "admin-client", "manage"), func(c *gin.Context) {
		id := c.Param("id")
		if !manageableClients[id] {
			c.JSON(http.StatusForbidden, gin.H{"error": "client not manageable"})
			return
		}
		uris, err := mgr.GetClientRedirectURIs(c.Request.Context(), id)
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
			c.JSON(http.StatusForbidden, gin.H{"error": "client not manageable"})
			return
		}
		var req struct {
			RedirectURI string `json:"redirect_uri" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if !validRedirectURI(req.RedirectURI) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "redirect_uri must be an absolute http(s) URL with a host and no wildcard or fragment"})
			return
		}
		uris, err := mgr.AddClientRedirectURI(c.Request.Context(), id, req.RedirectURI)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"redirect_uris": uris})
	})

	// DELETE /clients/:id/redirect-uris { "redirect_uri": "..." } -> { "redirect_uris" }
	g.DELETE("/clients/:id/redirect-uris", RequirePermission(e, "admin-client", "manage"), func(c *gin.Context) {
		id := c.Param("id")
		if !manageableClients[id] {
			c.JSON(http.StatusForbidden, gin.H{"error": "client not manageable"})
			return
		}
		var req struct {
			RedirectURI string `json:"redirect_uri" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		uris, err := mgr.RemoveClientRedirectURI(c.Request.Context(), id, req.RedirectURI)
		if err != nil {
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
