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
	bindings []*interfaces.CapabilityBinding, withDetail bool) backfillResult {
	result := backfillResult{available: true}
	if len(bindings) == 0 {
		return result
	}

	boxBindings := map[string][]*interfaces.CapabilityBinding{}
	skillBindings := map[string][]*interfaces.CapabilityBinding{}
	for _, binding := range bindings {
		switch binding.CapabilityType {
		case interfaces.CAPABILITY_TYPE_FUNCTION:
			boxBindings[binding.OwnerID] = append(boxBindings[binding.OwnerID], binding)
		case interfaces.CAPABILITY_TYPE_SKILL:
			skillBindings[binding.CapabilityID] = append(skillBindings[binding.CapabilityID], binding)
		}
	}

	if len(boxBindings) > 0 {
		summaries, ok := cbs.backfillFunctions(ctx, boxBindings, withDetail)
		result.boxes = summaries
		result.available = result.available && ok
	}
	if len(skillBindings) > 0 {
		result.available = cbs.backfillSkills(ctx, skillBindings, withDetail) && result.available
	}
	return result
}

// backfillFunctions resolves one tool box per call and reports what is bound versus what the box
// currently holds.
func (cbs *capabilityBindingService) backfillFunctions(ctx context.Context,
	boxBindings map[string][]*interfaces.CapabilityBinding, withDetail bool) ([]*interfaces.CapabilityBoxSummary, bool) {
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

		mounted := map[string]struct{}{}
		for _, binding := range bindings {
			tool, ok := byToolID[binding.CapabilityID]
			if !ok {
				binding.Status = interfaces.CAPABILITY_STATUS_MISSING
				continue
			}
			binding.Name = tool.Name
			binding.OwnerName = tool.BoxName
			if withDetail {
				binding.Description = tool.Description
			}
			binding.Status = tool.Status
			if tool.Status == interfaces.EXEC_TOOL_STATUS_ENABLED {
				mounted[tool.ToolID] = struct{}{}
			}
		}
		summary.MountedTools = len(mounted)
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
