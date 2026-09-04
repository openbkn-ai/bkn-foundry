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
	Annotations map[string]interface{} `json:"annotations"`
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
	// ListPublishedToolboxes lists the published Function toolboxes visible to the caller.
	ListPublishedToolboxes(ctx context.Context, req *ListPublishedToolboxesRequest) (*ListPublishedToolboxesResponse, error)
	// ListPublishedTools lists the enabled Function tools inside one published toolbox.
	ListPublishedTools(ctx context.Context, req *ListPublishedToolsRequest) (*ListPublishedToolsResponse, error)
	// ExecutePublishedTool invokes one enabled Function tool through the public Toolbox proxy.
	ExecutePublishedTool(ctx context.Context, req *ExecutePublishedToolRequest) (map[string]any, error)
}

// ==================== Published Function Tool Catalogue ====================

// ListPublishedToolboxesRequest lists only the published Function toolboxes
// visible to the current caller. It deliberately carries no service address or
// creator filter: this is an Agent discovery contract, not an admin API.
type ListPublishedToolboxesRequest struct {
	Keyword string `json:"keyword,omitempty"`
}

// PublishedToolboxSummary is one published Function toolbox.
type PublishedToolboxSummary struct {
	ToolboxID   string `json:"toolbox_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ListPublishedToolboxesResponse is the caller-visible toolbox directory.
type ListPublishedToolboxesResponse struct {
	Toolboxes []PublishedToolboxSummary `json:"toolboxes"`
}

// ListPublishedToolsRequest lists the enabled Function tools of one published
// toolbox visible to the current caller.
type ListPublishedToolsRequest struct {
	ToolboxID string `json:"toolbox_id"`
}

// PublishedToolSummary is one enabled Function tool. InputSchema is trimmed to
// the business-input contract; transport topology never reaches a model.
type PublishedToolSummary struct {
	ToolID      string         `json:"tool_id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	UseRule     string         `json:"use_rule,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// ListPublishedToolsResponse is the enabled Function catalogue of one toolbox.
type ListPublishedToolsResponse struct {
	ToolboxID string                 `json:"toolbox_id"`
	Tools     []PublishedToolSummary `json:"tools"`
}

// ExecutePublishedToolRequest invokes one enabled Function tool. Parameters
// carries only the function's own business input.
type ExecutePublishedToolRequest struct {
	ToolboxID  string         `json:"toolbox_id"`
	ToolID     string         `json:"tool_id"`
	Parameters map[string]any `json:"parameters"`
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
