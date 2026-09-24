// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kntools"
)

// handleSearchCapabilities handles search_capabilities: one ranking over every kind the knowledge
// network mounted (#1388). Skills, Function tools and MCP tools share an index and a ranking since
// #1370; this is the entry that lets an agent see them competing instead of asking twice.
func handleSearchCapabilities(svc kntools.KnToolsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		searchReq := &kntools.SearchCapabilitiesReq{}
		if err := bindArguments(req, searchReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Every other tool on this surface takes the network from the X-Kn-ID header when the
		// arguments omit it, and clients configure it once per connection rather than repeating
		// it on each call — our own CLI among them. find_skills honoured it; the two narrow tool
		// searches never did, so consolidating onto this one has to pick the behaviour up.
		if strings.TrimSpace(searchReq.KnID) == "" {
			searchReq.KnID = getKnIDFromHeader(req)
		}

		resp, err := svc.SearchCapabilities(ctx, searchReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(withCapabilityCalls(resp, searchReq.KnID), format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// capabilityCall is the next call that uses a capability, with the IDs already
// filled in. Agents given only owner_id and capability_id kept calling a
// function by its name instead, a round trip lost each time; a call to copy
// removes the mapping step (owner_id is toolbox_id, capability_id is tool_id).
type capabilityCall struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

type capabilityEntryWithCall struct {
	kntools.CapabilityEntry
	Call *capabilityCall `json:"call,omitempty"`
}

type searchCapabilitiesView struct {
	Capabilities []capabilityEntryWithCall `json:"capabilities"`
	TotalMatched int                       `json:"total_matched"`
	Truncated    bool                      `json:"truncated,omitempty"`
	Message      string                    `json:"message,omitempty"`
}

// withCapabilityCalls adds the call to each entry. Only the MCP answer
// changes: the service result and the REST route stay as they are.
func withCapabilityCalls(resp *kntools.SearchCapabilitiesResp, knID string) *searchCapabilitiesView {
	if resp == nil {
		return nil
	}
	view := &searchCapabilitiesView{
		Capabilities: make([]capabilityEntryWithCall, 0, len(resp.Capabilities)),
		TotalMatched: resp.TotalMatched, Truncated: resp.Truncated, Message: resp.Message,
	}
	for _, entry := range resp.Capabilities {
		view.Capabilities = append(view.Capabilities, capabilityEntryWithCall{
			CapabilityEntry: entry, Call: capabilityCallFor(entry, knID),
		})
	}
	return view
}

func capabilityCallFor(entry kntools.CapabilityEntry, knID string) *capabilityCall {
	switch entry.CapabilityType {
	case interfaces.CapabilityTypeFunction, interfaces.CapabilityTypeMCPTool:
		return &capabilityCall{Tool: toolKeyExecuteTool, Arguments: map[string]any{
			"kn_id": knID, "toolbox_id": entry.OwnerID, "tool_id": entry.CapabilityID,
			"arguments": map[string]any{},
		}}
	case interfaces.CapabilityTypeSkill:
		return &capabilityCall{Tool: toolKeyGetSkillContent, Arguments: map[string]any{
			"kn_id": knID, "skill_id": entry.CapabilityID,
		}}
	default:
		return nil
	}
}

// handleExecuteTool runs one published Function tool.
//
// The managed Interaction on the request context travels to Execution Factory
// as the bkn-conversation-id / bkn-interaction-id pair, so the function runs
// inside the same Interaction that asked for it and its work stays auditable.
func handleExecuteTool(svc kntools.KnToolsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		execReq := &kntools.ExecuteToolReq{}
		if err := bindArguments(req, execReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if strings.TrimSpace(execReq.KnID) == "" {
			execReq.KnID = getKnIDFromHeader(req)
		}

		resp, err := svc.ExecuteTool(ctx, execReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Match execute_action and execute_skill: an execution result is always
		// machine-readable JSON, never reflowed into a text table.
		result, err := BuildMCPToolResult(resp, rest.FormatJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}
