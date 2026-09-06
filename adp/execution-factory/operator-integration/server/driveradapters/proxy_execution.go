// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	stderrors "errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	proxyexecution "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/proxy_execution"
)

var trustedProxyExecutionHeaders = []string{
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

// stripProxyExecutionHeaders ensures the public Tool and MCP APIs always keep
// their direct-caller behavior even when a client forges internal proxy headers.
func stripProxyExecutionHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		for _, header := range trustedProxyExecutionHeaders {
			c.Request.Header.Del(header)
		}
		c.Next()
	}
}

// managedProxyExecutionBoundary recognizes only ontology-query's trusted
// dual-principal context, rejects every non-execution internal route, and
// passes validated context to the final PEP at the physical outbound boundary.
func managedProxyExecutionBoundary(recorder interfaces.ProxyExecutionAuditRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !hasProxyExecutionContext(c) {
			c.Next()
			return
		}

		request, err := proxyExecutionContextFromHeaders(c)
		if err != nil {
			rejectProxyExecution(c, recorder, request, http.StatusForbidden, "invalid_trusted_context")
			return
		}
		// The caller's platform credential is never a third-party Tool or MCP
		// credential. Configured business headers in the execution body remain
		// available and are sanitized by the existing outbound clients.
		c.Request.Header.Del("Authorization")
		c.Request.Header.Del("X-Authorization")
		c.Request.Header.Del("X-Api-Key")
		c.Request = c.Request.WithContext(interfaces.WithProxyExecutionContext(c.Request.Context(), request))
		c.Next()
	}
}

func hasProxyExecutionContext(c *gin.Context) bool {
	for _, header := range trustedProxyExecutionHeaders {
		if strings.TrimSpace(c.GetHeader(header)) != "" {
			return true
		}
	}
	return false
}

func proxyExecutionContextFromHeaders(c *gin.Context) (interfaces.ProxyExecutionContext, error) {
	request := interfaces.ProxyExecutionContext{
		CallerID:    strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNCallerID)),
		CallerType:  strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNCallerType)),
		KnowledgeID: strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNKnowledgeID)),
		ChildType:   strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNChildType)),
		ChildID:     strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNChildID)),
		ProxyID:     strings.TrimSpace(c.GetHeader(string(interfaces.HeaderXAccountID))),
		ProxyType:   strings.TrimSpace(c.GetHeader(string(interfaces.HeaderXAccountType))),
		TargetType:  strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNTargetType)),
		TargetID:    strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNTargetID)),
		Operation:   strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNOperation)),
		ExecutionID: strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNExecutionID)),
	}
	version, err := strconv.ParseUint(strings.TrimSpace(c.GetHeader(interfaces.HTTPHeaderBKNProxyVersion)), 10, 64)
	request.ProxyVersion = version
	if err != nil || version == 0 {
		return request, stderrors.New("proxy version must be a positive integer")
	}
	if request.CallerID == "" || request.CallerType == "" || request.KnowledgeID == "" ||
		request.ChildType == "" || request.ChildID == "" || request.ProxyID == "" ||
		request.ProxyType != interfaces.ProxyAccountTypeApp ||
		request.Operation != interfaces.ProxyOperationExecute {
		return request, stderrors.New("trusted proxy context is incomplete")
	}
	if request.ChildType == interfaces.ProxyChildTypeAction && request.ExecutionID == "" {
		return request, stderrors.New("action proxy context requires an execution id")
	}

	switch {
	case c.Request.Method == http.MethodPost &&
		strings.HasSuffix(c.FullPath(), "/tool-box/:box_id/proxy/:tool_id"):
		if request.TargetType != interfaces.ProxyTargetTypeToolBox ||
			request.TargetID == "" || request.TargetID != strings.TrimSpace(c.Param("box_id")) ||
			(request.ChildType != interfaces.ProxyChildTypeAction &&
				request.ChildType != interfaces.ProxyChildTypeLogic) {
			return request, stderrors.New("proxy target does not match the Tool execution route")
		}
	case c.Request.Method == http.MethodPost &&
		strings.HasSuffix(c.FullPath(), "/mcp/proxy/:mcp_id/tool/call"):
		if request.TargetType != interfaces.ProxyTargetTypeMCP ||
			request.TargetID == "" || request.TargetID != strings.TrimSpace(c.Param("mcp_id")) ||
			request.ChildType != interfaces.ProxyChildTypeAction {
			return request, stderrors.New("proxy target does not match the MCP execution route")
		}
	default:
		return request, stderrors.New("managed proxies may use only Tool or MCP execution routes")
	}
	return request, nil
}

func rejectProxyExecution(
	c *gin.Context,
	recorder interfaces.ProxyExecutionAuditRecorder,
	request interfaces.ProxyExecutionContext,
	status int,
	reason string,
) {
	proxyexecution.RecordDecision(c.Request.Context(), recorder, request, "deny", reason)
	c.Abort()
	rest.ReplyError(c, oerrors.DefaultHTTPError(c.Request.Context(), status, reason))
}
