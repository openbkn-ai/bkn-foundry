// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"net/http"
	"slices"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

// mcpProfile is one published shape of the MCP server.
//
// Every profile assembles the same tools with the same handlers and
// middlewares; a profile only decides what it publishes and what the server
// instructions say. Assembling everything and narrowing afterwards keeps
// verifyDecoratorsLanded meaningful for every profile, and because mcp-go runs
// tool filters on tools/call as well as tools/list, a tool a profile drops is
// refused exactly like a tool that does not exist.
type mcpProfile struct {
	endpointPath string
	instructions func(*mcpLocaleBundle) string
	// published lists the tool names the profile offers. Nil offers every
	// assembled tool.
	published map[string]struct{}
	// inlinePTC registers run_code and run_shell.
	inlinePTC bool
	// gateway registers search_native_tools, describe_native_tool and
	// execute_native_read_tool over the long-tail targets.
	gateway bool
	// view rewrites how a published tool is described. Nil publishes the
	// assembled definition as is.
	view func(mcp.Tool) mcp.Tool
	// textResults sends business results as text only, with the receipt in
	// _meta (see compactResultMiddleware).
	textResults bool
	// strictArguments refuses argument names a published definition does not
	// declare (see compactArgumentsMiddleware).
	strictArguments bool
}

func (p mcpProfile) filter(_ context.Context, tools []mcp.Tool) []mcp.Tool {
	out := make([]mcp.Tool, 0, len(p.published))
	for _, tool := range tools {
		if _, ok := p.published[tool.Name]; ok {
			if p.view != nil {
				tool = p.view(tool)
			}
			out = append(out, tool)
		}
	}
	return out
}

// fullProfile is /mcp: every assembled tool, the full instructions and the
// inline sandbox execution tools.
var fullProfile = mcpProfile{
	endpointPath: endpointPath,
	instructions: (*mcpLocaleBundle).ServerInstructions,
	inlinePTC:    true,
}

const compactEndpointPath = "/api/agent-retrieval/v1/mcp-compact"

// compactProfileTools is the fixed tool list of /mcp-compact. It is the same
// for every connection and every request; only deployment configuration and
// the caller's authorization change what a caller can use.
var compactProfileTools = []string{
	toolKeyStartInteraction,
	toolKeyFinishInteraction,
	toolKeyListKnowledgeNetworks,
	toolKeyGetKnDetail,
	toolKeySearchSchema,
	toolKeySearchInstance,
	toolKeyQueryObjectInstance,
	toolKeyQueryMetric,
}

// compactProfile is /mcp-compact: a small fixed tool list for hosts that load
// every tool definition into the model, with instructions that route only
// between those tools, the gateway to the long tail, and no sandbox execution.
var compactProfile = mcpProfile{
	endpointPath:    compactEndpointPath,
	instructions:    (*mcpLocaleBundle).CompactServerInstructions,
	published:       toolNameSet(append(slices.Clone(compactProfileTools), gatewayToolOrder...)),
	inlinePTC:       false,
	gateway:         true,
	view:            compactToolView,
	textResults:     true,
	strictArguments: true,
}

func toolNameSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

// NewCompactMCPHandler builds the handler behind /mcp-compact, one server per
// locale like the full profile. There is no switch: a new URL is opt-in by
// itself, and /mcp is unaffected by it.
func NewCompactMCPHandler() http.Handler {
	return newLocalizedMCPHandlerForProfile(bkntrace.NewLifecycleClientFromEnv(), defaultPTCServicePort, compactProfile)
}

// BuildCompactMCPInfoForLocale describes /mcp-compact: the full catalogue
// narrowed to the profile's tools, plus the gateway tools the full catalogue
// leaves out, so it agrees with the profile's tools/list. It carries no
// toolkit_version, because the profile publishes no sandbox execution tools.
func BuildCompactMCPInfoForLocale(endpoint, localeName string) (*MCPInfo, error) {
	info, err := buildMCPInfoForLocale(endpoint, localeName, false)
	if err != nil {
		return nil, err
	}
	tools := make([]MCPToolInfo, 0, len(compactProfile.published))
	for _, tool := range info.Tools {
		if _, ok := compactProfile.published[tool.Name]; ok {
			tools = append(tools, compactInfoView(tool))
		}
	}
	locale := loadMCPLocaleBundle(localeName)
	for _, key := range gatewayToolOrder {
		meta := locale.ToolMeta(key)
		input, output := tryLoadToolSchemas(locale, key)
		tools = append(tools, MCPToolInfo{
			Name: meta.Name, Title: meta.Title, Group: meta.Group, GroupTitle: meta.GroupTitle,
			Order: meta.Order, Description: meta.Description, InputSchema: input, OutputSchema: output,
		})
	}
	info.Tools = tools
	info.ToolCount = len(tools)
	return info, nil
}
