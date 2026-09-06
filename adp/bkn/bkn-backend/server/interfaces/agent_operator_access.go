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

// ToolBrief is one tool of a tool box, with the two lifecycle states that decide whether it can
// be bound: its own switch and the box's publication state.
type ToolBrief struct {
	BoxID       string
	BoxName     string
	BoxStatus   string
	BoxInternal bool
	ToolID      string
	Name        string
	Description string
	Status      string
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
	// ListBoxTools reads every tool of a tool box in one call, each with its status. It returns
	// (nil, nil) when the box does not exist. The box endpoint inlines its tools, so validating
	// and expanding a whole-box mount both cost one request per box rather than one per tool.
	ListBoxTools(ctx context.Context, boxID string) ([]*ToolBrief, error)
}
