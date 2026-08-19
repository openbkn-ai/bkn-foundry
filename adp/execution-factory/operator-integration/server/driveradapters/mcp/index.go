// Package mcp provides MCP (Model Context Protocol) driver adapters implementation.
// This package contains handlers for MCP server management, tool execution, and market integration.
package mcp

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	logicsmcp "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/mcpinstance"
)

type MCPPublicHandler interface {
	// ParseSSE parses SSE type MCP services.
	ParseSSE(c *gin.Context)
	// AddMCPServer register MCP service.
	AddMCPServer(c *gin.Context)
	// DeleteMCPServer Delete MCP service.
	DeleteMCPServer(c *gin.Context)
	// QueryMCPServerPage Query MCP service.
	QueryMCPServerPage(c *gin.Context)
	// QueryMCPServerDetail Query MCP service details.
	QueryMCPServerDetail(c *gin.Context)
	// UpdateMCPServer Update MCP service.
	UpdateMCPServer(c *gin.Context)
	// UpdateMCPServerStatus updates MCP service status.
	UpdateMCPServerStatus(c *gin.Context)
	// DebugTool tool debugging.
	DebugTool(c *gin.Context)

	// GetMCPTools query MCP service tool.
	GetMCPTools(c *gin.Context)
	// CallMCPTool calls MCP service tool.
	CallMCPTool(c *gin.Context)

	// QueryMCPServerMarketList Query MCP service market list.
	QueryMCPServerMarketList(c *gin.Context)
	// QueryMCPServerMarketDetail Query MCP service market details.
	QueryMCPServerMarketDetail(c *gin.Context)
	// QueryMCPServerMarketBatch Query MCP service market details in batches.
	QueryMCPServerMarketBatch(c *gin.Context)

	// HandleStreamingHttp Streaming based on HTTP chunked transmission.
	HandleStreamingHttp(c *gin.Context)
	// HandleServerSentEvents SSE event handling.
	HandleServerSentEvents(c *gin.Context)
	// HandleMessage message processing.
	HandleSSEMessage(c *gin.Context)
}

type MCPPrivateHandler interface {
	// GetMCPTools query MCP service tool.
	GetMCPTools(c *gin.Context)
	// CallMCPTool calls MCP service tool.
	CallMCPTool(c *gin.Context)
}

var (
	once sync.Once
	h    *mcpHandle
)

type mcpHandle struct {
	Logger      interfaces.Logger
	mcpService  interfaces.IMCPService
	mcpInstance interfaces.InstanceService
}

// NewMCPHandler creates an MCP handler.
func NewMCPHandler() *mcpHandle {
	once.Do(func() {
		conf := config.NewConfigLoader()
		mcpService := logicsmcp.NewMCPServiceImpl()
		instanceService := mcpinstance.NewMCPInstanceService(mcpService)
		h = &mcpHandle{
			Logger:      conf.GetLogger(),
			mcpService:  mcpService,
			mcpInstance: instanceService,
		}
	})
	return h
}
