// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/openbkn-ai/bkn-comm-go/rest"

	"bkn-backend/common/bkntrace"
	"bkn-backend/common/operationaudit"
	"bkn-backend/interfaces"
)

const maximumOperationAuditRange = 30 * 24 * time.Hour

func (r *restHandler) ListOperationAudits(c *gin.Context) {
	visitor, profile, ok := r.operationAuditQueryProfile(c)
	if !ok {
		return
	}
	from, to, valid := operationAuditRange(c)
	if !valid {
		return
	}
	if r.auditQueryStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "operation audit source is unavailable"})
		return
	}
	filter := operationaudit.Filter{
		TenantID:         strings.TrimSpace(c.GetHeader("x-tenant-id")),
		BusinessDomainID: strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_BUSINESS_DOMAIN)),
		From:             from,
		To:               to,
		ActorID:          strings.TrimSpace(c.Query("actor_id")),
		Action:           strings.TrimSpace(c.Query("action")),
		TargetType:       strings.TrimSpace(c.Query("target_type")),
		TargetID:         strings.TrimSpace(c.Query("target_id")),
		Outcome:          strings.TrimSpace(c.Query("outcome")),
	}
	if filter.TenantID == "" || filter.BusinessDomainID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "x-tenant-id and x-business-domain are required"})
		return
	}
	requestedActor := filter.ActorID
	applyOperationAuditScope(&filter, nil, visitor.ID, profile)
	if requestedActor != "" && filter.ActorID != requestedActor {
		c.JSON(http.StatusForbidden, gin.H{"error": "actor filter is outside the authorized scope"})
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
		filter.BeforeTime = parsed.UTC()
		filter.BeforeEventID = strings.TrimSpace(c.Query("before_event_id"))
	}
	page, err := r.auditQueryStore.List(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation audit query failed"})
		return
	}
	response := gin.H{"entries": operationAuditResponses(page.Entries), "has_more": page.HasMore}
	if page.HasMore {
		response["next_before_time"] = page.NextBeforeTime.UTC().Format(time.RFC3339Nano)
		response["next_before_event_id"] = page.NextBeforeEventID
	}
	c.JSON(http.StatusOK, response)
}

func (r *restHandler) GetOperationAudit(c *gin.Context) {
	visitor, profile, ok := r.operationAuditQueryProfile(c)
	if !ok {
		return
	}
	if r.auditQueryStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "operation audit source is unavailable"})
		return
	}
	scope := operationaudit.Scope{
		TenantID:         strings.TrimSpace(c.GetHeader("x-tenant-id")),
		BusinessDomainID: strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_BUSINESS_DOMAIN)),
	}
	if scope.TenantID == "" || scope.BusinessDomainID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "x-tenant-id and x-business-domain are required"})
		return
	}
	applyOperationAuditScope(nil, &scope, visitor.ID, profile)
	entry, found, err := r.auditQueryStore.Get(c.Request.Context(), strings.TrimSpace(c.Param("event_id")), scope)
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

func (r *restHandler) operationAuditQueryProfile(c *gin.Context) (visitor hydra.Visitor, profile bkntrace.OperationAuditProfile, ok bool) {
	verified, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return hydra.Visitor{}, bkntrace.OperationAuditProfile{}, false
	}
	visitor = verified
	if r.auditAccessResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "operation audit authorization is unavailable"})
		return hydra.Visitor{}, bkntrace.OperationAuditProfile{}, false
	}
	profile, err = r.auditAccessResolver(c.Request.Context(), c.GetHeader("Authorization"), verified.ID)
	if err != nil {
		status := http.StatusServiceUnavailable
		message := "operation audit authorization is unavailable"
		if errors.Is(err, bkntrace.ErrAccessProfileDenied) {
			status = http.StatusForbidden
			message = "operation audit access denied"
		}
		c.JSON(status, gin.H{"error": message})
		return hydra.Visitor{}, bkntrace.OperationAuditProfile{}, false
	}
	return visitor, profile, true
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

func applyOperationAuditScope(filter *operationaudit.Filter, scope *operationaudit.Scope, actorID string, profile bkntrace.OperationAuditProfile) {
	if profile.IsAdmin() {
		return
	}
	if len(profile.ManagedKnowledgeNetworkIDs) > 0 {
		if filter != nil {
			filter.KnowledgeNetworkIDs = append([]string(nil), profile.ManagedKnowledgeNetworkIDs...)
		}
		if scope != nil {
			scope.KnowledgeNetworkIDs = append([]string(nil), profile.ManagedKnowledgeNetworkIDs...)
		}
		return
	}
	if filter != nil {
		filter.ActorID = actorID
	}
	if scope != nil {
		scope.ActorID = actorID
	}
}

type operationAuditResponseDTO struct {
	EventID            string         `json:"event_id"`
	EventTime          string         `json:"event_time"`
	RecordedAt         string         `json:"recorded_at"`
	TenantID           string         `json:"tenant_id"`
	BusinessDomainID   string         `json:"business_domain_id"`
	KnowledgeNetworkID string         `json:"knowledge_network_id"`
	ActorID            string         `json:"actor_id"`
	ActorName          string         `json:"actor_name"`
	ActorType          string         `json:"actor_type"`
	AuthMethod         string         `json:"auth_method"`
	CredentialID       string         `json:"credential_id,omitempty"`
	RequestID          string         `json:"request_id"`
	SourceChannel      string         `json:"source_channel"`
	Method             string         `json:"method"`
	Action             string         `json:"action"`
	TargetType         string         `json:"target_type"`
	TargetID           string         `json:"target_id"`
	TargetName         string         `json:"target_name"`
	Outcome            string         `json:"outcome"`
	FailureCode        string         `json:"failure_code,omitempty"`
	FailureMessage     string         `json:"failure_message,omitempty"`
	ChangeSummary      map[string]any `json:"change_summary"`
	SchemaVersion      string         `json:"schema_version"`
}

func operationAuditResponses(entries []operationaudit.Entry) []operationAuditResponseDTO {
	result := make([]operationAuditResponseDTO, 0, len(entries))
	for _, entry := range entries {
		result = append(result, operationAuditResponse(entry))
	}
	return result
}

func operationAuditResponse(entry operationaudit.Entry) operationAuditResponseDTO {
	return operationAuditResponseDTO{
		EventID: entry.EventID, EventTime: entry.EventTime.UTC().Format(time.RFC3339Nano), RecordedAt: entry.RecordedAt.UTC().Format(time.RFC3339Nano),
		TenantID: entry.TenantID, BusinessDomainID: entry.BusinessDomainID, KnowledgeNetworkID: entry.KnowledgeNetworkID,
		ActorID: entry.ActorID, ActorName: entry.ActorName, ActorType: entry.ActorType, AuthMethod: entry.AuthMethod, CredentialID: entry.CredentialID,
		RequestID: entry.RequestID, SourceChannel: entry.SourceChannel, Method: entry.Method, Action: entry.Action,
		TargetType: entry.TargetType, TargetID: entry.TargetID, TargetName: entry.TargetName, Outcome: entry.Outcome,
		FailureCode: entry.FailureCode, FailureMessage: entry.FailureMessage, ChangeSummary: entry.ChangeSummary, SchemaVersion: entry.SchemaVersion,
	}
}
