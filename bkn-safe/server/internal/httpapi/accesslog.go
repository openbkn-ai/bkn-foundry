// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/accesslog"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
)

// registerLogout records a voluntary user-initiated logout before Studio
// navigates to Hydra's browser logout endpoint. Failed or passive token expiry
// is intentionally not represented as a logout fact.
func registerLogout(group *gin.RouterGroup, store accesslog.Recorder, directory *directory.Service) {
	group.POST("/logout", func(c *gin.Context) {
		actorID := c.GetString(ctxAccessorID)
		actorName := accessActorName(c, directory, actorID)
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

func accessActorName(c *gin.Context, directory *directory.Service, actorID string) string {
	if directory == nil || actorID == "" {
		return ""
	}
	names, err := directory.ResolveUserNames(c.Request.Context(), []string{actorID})
	if err != nil || len(names) == 0 {
		return ""
	}
	return names[0].Name
}

func requestIDFromHeader(c *gin.Context) string {
	value := strings.TrimSpace(c.GetHeader("x-request-id"))
	if validAuditRequestID(value) {
		return value
	}
	return ""
}
