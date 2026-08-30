package driveradapters

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/mcp"
)

type MCPRestHandler interface {
	// RegisterPrivate register internal API.
	RegisterPrivate(engine *gin.RouterGroup)

	// RegisterPublic Register external API.
	RegisterPublic(engine *gin.RouterGroup)
}

type mcpRestHandler struct {
	MCPPublicHandler  mcp.MCPPublicHandler
	MCPPrivateHandler mcp.MCPPrivateHandler
}

var (
	mcpRestHandlerOnce sync.Once
	mHandler           MCPRestHandler
)

func NewMCPRestHandler() MCPRestHandler {
	mcpRestHandlerOnce.Do(func() {
		mHandler = &mcpRestHandler{
			MCPPublicHandler:  mcp.NewMCPHandler(),
			MCPPrivateHandler: mcp.NewMCPHandler(),
		}
	})
	return mHandler
}

func (r *mcpRestHandler) RegisterPrivate(engine *gin.RouterGroup) {
	mcpGroup := engine.Group("/mcp")

	// MCP proxy related interfaces.
	mcpProxyGroup := mcpGroup.Group("/proxy")
	// Get the tool list of the specified MCP Server GET /api/agent-operator-integration/internal-v1/mcp/proxy/{mcp_id}/tools.
	mcpProxyGroup.GET("/:mcp_id/tools", r.MCPPrivateHandler.GetMCPTools)
	// Call the tool of the specified MCP Server POST /api/agent-operator-integration/internal-v1/mcp/proxy/{mcp_id}/tool/call.
	mcpProxyGroup.POST("/:mcp_id/tool/call", r.MCPPrivateHandler.CallMCPTool)
}

func (r *mcpRestHandler) RegisterPublic(engine *gin.RouterGroup) {
	// MCP related interfaces.
	mcpGroup := engine.Group("/mcp")

	// MCP management related interfaces.
	// MCP service parsing POST /api/agent-operator-integration/v1/mcp/parse/sse.
	mcpGroup.POST("/parse/sse", r.MCPPublicHandler.ParseSSE)
	// Add MCP Server configuration POST /api/agent-operator-integration/v1/mcp.
	mcpGroup.POST("/", r.MCPPublicHandler.AddMCPServer)
	// Delete MCP Server configuration POST /api/agent-operator-integration/v1/mcp/delete.
	mcpGroup.DELETE("/:mcp_id", r.MCPPublicHandler.DeleteMCPServer)
	// Get the MCP Server configuration list GET /api/agent-operator-integration/v1/mcp/list.
	mcpGroup.GET("/list", r.MCPPublicHandler.QueryMCPServerPage)
	// Get MCP Server configuration details GET /api/agent-operator-integration/v1/mcp/{mcp_id}
	mcpGroup.GET("/:mcp_id", r.MCPPublicHandler.QueryMCPServerDetail)
	// Edit MCP Server configuration POST /api/agent-operator-integration/v1/mcp/{mcp_id}
	mcpGroup.PUT("/:mcp_id", r.MCPPublicHandler.UpdateMCPServer)
	// Update MCP Server status POST /api/agent-operator-integration/v1/mcp/{mcp_id}/status.
	mcpGroup.POST("/:mcp_id/status", r.MCPPublicHandler.UpdateMCPServerStatus)
	// MCP tool debugging POST /api/agent-operator-integration/v1/mcp/{mcp_id}/tool/{tool_name}/debug.
	mcpGroup.POST("/:mcp_id/tool/:tool_name/debug", r.MCPPublicHandler.DebugTool)

	// MCP service market related interfaces.
	mcpGroup.GET("/market/list", r.MCPPublicHandler.QueryMCPServerMarketList)
	// Batch query MCP service market details GET /api/agent-operator-integration/v1/mcp/market/{mcp_ids}/{fields}
	mcpGroup.GET("/market/batch/:mcp_ids/:fields", r.MCPPublicHandler.QueryMCPServerMarketBatch)
	mcpGroup.GET("/market/:mcp_id", r.MCPPublicHandler.QueryMCPServerMarketDetail)

	// MCP proxy related interfaces.
	// Get the tool list of the specified MCP Server GET /api/agent-operator-integration/v1/mcp/proxy/{mcp_id}/tools.
	mcpGroup.GET("/proxy/:mcp_id/tools", r.MCPPublicHandler.GetMCPTools)
	// Call the tool of the specified MCP Server POST /api/agent-operator-integration/v1/mcp/proxy/{mcp_id}/tool/call.
	mcpGroup.POST("/proxy/:mcp_id/tool/call", r.MCPPublicHandler.CallMCPTool)

	// MCP endpoint related interfaces.
	// Streamable Http Endpoint
	mcpGroup.Any("/app/:mcp_id/mcp", r.MCPPublicHandler.HandleStreamingHttp)
	// SSE Endpoint
	mcpGroup.GET("/app/:mcp_id/sse", r.MCPPublicHandler.HandleServerSentEvents)
	// message endpoint
	mcpGroup.POST("/app/:mcp_id/message", r.MCPPublicHandler.HandleSSEMessage)
}
