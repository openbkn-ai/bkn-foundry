// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package capability_binding

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"bkn-backend/interfaces"
)

// backfillResult carries what a page of bindings needs beyond the stored rows.
type backfillResult struct {
	boxes []*interfaces.CapabilityBoxSummary
	// available is false when any execution-factory call failed. The bindings are still returned;
	// the flag stops an empty name from being read as a deleted capability.
	available bool
}

// backfillMetadata fills name, description and status on a page of bindings, marks the ones whose
// target is gone, and summarises the tool boxes behind whole-box mounts.
//
// Function metadata is read per box, not per tool: the box endpoint inlines its tools, so a page
// of twenty function bindings spread over three boxes costs three calls rather than twenty. Skill
// names are one call for the whole page. Only skill *detail* is per skill, which is why it is
// behind with_detail rather than always paid.
//
// An execution factory that cannot be reached degrades to the stored rows. Listing memberships is
// the point of the endpoint; the names are decoration, and failing the whole request over them
// would take the mount UI down with the factory.
func (cbs *capabilityBindingService) backfillMetadata(ctx context.Context,
	query interfaces.CapabilityBindingsQueryParams, bindings []*interfaces.CapabilityBinding,
	withDetail bool) backfillResult {
	result := backfillResult{available: true}
	if len(bindings) == 0 {
		return result
	}

	boxBindings := map[string][]*interfaces.CapabilityBinding{}
	skillBindings := map[string][]*interfaces.CapabilityBinding{}
	mcpBindings := map[string][]*interfaces.CapabilityBinding{}
	for _, binding := range bindings {
		switch binding.CapabilityType {
		case interfaces.CAPABILITY_TYPE_FUNCTION:
			boxBindings[binding.OwnerID] = append(boxBindings[binding.OwnerID], binding)
		case interfaces.CAPABILITY_TYPE_SKILL:
			skillBindings[binding.CapabilityID] = append(skillBindings[binding.CapabilityID], binding)
		case interfaces.CAPABILITY_TYPE_MCP_TOOL:
			mcpBindings[binding.OwnerID] = append(mcpBindings[binding.OwnerID], binding)
		}
	}

	if len(boxBindings) > 0 {
		summaries, ok := cbs.backfillFunctions(ctx, query, boxBindings, withDetail)
		result.boxes = summaries
		result.available = result.available && ok
	}
	if len(skillBindings) > 0 {
		result.available = cbs.backfillSkills(ctx, skillBindings, withDetail) && result.available
	}
	if len(mcpBindings) > 0 {
		result.available = cbs.backfillMCPTools(ctx, mcpBindings, withDetail) && result.available
	}
	return result
}

// backfillFunctions resolves one tool box per call and reports what is bound versus what the box
// currently holds.
func (cbs *capabilityBindingService) backfillFunctions(ctx context.Context,
	query interfaces.CapabilityBindingsQueryParams, boxBindings map[string][]*interfaces.CapabilityBinding,
	withDetail bool) ([]*interfaces.CapabilityBoxSummary, bool) {
	available := true
	summaries := make([]*interfaces.CapabilityBoxSummary, 0, len(boxBindings))

	for boxID, bindings := range boxBindings {
		tools, err := cbs.aoa.ListBoxTools(ctx, boxID)
		if err != nil {
			// Unreachable is not the same as absent: leaving the metadata empty and flagging the
			// page keeps a transient outage from looking like a deleted tool box.
			logger.Warnf("capability metadata backfill: tool box %s unreachable: %v", boxID, err)
			available = false
			continue
		}

		summary := &interfaces.CapabilityBoxSummary{BoxID: boxID}
		if tools == nil {
			// The box is gone, so every binding under it dangles.
			summary.BoxMissing = true
			for _, binding := range bindings {
				binding.Status = interfaces.CAPABILITY_STATUS_MISSING
			}
			summaries = append(summaries, summary)
			continue
		}

		byToolID := make(map[string]*interfaces.ToolBrief, len(tools))
		for _, tool := range tools {
			byToolID[tool.ToolID] = tool
			if tool.Status == interfaces.EXEC_TOOL_STATUS_ENABLED {
				summary.TotalTools++
			}
			summary.BoxName = tool.BoxName
		}

		for _, binding := range bindings {
			tool, ok := byToolID[binding.CapabilityID]
			if !ok {
				binding.Status = interfaces.CAPABILITY_STATUS_MISSING
				continue
			}
			binding.Name = tool.Name
			binding.OwnerName = tool.BoxName
			binding.MetadataType = tool.BoxMetadataType
			if withDetail {
				binding.Description = tool.Description
			}
			binding.Status = tool.Status
		}

		// The mounted count is over the whole branch, not over this page. Counting the page
		// against a box-wide total mixes two scales: page one of a fully mounted 23-tool box
		// would read "10 of 23 mounted, 13 to add", and the top-up it offers would do nothing
		// because every tool is already bound.
		mounted, err := cbs.countMountedTools(ctx, query, boxID, byToolID)
		if err != nil {
			// A wrong number is worse than no number: the summary is dropped rather than
			// published with a count that would send the caller after tools already bound.
			logger.Warnf("capability metadata backfill: mounted count for box %s failed: %v", boxID, err)
			available = false
			continue
		}
		summary.MountedTools = mounted
		summary.UnmountedTools = summary.TotalTools - summary.MountedTools
		if summary.UnmountedTools < 0 {
			summary.UnmountedTools = 0
		}
		summaries = append(summaries, summary)
	}
	return summaries, available
}

