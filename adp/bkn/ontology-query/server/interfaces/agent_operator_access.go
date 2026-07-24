// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

const (
	PARAMETER_HEADER = "header"
	PARAMETER_BODY   = "body"
	PARAMETER_QUERY  = "query"
	PARAMETER_PATH   = "path"
)

// ToolExecutionRequest represents the request to execute a tool via tool-box API
type ToolExecutionRequest struct {
	Header  map[string]any `json:"header,omitempty"`
	Body    map[string]any `json:"body,omitempty"`
	Query   map[string]any `json:"query,omitempty"`
	Path    map[string]any `json:"path,omitempty"`
	Timeout int64          `json:"timeout,omitempty"` // 超时时间，单位秒
}

//go:generate mockgen -source ../interfaces/agent_operator_access.go -destination ../interfaces/mock/mock_agent_operator_access.go
type AgentOperatorAccess interface {
	// ExecuteTool executes a tool via tool-box API
	// API: POST /tool-box/{box_id}/proxy/{tool_id}
	ExecuteTool(ctx context.Context, boxID string, toolID string, execRequest ToolExecutionRequest) (any, error)
	// ExecuteMCP executes an MCP-based action through agent-operator-integration
	// API: POST /mcp/proxy/{mcp_id}/tool/call
	ExecuteMCP(ctx context.Context, mcpID string, toolName string, execRequest MCPExecutionRequest) (any, error)
}
