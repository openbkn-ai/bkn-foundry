// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/config"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/observability"
)

func MetricsMiddleware(metrics *observability.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		if metrics == nil {
			return
		}

		status := c.Writer.Status()
		route := normalizeRoute(c.FullPath(), c.Request.URL.Path)
		metrics.IncRequest(c.Request.Method, route, strconv.Itoa(status))
		_ = start
	}
}

func AuditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		userID, _ := c.Get(contextKeyUserID)
		userIDText, _ := userID.(string)

		observability.WriteAuditEvent(observability.AuditEvent{
			RequestID:  requestIDFromContext(c),
			UserID:     userIDText,
			Method:     c.Request.Method,
			Path:       c.Request.URL.Path,
			Route:      normalizeRoute(c.FullPath(), c.Request.URL.Path),
			Status:     c.Writer.Status(),
			DurationMS: time.Since(start).Milliseconds(),
		})
	}
}

func FeatureGateMiddleware(features config.FeatureFlags) gin.HandlerFunc {
	return func(c *gin.Context) {
		feature := requiredFeature(c.Request.Method, c.FullPath())
		if feature == "" || isFeatureEnabled(features, feature) {
			c.Next()
			return
		}

		writeError(c, http.StatusNotFound, "feature_disabled", "feature is disabled")
		c.Abort()
	}
}

func requiredFeature(method, route string) string {
	switch route {
	case "/api/capabilities-lab/v1/catalog":
		return "catalog"
	case "/api/capabilities-lab/v1/catalog/install":
		return "catalog"
	case "/api/capabilities-lab/v1/capabilities/function":
		return "function"
	case "/api/capabilities-lab/v1/function/execute":
		return "function"
	case "/api/capabilities-lab/v1/template/python":
		return "function"
	case "/api/capabilities-lab/v1/capabilities/import":
		return "impex"
	case "/api/capabilities-lab/v1/capabilities/:id/export":
		return "impex"
	case "/api/capabilities-lab/v1/capabilities/mcp/parse-sse":
		return "mcp_sse_wizard"
	case "/api/capabilities-lab/v1/capabilities/:id/skill/content":
		return "skill_files"
	case "/api/capabilities-lab/v1/capabilities/:id/skill/files/read":
		return "skill_files"
	default:
		return ""
	}
}

func isFeatureEnabled(features config.FeatureFlags, feature string) bool {
	switch feature {
	case "catalog":
		return features.Catalog
	case "function":
		return features.Function
	case "impex":
		return features.Impex
	case "mcp_sse_wizard":
		return features.McpSseWizard
	case "skill_files":
		return features.SkillFiles
	default:
		return true
	}
}

func normalizeRoute(fullPath, requestPath string) string {
	if fullPath != "" {
		return strings.TrimPrefix(fullPath, "/api/capabilities-lab/v1")
	}

	if requestPath == "" {
		return "unknown"
	}

	return requestPath
}
