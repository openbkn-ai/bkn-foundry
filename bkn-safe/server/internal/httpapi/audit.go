// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"bkn-safe/internal/audit"
)

const adminPathPrefix = "/api/safe/v1/admin/"

// maxAuditBody caps how much of the request body the audit middleware buffers
// for the Detail snapshot — enough for any normal admin payload, bounded so a
// huge body can't blow up memory or the column.
const maxAuditBody = 64 << 10

// maxAuditDetail caps the stored Detail string (the column is 2048).
const maxAuditDetail = 2000

// sensitiveBodyKeys are masked in the audit Detail snapshot so credentials never
// land in the trail.
var sensitiveBodyKeys = []string{"password", "new_password", "old_password"}

// auditMiddleware records every mutating (non-GET) admin request that reaches a
// handler — i.e. one that already passed RequireAdmin, so the accessor id is
// set. It buffers the request body up front (restoring it for the handler), runs
// the request, then records actor/action/target, the resulting status, and a
// redacted Detail snapshot of the body. Recording errors are attached to the gin
// context (logged) but never fail the request: auditing must not break the
// operation it audits. Read requests are skipped, so the audit-log read endpoint
// itself produces no entries (no feedback loop).
func auditMiddleware(store *audit.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var raw []byte
		if isMutating(c.Request.Method) && c.Request.Body != nil {
			// Buffer (bounded) then restore the body so the handler still reads it.
			raw, _ = io.ReadAll(io.LimitReader(c.Request.Body, maxAuditBody))
			c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		}
		c.Next()
		if !isMutating(c.Request.Method) {
			return
		}
		resource, action := auditTarget(c.FullPath())
		if err := store.Record(c.Request.Context(), audit.Entry{
			ActorID:  c.GetString(ctxAccessorID),
			Method:   c.Request.Method,
			Resource: resource,
			Action:   action,
			TargetID: c.Param("id"),
			Detail:   auditDetail(raw),
			Status:   c.Writer.Status(),
			ClientIP: c.ClientIP(),
		}); err != nil {
			_ = c.Error(err)
		}
	}
}

// isMutating reports whether the method is a write the audit trail records.
func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// auditDetail turns a JSON request body into a redacted, truncated snapshot for
// the audit Detail column: sensitive keys are masked, the rest is preserved so a
// reader can see WHAT changed. Returns "" for an empty or non-object body (no
// useful detail to record).
func auditDetail(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "" // array/scalar/invalid — nothing structured to summarize
	}
	for _, k := range sensitiveBodyKeys {
		if _, ok := m[k]; ok {
			m[k] = "***"
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	if len(b) > maxAuditDetail {
		return string(b[:maxAuditDetail])
	}
	return string(b)
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
