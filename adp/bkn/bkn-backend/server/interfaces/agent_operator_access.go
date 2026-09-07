// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// Execution-factory lifecycle values this service compares against. They are the strings the
// execution factory serialises, repeated here because BKN only ever reads them.
const (
	// EXEC_TOOL_STATUS_ENABLED is the per-tool switch inside a tool box.
	EXEC_TOOL_STATUS_ENABLED = "enabled"
	// EXEC_BOX_METADATA_TYPE_OPENAPI and _FUNCTION are the two kinds of tool box. A binding is
	// counted and listed as an API or as a function according to its box.
	EXEC_BOX_METADATA_TYPE_OPENAPI  = "openapi"
	EXEC_BOX_METADATA_TYPE_FUNCTION = "function"
	// EXEC_TOOL_STATUS_DISABLED is its opposite. An MCP tool has no switch of its own and is
	// reported in this same vocabulary, so one reader can filter every capability type alike.
	EXEC_TOOL_STATUS_DISABLED = "disabled"
	// EXEC_BOX_STATUS_PUBLISHED is the tool box lifecycle state that makes its tools callable.
	EXEC_BOX_STATUS_PUBLISHED = "published"
	// EXEC_SKILL_STATUS_PUBLISHED is the skill lifecycle state that makes it loadable.
	EXEC_SKILL_STATUS_PUBLISHED = "published"
	// EXEC_SKILL_STATUS_EDITING is a skill that was published and has been edited since. Its
	// published version stays in the retrieval index and stays loadable, so it is bindable too:
	// editing a description must not make a skill unmountable.
	EXEC_SKILL_STATUS_EDITING = "editing"
)

// SkillIsBindable reports whether a skill can be bound to a knowledge network.
//
// Both published and editing qualify. The execution factory flips published to editing on a
// metadata edit while keeping the published version in the index, so treating editing as
// unusable would tell the caller to publish a skill that is already published — and re-publishing
// would be the only way out of an error the caller did not cause.
func SkillIsBindable(status string) bool {
	return status == EXEC_SKILL_STATUS_PUBLISHED || status == EXEC_SKILL_STATUS_EDITING
}

// SkillBrief is the part of a skill BKN needs to validate a capability binding: whether it
// exists, and whether it is usable. Everything else stays in the execution factory.
type SkillBrief struct {
	SkillID     string
	Name        string
	Description string
	Status      string
}

// ToolBoxBrief identifies a tool box without its tools.
type ToolBoxBrief struct {
	BoxID  string
	Name   string
	Status string
}

// ToolBrief is one tool of a tool box, with the two lifecycle states that decide whether it can
// be bound: its own switch and the box's publication state.
type ToolBrief struct {
	BoxID   string
	BoxName string
	// BoxMetadataType is what kind of tools the box holds: "openapi" or "function". It belongs to
	// the box, not the tool, and decides which of the two lists a binding appears in.
	BoxMetadataType string
	BoxStatus       string
	BoxInternal     bool
	ToolID          string
	Name            string
	Description     string
	Status          string
}

// MCPToolBrief is one tool exposed by an MCP Server.
//
// An MCP tool is addressed by name, not by id: that is the MCP protocol's own contract, and it is
// what ActionSource already uses for type=mcp. The box_id/tool_id pair inside the server's
// tool_configs is where the tool was assembled from, not how it is called.
type MCPToolBrief struct {
	MCPID       string
	MCPName     string
	MCPStatus   string
	Name        string
	Description string
}

//go:generate mockgen -source ../interfaces/agent_operator_access.go -destination ../interfaces/mock/mock_agent_operator_access.go -package mock_interfaces
type AgentOperatorAccess interface {
	// GetToolByID verifies the tool exists in the tool-box via internal GET .../tool-box/{box_id}/tool/{tool_id}.
	GetToolByID(ctx context.Context, boxID, toolID string) error
	// GetMcpToolByName verifies the MCP server exposes a tool with the given name (internal GET .../mcp/proxy/{mcp_id}/tools).
	GetMcpToolByName(ctx context.Context, mcpID, toolName string) error
	// GetSkillByID reads a skill's identity and lifecycle state. It returns (nil, nil) when the
	// skill does not exist, so the caller can tell "no such skill" from "exists but unpublished"
	// — the market endpoint collapses both into 404, which is why it is not used here.
	GetSkillByID(ctx context.Context, skillID string) (*SkillBrief, error)
	// GetSkillNamesByIDs resolves several skills to their names in one call. Skills that do not
	// exist are absent from the result rather than present with an empty name, so the difference
	// between the request and the answer is exactly the set of dangling references.
	GetSkillNamesByIDs(ctx context.Context, skillIDs []string) (map[string]string, error)
	// FindSkillsByName looks a skill up by exact name. It returns every match: a name is not
	// unique in the execution factory, and an importer that picked one at random would bind a
	// different capability than the model meant.
	FindSkillsByName(ctx context.Context, name string) ([]*SkillBrief, error)
	// FindToolBoxesByName looks a tool box up by exact name, with the same rule about duplicates.
	FindToolBoxesByName(ctx context.Context, name string) ([]*ToolBoxBrief, error)
	// ListBoxTools reads every tool of a tool box in one call, each with its status. It returns
	// (nil, nil) when the box does not exist. The box endpoint inlines its tools, so validating
	// and expanding a whole-box mount both cost one request per box rather than one per tool.
	ListBoxTools(ctx context.Context, boxID string) ([]*ToolBrief, error)

	// ListMCPTools reads every tool an MCP Server exposes, in one call.
	//
	// It returns nil (and no error) when the server does not exist, matching ListBoxTools: a
	// missing container and an empty one are different answers, and only the caller knows which
	// of the two is an error for what it is doing.
	ListMCPTools(ctx context.Context, mcpID string) ([]*MCPToolBrief, error)

	// FindMCPServersByName returns the ids of MCP Servers with exactly this name.
	//
	// Several can share a name, and the caller decides what to do about that: an import refuses
	// to guess, because binding one of them would bind something the model did not name.
	FindMCPServersByName(ctx context.Context, name string) ([]string, error)
}
