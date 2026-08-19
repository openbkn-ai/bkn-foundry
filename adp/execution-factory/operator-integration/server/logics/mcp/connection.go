package mcp

import (
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const (
	// External MCP Server Stream URL.
	externalMCPStreamURI = "/api/agent-operator-integration/v1/mcp/app/:mcp_id/mcp"
	// External MCP Server SSE URL.
	externalMCPSSEURI = "/api/agent-operator-integration/v1/mcp/app/:mcp_id/sse"
	// internalMCPStreamURL Internal MCP streaming URL, parameter is mcpID.
	internalMCPStreamURI = "/api/agent-operator-integration/internal-v1/mcp/app/%s/%d/stream"
	// internalMCPSSEURL Internal MCP SSE URL, parameter is mcpID.
	internalMCPSSEURI = "/api/agent-operator-integration/internal-v1/mcp/app/%s/%d/sse"
)

// generateExternalConnectionInfo generates external MCP Server connection information.
func (s *mcpServiceImpl) generateExternalConnectionInfo(mcpID string,
	creationType interfaces.MCPCreationType,
) (connectionInfo *interfaces.MCPConnectionInfo) {
	connectionInfo = &interfaces.MCPConnectionInfo{}
	// Only when the tool-imported MCP is hosted by the platform itself, can the app endpoint have something to serve;
	// The customized (proxy external MCP) platform side is only the client and does not have a local instance.
	// The sent app endpoint address must be 404, so no connection information is returned.
	// For upstream access, please directly use the URL filled in the configuration.
	if creationType != interfaces.MCPCreationTypeToolImported {
		return nil
	}

	// Generate SSE URL.
	connectionInfo.SSEURL = strings.NewReplacer(
		":mcp_id", mcpID,
	).Replace(externalMCPSSEURI)

	// Generate Stream URL.
	connectionInfo.StreamURL = strings.NewReplacer(
		":mcp_id", mcpID,
	).Replace(externalMCPStreamURI)
	return
}

// generateInternalConnectionInfo generates internal MCP Server connection information.
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
