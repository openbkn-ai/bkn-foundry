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

	// GetSkillNamesByIDs resolves Skill names straight from the registry.
	//
	// It exists as the floor under SearchCapabilities: that one reads the capability index, and an
	// index that was never built answers an unfiltered listing with nothing. A Skill the network
	// has bound must still be listed by name even then — a name without a description is a
	// degraded answer, an empty list is a wrong one. Unknown ids are absent from the result.
	GetSkillNamesByIDs(ctx context.Context, skillIDs []string) (map[string]string, error)

	// SearchBoundTools ranks Function tools inside a whitelist of "{box_id}/{tool_id}" references.
	//
	// Like the Skill side, the whitelist is the scope and it is fail-closed on the far side. The
	// hits carry identity and prose only — the input schema is not part of this answer, and the
	// caller fetches it for the hits it keeps.
	SearchBoundTools(ctx context.Context, req *SearchBoundToolsRequest) ([]ToolHit, error)

	// SearchCapabilities ranks Skills, Function tools and MCP tools together, in one space.
	//
	// It replaces asking three surfaces and concatenating their answers. The three were ordered by
	// three incomparable rules — an unbounded BM25 score, a SQL LIKE with no score at all, and a
	// literal substring match — so the combined order only said which list came first. Here one
	// query runs against one index and the order means something.
	//
	// The whitelist is the scope and it is fail-closed on the far side: no refs returns nothing,
	// never the whole platform.
	SearchCapabilities(ctx context.Context, req *SearchCapabilitiesRequest) ([]CapabilityHit, error)

	// ToolBoxLifecycle reads a tool box's publication state and which of its tools are enabled,
	// over the internal face with this service's identity, so it answers on both faces (#1443).
	// A box that cannot be read comes back unpublished with no enabled tools: this gates what is
	// offered for calling, and unknown is not callable. The caller-visible tools listing cannot
	// stand in for it — it needs a caller token the internal face never carries.
	ToolBoxLifecycle(ctx context.Context, boxID string) (*ToolBoxLifecycle, error)

	// MCPServerIsUsable reports whether the MCP Server is published, and so whether the tools it
	// exposes may be called.
	//
	// The proxy's tool listing answers regardless of the server's state, so it cannot stand in
	// for this: a server taken offline after a tool was mounted still lists that tool. The
	// question has to be put to the server itself.
	MCPServerIsUsable(ctx context.Context, mcpID string) (bool, error)
}

// SearchBoundToolsRequest asks Execution Factory to rank a bounded set of Function tools.
type SearchBoundToolsRequest struct {
	Query string
	// ToolRefs are flat "{box_id}/{tool_id}" references: a tool id is scoped to its box.
	ToolRefs []string
	TopK     int
}

// ToolHit is one ranked Function tool.
type ToolHit struct {
	BoxID       string  `json:"box_id"`
	ToolID      string  `json:"tool_id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	Score       float64 `json:"score"`
	MatchedBy   string  `json:"matched_by"`
}

// SearchCapabilityRef is one capability's identity as the retrieval face names it.
//
// It carries the same three parts as the binding's CapabilityRef, but the owner is called owner_id
// rather than box_id: the retrieval index holds all three kinds, and for an MCP tool that field
// holds a server id, not a box. Keeping them as separate types keeps each wire shape honest
// instead of making one name mean two things.
type SearchCapabilityRef struct {
	CapabilityType string `json:"capability_type"`
	OwnerID        string `json:"owner_id"`
	CapabilityID   string `json:"capability_id"`
}

// SearchCapabilitiesRequest asks Execution Factory to rank a bounded set of capabilities.
type SearchCapabilitiesRequest struct {
	Query string                `json:"query"`
	Refs  []SearchCapabilityRef `json:"refs"`
	TopK  int                   `json:"top_k"`
	// Types narrows the answer to certain capability types. It narrows within Refs and can never
	// reach outside it; empty means every type in Refs.
	Types []string `json:"types"`
	// MetadataTypes narrows Function tools to certain tool box kinds ("openapi" or "function").
	// The product shows four kinds where the bindings store three: an API tool is a Function
	// binding whose box is an openapi box. Empty means both.
	MetadataTypes []string `json:"metadata_types,omitempty"`
}

// CapabilityHit is one ranked capability.
//
// MatchedBy says which retrieval channel found it — the vector one, the lexical one, both, or the
// whitelist filter alone when there was no query to rank against.
type CapabilityHit struct {
	SearchCapabilityRef
	// MetadataType is the tool box kind for a Function tool, empty for the other kinds.
	MetadataType string  `json:"metadata_type,omitempty"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	MatchedBy    string  `json:"matched_by"`
	Score        float64 `json:"score"`
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

// ToolBoxLifecycle is what decides whether a box's tools may be offered: the box is published,
// and the tool itself is enabled. Both are read from the execution factory's own records.
type ToolBoxLifecycle struct {
	Published    bool
	EnabledTools map[string]struct{}
	// EnabledKnown is false when the enabled-tools walk hit its page bound before the listing
	// ended. The set is then a prefix, not the answer, and a tool missing from it is unknown
	// rather than disabled. Callers must not read absence as withdrawal in that case.
	EnabledKnown bool
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
	// WorkingDirectory execution directory, relative to the workspace root.
	// The sandbox scopes artifact collection to it, so leaving it empty makes every
	// execution scan the shared workspace root.
	WorkingDirectory string `json:"working_directory,omitempty"`
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
