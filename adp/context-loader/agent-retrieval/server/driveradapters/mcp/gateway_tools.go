// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

// gatewayToolOrder is the registration order of the gateway tools.
var gatewayToolOrder = []string{toolKeySearchNativeTools, toolKeyDescribeNativeTool, toolKeyExecuteNativeTool}

// claimGatewayNames reserves the gateway tools' names on every profile, as the
// lifecycle tools' names are, so an enterprise tool cannot take one of them.
func (b *toolBuilder) claimGatewayNames() {
	for _, key := range gatewayToolOrder {
		b.claimName(b.locale.ToolMeta(key).Name, key)
	}
}

// registerGatewayTools adds the gateway to a profile's server. It runs after
// the builder has assembled and attached every native tool, because the
// catalogue resolves targets among exactly those.
//
// search and describe are ordinary managed tools: the server's guard records
// each call as its own Operation. The executor is passed through by the
// server's guard and governs its target itself (see nativeExecutor).
func registerGatewayTools(srv *server.MCPServer, b *toolBuilder, lifecycleClient *bkntrace.LifecycleClient) {
	catalog := newNativeCatalog(b)
	handlers := map[string]server.ToolHandlerFunc{
		toolKeySearchNativeTools:  handleSearchNativeTools(catalog),
		toolKeyDescribeNativeTool: handleDescribeNativeTool(catalog),
		toolKeyExecuteNativeTool:  newNativeExecutor(catalog, lifecycleClient).handle,
	}
	for _, key := range gatewayToolOrder {
		input, output := b.locale.ToolSchemas(key)
		srv.AddTool(newToolWithSchemas(b.locale.ToolMeta(key), input, output), handlers[key])
	}
}

func handleSearchNativeTools(catalog *nativeCatalog) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := getStringArg(req, "query", "")
		if query == "" {
			return refusalResult(&gatewayRefusal{Code: refusalInvalidArguments, Message: "query is required."}), nil
		}
		limit := searchDefaultLimit
		if value, ok := req.GetArguments()["limit"].(float64); ok {
			limit = int(value)
		}
		return jsonTextResult(catalog.search(ctx, query, limit))
	}
}

func handleDescribeNativeTool(catalog *nativeCatalog) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := getStringArg(req, "name", "")
		if name == "" {
			return refusalResult(&gatewayRefusal{Code: refusalInvalidArguments, Message: "name is required."}), nil
		}
		includeOutput, _ := req.GetArguments()["include_output_schema"].(bool)
		description, err := catalog.describe(ctx, name, includeOutput)
		var refusal *gatewayRefusal
		if errors.As(err, &refusal) {
			return refusalResult(refusal), nil
		}
		if err != nil {
			return nil, err
		}
		return jsonTextResult(description)
	}
}

// jsonTextResult returns value as JSON text. The gateway tools publish no
// output schema, so their results are text a model reads.
func jsonTextResult(value any) (*mcp.CallToolResult, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(raw)), nil
}
