// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/rs/xid"

	"vega-backend/common"
	"vega-backend/common/operationaudit"
	"vega-backend/interfaces"
)

const operationAuditVisitorKey = "vega.operation_audit.visitor"
const operationAuditRequestKey = "vega.operation_audit.request_id"
const maximumOperationAuditRequestBody = 64 << 10

type operationAuditRecorder interface {
	Record(context.Context, operationaudit.Entry) error
}

// OperationAudit persists one minimal management fact after a registered request.
// It deliberately excludes raw request bodies, connector settings and result data.
func (r *restHandler) OperationAudit() gin.HandlerFunc {
	return func(c *gin.Context) {
		rule, ok := registeredOperationAudit(c.Request.Method, c.FullPath(), c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE))
		if !ok {
			c.Next()
			return
		}
		request := captureOperationAuditRequest(c.Request)
		c.Next()
		if r == nil || r.auditRecorder == nil {
			return
		}

		visitor, _ := c.Get(operationAuditVisitorKey)
		actor, ok := visitor.(hydra.Visitor)
		if !ok || strings.TrimSpace(actor.ID) == "" {
			logger.Errorf("operation audit fact rejected: action=%s target_type=%s missing verified actor", rule.Action, rule.TargetType)
			return
		}
		tenantID := strings.TrimSpace(c.GetHeader("x-tenant-id"))
		businessDomainID := strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_BUSINESS_DOMAIN))
		requestID := operationAuditRequestID(c)
		if tenantID == "" || businessDomainID == "" {
			logger.Errorf("operation audit fact rejected: request_id=%s action=%s missing tenant or business domain", requestID, rule.Action)
			return
		}
		actorName := operationAuditActorName(c.Request.Context(), c.GetHeader("Authorization"), actor.ID)
		if actorName == "" {
			actorName = actor.ID
		}
		now := time.Now().UTC()
		targetID, targetName := operationAuditTarget(c, rule.TargetType, request, requestID)
		outcome, failureCode, failureMessage := operationAuditOutcome(c)
		entry := operationaudit.Entry{
			EventID: operationaudit.EventID(tenantID, requestID, c.Request.Method, c.FullPath()), EventTime: now, RecordedAt: now,
			TenantID: tenantID, BusinessDomainID: businessDomainID,
			ActorID: actor.ID, ActorName: actorName, ActorType: firstNonEmpty(string(actor.Type), "user"), AuthMethod: operationAuditAuthMethod(c.GetHeader("Authorization")),
			RequestID: requestID, SourceChannel: operationAuditSourceChannel(c.FullPath()), Method: c.Request.Method,
			Action: rule.Action, TargetType: rule.TargetType, TargetID: targetID, TargetName: targetName,
			Outcome: outcome, FailureCode: failureCode, FailureMessage: failureMessage,
		}
		if err := r.auditRecorder.Record(c.Request.Context(), entry); err != nil {
			logger.Errorf("operation audit persistence failed: request_id=%s action=%s target_type=%s error=%v", requestID, rule.Action, rule.TargetType, err)
		}
	}
}

func captureOperationAuditRequest(request *http.Request) map[string]any {
	if request == nil || request.Body == nil {
		return nil
	}
	// A declared oversized body is not audit input. Leave it untouched for the
	// business handler instead of consuming any of its stream.
	if request.ContentLength > maximumOperationAuditRequestBody {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumOperationAuditRequestBody+1))
	remaining := request.Body
	// For chunked requests the size is not known up front. Reassemble the
	// consumed prefix and unread suffix before returning, including on the
	// oversized path, so audit collection cannot change handler semantics.
	request.Body = struct {
		io.Reader
		io.Closer
	}{Reader: io.MultiReader(bytes.NewReader(body), remaining), Closer: remaining}
	if err != nil || len(body) > maximumOperationAuditRequestBody {
		return nil
	}
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return nil
	}
	return value
}

func operationAuditTarget(c *gin.Context, targetType string, request map[string]any, requestID string) (string, string) {
	targetID := strings.TrimSpace(firstNonEmpty(c.Param("id"), c.Param("ids")))
	if targetID == "" {
		targetID = targetType + ":" + requestID
	}
	name := ""
	if request != nil {
		for _, key := range []string{"name", "display_name", "catalog_name", "resource_name"} {
			if value, ok := request[key].(string); ok && strings.TrimSpace(value) != "" {
				name = strings.TrimSpace(value)
				break
			}
		}
	}
	if name == "" {
		name = targetID
	}
	return targetID, name
}

func operationAuditOutcome(c *gin.Context) (string, string, string) {
	status := c.Writer.Status()
	if status >= http.StatusOK && status < http.StatusBadRequest {
		return "success", "", ""
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "denied", fmt.Sprintf("http_%d", status), "request denied"
	}
	return "failure", fmt.Sprintf("http_%d", status), "management request failed"
}

func operationAuditRequestID(c *gin.Context) string {
	if value, exists := c.Get(operationAuditRequestKey); exists {
		if requestID, ok := value.(string); ok && requestID != "" {
			return requestID
		}
	}
	if traceContext, ok := common.GetTraceContextFromCtx(c.Request.Context()); ok && strings.TrimSpace(traceContext.RequestID) != "" {
		return rememberOperationAuditRequestID(c, strings.TrimSpace(traceContext.RequestID))
	}
	if value := strings.TrimSpace(c.GetHeader(common.HeaderBKNRequestID)); value != "" {
		return rememberOperationAuditRequestID(c, value)
	}
	return rememberOperationAuditRequestID(c, "req_"+xid.New().String())
}

func rememberOperationAuditRequestID(c *gin.Context, requestID string) string {
	c.Request.Header.Set(common.HeaderBKNRequestID, requestID)
	c.Header(common.HeaderBKNRequestID, requestID)
	c.Set(operationAuditRequestKey, requestID)
	return requestID
}

func operationAuditSourceChannel(path string) string {
	if strings.Contains(path, "/in/") {
		return "internal_api"
	}
	return "api"
}

func operationAuditAuthMethod(authorization string) string {
	if strings.Contains(strings.ToLower(authorization), "bak_") {
		return "api_key"
	}
	if strings.TrimSpace(authorization) != "" {
		return "oauth"
	}
	return "unknown"
}

func operationAuditActorName(ctx context.Context, authorization, expectedActorID string) string {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_SAFE_URL")), "/")
	if baseURL == "" || strings.TrimSpace(authorization) == "" {
		return ""
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/safe/v1/me", nil)
	if err != nil {
		return ""
	}
	request.Header.Set("Authorization", authorization)
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	var me struct {
		ID, Account, Name string
		Enabled           bool
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&me) != nil || !me.Enabled || me.ID != expectedActorID {
		return ""
	}
	return firstNonEmpty(strings.TrimSpace(me.Name), strings.TrimSpace(me.Account))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
