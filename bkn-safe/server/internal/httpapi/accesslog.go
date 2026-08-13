// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/accesslog"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
)

// registerAccessLogReads keeps access facts on the existing authenticated admin
// surface. It deliberately does not add another role model; the Trace log
// service continues to apply the platform's existing log/Trace access policy.
func registerAccessLogReads(group *gin.RouterGroup, store *accesslog.Store) {
	group.GET("/access-logs", func(c *gin.Context) {
		filter := accesslog.Filter{
			ActorID: c.Query("actor_id"),
			Action:  c.Query("action"),
			Outcome: c.Query("outcome"),
			Offset:  atoiDefault(c.Query("offset"), 0),
			Limit:   atoiDefault(c.Query("limit"), 0),
		}
		if !accessLogTimeFilter(c, "from", &filter.From) || !accessLogTimeFilter(c, "to", &filter.To) {
			return
		}
		logs, total, err := store.List(c.Request.Context(), filter)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"logs": logs, "total": total})
	})
	group.GET("/access-logs/:id", func(c *gin.Context) {
		entry, found, err := store.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			serverError(c, err)
			return
		}
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "access log not found"})
			return
		}
		c.JSON(http.StatusOK, entry)
	})
}

func accessLogTimeFilter(c *gin.Context, key string, target *time.Time) bool {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return true
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": key + " must be an RFC3339 timestamp"})
		return false
	}
	*target = parsed
	return true
}

// registerLogout records a voluntary user-initiated logout before Studio
// navigates to Hydra's browser logout endpoint. Failed or passive token expiry
// is intentionally not represented as a logout fact.
func registerLogout(group *gin.RouterGroup, store *accesslog.Store, directory *directory.Service) {
	group.POST("/logout", func(c *gin.Context) {
		actorID := c.GetString(ctxAccessorID)
		actorName := accessActorName(c, directory, actorID)
		if err := store.Record(c.Request.Context(), accesslog.Entry{
			ActorID: actorID, ActorNameSnapshot: actorName,
			AuthMethod: "oauth", SourceChannel: "studio", Action: "logout", Outcome: "success",
			RequestID: requestIDFromHeader(c), ClientIP: c.ClientIP(),
		}); err != nil {
			// Access recording must not strand a user in a session.
			c.Error(err)
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
