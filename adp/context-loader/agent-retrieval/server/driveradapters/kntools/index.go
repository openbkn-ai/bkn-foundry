// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kntools provides the internal REST portal for the published Function
// tool surface: search_tools and execute_tool. These share their service layer
// with the MCP tools of the same name, so both faces answer identically.
package kntools

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	logicsTools "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kntools"
)

// KnToolsHandler is the HTTP entry for published Function tool search and execution.
type KnToolsHandler interface {
	SearchTools(c *gin.Context)
	ExecuteTool(c *gin.Context)
}

type knToolsHandler struct {
	logger interfaces.Logger
	tools  logicsTools.KnToolsService
}

var (
	handlerOnce sync.Once
	handlerInst KnToolsHandler
)

// NewKnToolsHandler creates the KnToolsHandler singleton.
func NewKnToolsHandler() KnToolsHandler {
	handlerOnce.Do(func() {
		conf := config.NewConfigLoader()
		handlerInst = &knToolsHandler{
			logger: conf.GetLogger(),
			tools:  logicsTools.NewKnToolsService(),
		}
	})
	return handlerInst
}

// SearchTools finds callable published Function tools.
func (h *knToolsHandler) SearchTools(c *gin.Context) {
	ctx := c.Request.Context()
	req := &logicsTools.SearchToolsReq{}
	// The body is optional; an empty one lists everything the caller can call.
	_ = c.ShouldBindQuery(req)
	_ = c.ShouldBindJSON(req)

	resp, err := h.tools.SearchTools(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnToolsHandler#SearchTools] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// ExecuteTool invokes one published Function tool as the calling principal.
func (h *knToolsHandler) ExecuteTool(c *gin.Context) {
	ctx := c.Request.Context()
	req := &logicsTools.ExecuteToolReq{}
	if err := c.ShouldBindJSON(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error()))
		return
	}

	resp, err := h.tools.ExecuteTool(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnToolsHandler#ExecuteTool] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}
