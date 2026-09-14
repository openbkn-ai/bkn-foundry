package mcp

import (
	"net/http"

	"github.com/creasty/defaults"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

// ParseSSE parses SSE type MCP services.
func (h *mcpHandle) ParseSSE(c *gin.Context) {
	var err error
	req := &interfaces.MCPParseSSERequest{}

	if err = utils.GetBindJSONRaw(c, req); err != nil {
		rest.ReplyError(c, err)
		return
	}

	ctx := c.Request.Context()
	if err = defaults.Set(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	result, err := h.mcpService.ParseSSE(ctx, req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// DebugTool tool debugging.
func (h *mcpHandle) DebugTool(c *gin.Context) {
	var err error
	ctx := c.Request.Context()
	req := &interfaces.MCPToolDebugRequest{}

	if err = c.ShouldBindHeader(req); err != nil {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	if err = c.ShouldBindUri(req); err != nil {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Use map[string]any to receive the request body.
	var body map[string]any
	if err = c.ShouldBindJSON(&body); err != nil {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	req.Parameters = body

	if err = defaults.Set(req); err != nil {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	result, err := h.mcpService.DebugTool(ctx, req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// GetMCPTools query MCP service tool.
func (h *mcpHandle) GetMCPTools(c *gin.Context) {
	var err error
	req := &interfaces.MCPProxyToolListRequest{}

	if err = c.ShouldBindUri(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	if err = c.ShouldBindQuery(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	if err = c.ShouldBindHeader(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	if err = defaults.Set(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	result, err := h.mcpService.GetMCPTools(c.Request.Context(), req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	rest.ReplyOK(c, http.StatusOK, result)
}

// GetMCPToolDefinitionAsProxy reads one MCP tool's invocation contract for a
// managed knowledge-network proxy. The service refuses any request whose
// trusted proxy context was not validated on this route.
func (h *mcpHandle) GetMCPToolDefinitionAsProxy(c *gin.Context) {
	req := &interfaces.MCPToolDefinitionRequest{}
	if err := c.ShouldBindUri(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error()))
		return
	}
	if err := c.ShouldBindQuery(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error()))
		return
	}
	if err := validator.New().Struct(req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	resp, err := h.mcpService.GetMCPToolDefinitionAsProxy(c.Request.Context(), req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// CallMCPTool calls MCP service tool.
func (h *mcpHandle) CallMCPTool(c *gin.Context) {
	var err error
	req := &interfaces.MCPProxyCallToolRequest{}
	if err = c.ShouldBindUri(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	if err = c.ShouldBindHeader(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	if err = c.ShouldBindJSON(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	if err = defaults.Set(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	callToolResp, err := h.mcpService.CallMCPTool(c.Request.Context(), req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, callToolResp)
}
