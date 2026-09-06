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

	entries := make([]*interfaces.AttachCapabilityEntry, 0,
		len(declared.Skills)+len(declared.Functions))

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
			if tool.Status != interfaces.EXEC_TOOL_STATUS_ENABLED {
				return "", "", skip(CapabilitySkipUnusable,
					fmt.Sprintf("tool %s is %s", declared.ToolID, tool.Status))
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
		if named[0].Status != interfaces.EXEC_TOOL_STATUS_ENABLED {
			return "", "", skip(CapabilitySkipUnusable,
				fmt.Sprintf("tool %q is %s", declared.ToolName, named[0].Status))
		}
		return boxes[0].BoxID, named[0].ToolID, nil
	default:
		return "", "", skip(CapabilitySkipAmbiguous,
			fmt.Sprintf("%d tools in %q are named %q", len(named), declared.BoxName, declared.ToolName))
	}
}

func findTool(tools []*interfaces.ToolBrief, toolID string) *interfaces.ToolBrief {
	for _, tool := range tools {
		if tool.ToolID == toolID {
			return tool
		}
	}
	return nil
}
