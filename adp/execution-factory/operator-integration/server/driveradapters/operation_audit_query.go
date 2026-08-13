package driveradapters

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/common/operationaudit"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	infra "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const maximumExecutionAuditRange = 30 * 24 * time.Hour

func (r *restPublicHandler) ListOperationAudits(c *gin.Context) {
	if !executionAuditReader(c) {
		return
	}
	from, to, ok := executionAuditRange(c)
	if !ok {
		return
	}
	filter := operationaudit.Filter{TenantID: strings.TrimSpace(c.GetHeader("x-tenant-id")), BusinessDomain: strings.TrimSpace(c.GetHeader(string(interfaces.HeaderXBusinessDomain))), From: from, To: to}
	filter.ActorID = strings.TrimSpace(c.Query("actor_id"))
	filter.Action = strings.TrimSpace(c.Query("action"))
	filter.TargetType = strings.TrimSpace(c.Query("target_type"))
	filter.TargetID = strings.TrimSpace(c.Query("target_id"))
	filter.Outcome = strings.TrimSpace(c.Query("outcome"))
	if filter.TenantID == "" || filter.BusinessDomain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "x-tenant-id and x-business-domain are required"})
		return
	}
	if value, err := strconv.Atoi(c.Query("limit")); err == nil {
		filter.Limit = value
	}
	if value := strings.TrimSpace(c.Query("before_time")); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "before_time must be RFC3339"})
			return
		}
		filter.BeforeTime = parsed.UTC()
		filter.BeforeEventID = strings.TrimSpace(c.Query("before_event_id"))
	}
	page, err := r.auditStore.List(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation audit query failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": page.Entries, "has_more": page.HasMore})
}

func (r *restPublicHandler) GetOperationAudit(c *gin.Context) {
	if !executionAuditReader(c) {
		return
	}
	tenantID, businessDomain := strings.TrimSpace(c.GetHeader("x-tenant-id")), strings.TrimSpace(c.GetHeader(string(interfaces.HeaderXBusinessDomain)))
	if tenantID == "" || businessDomain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "x-tenant-id and x-business-domain are required"})
		return
	}
	entry, found, err := r.auditStore.Get(c.Request.Context(), strings.TrimSpace(c.Param("event_id")), tenantID, businessDomain)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation audit query failed"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "operation audit event not found"})
		return
	}
	c.JSON(http.StatusOK, entry)
}

func executionAuditRange(c *gin.Context) (time.Time, time.Time, bool) {
	from, fromErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(c.Query("from")))
	to, toErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(c.Query("to")))
	if fromErr != nil || toErr != nil || !from.Before(to) || to.Sub(from) > maximumExecutionAuditRange {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from/to must be a valid RFC3339 range of at most 30 days"})
		return time.Time{}, time.Time{}, false
	}
	return from.UTC(), to.UTC(), true
}

// The first release intentionally limits cross-user audit queries to the
// platform audit roles. Object-level audit permissions are not introduced.
func executionAuditReader(c *gin.Context) bool {
	auth, ok := infra.GetAccountAuthContextFromCtx(c.Request.Context())
	if !ok || auth == nil || auth.AccountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return false
	}
	user, err := drivenadapters.NewUserManagementClient().GetUserInfo(c.Request.Context(), auth.AccountID, "roles")
	if err != nil || user == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "operation audit access denied"})
		return false
	}
	for _, role := range user.Roles {
		if role == "super_admin" || role == "admin" || role == "audit" {
			return true
		}
	}
	c.JSON(http.StatusForbidden, gin.H{"error": "operation audit access denied"})
	return false
}
