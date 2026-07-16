// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/audit"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/directory"
	"bkn-safe/internal/model"
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
func auditMiddleware(store *audit.Store, dir *directory.Service, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var raw []byte
		if isMutating(c.Request.Method) && c.Request.Body != nil {
			// Buffer (bounded) then restore the body so the handler still reads it.
			raw, _ = io.ReadAll(io.LimitReader(c.Request.Body, maxAuditBody))
			c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		}
		resource, action := auditTarget(c.FullPath())
		targetID := c.Param("id")
		detail := auditDetail(raw)
		beforeName := auditTargetName(c.Request.Context(), dir, db, resource, targetID, detail)
		c.Next()
		if !isMutating(c.Request.Method) {
			return
		}
		targetName := beforeName
		if c.Writer.Status() < http.StatusBadRequest {
			if name := auditTargetName(c.Request.Context(), dir, db, resource, targetID, detail); name != "" {
				targetName = name
			}
		}
		if err := store.Record(c.Request.Context(), audit.Entry{
			ActorID:    c.GetString(ctxAccessorID),
			Method:     c.Request.Method,
			Resource:   resource,
			Action:     action,
			TargetID:   targetID,
			TargetName: targetName,
			Detail:     detail,
			Status:     c.Writer.Status(),
			ClientIP:   c.ClientIP(),
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

func auditTargetName(
	ctx context.Context,
	dir *directory.Service,
	db *gorm.DB,
	resource string,
	targetID string,
	detail string,
) string {
	if targetID == "" {
		return auditDetailName(ctx, dir, db, resource, detail)
	}
	switch resource {
	case "users":
		if dir == nil {
			return ""
		}
		names, err := dir.ResolveUserNames(ctx, []string{targetID})
		if err == nil && len(names) > 0 {
			return names[0].Name
		}
	case "departments":
		if dir == nil {
			return ""
		}
		names, err := dir.ResolveDepartmentNames(ctx, []string{targetID})
		if err == nil && len(names) > 0 {
			return names[0].Name
		}
	case "roles":
		if db == nil {
			return ""
		}
		var role model.Role
		if err := db.WithContext(ctx).Select("name").First(&role, "id = ?", targetID).Error; err == nil {
			return role.Name
		}
	}
	return ""
}

func auditDetailName(
	ctx context.Context,
	dir *directory.Service,
	db *gorm.DB,
	resource string,
	detail string,
) string {
	if detail == "" {
		return ""
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(detail), &body); err != nil {
		return ""
	}
	if name, ok := body["name"].(string); ok && name != "" {
		return name
	}
	if resource == "role-bindings" {
		roleID, _ := body["role_id"].(string)
		accessorID, _ := body["accessor_id"].(string)
		roleName := roleNameByID(ctx, db, roleID)
		accessorName := accessorNameByID(ctx, dir, accessorID)
		if roleName != "" && accessorName != "" {
			return roleName + " / " + accessorName
		}
		if roleName != "" {
			return roleName
		}
		return accessorName
	}
	return ""
}

func roleNameByID(ctx context.Context, db *gorm.DB, id string) string {
	if db == nil || id == "" {
		return ""
	}
	var role model.Role
	if err := db.WithContext(ctx).Select("name").First(&role, "id = ?", id).Error; err != nil {
		return ""
	}
	return role.Name
}

func accessorNameByID(ctx context.Context, dir *directory.Service, id string) string {
	if dir == nil || id == "" {
		return ""
	}
	if names, err := dir.ResolveUserNames(ctx, []string{id}); err == nil && len(names) > 0 {
		return names[0].Name
	}
	if names, err := dir.ResolveDepartmentNames(ctx, []string{id}); err == nil && len(names) > 0 {
		return names[0].Name
	}
	return ""
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
func registerAuditReads(g *gin.RouterGroup, store *audit.Store, e *authz.Enforcer) {
	// GET /audit-logs — list audit entries newest-first, filterable. Query:
	// ?actor_id=&resource=&action=&target_id=&from=&to=&offset=&limit=
	// from/to are RFC3339 timestamps. -> { logs:[...], total }
	g.GET("/audit-logs", RequirePermission(e, "admin-audit", "view"), func(c *gin.Context) {
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
