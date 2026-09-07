// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package capability_binding

import (
	"context"
	"fmt"
	"strings"

	bknsdk "bkn-backend/bkn-specification/bkn"
	"bkn-backend/interfaces"
)

// Reasons a declared capability could not be bound. They are returned to the caller rather than
// logged, because a silently short import is the worst outcome here: the network looks imported,
// its SKILLs list is empty, and nothing says why.
const (
	CapabilitySkipNotFound    = "not_found"
	CapabilitySkipAmbiguous   = "ambiguous_name"
	CapabilitySkipUnusable    = "not_usable"
	CapabilitySkipUnreachable = "execution_factory_unreachable"
)

// ImportCapabilities resolves a model file's capability declarations against this environment and
// mounts what it can.
//
// Resolution order is id first, then name. Ids are exact but environment-local; names are the only
// thing that survives a move between environments, and a model imported elsewhere would otherwise
// arrive with everything unbound. A name matching several capabilities is not resolved at all:
// picking one would bind something the model did not name.
//
// Nothing here fails the import. A knowledge network whose capabilities are missing is still a
// knowledge network; the caller gets the list of what was skipped and why.
func (cbs *capabilityBindingService) ImportCapabilities(ctx context.Context, knID, branch string,
	declared *bknsdk.BknCapabilities) (*interfaces.CapabilityImportReport, error) {
	report := &interfaces.CapabilityImportReport{Skipped: []*interfaces.CapabilitySkip{}}
	if declared == nil {
		return report, nil
	}

	// No capacity hint: the two lengths come from an uploaded file, and summing them is a
	// tainted-arithmetic pattern for no gain on a list this short.
	var entries []*interfaces.AttachCapabilityEntry

	for _, skill := range declared.Skills {
		if skill == nil {
			continue
		}
		resolvedID, skip := cbs.resolveSkill(ctx, skill)
		if skip != nil {
			report.Skipped = append(report.Skipped, skip)
			continue
		}
		entries = append(entries, &interfaces.AttachCapabilityEntry{
			CapabilityType: interfaces.CAPABILITY_TYPE_SKILL,
			CapabilityID:   resolvedID,
		})
	}

	for _, function := range declared.Functions {
		if function == nil {
			continue
		}
		boxID, toolID, skip := cbs.resolveFunction(ctx, function)
		if skip != nil {
			report.Skipped = append(report.Skipped, skip)
			continue
		}
		entries = append(entries, &interfaces.AttachCapabilityEntry{
			CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
			OwnerID:        boxID,
			CapabilityID:   toolID,
		})
	}

	for _, mcpTool := range declared.MCPTools {
		if mcpTool == nil {
			continue
		}
		mcpID, toolName, skip := cbs.resolveMCPTool(ctx, mcpTool)
		if skip != nil {
			report.Skipped = append(report.Skipped, skip)
			continue
		}
		entries = append(entries, &interfaces.AttachCapabilityEntry{
			CapabilityType: interfaces.CAPABILITY_TYPE_MCP_TOOL,
			OwnerID:        mcpID,
			CapabilityID:   toolName,
		})
	}

	if len(entries) == 0 {
		return report, nil
	}
	bound, err := cbs.AttachCapabilities(ctx, nil, knID, branch, entries)
	if err != nil {
		return nil, err
	}
	report.Bound = len(bound)
	return report, nil
}

func (cbs *capabilityBindingService) resolveSkill(ctx context.Context,
	declared *bknsdk.BknCapabilitySkill) (string, *interfaces.CapabilitySkip) {
	label := declared.Name
	if label == "" {
		label = declared.ID
	}
	skip := func(reason, detail string) *interfaces.CapabilitySkip {
		return &interfaces.CapabilitySkip{
			CapabilityType: interfaces.CAPABILITY_TYPE_SKILL,
			Name:           label,
			DeclaredID:     declared.ID,
			Reason:         reason,
			Detail:         detail,
		}
	}

	if id := strings.TrimSpace(declared.ID); id != "" {
		skill, err := cbs.aoa.GetSkillByID(ctx, id)
		if err != nil {
			return "", skip(CapabilitySkipUnreachable, err.Error())
		}
		if skill != nil {
			if !interfaces.SkillIsBindable(skill.Status) {
				return "", skip(CapabilitySkipUnusable,
					fmt.Sprintf("skill %s is %s", id, skill.Status))
			}
			return id, nil
		}
	}

	if declared.Name == "" {
		return "", skip(CapabilitySkipNotFound, "no skill with the declared id, and no name to fall back to")
	}
	matches, err := cbs.aoa.FindSkillsByName(ctx, declared.Name)
	if err != nil {
		return "", skip(CapabilitySkipUnreachable, err.Error())
	}
	switch len(matches) {
	case 0:
		return "", skip(CapabilitySkipNotFound, fmt.Sprintf("no skill named %q", declared.Name))
	case 1:
		if !interfaces.SkillIsBindable(matches[0].Status) {
			return "", skip(CapabilitySkipUnusable,
				fmt.Sprintf("skill %q is %s", declared.Name, matches[0].Status))
		}
		return matches[0].SkillID, nil
	default:
		return "", skip(CapabilitySkipAmbiguous,
			fmt.Sprintf("%d skills are named %q", len(matches), declared.Name))
	}
}

