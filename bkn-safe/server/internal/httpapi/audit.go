package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"bkn-safe/internal/audit"
)

const adminPathPrefix = "/api/safe/v1/admin/"

// auditMiddleware records every mutating (non-GET) admin request that reaches a
// handler — i.e. one that already passed RequireAdmin, so the accessor id is
// set. It runs the request first, then records the resulting status. Recording
// errors are attached to the gin context (logged) but never fail the request:
// auditing must not break the operation it audits. Read requests are skipped, so
// the audit-log read endpoint itself produces no entries (no feedback loop).
func auditMiddleware(store *audit.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return
		}
		resource, action := auditTarget(c.FullPath())
		if err := store.Record(c.Request.Context(), audit.Entry{
			ActorID:  c.GetString(ctxAccessorID),
			Method:   c.Request.Method,
			Resource: resource,
			Action:   action,
			TargetID: c.Param("id"),
			Status:   c.Writer.Status(),
			ClientIP: c.ClientIP(),
		}); err != nil {
			_ = c.Error(err)
		}
	}
}

// auditTarget derives the resource (top-level admin noun) and action (dotted
// non-param route segments) from the matched route template. E.g.
// "/api/safe/v1/admin/departments/:id/members" -> ("departments",
// "departments.members"); "/api/safe/v1/admin/users" -> ("users", "users").
// Method (carried separately) distinguishes create/update/delete.
func auditTarget(fullPath string) (resource, action string) {
	rest := strings.TrimPrefix(fullPath, adminPathPrefix)
	parts := make([]string, 0, 3)
	for _, seg := range strings.Split(rest, "/") {
		if seg == "" || strings.HasPrefix(seg, ":") {
			continue
		}
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return "", ""
	}
	return parts[0], strings.Join(parts, ".")
}

// registerAuditReads mounts the audit-log read endpoint under the admin group.
// It is a GET, so auditMiddleware does not record calls to it.
func registerAuditReads(g *gin.RouterGroup, store *audit.Store) {
	// GET /audit-logs — list audit entries newest-first, filterable. Query:
	// ?actor_id=&resource=&action=&target_id=&from=&to=&offset=&limit=
	// from/to are RFC3339 timestamps. -> { logs:[...], total }
	g.GET("/audit-logs", func(c *gin.Context) {
		f := audit.Filter{
			ActorID:  c.Query("actor_id"),
			Resource: c.Query("resource"),
			Action:   c.Query("action"),
			TargetID: c.Query("target_id"),
			Offset:   atoiDefault(c.Query("offset"), 0),
			Limit:    atoiDefault(c.Query("limit"), 0),
		}
		if v := c.Query("from"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "from must be an RFC3339 timestamp"})
				return
			}
			f.From = t
		}
		if v := c.Query("to"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "to must be an RFC3339 timestamp"})
				return
			}
			f.To = t
		}
		logs, total, err := store.List(c.Request.Context(), f)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"logs": logs, "total": total})
	})
}
