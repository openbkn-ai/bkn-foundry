// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package capability_binding

import (
	"context"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"bkn-backend/interfaces"
)

// capabilityKey identifies a capability across both the bindings and the model that uses it.
type capabilityKey struct {
	capabilityType string
	ownerID        string
	capabilityID   string
}

func (k capabilityKey) empty() bool {
	return k.capabilityID == ""
}

// provenance is what the model says about the tools a network uses: which object types and action
// types reach for which capability.
type provenance struct {
	// byCapability maps a capability to the object types and action types using it.
	byCapability map[capabilityKey][]*interfaces.CapabilitySource
	// available is false when the model could not be read. The bindings are still listed; only
	// the "why" is missing, and saying so is better than reporting no references at all.
	available bool
}

// collectProvenance reads the branch's object types and action types and records which capability
// each one uses.
//
// This is computed on read rather than written into the binding table. The truth about what uses
// a tool lives in the object type and the action type; a copy in the binding rows would have to
// be maintained on every edit to either, and the copy is what drifts. Reading it costs two
// queries over one branch — both store their references inline as JSON columns, so neither fans
// out per row.
func (cbs *capabilityBindingService) collectProvenance(ctx context.Context, knID,
	branch string) *provenance {
	result := &provenance{
		byCapability: map[capabilityKey][]*interfaces.CapabilitySource{},
		available:    true,
	}
	if cbs.ota == nil || cbs.ata == nil {
		result.available = false
		return result
	}

	// One entry per (capability, kind), so several object types using the same tool collapse into
	// one source carrying several refs rather than repeating the kind.
	refs := map[capabilityKey]map[string][]*interfaces.CapabilitySourceRef{}
	add := func(key capabilityKey, kind string, ref *interfaces.CapabilitySourceRef) {
		if key.empty() {
			return
		}
		if refs[key] == nil {
			refs[key] = map[string][]*interfaces.CapabilitySourceRef{}
		}
		refs[key][kind] = append(refs[key][kind], ref)
	}

	objectTypes, err := cbs.ota.ListObjectTypes(ctx, nil, interfaces.ObjectTypesQueryParams{
		KNID:   knID,
		Branch: branch,
		PaginationQueryParameters: interfaces.PaginationQueryParameters{
			Limit: noPagingLimit,
		},
	})
	if err != nil {
		logger.Warnf("capability provenance: object types unreadable for kn_id=%s: %v", knID, err)
		result.available = false
	}
	for _, objectType := range objectTypes {
		if objectType == nil {
			continue
		}
		for _, property := range objectType.LogicProperties {
			if property == nil || property.DataSource == nil {
				continue
			}
			key := functionKeyOf(property.DataSource.BoxID, property.DataSource.ToolID)
			add(key, interfaces.CAPABILITY_SOURCE_OBJECT_TYPE, &interfaces.CapabilitySourceRef{
				ID:   objectType.OTID,
				Name: objectType.OTName,
				// Which property, not just which object type: dropping a property and dropping
				// the object type are different repairs.
				Property: property.Name,
			})
		}
	}

	actionTypes, err := cbs.ata.ListActionTypes(ctx, interfaces.ActionTypesQueryParams{
		KNID:   knID,
		Branch: branch,
		PaginationQueryParameters: interfaces.PaginationQueryParameters{
			Limit: noPagingLimit,
		},
	})
	if err != nil {
		logger.Warnf("capability provenance: action types unreadable for kn_id=%s: %v", knID, err)
		result.available = false
	}
	for _, actionType := range actionTypes {
		if actionType == nil {
			continue
		}
		key := actionSourceKey(actionType.ActionSource)
		add(key, interfaces.CAPABILITY_SOURCE_ACTION_TYPE, &interfaces.CapabilitySourceRef{
			ID:   actionType.ATID,
			Name: actionType.ATName,
		})
	}

	for key, byKind := range refs {
		sources := make([]*interfaces.CapabilitySource, 0, len(byKind))
		// Object types before action types, so the order does not depend on map iteration.
		for _, kind := range []string{
			interfaces.CAPABILITY_SOURCE_OBJECT_TYPE,
			interfaces.CAPABILITY_SOURCE_ACTION_TYPE,
		} {
			if kindRefs, ok := byKind[kind]; ok {
				sources = append(sources, &interfaces.CapabilitySource{Kind: kind, Refs: kindRefs})
			}
		}
		result.byCapability[key] = sources
	}
	return result
}

