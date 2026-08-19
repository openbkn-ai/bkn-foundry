package mcp

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func (h *mcpHandle) HandleStreamingHttp(c *gin.Context) {
	var err error
	req := &interfaces.MCPAppEndpointRequest{}

	if err = c.ShouldBindUri(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	result, err := h.mcpService.GetMCPInstanceConfig(c.Request.Context(), req.MCPID, interfaces.MCPModeStream)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	// Obtain and serve directly from the local instance pool; increase the active count during the connection survival period to avoid instances being eliminated by LRU/TTL.
	instance, err := h.mcpInstance.GetMCPInstance(c.Request.Context(), result.MCPID, result.Version)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	atomic.AddInt64(&instance.ActiveStreamConn, 1)
	defer atomic.AddInt64(&instance.ActiveStreamConn, -1)
	instance.StreamServer.ServeHTTP(c.Writer, c.Request)
}

func (h *mcpHandle) HandleServerSentEvents(c *gin.Context) {
	var err error
	req := &interfaces.MCPAppEndpointRequest{}

	if err = c.ShouldBindUri(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	result, err := h.mcpService.GetMCPInstanceConfig(c.Request.Context(), req.MCPID, interfaces.MCPModeSSE)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	// Obtain and serve directly from the local instance pool; increase the active count during the connection survival period to avoid instances being eliminated by LRU/TTL.
	instance, err := h.mcpInstance.GetMCPInstance(c.Request.Context(), result.MCPID, result.Version)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	atomic.AddInt64(&instance.ActiveSSEConn, 1)
	defer atomic.AddInt64(&instance.ActiveSSEConn, -1)
	instance.SSEServer.SSEHandler().ServeHTTP(c.Writer, c.Request)
}

func (h *mcpHandle) HandleSSEMessage(c *gin.Context) {
	var err error
	req := &interfaces.MCPAppEndpointRequest{}

	if err = c.ShouldBindUri(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	result, err := h.mcpService.GetMCPInstanceConfig(c.Request.Context(), req.MCPID, interfaces.MCPModeSSE)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	// Get and serve directly from local memory instance.
	instance, err := h.mcpInstance.GetMCPInstance(c.Request.Context(), result.MCPID, result.Version)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	instance.SSEServer.MessageHandler().ServeHTTP(c.Writer, c.Request)
}
