// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package capability exposes the unified capability retrieval surface.
package capability

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	logicscapability "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/capability"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// CapabilityHandler is the HTTP surface of capability retrieval.
type CapabilityHandler interface {
	SearchCapabilities(c *gin.Context)
}

type capabilityHandler struct {
	SearchService interfaces.CapabilitySearchService
}

var (
	once    sync.Once
	handler CapabilityHandler
)

// NewCapabilityHandler returns the capability handler singleton.
func NewCapabilityHandler() CapabilityHandler {
	once.Do(func() {
		handler = &capabilityHandler{SearchService: logicscapability.NewCapabilitySearchService()}
	})
	return handler
}

// SearchCapabilities ranks Skills, Function tools and MCP tools in one space (#1370).
func (h *capabilityHandler) SearchCapabilities(c *gin.Context) {
	req := &interfaces.SearchCapabilitiesReq{}
	if err := utils.GetBindJSONRaw(c, req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	resp, err := h.SearchService.SearchCapabilities(c.Request.Context(), req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}
