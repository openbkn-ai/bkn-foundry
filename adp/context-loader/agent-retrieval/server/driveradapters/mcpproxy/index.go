// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package mcpproxy provides HTTP proxy handler for MCP tool invocations.
package mcpproxy

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type MCPProxyHandler interface {
	CallMCPTool(c *gin.Context)
}

type mcpProxyHandler struct {
	operatorIntegration interfaces.DrivenOperatorIntegration
}

func NewMCPProxyHandler() MCPProxyHandler {
	return &mcpProxyHandler{
		operatorIntegration: drivenadapters.NewOperatorIntegrationClient(),
	}
}

// CallMCPTool agent calls the MCP tool.
func (h *mcpProxyHandler) CallMCPTool(c *gin.Context) {
	ctx := c.Request.Context()

	// 1. getpathparameter.
	mcpID := c.Param("mcp_id")
	toolName := c.Param("tool_name")

	if mcpID == "" || toolName == "" {
		rest.ReplyError(c, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest, "mcp_id and tool_name are required"))
		return
	}

	// 2. Parse the request body (flattened parameters)
	var parameters map[string]interface{}
	if err := common.BindPreciseJSON(c.Request.Body, &parameters); err != nil {
		// Allow empty parameters {}
		if err.Error() == "EOF" {
			parameters = make(map[string]interface{})
		} else {
			rest.ReplyError(c, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest, "invalid request body"))
			return
		}
	}

	// 3. Call OperatorIntegration.
	req := &interfaces.CallMCPToolRequest{
		McpID:      mcpID,
		ToolName:   toolName,
		Parameters: parameters,
	}

	resp, err := h.operatorIntegration.CallMCPTool(ctx, req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	// 4. Return results.
	rest.ReplyOK(c, http.StatusOK, resp)
}