func (cbs *capabilityBindingService) resolveFunction(ctx context.Context,
	declared *bknsdk.BknCapabilityFunction) (string, string, *interfaces.CapabilitySkip) {
	label := declared.ToolName
	if label == "" {
		label = declared.ToolID
	}
	skip := func(reason, detail string) *interfaces.CapabilitySkip {
		return &interfaces.CapabilitySkip{
			CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
			Name:           label,
			BoxName:        declared.BoxName,
			DeclaredID:     declared.ToolID,
			DeclaredBoxID:  declared.BoxID,
			Reason:         reason,
			Detail:         detail,
		}
	}

	// Exact ids first: in the environment that produced the file they identify the tool outright.
	if declared.BoxID != "" && declared.ToolID != "" {
		tools, err := cbs.aoa.ListBoxTools(ctx, declared.BoxID)
		if err != nil {
			return "", "", skip(CapabilitySkipUnreachable, err.Error())
		}
		if tool := findTool(tools, declared.ToolID); tool != nil {
			if unusable := toolUnusableReason(tool); unusable != "" {
				return "", "", skip(CapabilitySkipUnusable, unusable)
			}
			return declared.BoxID, declared.ToolID, nil
		}
	}

	if declared.BoxName == "" || declared.ToolName == "" {
		return "", "", skip(CapabilitySkipNotFound,
			"no tool with the declared ids, and no names to fall back to")
	}
	boxes, err := cbs.aoa.FindToolBoxesByName(ctx, declared.BoxName)
	if err != nil {
		return "", "", skip(CapabilitySkipUnreachable, err.Error())
	}
	switch len(boxes) {
	case 0:
		return "", "", skip(CapabilitySkipNotFound, fmt.Sprintf("no tool box named %q", declared.BoxName))
	case 1:
	default:
		return "", "", skip(CapabilitySkipAmbiguous,
			fmt.Sprintf("%d tool boxes are named %q", len(boxes), declared.BoxName))
	}

	tools, err := cbs.aoa.ListBoxTools(ctx, boxes[0].BoxID)
	if err != nil {
		return "", "", skip(CapabilitySkipUnreachable, err.Error())
	}
	named := make([]*interfaces.ToolBrief, 0, 1)
	for _, tool := range tools {
		if tool.Name == declared.ToolName {
			named = append(named, tool)
		}
	}
	switch len(named) {
	case 0:
		return "", "", skip(CapabilitySkipNotFound,
			fmt.Sprintf("tool box %q has no tool named %q", declared.BoxName, declared.ToolName))
	case 1:
		if unusable := toolUnusableReason(named[0]); unusable != "" {
			return "", "", skip(CapabilitySkipUnusable, unusable)
		}
		return boxes[0].BoxID, named[0].ToolID, nil
	default:
		return "", "", skip(CapabilitySkipAmbiguous,
			fmt.Sprintf("%d tools in %q are named %q", len(named), declared.BoxName, declared.ToolName))
	}
}

// toolUnusableReason mirrors what validateTool rejects at mount time, and returns the empty string
// when the tool is mountable.
//
// The two checks have to agree. Resolution that accepted a tool the mount then rejects does not
// cost that one entry: AttachCapabilities validates the batch as a unit, so a single tool in an
// unpublished box fails the whole call, and the import reports one execution_factory_unreachable
// in place of the report — every Skill that resolved cleanly is lost with it, and the reason
// points at the wrong thing.
func toolUnusableReason(tool *interfaces.ToolBrief) string {
	if !boxIsUsable(tool) {
		return fmt.Sprintf("tool box %s is %s", tool.BoxID, tool.BoxStatus)
	}
	if tool.Status != interfaces.EXEC_TOOL_STATUS_ENABLED {
		return fmt.Sprintf("tool %s is %s", tool.ToolID, tool.Status)
	}
	return ""
}

