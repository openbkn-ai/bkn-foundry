package mcp

import (
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const (
	// 对外MCP Server Stream URL
	externalMCPStreamURI = "/api/agent-operator-integration/v1/mcp/app/:mcp_id/mcp"
	// 对外MCP Server SSE URL
	externalMCPSSEURI = "/api/agent-operator-integration/v1/mcp/app/:mcp_id/sse"
	// internalMCPStreamURL 内部MCP流式URL, 参数为mcpID
	internalMCPStreamURI = "/api/agent-operator-integration/internal-v1/mcp/app/%s/%d/stream"
	// internalMCPSSEURL 内部MCP SSE URL, 参数为mcpID
	internalMCPSSEURI = "/api/agent-operator-integration/internal-v1/mcp/app/%s/%d/sse"
)

// generateExternalConnectionInfo 生成对外MCP Server连接信息
func (s *mcpServiceImpl) generateExternalConnectionInfo(mcpID string,
	creationType interfaces.MCPCreationType,
) (connectionInfo *interfaces.MCPConnectionInfo) {
	connectionInfo = &interfaces.MCPConnectionInfo{}
	// 只有工具导入型 MCP 由平台自己承载实例，app endpoint 才有东西可服务；
	// 自定义型（代理外部 MCP）平台侧只是客户端，没有本地实例，
	// 发出去的 app endpoint 地址必然 404，因此不返回连接信息，
	// 上游接入请直接使用配置中填写的 URL。
	if creationType != interfaces.MCPCreationTypeToolImported {
		return nil
	}

	// 生成SSE URL
	connectionInfo.SSEURL = strings.NewReplacer(
		":mcp_id", mcpID,
	).Replace(externalMCPSSEURI)

	// 生成Stream URL
	connectionInfo.StreamURL = strings.NewReplacer(
		":mcp_id", mcpID,
	).Replace(externalMCPStreamURI)
	return
}

// generateInternalConnectionInfo 生成内部MCP Server连接信息
func (s *mcpServiceImpl) generateInternalMCPURL(mcpID string,
	mcpVersion int,
	mode interfaces.MCPMode,
) (url string) {
	baseURL := strings.TrimRight(interfaces.AOIServerURL, "/")
	switch mode {
	case interfaces.MCPModeStream:
		url = fmt.Sprintf("%s%s", baseURL, fmt.Sprintf(internalMCPStreamURI, mcpID, mcpVersion))
	case interfaces.MCPModeSSE:
		url = fmt.Sprintf("%s%s", baseURL, fmt.Sprintf(internalMCPSSEURI, mcpID, mcpVersion))
	case interfaces.MCPModeStdioNpx, interfaces.MCPModeStdioUv:
	}
	return
}
