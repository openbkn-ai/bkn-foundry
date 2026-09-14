// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	proxyexecution "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/proxy_execution"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// GetMCPToolDefinitionAsProxy reads the invocation contract of the MCP tool an
// action type is bound to, for that knowledge network's managed proxy (#1548).
//
// It answers from the version callers are served, under the same rule as a
// call: a server that is not served lists nothing through the proxy. Only the
// named tool's name, description and input schema leave.
func (s *mcpServiceImpl) GetMCPToolDefinitionAsProxy(ctx context.Context,
	req *interfaces.MCPToolDefinitionRequest) (resp *interfaces.MCPToolDefinition, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"mcp_id":        req.MCPID,
		"bkn.tool.name": req.ToolName,
	})
	// Authorization comes before the config read and before any connection to the
	// server: a proxy that may not read reaches neither.
	if err = proxyexecution.AuthorizeDefinitionRead(ctx, s.ProxyAuthorizer, s.ProxyAudit); err != nil {
		return nil, err
	}

	serverConfig, err := s.DBMCPServerConfig.SelectByID(ctx, nil, req.MCPID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("select mcp server config by id error: %v", err)
		return nil, oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError,
			fmt.Sprintf("select mcp server config by id error: %v", err))
	}
	if serverConfig == nil {
		return nil, oerrors.DefaultHTTPError(ctx, http.StatusNotFound, "mcp server config not found")
	}
	listToolsReq, served, err := s.servingListToolsRequest(ctx, serverConfig)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("select mcp server release by id error: %v", err)
		return nil, oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError,
			fmt.Sprintf("select mcp server release by id error: %v", err))
	}
	if !served {
		return nil, oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtMCPServerNotPublished, nil)
	}
	listToolsResp, err := s.listTools(ctx, listToolsReq)
	if err != nil {
		return nil, err
	}
	for _, tool := range listToolsResp.Tools {
		if tool.Name != req.ToolName {
			continue
		}
		return mcpToolDefinition(ctx, req.MCPID, tool)
	}
	return nil, oerrors.DefaultHTTPError(ctx, http.StatusNotFound, "mcp tool not found")
}

// mcpToolDefinition projects a listed tool through its own JSON form, so the
// input schema is byte for byte what the tool listing shows.
func mcpToolDefinition(ctx context.Context, mcpID string, tool mcp.Tool) (*interfaces.MCPToolDefinition, error) {
	encoded, err := json.Marshal(tool)
	if err != nil {
		return nil, oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError,
			fmt.Sprintf("encode mcp tool %s: %v", tool.Name, err))
	}
	var listed struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err = json.Unmarshal(encoded, &listed); err != nil {
		return nil, oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError,
			fmt.Sprintf("decode mcp tool %s: %v", tool.Name, err))
	}
	return &interfaces.MCPToolDefinition{
		MCPID:       mcpID,
		Name:        listed.Name,
		Description: listed.Description,
		InputSchema: listed.InputSchema,
	}, nil
}
