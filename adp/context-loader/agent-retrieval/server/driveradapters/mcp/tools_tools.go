// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kntools"
)

// handleSearchTools handles search_tools calls over the published Function catalogue.
func handleSearchTools(svc kntools.KnToolsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		searchReq := &kntools.SearchToolsReq{}
		if err := bindArguments(req, searchReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := svc.SearchTools(ctx, searchReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleExecuteTool runs one published Function tool.
//
// The managed Interaction on the request context travels to Execution Factory
// as the bkn-conversation-id / bkn-interaction-id pair, so the function runs
// inside the same Interaction that asked for it and its work stays auditable.
func handleExecuteTool(svc kntools.KnToolsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		execReq := &kntools.ExecuteToolReq{}
		if err := bindArguments(req, execReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := svc.ExecuteTool(ctx, execReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Match execute_action and execute_skill: an execution result is always
		// machine-readable JSON, never reflowed into a text table.
		result, err := BuildMCPToolResult(resp, rest.FormatJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}
