// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/accesslog"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
)

// StudioSessionLogout verifies and revokes the presented Studio credentials.
// An empty subject means the credentials were already inactive.
type StudioSessionLogout interface {
	Logout(context.Context, string, string, string) (subject string, err error)
}

func registerLogout(group *gin.RouterGroup, revoker StudioSessionLogout, store accesslog.Recorder, directory *directory.Service, users ...*auth.UserStore) {
	group.POST("/logout", func(c *gin.Context) {
		if revoker == nil {
			abortPublicError(c, http.StatusServiceUnavailable)
			return
		}
		var input struct {
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&input); err != nil {
				abortPublicError(c, http.StatusBadRequest)
				return
			}
		}
		actorID, err := revoker.Logout(c.Request.Context(), bearerToken(c), strings.TrimSpace(input.RefreshToken), strings.TrimSpace(input.IDToken))
		if err != nil {
			if errors.Is(err, auth.ErrLogoutToken) {
				abortGate(c, http.StatusUnauthorized, gateAuthn)
			} else {
				abortPublicError(c, http.StatusServiceUnavailable)
			}
			return
		}
		if actorID == "" || store == nil {
			c.Status(http.StatusNoContent)
			return
		}
		actorName := accessActorName(c, directory, actorID, users...)
		if err := store.Record(c.Request.Context(), accesslog.Entry{
			ActorID: actorID, ActorNameSnapshot: actorName,
			AuthMethod: "oauth", SourceChannel: "studio", Action: "logout", Outcome: "success",
			RequestID: requestIDFromHeader(c), ClientIP: c.ClientIP(),
		}); err != nil {
			// Access recording must not strand a user in a session.
			_ = c.Error(err)
		}
		c.Status(http.StatusNoContent)
	})
}

func accessActorName(c *gin.Context, directory *directory.Service, actorID string, users ...*auth.UserStore) string {
	if actorID == "" {
		return ""
	}
	if directory != nil {
		names, err := directory.ResolveUserNames(c.Request.Context(), []string{actorID})
		if err == nil && len(names) > 0 && strings.TrimSpace(names[0].Name) != "" {
			return names[0].Name
		}
	}
	if len(users) > 0 && users[0] != nil {
		if user, err := users[0].ByID(c.Request.Context(), actorID); err == nil && user != nil {
			if name := strings.TrimSpace(user.Name); name != "" {
				return name
			}
			return strings.TrimSpace(user.Account)
		}
	}
	return ""
}

func requestIDFromHeader(c *gin.Context) string {
	value := strings.TrimSpace(c.GetHeader("x-request-id"))
	if validAuditRequestID(value) {
		return value
	}
	return ""
}
