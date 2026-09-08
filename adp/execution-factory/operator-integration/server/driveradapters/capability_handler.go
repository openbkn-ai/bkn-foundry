// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/capability"
)

// CapabilityRestHandler registers the unified capability retrieval routes.
type CapabilityRestHandler interface {
	RegisterPrivate(engine *gin.RouterGroup)
}

type capabilityRestHandler struct {
	CapabilityHandler capability.CapabilityHandler
}

var (
	capabilityOnce    sync.Once
	capabilityHandler CapabilityRestHandler
)

// NewCapabilityRestHandler returns the capability route handler singleton.
func NewCapabilityRestHandler() CapabilityRestHandler {
	capabilityOnce.Do(func() {
		capabilityHandler = &capabilityRestHandler{CapabilityHandler: capability.NewCapabilityHandler()}
	})
	return capabilityHandler
}

func (r *capabilityRestHandler) RegisterPrivate(engine *gin.RouterGroup) {
	// Unified retrieval over Skills, Function tools and MCP tools.
	//
	// Internal face only, for the same reason the Skill retrieval endpoint is: the whitelist in
	// the request body carries the caller's authorization decision, so a public caller supplying
	// its own whitelist would be deciding its own scope.
	engine.POST("/capabilities/search", r.CapabilityHandler.SearchCapabilities)
}
