// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common/operationaudit"
	visitor2 "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common/visitor"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

const operationAuditVisitorKey = "vega.operation_audit.visitor"
const operationAuditRequestKey = "vega.operation_audit.request_id"
const operationAuditTargetIDKey = "vega.operation_audit.target_id"
const maximumOperationAuditRequestBody = 64 << 10

var operationAuditFieldName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,127}$`)

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
		if !ok && operationAuditSourceChannel(c.FullPath()) == "internal_api" {
			// Internal handlers authorize against the forwarded account headers.
			// Use that same identity for Audit, never a client-provided body field.
			actor = visitor2.GenerateVisitor(c)
			ok = true
		}
		if !ok || strings.TrimSpace(actor.ID) == "" {
			logger.Errorf("operation audit fact rejected: action=%s target_type=%s missing verified actor", rule.Action, rule.TargetType)
			return
		}
		requestID, err := operationAuditRequestID(c)
		if err != nil {
			logger.Errorf("operation audit request ID generation failed: action=%s target_type=%s error=%v", rule.Action, rule.TargetType, err)
			return
		}
		attemptID, err := uuid.NewV7()
		if err != nil {
			logger.Errorf("operation audit event ID generation failed: action=%s target_type=%s error=%v", rule.Action, rule.TargetType, err)
			return
		}
		now := time.Now().UTC()
		targetID, targetName := operationAuditTarget(c, rule.TargetType, request, requestID)
		outcome, failureCode, failureMessage := operationAuditOutcome(c)
		entry := operationaudit.Entry{
			EventID:        attemptID.String(),
			EventTime:      now,
			RecordedAt:     now,
			ActorID:        actor.ID,
			ActorName:      actor.ID,
			ActorType:      firstNonEmpty(string(actor.Type), "user"),
			AuthMethod:     operationAuditAuthMethod(c.FullPath(), c.GetHeader("Authorization")),
			RequestID:      operationAuditCorrelationID(requestID),
			SourceChannel:  "api",
			Method:         c.Request.Method,
			HTTPStatus:     c.Writer.Status(),
			Action:         rule.Action,
			TargetType:     rule.TargetType,
			TargetID:       targetID,
			TargetName:     targetName,
			Outcome:        outcome,
			FailureCode:    failureCode,
			FailureMessage: failureMessage,
			ChangedFields:  operationAuditChangedFields(request),
		}
		if err := r.auditRecorder.Record(c.Request.Context(), entry); err != nil {
			logger.Errorf("operation audit persistence failed: request_id=%s action=%s target_type=%s error=%v", requestID, rule.Action, rule.TargetType, err)
		}
	}
}

func operationAuditChangedFields(request map[string]any) []string {
	fields := make([]string, 0, len(request))
	for name := range request {
		if !operationAuditFieldName.MatchString(name) {
			continue
		}
		fields = append(fields, name)
	}
	sort.Strings(fields)
	if len(fields) > 100 {
		fields = fields[:100]
	}
	return fields
}

func operationAuditCorrelationID(requestID string) string {
	if len(requestID) <= 128 {
		return requestID
	}
	// A deterministic alias keeps oversized IDs within the frozen Audit record.
	digest := sha256.Sum256([]byte(requestID))
	return "req_" + hex.EncodeToString(digest[:])
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
	if common.UnmarshalPreciseJSON(body, &value) != nil {
		return nil
	}
	return value
}

func operationAuditTarget(c *gin.Context, targetType string, request map[string]any, requestID string) (string, string) {
	targetID := strings.TrimSpace(firstNonEmpty(c.Param("id"), c.Param("ids"), c.Param("type")))
	if createdID, exists := c.Get(operationAuditTargetIDKey); exists {
		if id, ok := createdID.(string); ok && strings.TrimSpace(id) != "" {
			targetID = strings.TrimSpace(id)
		}
	}
	if targetID == "" {
		targetID = targetType + ":" + requestID
	}
	if strings.Contains(targetID, ",") {
		digest := sha256.Sum256([]byte(targetID))
		return "batch:" + hex.EncodeToString(digest[:]), fmt.Sprintf("%d targets", strings.Count(targetID, ",")+1)
	}
	if len(targetID) > 256 {
		digest := sha256.Sum256([]byte(targetID))
		targetID = "sha256:" + hex.EncodeToString(digest[:])
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

func operationAuditRequestID(c *gin.Context) (string, error) {
	if value, exists := c.Get(operationAuditRequestKey); exists {
		if requestID, ok := value.(string); ok && requestID != "" {
			return requestID, nil
		}
	}
	if traceContext, ok := common.GetTraceContextFromCtx(c.Request.Context()); ok && strings.TrimSpace(traceContext.RequestID) != "" {
		return rememberOperationAuditRequestID(c, strings.TrimSpace(traceContext.RequestID)), nil
	}
	if value := strings.TrimSpace(c.GetHeader(common.HeaderBKNRequestID)); value != "" {
		return rememberOperationAuditRequestID(c, value), nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate operation audit request UUIDv7: %w", err)
	}
	return rememberOperationAuditRequestID(c, "req_"+id.String()), nil
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

func operationAuditAuthMethod(path, authorization string) string {
	if operationAuditSourceChannel(path) == "internal_api" {
		return "internal_forwarded_header"
	}
	if strings.Contains(strings.ToLower(authorization), "bak_") {
		return "api_key"
	}
	if strings.TrimSpace(authorization) != "" {
		return "oauth"
	}
	return "unknown"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