// noPagingLimit disables paging on the model reads. A page of the object types would produce a
// page of the reasons, and a missing reason reads as "nothing uses this" — the opposite of true.
const noPagingLimit = -1

// functionKeyOf builds the key of a tool box tool, or an empty key when the reference is not one.
func functionKeyOf(boxID, toolID string) capabilityKey {
	boxID, toolID = strings.TrimSpace(boxID), strings.TrimSpace(toolID)
	if boxID == "" || toolID == "" {
		return capabilityKey{}
	}
	return capabilityKey{
		capabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
		ownerID:        boxID,
		capabilityID:   toolID,
	}
}

// actionSourceKey turns an action type's executor into a capability key.
//
// An action type reaches a tool either through a tool box or through an MCP server, and the two
// are different capability types — the same split the bindings make.
func actionSourceKey(source interfaces.ActionSource) capabilityKey {
	if mcpID := strings.TrimSpace(source.McpID); mcpID != "" {
		toolName := strings.TrimSpace(source.ToolName)
		if toolName == "" {
			return capabilityKey{}
		}
		return capabilityKey{
			capabilityType: interfaces.CAPABILITY_TYPE_MCP_TOOL,
			ownerID:        mcpID,
			capabilityID:   toolName,
		}
	}
	return functionKeyOf(source.BoxID, source.ToolID)
}

// keyOfBinding is the same identity, taken from a stored row.
func keyOfBinding(binding *interfaces.CapabilityBinding) capabilityKey {
	if binding == nil {
		return capabilityKey{}
	}
	return capabilityKey{
		capabilityType: binding.CapabilityType,
		ownerID:        binding.OwnerID,
		capabilityID:   binding.CapabilityID,
	}
}

// applyProvenance labels each stored binding with why it is here, and appends the capabilities the
// model uses but nobody mounted.
//
// The appended ones are ordinary members of the list with no manual source. They cannot be
// released — there is no row to delete, and the way to remove one is to change the object type or
// action type that reaches for it.
func applyProvenance(entries []*interfaces.CapabilityBinding, total int, sources *provenance,
	query interfaces.CapabilityBindingsQueryParams) ([]*interfaces.CapabilityBinding, int) {
	if sources == nil {
		return entries, total
	}

	stored := make(map[capabilityKey]struct{}, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		key := keyOfBinding(entry)
		stored[key] = struct{}{}

		// A stored row is always a mount. Expanding a box is still a mount, distinguished so the
		// list view can report how much of a box is not bound.
		kind := interfaces.CAPABILITY_SOURCE_MANUAL
		if entry.BoundAsBox {
			kind = interfaces.CAPABILITY_SOURCE_BOX
		}
		entry.Sources = append([]*interfaces.CapabilitySource{{Kind: kind}}, sources.byCapability[key]...)
	}

	// Only a page that reaches the end can carry the implicit ones without duplicating them
	// across pages. Offset zero with everything fetched is the workspace's own request; any
	// other page shows the stored rows alone.
	if query.Offset > 0 || (query.Limit > 0 && len(entries) >= query.Limit) {
		return entries, total
	}

	for key, refs := range sources.byCapability {
		if _, mounted := stored[key]; mounted {
			continue
		}
		if query.CapabilityType != "" && key.capabilityType != query.CapabilityType {
			continue
		}
		if query.OwnerID != "" && key.ownerID != query.OwnerID {
			continue
		}
		entries = append(entries, &interfaces.CapabilityBinding{
			KNID:           query.KNID,
			Branch:         query.Branch,
			CapabilityType: key.capabilityType,
			OwnerID:        key.ownerID,
			CapabilityID:   key.capabilityID,
			Sources:        refs,
		})
		total++
	}
	return entries, total
}
