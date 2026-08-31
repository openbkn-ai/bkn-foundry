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
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/common/bkntrace"
	"bkn-backend/common/operationaudit"
	berrors "bkn-backend/errors"
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
		replyOperationAuditError(c, http.StatusServiceUnavailable, berrors.BknBackend_OperationAudit_ServiceUnavailable, nil)
		return
	}
	filter := operationaudit.Filter{
		From:       from,
		To:         to,
		ActorID:    strings.TrimSpace(c.Query("actor_id")),
		Action:     strings.TrimSpace(c.Query("action")),
		TargetType: strings.TrimSpace(c.Query("target_type")),
		TargetID:   strings.TrimSpace(c.Query("target_id")),
		Outcome:    strings.TrimSpace(c.Query("outcome")),
	}
	requestedActor := filter.ActorID
	applyOperationAuditScope(&filter, nil, visitor.ID, profile)
	if requestedActor != "" && filter.ActorID != requestedActor {
		replyOperationAuditError(c, http.StatusForbidden, berrors.BknBackend_OperationAudit_AccessDenied, gin.H{"field": "actor_id"})
		return
	}
	if limit, err := strconv.Atoi(strings.TrimSpace(c.Query("limit"))); err == nil {
		filter.Limit = limit
	}
	if before := strings.TrimSpace(c.Query("before_time")); before != "" {
		parsed, err := time.Parse(time.RFC3339Nano, before)
		if err != nil {
			replyOperationAuditError(c, http.StatusBadRequest, berrors.BknBackend_OperationAudit_InvalidBeforeTime, gin.H{"field": "before_time", "format": "RFC3339"})
			return
		}
		filter.BeforeTime = parsed.UTC()
		filter.BeforeEventID = strings.TrimSpace(c.Query("before_event_id"))
	}
	page, err := r.auditQueryStore.List(c.Request.Context(), filter)
	if err != nil {
		replyOperationAuditError(c, http.StatusInternalServerError, berrors.BknBackend_OperationAudit_QueryFailed, nil)
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
		replyOperationAuditError(c, http.StatusServiceUnavailable, berrors.BknBackend_OperationAudit_ServiceUnavailable, nil)
		return
	}
	scope := operationaudit.Scope{}
	applyOperationAuditScope(nil, &scope, visitor.ID, profile)
	entry, found, err := r.auditQueryStore.Get(c.Request.Context(), strings.TrimSpace(c.Param("event_id")), scope)
	if err != nil {
		replyOperationAuditError(c, http.StatusInternalServerError, berrors.BknBackend_OperationAudit_QueryFailed, nil)
		return
	}
	if !found {
		replyOperationAuditError(c, http.StatusNotFound, berrors.BknBackend_OperationAudit_NotFound, gin.H{"event_id": strings.TrimSpace(c.Param("event_id"))})
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
		replyOperationAuditError(c, http.StatusServiceUnavailable, berrors.BknBackend_OperationAudit_ServiceUnavailable, nil)
		return hydra.Visitor{}, bkntrace.OperationAuditProfile{}, false
	}
	profile, err = r.auditAccessResolver(c.Request.Context(), c.GetHeader("Authorization"), verified.ID)
	if err != nil {
		status := http.StatusServiceUnavailable
		code := berrors.BknBackend_OperationAudit_ServiceUnavailable
		if errors.Is(err, bkntrace.ErrAccessProfileDenied) {
			status = http.StatusForbidden
			code = berrors.BknBackend_OperationAudit_AccessDenied
		}
		replyOperationAuditError(c, status, code, nil)
		return hydra.Visitor{}, bkntrace.OperationAuditProfile{}, false
	}
	return visitor, profile, true
}

func operationAuditRange(c *gin.Context) (time.Time, time.Time, bool) {
	from, fromErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(c.Query("from")))
	to, toErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(c.Query("to")))
	if fromErr != nil || toErr != nil || !from.Before(to) || to.Sub(from) > maximumOperationAuditRange {
		replyOperationAuditError(c, http.StatusBadRequest, berrors.BknBackend_OperationAudit_InvalidRange, gin.H{
			"fields":       []string{"from", "to"},
			"format":       "RFC3339",
			"max_duration": maximumOperationAuditRange.String(),
		})
		return time.Time{}, time.Time{}, false
	}
	return from.UTC(), to.UTC(), true
}

func replyOperationAuditError(c *gin.Context, status int, code string, details any) {
	err := rest.NewHTTPError(c.Request.Context(), status, code)
	if details != nil {
		err.WithErrorDetails(details)
	}
	rest.ReplyError(c, err)
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
		EventID:            entry.EventID,
		EventTime:          entry.EventTime.UTC().Format(time.RFC3339Nano),
		RecordedAt:         entry.RecordedAt.UTC().Format(time.RFC3339Nano),
		KnowledgeNetworkID: entry.KnowledgeNetworkID,
		ActorID:            entry.ActorID,
		ActorName:          entry.ActorName,
		ActorType:          entry.ActorType,
		AuthMethod:         entry.AuthMethod,
		CredentialID:       entry.CredentialID,
		RequestID:          entry.RequestID,
		SourceChannel:      entry.SourceChannel,
		Method:             entry.Method,
		Action:             entry.Action,
		TargetType:         entry.TargetType,
		TargetID:           entry.TargetID,
		TargetName:         entry.TargetName,
		Outcome:            entry.Outcome,
		FailureCode:        entry.FailureCode,
		FailureMessage:     entry.FailureMessage,
		ChangeSummary:      entry.ChangeSummary,
		SchemaVersion:      entry.SchemaVersion,
	}
}