// backfillSkills resolves names for the whole page in one call. The IDs that come back missing are
// exactly the dangling ones, because the endpoint skips unknown IDs instead of returning them
// empty.
func (cbs *capabilityBindingService) backfillSkills(ctx context.Context,
	skillBindings map[string][]*interfaces.CapabilityBinding, withDetail bool) bool {
	skillIDs := make([]string, 0, len(skillBindings))
	for skillID := range skillBindings {
		skillIDs = append(skillIDs, skillID)
	}

	names, err := cbs.aoa.GetSkillNamesByIDs(ctx, skillIDs)
	if err != nil {
		logger.Warnf("capability metadata backfill: skill names unreachable: %v", err)
		return false
	}

	available := true
	for skillID, bindings := range skillBindings {
		name, found := names[skillID]
		for _, binding := range bindings {
			if !found {
				binding.Status = interfaces.CAPABILITY_STATUS_MISSING
				continue
			}
			binding.Name = name
		}
		if !found || !withDetail {
			continue
		}
		// Description and status of a skill are only available one skill at a time, so this fan-out
		// is bounded by the page size and is the reason detail is opt-in.
		skill, detailErr := cbs.aoa.GetSkillByID(ctx, skillID)
		if detailErr != nil {
			logger.Warnf("capability metadata backfill: skill %s detail unreachable: %v", skillID, detailErr)
			available = false
			continue
		}
		if skill == nil {
			for _, binding := range bindings {
				binding.Status = interfaces.CAPABILITY_STATUS_MISSING
			}
			continue
		}
		for _, binding := range bindings {
			binding.Description = skill.Description
			binding.Status = skill.Status
		}
	}
	return available
}

// countMountedTools counts the box's enabled tools bound anywhere on this branch.
//
// It reads the bindings of one box rather than reusing the page: the page is a slice of the whole
// list, while the box total it is compared against is not, and subtracting one from the other
// produces a number that is only right when the page happens to hold every binding of the box.
func (cbs *capabilityBindingService) countMountedTools(ctx context.Context,
	query interfaces.CapabilityBindingsQueryParams, boxID string,
	tools map[string]*interfaces.ToolBrief) (int, error) {
	rows, err := cbs.cba.ListBindings(ctx, interfaces.CapabilityBindingsQueryParams{
		KNID:           query.KNID,
		Branch:         query.Branch,
		CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
		OwnerID:        boxID,
	})
	if err != nil {
		return 0, err
	}
	mounted := map[string]struct{}{}
	for _, row := range rows {
		tool, ok := tools[row.CapabilityID]
		if !ok || tool.Status != interfaces.EXEC_TOOL_STATUS_ENABLED {
			// A binding to a tool that is gone or disabled is not something a top-up would
			// re-add, so it does not count as mounted either.
			continue
		}
		mounted[row.CapabilityID] = struct{}{}
	}
	return len(mounted), nil
}

// backfillMCPTools resolves names and descriptions one MCP Server at a time, matching how the
// function side reads a whole tool box per call rather than a tool at a time.
//
// A server that is gone marks every binding under it missing; a name the server no longer exposes
// marks just that one. The two are different repairs — re-point the binding, or re-publish the
// server — so they must not look alike.
func (cbs *capabilityBindingService) backfillMCPTools(ctx context.Context,
	mcpBindings map[string][]*interfaces.CapabilityBinding, withDetail bool) bool {
	available := true

	for mcpID, bindings := range mcpBindings {
		tools, err := cbs.aoa.ListMCPTools(ctx, mcpID)
		if err != nil {
			// Unreachable is not absent: leaving the metadata empty and flagging the page keeps a
			// transient outage from looking like a deleted MCP Server.
			logger.Warnf("capability metadata backfill: mcp server %s unreachable: %v", mcpID, err)
			available = false
			continue
		}
		if tools == nil {
			for _, binding := range bindings {
				binding.Status = interfaces.CAPABILITY_STATUS_MISSING
			}
			continue
		}

		byName := make(map[string]*interfaces.MCPToolBrief, len(tools))
		serverName := ""
		serverStatus := ""
		for _, tool := range tools {
			byName[tool.Name] = tool
			serverName = tool.MCPName
			serverStatus = tool.MCPStatus
		}

		for _, binding := range bindings {
			tool, ok := byName[binding.CapabilityID]
			if !ok {
				binding.Status = interfaces.CAPABILITY_STATUS_MISSING
				continue
			}
			binding.Name = tool.Name
			binding.OwnerName = serverName
			if withDetail {
				binding.Description = tool.Description
			}
			// An MCP tool has no status of its own; it is callable exactly when its server is
			// published. The value written here is the tool-level vocabulary the function side
			// uses (enabled/disabled), not the server's lifecycle state: a reader filtering on
			// "enabled" would otherwise treat every healthy MCP tool as unusable, and the
			// server's own state is already carried by owner metadata.
			if serverStatus == interfaces.EXEC_BOX_STATUS_PUBLISHED {
				binding.Status = interfaces.EXEC_TOOL_STATUS_ENABLED
			} else {
				binding.Status = interfaces.EXEC_TOOL_STATUS_DISABLED
			}
		}
	}
	return available
}
