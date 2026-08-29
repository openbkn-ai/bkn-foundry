// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package mcp

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// handleListPublishedToolboxes exposes the caller-visible Function directory.
// It is deliberately separate from Skills discovery: a Skill is a reusable
// procedure, while a published Function is an executable business operation.
func handleListPublishedToolboxes(executor interfaces.DrivenOperatorIntegration) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resp, err := executor.ListPublishedToolboxes(ctx, &interfaces.ListPublishedToolboxesRequest{
			Keyword: strings.TrimSpace(getStringArg(req, "keyword", "")),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(resp, rest.FormatJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

func handleListPublishedTools(executor interfaces.DrivenOperatorIntegration) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		toolboxID := strings.TrimSpace(getStringArg(req, "toolbox_id", ""))
		if toolboxID == "" {
			return mcp.NewToolResultError("toolbox_id is required"), nil
		}
		resp, err := executor.ListPublishedTools(ctx, &interfaces.ListPublishedToolsRequest{ToolboxID: toolboxID})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(resp, rest.FormatJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}
