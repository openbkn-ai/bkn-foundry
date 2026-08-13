// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"vega-backend/common/operationaudit"
	"vega-backend/interfaces"
)

const maximumOperationAuditRange = 30 * 24 * time.Hour

type operationAuditQueryStore interface {
	List(ctx context.Context, filter operationaudit.Filter) (operationaudit.Page, error)
	Get(ctx context.Context, eventID, tenantID, businessDomain string) (operationaudit.Entry, bool, error)
}

func (r *restHandler) ListOperationAudits(c *gin.Context) {
	if !r.requireOperationAuditReader(c) {
		return
	}
	from, to, ok := operationAuditRange(c)
	if !ok {
		return
	}
	if r.auditQueryStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "operation audit source is unavailable"})
		return
	}
	filter := operationaudit.Filter{TenantID: strings.TrimSpace(c.GetHeader("x-tenant-id")), BusinessDomain: strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_BUSINESS_DOMAIN)), From: from, To: to}
	filter.ActorID = strings.TrimSpace(c.Query("actor_id"))
	filter.Action = strings.TrimSpace(c.Query("action"))
	filter.TargetType = strings.TrimSpace(c.Query("target_type"))
	filter.TargetID = strings.TrimSpace(c.Query("target_id"))
	filter.Outcome = strings.TrimSpace(c.Query("outcome"))
	if filter.TenantID == "" || filter.BusinessDomain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "x-tenant-id and x-business-domain are required"})
		return
	}
	if limit, err := strconv.Atoi(strings.TrimSpace(c.Query("limit"))); err == nil {
		filter.Limit = limit
	}
	if before := strings.TrimSpace(c.Query("before_time")); before != "" {
		parsed, err := time.Parse(time.RFC3339Nano, before)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "before_time must be RFC3339"})
			return
		}
		filter.BeforeTime, filter.BeforeEventID = parsed.UTC(), strings.TrimSpace(c.Query("before_event_id"))
	}
	page, err := r.auditQueryStore.List(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation audit query failed"})
		return
	}
	response := gin.H{"entries": operationAuditResponses(page.Entries), "has_more": page.HasMore}
	if page.HasMore && len(page.Entries) > 0 {
		last := page.Entries[len(page.Entries)-1]
		response["next_before_time"] = last.EventTime.UTC().Format(time.RFC3339Nano)
		response["next_before_event_id"] = last.EventID
	}
	c.JSON(http.StatusOK, response)
}

func (r *restHandler) GetOperationAudit(c *gin.Context) {
	if !r.requireOperationAuditReader(c) {
		return
	}
	if r.auditQueryStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "operation audit source is unavailable"})
		return
	}
	tenantID, businessDomain := strings.TrimSpace(c.GetHeader("x-tenant-id")), strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_BUSINESS_DOMAIN))
	if tenantID == "" || businessDomain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "x-tenant-id and x-business-domain are required"})
		return
	}
	entry, found, err := r.auditQueryStore.Get(c.Request.Context(), strings.TrimSpace(c.Param("event_id")), tenantID, businessDomain)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation audit query failed"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "operation audit event not found"})
		return
	}
	c.JSON(http.StatusOK, operationAuditResponse(entry))
}

func operationAuditRange(c *gin.Context) (time.Time, time.Time, bool) {
	from, fromErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(c.Query("from")))
	to, toErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(c.Query("to")))
	if fromErr != nil || toErr != nil || !from.Before(to) || to.Sub(from) > maximumOperationAuditRange {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from/to must be a valid RFC3339 range of at most 30 days"})
		return time.Time{}, time.Time{}, false
	}
	return from.UTC(), to.UTC(), true
}

func (r *restHandler) requireOperationAuditReader(c *gin.Context) bool {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return false
	}
	if !operationAuditReader(c.Request.Context(), c.GetHeader("Authorization"), visitor.ID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "operation audit access denied"})
		return false
	}
	return true
}

func operationAuditReader(ctx context.Context, authorization, expectedActorID string) bool {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_SAFE_URL")), "/")
	if baseURL == "" || strings.TrimSpace(authorization) == "" || strings.TrimSpace(expectedActorID) == "" {
		return false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/safe/v1/me", nil)
	if err != nil {
		return false
	}
	request.Header.Set("Authorization", authorization)
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	var me struct {
		ID      string
		Enabled bool
		Roles   []string
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&me) != nil || !me.Enabled || me.ID != expectedActorID {
		return false
	}
	for _, role := range me.Roles {
		if role == "super_admin" || role == "admin" || role == "audit" {
			return true
		}
	}
	return false
}

type operationAuditResponseDTO struct {
	EventID          string `json:"event_id"`
	EventTime        string `json:"event_time"`
	RecordedAt       string `json:"recorded_at"`
	TenantID         string `json:"tenant_id"`
	BusinessDomainID string `json:"business_domain_id"`
	ActorID          string `json:"actor_id"`
	ActorName        string `json:"actor_name"`
	ActorType        string `json:"actor_type"`
	AuthMethod       string `json:"auth_method"`
	RequestID        string `json:"request_id"`
	SourceChannel    string `json:"source_channel"`
	Method           string `json:"method"`
	Action           string `json:"action"`
	TargetType       string `json:"target_type"`
	TargetID         string `json:"target_id"`
	TargetName       string `json:"target_name"`
	Outcome          string `json:"outcome"`
	FailureCode      string `json:"failure_code,omitempty"`
	FailureMessage   string `json:"failure_message,omitempty"`
	SchemaVersion    string `json:"schema_version"`
}

func operationAuditResponses(entries []operationaudit.Entry) []operationAuditResponseDTO {
	result := make([]operationAuditResponseDTO, 0, len(entries))
	for _, entry := range entries {
		result = append(result, operationAuditResponse(entry))
	}
	return result
}
func operationAuditResponse(entry operationaudit.Entry) operationAuditResponseDTO {
	return operationAuditResponseDTO{EventID: entry.EventID, EventTime: entry.EventTime.UTC().Format(time.RFC3339Nano), RecordedAt: entry.RecordedAt.UTC().Format(time.RFC3339Nano), TenantID: entry.TenantID, BusinessDomainID: entry.BusinessDomainID, ActorID: entry.ActorID, ActorName: entry.ActorName, ActorType: entry.ActorType, AuthMethod: entry.AuthMethod, RequestID: entry.RequestID, SourceChannel: entry.SourceChannel, Method: entry.Method, Action: entry.Action, TargetType: entry.TargetType, TargetID: entry.TargetID, TargetName: entry.TargetName, Outcome: entry.Outcome, FailureCode: entry.FailureCode, FailureMessage: entry.FailureMessage, SchemaVersion: "1.0"}
}

var _ operationAuditQueryStore = (*operationaudit.Store)(nil)