func findTool(tools []*interfaces.ToolBrief, toolID string) *interfaces.ToolBrief {
	for _, tool := range tools {
		if tool.ToolID == toolID {
			return tool
		}
	}
	return nil
}

// resolveMCPTool resolves one declared MCP tool against this environment.
//
// Only the server is resolved by id then name; the tool itself is matched by name either way,
// because that is how MCP addresses tools. A server name matching several servers is left
// unresolved for the same reason a Skill name is: picking one would bind something the model did
// not name.
func (cbs *capabilityBindingService) resolveMCPTool(ctx context.Context,
	declared *bknsdk.BknCapabilityMCPTool) (string, string, *interfaces.CapabilitySkip) {
	toolName := strings.TrimSpace(declared.ToolName)
	skip := func(reason, detail string) *interfaces.CapabilitySkip {
		return &interfaces.CapabilitySkip{
			CapabilityType: interfaces.CAPABILITY_TYPE_MCP_TOOL,
			Name:           toolName,
			BoxName:        declared.MCPName,
			DeclaredID:     toolName,
			DeclaredBoxID:  declared.MCPID,
			Reason:         reason,
			Detail:         detail,
		}
	}
	if toolName == "" {
		return "", "", skip(CapabilitySkipNotFound, "the declaration carries no tool name")
	}

	// The id first: in the environment that produced the file it names the server outright.
	if mcpID := strings.TrimSpace(declared.MCPID); mcpID != "" {
		tools, err := cbs.aoa.ListMCPTools(ctx, mcpID)
		if err != nil {
			return "", "", skip(CapabilitySkipUnreachable, err.Error())
		}
		// A hit only counts when the server actually exposes the named tool. An id that resolves
		// to some other environment's server — ids collide across environments, which is the
		// whole reason names are written alongside them — falls through to the name below rather
		// than failing here, matching how resolveFunction treats a box that lacks the tool.
		if hasMCPTool(tools, toolName) {
			if unusable := mcpUnusableReason(tools, toolName); unusable != "" {
				return "", "", skip(CapabilitySkipUnusable, unusable)
			}
			return mcpID, toolName, nil
		}
	}

	if strings.TrimSpace(declared.MCPName) == "" {
		return "", "", skip(CapabilitySkipNotFound,
			"no mcp server with the declared id, and no server name to fall back to")
	}
	servers, err := cbs.aoa.FindMCPServersByName(ctx, declared.MCPName)
	if err != nil {
		return "", "", skip(CapabilitySkipUnreachable, err.Error())
	}
	switch len(servers) {
	case 0:
		return "", "", skip(CapabilitySkipNotFound,
			fmt.Sprintf("no mcp server named %q", declared.MCPName))
	case 1:
	default:
		return "", "", skip(CapabilitySkipAmbiguous,
			fmt.Sprintf("%d mcp servers are named %q", len(servers), declared.MCPName))
	}

	tools, err := cbs.aoa.ListMCPTools(ctx, servers[0])
	if err != nil {
		return "", "", skip(CapabilitySkipUnreachable, err.Error())
	}
	if tools == nil {
		return "", "", skip(CapabilitySkipNotFound,
			fmt.Sprintf("mcp server %q disappeared between lookup and read", declared.MCPName))
	}
	if !hasMCPTool(tools, toolName) {
		return "", "", skip(CapabilitySkipNotFound,
			fmt.Sprintf("mcp server %q exposes no tool named %q", declared.MCPName, toolName))
	}
	if unusable := mcpUnusableReason(tools, toolName); unusable != "" {
		return "", "", skip(CapabilitySkipUnusable, unusable)
	}
	return servers[0], toolName, nil
}

// hasMCPTool reports whether the listing exposes a tool by this exact name.
func hasMCPTool(tools []*interfaces.MCPToolBrief, toolName string) bool {
	for _, tool := range tools {
		if tool != nil && tool.Name == toolName {
			return true
		}
	}
	return false
}

// mcpUnusableReason reports why a server's tools cannot be bound, or the empty string when they
// can. It mirrors toolUnusableReason: resolution has to apply the same bar the mount enforces, or
// one entry fails the whole batch and takes every cleanly resolved one with it.
func mcpUnusableReason(tools []*interfaces.MCPToolBrief, toolName string) string {
	if len(tools) == 0 {
		return ""
	}
	if !mcpIsUsable(tools[0]) {
		return fmt.Sprintf("mcp server %s is %s", tools[0].MCPID, tools[0].MCPStatus)
	}
	return ""
}
