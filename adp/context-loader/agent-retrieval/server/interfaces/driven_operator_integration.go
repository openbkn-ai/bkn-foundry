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
	// ListSkills 浏览已发布技能（技能市场）
	ListSkills(ctx context.Context, req *ListSkillsRequest) (*ListSkillsResponse, error)
	// GetSkillContent 取技能主文档（SKILL.md）正文与包内文件清单
	GetSkillContent(ctx context.Context, skillID string) (*GetSkillContentResponse, error)
	// ReadSkillFile 读技能包内单个文件正文
	ReadSkillFile(ctx context.Context, req *ReadSkillFileRequest) (*ReadSkillFileResponse, error)
	// ExecuteSkill 在沙箱内执行技能入口命令
	ExecuteSkill(ctx context.Context, req *ExecuteSkillRequest) (*ExecuteSkillResponse, error)
	// ExecuteFunction 在沙箱内执行一段代码（PTC 的 run_code / run_shell）
	ExecuteFunction(ctx context.Context, req *ExecuteFunctionRequest) (*ExecuteFunctionResponse, error)
}

// ExecuteFunctionRequest 沙箱代码执行请求。
type ExecuteFunctionRequest struct {
	// Code 完整脚本。Language 为 python 时必须导出 handler(event)。
	Code string `json:"code"`
	// Language 执行工厂只认 python / javascript / shell；bash 会被沙箱控制面 422 拒掉。
	Language string `json:"language"`
	// Event 传给入口函数的事件对象。凭据与会话上下文走这里而不是 env_vars：
	// 沙箱会话是池化复用的，env 会把上一个调用方的值留在容器里。
	Event map[string]any `json:"event"`
	// Timeout 执行超时，单位秒。
	Timeout int `json:"timeout,omitempty"`
}

// ExecuteFunctionResponse 沙箱代码执行结果。
//
// 代码自身报错也是 HTTP 200，据 ExitCode 与 Stderr 判断，不能只看状态码。
type ExecuteFunctionResponse struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	SessionID string `json:"session_id"`
}
