package mcp

import (
	"net/http"

	"github.com/creasty/defaults"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// QueryMCPServerMarketList Query MCP service market list.
func (h *mcpHandle) QueryMCPServerMarketList(c *gin.Context) {
	var err error
	req := &interfaces.MCPServerReleaseListRequest{}

	ctx := c.Request.Context()

	if err = c.ShouldBindHeader(req); err != nil {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	if err = c.ShouldBindQuery(req); err != nil {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
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
	var result *interfaces.MCPServerReleaseListResponse

	// Query the MCP Server list.
	result, err = h.mcpService.QueryRelease(ctx, req)

	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// QueryMCPServerMarketDetail Query MCP service market details.
func (h *mcpHandle) QueryMCPServerMarketDetail(c *gin.Context) {
	var err error
	ctx := c.Request.Context()

	req := &interfaces.MCPServerReleaseDetailRequest{}

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

	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	result, err := h.mcpService.GetReleaseDetail(ctx, req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// QueryMCPServerMarketBatch Query MCP service market details in batches.
func (h *mcpHandle) QueryMCPServerMarketBatch(c *gin.Context) {
	var err error
	ctx := c.Request.Context()

	req := &interfaces.MCPServerReleaseBatchRequest{}

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

	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	result, err := h.mcpService.QueryReleaseBatch(ctx, req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}
