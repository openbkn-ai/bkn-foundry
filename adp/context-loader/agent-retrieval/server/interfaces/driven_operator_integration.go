// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// ==================== Toolbox Service Related Structures ====================

// GetToolDetailRequest Get tool detail request
type GetToolDetailRequest struct {
	BoxID  string
	ToolID string
}

// GetToolDetailResponse Get tool detail response
type GetToolDetailResponse struct {
	ToolID       string         `json:"tool_id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Status       string         `json:"status"` // enabled/disabled
	MetadataType string         `json:"metadata_type"`
	Metadata     ToolMetadata   `json:"metadata"`
	UseRule      string         `json:"use_rule,omitempty"`
	GlobalParams map[string]any `json:"global_parameters,omitempty"`
	CreateTime   int64          `json:"create_time"`
	UpdateTime   int64          `json:"update_time"`
	CreateUser   string         `json:"create_user"`
	UpdateUser   string         `json:"update_user"`
	ExtendInfo   map[string]any `json:"extend_info,omitempty"`
}

// ToolMetadata Tool metadata
type ToolMetadata struct {
	Version     string         `json:"version"`
	Summary     string         `json:"summary"`
	Description string         `json:"description"`
	ServerURL   string         `json:"server_url"`
	Path        string         `json:"path"`
	Method      string         `json:"method"`
	CreateTime  int64          `json:"create_time"`
	UpdateTime  int64          `json:"update_time"`
	CreateUser  string         `json:"create_user"`
	UpdateUser  string         `json:"update_user"`
	APISpec     map[string]any `json:"api_spec"` // OpenAPI specification
}

// GetMCPToolDetailRequest Get MCP tool detail request
type GetMCPToolDetailRequest struct {
	McpID    string
	ToolName string
}

// GetMCPToolDetailResponse Get MCP tool detail response
type GetMCPToolDetailResponse struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	// OutputSchema is the MCP tool output schema, forwarded verbatim by Execution Factory.
	OutputSchema map[string]interface{} `json:"outputSchema"`
	Annotations  map[string]interface{} `json:"annotations"`
}

// CallMCPToolRequest Call MCP tool request
type CallMCPToolRequest struct {
	McpID      string                 `json:"mcp_id"`
	ToolName   string                 `json:"tool_name"`
	Parameters map[string]interface{} `json:"parameters"`
}

// ==================== Driven Adapters Interface ====================

// DrivenOperatorIntegration Operator integration service interface
type DrivenOperatorIntegration interface {
	// GetToolDetail Get tool detail
	GetToolDetail(ctx context.Context, req *GetToolDetailRequest) (*GetToolDetailResponse, error)
	// GetMCPToolDetail Get MCP tool detail
	GetMCPToolDetail(ctx context.Context, req *GetMCPToolDetailRequest) (*GetMCPToolDetailResponse, error)
	// CallMCPTool Call MCP tool
	CallMCPTool(ctx context.Context, req *CallMCPToolRequest) (map[string]interface{}, error)
	// ListSkills Browse published skills (Skills Marketplace)
	ListSkills(ctx context.Context, req *ListSkillsRequest) (*ListSkillsResponse, error)
	// GetSkillContent gets the text of the skill master document (SKILL.md) and the file list in the package.
	GetSkillContent(ctx context.Context, skillID string) (*GetSkillContentResponse, error)
	// ReadSkillFile reads the text of a single file in the skill package.
	ReadSkillFile(ctx context.Context, req *ReadSkillFileRequest) (*ReadSkillFileResponse, error)
	// ExecuteSkill executes the skill entry command in the sandbox.
	ExecuteSkill(ctx context.Context, req *ExecuteSkillRequest) (*ExecuteSkillResponse, error)
	// ExecuteFunction executes a piece of code within the sandbox (PTC's run_code / run_shell)
	ExecuteFunction(ctx context.Context, req *ExecuteFunctionRequest) (*ExecuteFunctionResponse, error)
}

// ExecuteFunctionRequest sandbox code execution request.
type ExecuteFunctionRequest struct {
	// Code complete script. When Language is python, handler(event) must be exported.
	Code string `json:"code"`
	// The Language execution factory only recognizes python / javascript / shell; bash will be rejected by the sandbox control plane 422.
	Language string `json:"language"`
	// Event The event object passed to the entry function. Credentials and session context go here instead of env_vars:
	// Sandbox sessions are pooled and reused, and env will leave the value of the previous caller in the container.
	Event map[string]any `json:"event"`
	// Timeout Execution timeout, unit seconds.
	Timeout int `json:"timeout,omitempty"`
}

// ExecuteFunctionResponse Sandbox code execution result.
//
// The error reported by the code itself is also HTTP 200. Judging from ExitCode and Stderr, you cannot just look at the status code.
type ExecuteFunctionResponse struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	SessionID string `json:"session_id"`
}
