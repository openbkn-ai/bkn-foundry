// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

// Clients that registered themselves (oauthregister.go) and the grants users
// gave them. A user sees and disconnects their own; an administrator sees and
// removes any self-registered client, e.g. the duplicates an agent leaves
// behind each time it is reinstalled. Studio's and the CLI's own clients are
// never listed or touched here.

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
)

// ConnectedClientManager reads and revokes self-registered clients in Hydra.
type ConnectedClientManager interface {
	ListConnectedClients(ctx context.Context, subject string) ([]auth.ConnectedClient, error)
	RevokeConnectedClient(ctx context.Context, subject, clientID string) error
	ListDynamicClients(ctx context.Context) ([]auth.ConnectedClient, error)
	DeleteDynamicClient(ctx context.Context, clientID string) error
}

type connectedClientView struct {
	ClientID     string     `json:"client_id"`
	ClientName   string     `json:"client_name"`
	RedirectURIs []string   `json:"redirect_uris"`
	GrantedAt    *time.Time `json:"granted_at,omitempty"`
	Scopes       []string   `json:"scopes,omitempty"`
}

type dynamicClientView struct {
	ClientID       string     `json:"client_id"`
	ClientName     string     `json:"client_name"`
	RedirectURIs   []string   `json:"redirect_uris"`
	RegisteredAt   *time.Time `json:"registered_at,omitempty"`
	RegisteredFrom string     `json:"registered_from,omitempty"`
}

// registerMeConnectedClients mounts GET/DELETE /me/oauth/clients.
func registerMeConnectedClients(g *gin.RouterGroup, m ConnectedClientManager) {
	g.GET("/oauth/clients", func(c *gin.Context) {
		clients, err := m.ListConnectedClients(c.Request.Context(), c.GetString(ctxAccessorID))
		if err != nil {
			serverError(c, err)
			return
		}
		entries := make([]connectedClientView, 0, len(clients))
		for _, cl := range clients {
			entries = append(entries, connectedClientView{
				ClientID:     cl.ClientID,
				ClientName:   cl.ClientName,
				RedirectURIs: nonNil(cl.RedirectURIs),
				GrantedAt:    timePtr(cl.GrantedAt),
				Scopes:       cl.Scopes,
			})
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries, "total": len(entries)})
	})

	// Disconnect: the client's access and refresh tokens for this user stop
	// working; it has to send the user through sign-in again.
	g.DELETE("/oauth/clients/:client_id", func(c *gin.Context) {
		clientID := c.Param("client_id")
		setAuditOperation(c, "disconnect_client", clientID, "")
		err := m.RevokeConnectedClient(c.Request.Context(), c.GetString(ctxAccessorID), clientID)
		if errors.Is(err, auth.ErrNotDynamicClient) {
			replyPublicError(c, http.StatusNotFound)
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// registerAdminConnectedClients mounts GET/DELETE /admin/oauth/clients under
// the same permission as access-origin management.
func registerAdminConnectedClients(g *gin.RouterGroup, m ConnectedClientManager, e *authz.Enforcer) {
	g.GET("/oauth/clients", RequirePermission(e, "admin-client", "manage"), func(c *gin.Context) {
		clients, err := m.ListDynamicClients(c.Request.Context())
		if err != nil {
			serverError(c, err)
			return
		}
		entries := make([]dynamicClientView, 0, len(clients))
		for _, cl := range clients {
			entries = append(entries, dynamicClientView{
				ClientID:       cl.ClientID,
				ClientName:     cl.ClientName,
				RedirectURIs:   nonNil(cl.RedirectURIs),
				RegisteredAt:   timePtr(cl.RegisteredAt),
				RegisteredFrom: cl.RegisteredFrom,
			})
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries, "total": len(entries)})
	})

	g.DELETE("/oauth/clients/:client_id", RequirePermission(e, "admin-client", "manage"), func(c *gin.Context) {
		clientID := c.Param("client_id")
		setAuditOperation(c, "remove_client", clientID, "")
		err := m.DeleteDynamicClient(c.Request.Context(), clientID)
		if errors.Is(err, auth.ErrNotDynamicClient) {
			replyPublicError(c, http.StatusNotFound)
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
