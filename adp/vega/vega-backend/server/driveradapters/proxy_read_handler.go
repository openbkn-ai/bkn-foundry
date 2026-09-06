// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/trace"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

var trustedProxyContextHeaders = []string{
	interfaces.HTTPHeaderBKNCallerID,
	interfaces.HTTPHeaderBKNCallerType,
	interfaces.HTTPHeaderBKNKnowledgeID,
	interfaces.HTTPHeaderBKNChildType,
	interfaces.HTTPHeaderBKNChildID,
	interfaces.HTTPHeaderBKNProxyVersion,
	interfaces.HTTPHeaderBKNTargetType,
	interfaces.HTTPHeaderBKNTargetID,
	interfaces.HTTPHeaderBKNOperation,
	interfaces.HTTPHeaderBKNExecutionID,
}

type proxyReadAuditEvent struct {
	CallerID       string `json:"caller_id"`
	CallerType     string `json:"caller_type"`
	KnowledgeID    string `json:"kn_id"`
	ChildType      string `json:"kn_child_type"`
	ChildID        string `json:"kn_child_id"`
	ProxyAccountID string `json:"proxy_account_id"`
	ProxyVersion   uint64 `json:"proxy_version"`
	ResourceType   string `json:"target_resource_type"`
	ResourceID     string `json:"target_resource_id"`
	Operation      string `json:"operation"`
	ExecutionID    string `json:"execution_id,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
	Decision       string `json:"decision"`
	Reason         string `json:"reason,omitempty"`
}

type proxyReadAuditRecorder interface {
	RecordProxyRead(context.Context, proxyReadAuditEvent)
}

type proxyReadAuditLogger struct{}

func (proxyReadAuditLogger) RecordProxyRead(_ context.Context, event proxyReadAuditEvent) {
	encoded, err := sonic.MarshalString(event)
	if err != nil {
		logger.Errorf("marshal proxy read audit failed: %v", err)
		return
	}
	logger.Infof("proxy read authorization audit: %s", encoded)
}

// stripProxyInternalHeaders removes trusted BKN context from public routes while
// preserving X-Account-ID/Type for the existing AUTH_ENABLED=false caller flow.
func (r *restHandler) stripProxyInternalHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		for _, header := range trustedProxyContextHeaders {
			c.Request.Header.Del(header)
		}
		c.Next()
	}
}

// GetResourceSchemaByProxy returns the minimal schema view needed by ontology-query.
func (r *restHandler) GetResourceSchemaByProxy(c *gin.Context) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	request, err := proxyReadContextFromHeaders(c, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if !r.authorizeProxyRead(c, ctx, request, err) {
		return
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: request.ProxyID, Type: request.ProxyType})

	resource, err := r.rs.InternalGetByID(ctx, nil, request.TargetID)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_Resource_InternalError_GetFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if resource == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	schema := resource.SchemaDefinition
	if schema == nil {
		schema = []*interfaces.Property{}
	}
	emitResourceReadEvidence(c, ctx, "data.resource.schema", []*interfaces.Resource{resource}, 1, map[string]string{"resource_id": resource.ID})
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{"schema_definition": schema})
}

// PostResourceDataByProxy accepts only Override: GET and cannot dispatch to write or raw SQL paths.
func (r *restHandler) PostResourceDataByProxy(c *gin.Context) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	request, err := proxyReadContextFromHeaders(c, interfaces.OPERATION_TYPE_QUERY_DATA)
	if err == nil && !strings.EqualFold(strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE)), http.MethodGet) {
		err = fmt.Errorf("proxy data endpoint only accepts X-HTTP-Method-Override: GET")
	}
	if !r.authorizeProxyRead(c, ctx, request, err) {
		return
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: request.ProxyID, Type: request.ProxyType})
	r.queryProxyResourceData(c, ctx, span)
}

func proxyReadContextFromHeaders(c *gin.Context, expectedOperation string) (interfaces.ProxyReadContext, error) {
	request := interfaces.ProxyReadContext{
		CallerID:    strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNCallerID)),
		CallerType:  strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNCallerType)),
		KnowledgeID: strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNKnowledgeID)),
		ChildType:   strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNChildType)),
		ChildID:     strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNChildID)),
		ProxyID:     strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_ACCOUNT_ID)),
		ProxyType:   strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_ACCOUNT_TYPE)),
		TargetType:  strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNTargetType)),
		TargetID:    strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNTargetID)),
		Operation:   strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNOperation)),
		ExecutionID: strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNExecutionID)),
	}
	version, err := strconv.ParseUint(strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNProxyVersion)), 10, 64)
	request.ProxyVersion = version
	if err != nil || version == 0 {
		return request, fmt.Errorf("proxy version must be a positive integer")
	}
	if request.CallerID == "" || request.CallerType == "" || request.KnowledgeID == "" ||
		request.ChildType == "" || request.ChildID == "" || request.ProxyID == "" {
		return request, fmt.Errorf("trusted proxy context is incomplete")
	}
	if request.ProxyType != interfaces.ProxyAccountTypeApp || request.TargetType != interfaces.ProxyTargetTypeResource ||
		request.TargetID == "" || request.TargetID != strings.TrimSpace(c.Param("id")) || request.Operation != expectedOperation {
		return request, fmt.Errorf("proxy target does not match the requested resource operation")
	}
	if !proxyChildMayRead(request.ChildType, expectedOperation) {
		return request, fmt.Errorf("knowledge-network child cannot perform the requested resource operation")
	}
	return request, nil
}

func proxyChildMayRead(childType, operation string) bool {
	switch operation {
	case interfaces.OPERATION_TYPE_VIEW_DETAIL:
		return childType == interfaces.ProxyChildTypeObjectType
	case interfaces.OPERATION_TYPE_QUERY_DATA:
		return childType == interfaces.ProxyChildTypeObjectType ||
			childType == interfaces.ProxyChildTypeRelationType ||
			childType == interfaces.ProxyChildTypeMetric
	default:
		return false
	}
}

func (r *restHandler) authorizeProxyRead(
	c *gin.Context, ctx context.Context, request interfaces.ProxyReadContext, contextErr error,
) bool {
	if contextErr != nil {
		r.recordProxyReadAudit(ctx, request, "deny", "invalid_trusted_context")
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden))
		return false
	}
	if r.pas == nil {
		r.recordProxyReadAudit(ctx, request, "deny", "authorization_unavailable")
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusServiceUnavailable, rest.PublicError_ServiceUnavailable))
		return false
	}
	err := r.pas.Authorize(ctx, request)
	if err == nil {
		r.recordProxyReadAudit(ctx, request, "allow", "")
		return true
	}
	if errors.Is(err, interfaces.ErrProxyAuthorizationDenied) {
		r.recordProxyReadAudit(ctx, request, "deny", "proxy_or_policy_denied")
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden))
		return false
	}
	r.recordProxyReadAudit(ctx, request, "deny", "authorization_unavailable")
	rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusServiceUnavailable, rest.PublicError_ServiceUnavailable))
	return false
}

func (r *restHandler) recordProxyReadAudit(ctx context.Context, request interfaces.ProxyReadContext, decision, reason string) {
	recorder := r.proxyAuditRecorder
	if recorder == nil {
		recorder = proxyReadAuditLogger{}
	}
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	spanContext := trace.SpanContextFromContext(ctx)
	traceID := ""
	if spanContext.IsValid() {
		traceID = spanContext.TraceID().String()
	}
	recorder.RecordProxyRead(ctx, proxyReadAuditEvent{
		CallerID:       request.CallerID,
		CallerType:     request.CallerType,
		KnowledgeID:    request.KnowledgeID,
		ChildType:      request.ChildType,
		ChildID:        request.ChildID,
		ProxyAccountID: request.ProxyID,
		ProxyVersion:   request.ProxyVersion,
		ResourceType:   request.TargetType,
		ResourceID:     request.TargetID,
		Operation:      request.Operation,
		ExecutionID:    request.ExecutionID,
		RequestID:      traceContext.RequestID,
		TraceID:        traceID,
		Decision:       decision,
		Reason:         reason,
	})
}
