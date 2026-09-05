// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package capability_binding

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

// resolvedCapability is one capability to bind after the request has been normalised and, for a
// whole-box mount, expanded.
type resolvedCapability struct {
	capabilityType string
	ownerID        string
	capabilityID   string
	comment        string
	boundAsBox     bool
}

// boxCache holds one tool-box lookup per box for the duration of a request. A whole-box mount
// validates and expands from the same response, and several entries naming the same box cost one
// call rather than one each.
type boxCache struct {
	svc    *capabilityBindingService
	loaded map[string][]*interfaces.ToolBrief
}

func newBoxCache(svc *capabilityBindingService) *boxCache {
	return &boxCache{svc: svc, loaded: map[string][]*interfaces.ToolBrief{}}
}

// tools returns the box's tools, or nil when the box does not exist. A transport failure is
// reported as 502: the target may well be valid and the write must not proceed on a guess.
func (c *boxCache) tools(ctx context.Context, boxID string) ([]*interfaces.ToolBrief, error) {
	if tools, ok := c.loaded[boxID]; ok {
		return tools, nil
	}
	tools, err := c.svc.aoa.ListBoxTools(ctx, boxID)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusBadGateway,
			berrors.BknBackend_CapabilityBinding_ExecutionFactoryUnavailable).
			WithErrorDetails(fmt.Sprintf("tool box %s: %s", boxID, err.Error()))
	}
	c.loaded[boxID] = tools
	return tools, nil
}

// resolveEntries normalises every request entry and expands whole-box mounts, then validates each
// resulting capability against the execution factory.
//
// Validation happens before anything is written. Binding a capability that does not exist, or one
// that cannot be called, produces a knowledge network that advertises something unusable; the
// error has to say which of the two it is, because the fixes are different — check the id, or
// publish the target.
func (cbs *capabilityBindingService) resolveEntries(ctx context.Context,
	entries []*interfaces.AttachCapabilityEntry) ([]*resolvedCapability, error) {
	boxes := newBoxCache(cbs)
	resolved := make([]*resolvedCapability, 0, len(entries))

	for _, entry := range entries {
		capabilityType, ownerID, capabilityID, err := normalizeAttachEntry(ctx, entry)
		if entry != nil && entry.AllTools && strings.TrimSpace(entry.CapabilityType) == interfaces.CAPABILITY_TYPE_FUNCTION {
			expanded, expandErr := cbs.expandBox(ctx, boxes, entry)
			if expandErr != nil {
				return nil, expandErr
			}
			resolved = append(resolved, expanded...)
			continue
		}
		if err != nil {
			return nil, err
		}

		switch capabilityType {
		case interfaces.CAPABILITY_TYPE_SKILL:
			if err := cbs.validateSkill(ctx, capabilityID); err != nil {
				return nil, err
			}
		case interfaces.CAPABILITY_TYPE_FUNCTION:
			if err := cbs.validateTool(ctx, boxes, ownerID, capabilityID); err != nil {
				return nil, err
			}
		}
		resolved = append(resolved, &resolvedCapability{
			capabilityType: capabilityType,
			ownerID:        ownerID,
			capabilityID:   capabilityID,
			comment:        strings.TrimSpace(entry.Comment),
		})
	}
	return resolved, nil
}

// expandBox turns a whole-box mount into one binding per enabled tool.
//
// Only enabled tools are taken: a disabled one is rejected when named directly, and expanding it
// here would create through the back door a binding the front door refuses. A box with no tools
// to mount is a 400 rather than a silent success, so "I mounted the box" and "nothing happened"
// cannot look the same.
func (cbs *capabilityBindingService) expandBox(ctx context.Context, boxes *boxCache,
	entry *interfaces.AttachCapabilityEntry) ([]*resolvedCapability, error) {
	boxID := strings.TrimSpace(entry.OwnerID)
	if boxID == "" {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_NullParameter_OwnerID)
	}

	tools, err := boxes.tools(ctx, boxID)
	if err != nil {
		return nil, err
	}
	if tools == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_TargetNotFound).
			WithErrorDetails(fmt.Sprintf("tool box not found: box_id=%s", boxID))
	}
	if len(tools) > 0 && !boxIsUsable(tools[0]) {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_TargetNotAvailable).
			WithErrorDetails(fmt.Sprintf("tool box is not published: box_id=%s status=%s", boxID, tools[0].BoxStatus))
	}

	comment := strings.TrimSpace(entry.Comment)
	expanded := make([]*resolvedCapability, 0, len(tools))
	for _, tool := range tools {
		if tool.Status != interfaces.EXEC_TOOL_STATUS_ENABLED {
			continue
		}
		expanded = append(expanded, &resolvedCapability{
			capabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
			ownerID:        boxID,
			capabilityID:   tool.ToolID,
			comment:        comment,
			boundAsBox:     true,
		})
	}
	if len(expanded) == 0 {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_EmptyToolBox).
			WithErrorDetails(fmt.Sprintf("tool box has no enabled tool to mount: box_id=%s", boxID))
	}
	return expanded, nil
}

func (cbs *capabilityBindingService) validateSkill(ctx context.Context, skillID string) error {
	skill, err := cbs.aoa.GetSkillByID(ctx, skillID)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadGateway,
			berrors.BknBackend_CapabilityBinding_ExecutionFactoryUnavailable).
			WithErrorDetails(fmt.Sprintf("skill %s: %s", skillID, err.Error()))
	}
	if skill == nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_TargetNotFound).
			WithErrorDetails(fmt.Sprintf("skill not found: skill_id=%s", skillID))
	}
	if skill.Status != interfaces.EXEC_SKILL_STATUS_PUBLISHED {
		return rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_TargetNotAvailable).
			WithErrorDetails(fmt.Sprintf("skill is not published: skill_id=%s status=%s", skillID, skill.Status))
	}
	return nil
}

// validateTool checks one tool of a box. metadata_type is deliberately not consulted: openapi and
// function tools are both bindable, because a binding names a tool and the box is only the
// execution factory's container for it.
func (cbs *capabilityBindingService) validateTool(ctx context.Context, boxes *boxCache, boxID, toolID string) error {
	tools, err := boxes.tools(ctx, boxID)
	if err != nil {
		return err
	}
	if tools == nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_TargetNotFound).
			WithErrorDetails(fmt.Sprintf("tool box not found: box_id=%s", boxID))
	}
	for _, tool := range tools {
		if tool.ToolID != toolID {
			continue
		}
		if !boxIsUsable(tool) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_CapabilityBinding_TargetNotAvailable).
				WithErrorDetails(fmt.Sprintf("tool box is not published: box_id=%s status=%s", boxID, tool.BoxStatus))
		}
		if tool.Status != interfaces.EXEC_TOOL_STATUS_ENABLED {
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_CapabilityBinding_TargetNotAvailable).
				WithErrorDetails(fmt.Sprintf("tool is disabled: box_id=%s tool_id=%s", boxID, toolID))
		}
		return nil
	}
	return rest.NewHTTPError(ctx, http.StatusBadRequest,
		berrors.BknBackend_CapabilityBinding_TargetNotFound).
		WithErrorDetails(fmt.Sprintf("tool not found: box_id=%s tool_id=%s", boxID, toolID))
}

// boxIsUsable reports whether the tool's box is in a state that lets the tool be called. Internal
// boxes are platform-managed and go through the same publication state, so they need no exemption.
func boxIsUsable(tool *interfaces.ToolBrief) bool {
	return tool.BoxStatus == interfaces.EXEC_BOX_STATUS_PUBLISHED
}
