package driveradapters

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/common/operationaudit"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	infra "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	infrarest "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const maximumExecutionAuditRange = 30 * 24 * time.Hour

type operationAuditQueryStore interface {
	List(ctx context.Context, filter operationaudit.Filter) (operationaudit.Page, error)
	Get(ctx context.Context, eventID, tenantID, businessDomain string) (operationaudit.Entry, bool, error)
}

func (r *restPublicHandler) ListOperationAudits(c *gin.Context) {
	if !r.executionAuditReader(c) {
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
		replyExecutionAuditError(c, http.StatusBadRequest, infraerrors.ErrExtOperationAuditMissingScope, map[string]any{"required_headers": []string{"x-tenant-id", string(interfaces.HeaderXBusinessDomain)}})
		return
	}
	if value, err := strconv.Atoi(c.Query("limit")); err == nil {
		filter.Limit = value
	}
	if value := strings.TrimSpace(c.Query("before_time")); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			replyExecutionAuditError(c, http.StatusBadRequest, infraerrors.ErrExtOperationAuditInvalidBeforeTime, map[string]any{"field": "before_time", "format": "RFC3339"})
			return
		}
		filter.BeforeTime = parsed.UTC()
		filter.BeforeEventID = strings.TrimSpace(c.Query("before_event_id"))
	}
	page, err := r.auditQueryStore.List(c.Request.Context(), filter)
	if err != nil {
		replyExecutionAuditError(c, http.StatusInternalServerError, infraerrors.ErrExtOperationAuditQueryFailed, nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": page.Entries, "has_more": page.HasMore})
}

func (r *restPublicHandler) GetOperationAudit(c *gin.Context) {
	if !r.executionAuditReader(c) {
		return
	}
	tenantID, businessDomain := strings.TrimSpace(c.GetHeader("x-tenant-id")), strings.TrimSpace(c.GetHeader(string(interfaces.HeaderXBusinessDomain)))
	if tenantID == "" || businessDomain == "" {
		replyExecutionAuditError(c, http.StatusBadRequest, infraerrors.ErrExtOperationAuditMissingScope, map[string]any{"required_headers": []string{"x-tenant-id", string(interfaces.HeaderXBusinessDomain)}})
		return
	}
	entry, found, err := r.auditQueryStore.Get(c.Request.Context(), strings.TrimSpace(c.Param("event_id")), tenantID, businessDomain)
	if err != nil {
		replyExecutionAuditError(c, http.StatusInternalServerError, infraerrors.ErrExtOperationAuditQueryFailed, nil)
		return
	}
	if !found {
		replyExecutionAuditError(c, http.StatusNotFound, infraerrors.ErrExtOperationAuditNotFound, map[string]any{"event_id": strings.TrimSpace(c.Param("event_id"))})
		return
	}
	c.JSON(http.StatusOK, entry)
}

func executionAuditRange(c *gin.Context) (time.Time, time.Time, bool) {
	from, fromErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(c.Query("from")))
	to, toErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(c.Query("to")))
	if fromErr != nil || toErr != nil || !from.Before(to) || to.Sub(from) > maximumExecutionAuditRange {
		replyExecutionAuditError(c, http.StatusBadRequest, infraerrors.ErrExtOperationAuditInvalidRange, map[string]any{"fields": []string{"from", "to"}, "format": "RFC3339", "max_duration": maximumExecutionAuditRange.String()})
		return time.Time{}, time.Time{}, false
	}
	return from.UTC(), to.UTC(), true
}

// The first release intentionally limits cross-user audit queries to the
// platform audit roles. Object-level audit permissions are not introduced.
func (r *restPublicHandler) executionAuditReader(c *gin.Context) bool {
	auth, ok := infra.GetAccountAuthContextFromCtx(c.Request.Context())
	if !ok || auth == nil || auth.AccountID == "" {
		replyExecutionAuditError(c, http.StatusUnauthorized, infraerrors.ErrExtOperationAuditAuthenticationRequired, nil)
		return false
	}
	userManagement := r.auditUserManagement
	if userManagement == nil {
		userManagement = drivenadapters.NewUserManagementClient()
	}
	user, err := userManagement.GetUserInfo(c.Request.Context(), auth.AccountID, "roles")
	if err != nil || user == nil {
		replyExecutionAuditError(c, http.StatusForbidden, infraerrors.ErrExtOperationAuditAccessDenied, nil)
		return false
	}
	for _, role := range user.Roles {
		if role == "super_admin" || role == "admin" || role == "audit" {
			return true
		}
	}
	replyExecutionAuditError(c, http.StatusForbidden, infraerrors.ErrExtOperationAuditAccessDenied, nil)
	return false
}

func replyExecutionAuditError(c *gin.Context, status int, code infraerrors.ErrorCode, details any) {
	infrarest.ReplyError(c, infraerrors.NewHTTPError(c.Request.Context(), status, code, details))
}

var _ operationAuditQueryStore = (*operationaudit.Store)(nil)
