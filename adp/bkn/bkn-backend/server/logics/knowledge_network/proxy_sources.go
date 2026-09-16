// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"bkn-backend/interfaces"
)

type proxyModelBinding struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Detail     string `json:"detail,omitempty"`
}

// buildProxyGrantSources derives the complete least-privilege source set from
// one candidate or freshly reloaded main model. It does not accept targets from
// request-specific proxy fields; every target comes from persisted BKN bindings.
func buildProxyGrantSources(kn *interfaces.KN, boxTypes ...map[string]string) ([]interfaces.ProxyGrantSourceSpec, string, error) {
	return buildProxyGrantSourcesWithCapabilities(kn, nil, boxTypes...)
}

// buildProxyGrantSourcesWithCapabilities includes explicit capability mounts
// in the same canonical projection as model-derived dependencies.
func buildProxyGrantSourcesWithCapabilities(kn *interfaces.KN,
	capabilities []*interfaces.CapabilityBinding, boxTypes ...map[string]string) ([]interfaces.ProxyGrantSourceSpec, string, error) {
	if kn == nil || strings.TrimSpace(kn.KNID) == "" {
		return nil, "", fmt.Errorf("knowledge network is required")
	}

	// Whole-network imports persist concept-group children through the same
	// object/relation/action services as top-level children. Include both
	// representations so proxy preflight covers every strict lookup that can
	// run later in the request.
	projectedObjectTypes := append([]*interfaces.ObjectType(nil), kn.ObjectTypes...)
	projectedRelationTypes := append([]*interfaces.RelationType(nil), kn.RelationTypes...)
	projectedActionTypes := append([]*interfaces.ActionType(nil), kn.ActionTypes...)
	for _, conceptGroup := range kn.ConceptGroups {
		if conceptGroup == nil {
			continue
		}
		projectedObjectTypes = append(projectedObjectTypes, conceptGroup.ObjectTypes...)
		projectedRelationTypes = append(projectedRelationTypes, conceptGroup.RelationTypes...)
		projectedActionTypes = append(projectedActionTypes, conceptGroup.ActionTypes...)
	}

	objectResources := make(map[string]string, len(projectedObjectTypes))
	objectTypes := make(map[string]struct{}, len(projectedObjectTypes))
	sources := make([]interfaces.ProxyGrantSourceSpec, 0)
	bindings := make([]proxyModelBinding, 0)
	seen := make(map[string]struct{})
	seenBindings := make(map[string]struct{})
	boxType := func(boxID string) string {
		if len(boxTypes) == 0 {
			return "tool_box"
		}
		return boxTypes[0][boxID]
	}

	// addSource records one grant source. inVersion says whether the binding
	// also enters the model version digest; see the Skill case below for the
	// one kind that does not.
	addSource := func(inVersion bool, bindingType, bindingID, resourceType, resourceID, operation, detail string) error {
		bindingID = strings.TrimSpace(bindingID)
		resourceID = strings.TrimSpace(resourceID)
		if bindingID == "" || resourceID == "" {
			return fmt.Errorf("%s binding and target IDs are required", bindingType)
		}
		sourceID := stableProxySourceID(kn.KNID, bindingType, bindingID)
		spec := interfaces.ProxyGrantSourceSpec{
			ResourceType: resourceType,
			ResourceID:   resourceID,
			Operation:    operation,
			SourceType:   interfaces.ProxyGrantSourceTypeKNBinding,
			SourceID:     sourceID,
			KNID:         kn.KNID,
			BindingType:  bindingType,
			BindingID:    bindingID,
		}
		key := strings.Join([]string{resourceType, resourceID, operation, sourceID}, "\x00")
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			sources = append(sources, spec)
		}
		if !inVersion {
			return nil
		}
		binding := proxyModelBinding{
			Type: bindingType, ID: bindingID, TargetType: resourceType, TargetID: resourceID, Detail: detail,
		}
		bindingKey := strings.Join([]string{binding.Type, binding.ID, binding.TargetType, binding.TargetID, binding.Detail}, "\x00")
		if _, ok := seenBindings[bindingKey]; !ok {
			seenBindings[bindingKey] = struct{}{}
			bindings = append(bindings, binding)
		}
		return nil
	}
	add := func(bindingType, bindingID, resourceType, resourceID, operation, detail string) error {
		return addSource(true, bindingType, bindingID, resourceType, resourceID, operation, detail)
	}

	for _, objectType := range projectedObjectTypes {
		if objectType == nil {
			continue
		}
		objectTypeID := strings.TrimSpace(objectType.OTID)
		if objectTypeID != "" {
			objectTypes[objectTypeID] = struct{}{}
		}
		if objectType.DataSource != nil && strings.TrimSpace(objectType.DataSource.ID) != "" {
			if strings.TrimSpace(objectType.DataSource.Type) != interfaces.DATA_SOURCE_TYPE_RESOURCE {
				return nil, "", fmt.Errorf("object type %s has unsupported data source type", objectType.OTID)
			}
			resourceID := strings.TrimSpace(objectType.DataSource.ID)
			objectResources[objectTypeID] = resourceID
			if err := add(interfaces.MODULE_TYPE_OBJECT_TYPE, objectType.OTID, "resource", resourceID,
				interfaces.OPERATION_TYPE_VIEW_DETAIL, "schema"); err != nil {
				return nil, "", err
			}
			if err := add(interfaces.MODULE_TYPE_OBJECT_TYPE, objectType.OTID, "resource", resourceID,
				interfaces.OPERATION_TYPE_QUERY_DATA, "data"); err != nil {
				return nil, "", err
			}
		}

		for _, property := range objectType.LogicProperties {
			if property == nil || property.DataSource == nil ||
				strings.TrimSpace(property.DataSource.Type) != interfaces.LOGIC_PROPERTY_TYPE_TOOL {
				continue
			}
			boxID := strings.TrimSpace(property.DataSource.BoxID)
			toolID := strings.TrimSpace(property.DataSource.ToolID)
			// Non-strict imports may keep an unbound logic property as a draft.
			// Grant the toolbox only after the concrete tool binding is complete.
			if boxID == "" || toolID == "" {
				continue
			}
			propertyBindingID := stableProxySourceID(kn.KNID, "logic_property",
				strings.Join([]string{objectTypeID, property.Name}, "\x00"))
			if err := add("logic_property", propertyBindingID, boxType(boxID), boxID,
				interfaces.OPERATION_TYPE_EXECUTE,
				strings.Join([]string{objectTypeID, property.Name, toolID}, ":")); err != nil {
				return nil, "", err
			}
		}
	}

	for _, relationType := range projectedRelationTypes {
		if relationType == nil {
			continue
		}
		backingResource, indirect, err := indirectRelationProxyResource(relationType.MappingRules)
		if err != nil {
			return nil, "", fmt.Errorf("relation type %s has invalid indirect mapping rules: %w", relationType.RTID, err)
		}
		if indirect && backingResource != nil && strings.TrimSpace(backingResource.ID) != "" {
			if strings.TrimSpace(backingResource.Type) != interfaces.DATA_SOURCE_TYPE_RESOURCE {
				return nil, "", fmt.Errorf("relation type %s has unsupported backing data source type", relationType.RTID)
			}
			if err := add(interfaces.MODULE_TYPE_RELATION_TYPE, relationType.RTID, "resource",
				strings.TrimSpace(backingResource.ID), interfaces.OPERATION_TYPE_QUERY_DATA,
				"backing_data_source"); err != nil {
				return nil, "", err
			}
		}
		for _, objectTypeID := range []string{relationType.SourceObjectTypeID, relationType.TargetObjectTypeID} {
			objectTypeID = strings.TrimSpace(objectTypeID)
			// A non-strict import may keep a relation endpoint empty while the
			// model is still being assembled. It has no permission target yet.
			if objectTypeID == "" {
				continue
			}
			if _, exists := objectTypes[objectTypeID]; !exists {
				// Dependency validation belongs to the model layer. Permission
				// projection stays tolerant of draft or legacy references so a
				// caller can repair or remove them through the same publish path.
				continue
			}
			resourceID := objectResources[objectTypeID]
			if resourceID == "" {
				continue
			}
			if err := add(interfaces.MODULE_TYPE_RELATION_TYPE, relationType.RTID, "resource", resourceID,
				interfaces.OPERATION_TYPE_QUERY_DATA, objectTypeID); err != nil {
				return nil, "", err
			}
		}
	}

	for _, metric := range kn.Metrics {
		if metric == nil {
			continue
		}
		scopeType := strings.TrimSpace(metric.ScopeType)
		scopeRef := strings.TrimSpace(metric.ScopeRef)
		// Non-strict imports may persist metrics without a scope. Such metrics
		// have no backing resource and therefore contribute no proxy grant.
		if scopeType == "" && scopeRef == "" {
			continue
		}
		if _, exists := objectTypes[scopeRef]; !exists {
			continue
		}
		resourceID := objectResources[scopeRef]
		if resourceID == "" {
			continue
		}
		if err := add(interfaces.MODULE_TYPE_METRIC, metric.ID, "resource", resourceID,
			interfaces.OPERATION_TYPE_QUERY_DATA, scopeType+":"+scopeRef); err != nil {
			return nil, "", err
		}
	}

	for _, actionType := range projectedActionTypes {
		if actionType == nil || strings.TrimSpace(actionType.ActionSource.Type) == "" {
			continue
		}
		switch strings.TrimSpace(actionType.ActionSource.Type) {
		case interfaces.ACTION_SOURCE_TYPE_TOOL:
			if strings.TrimSpace(actionType.ActionSource.BoxID) == "" ||
				strings.TrimSpace(actionType.ActionSource.ToolID) == "" {
				continue
			}
			if err := add(interfaces.MODULE_TYPE_ACTION_TYPE, actionType.ATID, boxType(actionType.ActionSource.BoxID),
				actionType.ActionSource.BoxID, interfaces.OPERATION_TYPE_EXECUTE,
				"tool:"+actionType.ActionSource.ToolID); err != nil {
				return nil, "", err
			}
		case interfaces.ACTION_SOURCE_TYPE_MCP:
			if strings.TrimSpace(actionType.ActionSource.McpID) == "" ||
				strings.TrimSpace(actionType.ActionSource.ToolName) == "" {
				continue
			}
			if err := add(interfaces.MODULE_TYPE_ACTION_TYPE, actionType.ATID, "mcp",
				actionType.ActionSource.McpID, interfaces.OPERATION_TYPE_EXECUTE,
				"tool:"+actionType.ActionSource.ToolName); err != nil {
				return nil, "", err
			}
		default:
			return nil, "", fmt.Errorf("action type %s has unsupported action source type", actionType.ATID)
		}
	}

	for _, capability := range capabilities {
		if capability == nil || capability.Branch != interfaces.MAIN_BRANCH || capability.KNID != kn.KNID {
			continue
		}
		detail := capability.CapabilityType + ":" + strings.TrimSpace(capability.CapabilityID)
		switch capability.CapabilityType {
		case interfaces.CAPABILITY_TYPE_SKILL:
			// The proxy reads a mounted Skill for callers who may view the network
			// (#1550); running one stays caller-scoped. The source is resolvable and
			// synchronized like any other, but it stays out of the model version:
			// that version gates every proxied read of the network, and a Skill
			// grant is best effort, so a Skill must never be what holds a network's
			// data and execution bindings back.
			// Unlike a tool, an incomplete Skill row is skipped rather than rejected:
			// rejecting it would fail the whole projection, and with it the network.
			if strings.TrimSpace(capability.CapabilityID) == "" || strings.TrimSpace(capability.ID) == "" {
				continue
			}
			if err := addSource(false, interfaces.KNProxyBindingTypeCapability, capability.ID,
				interfaces.KNProxyTargetTypeSkill, capability.CapabilityID,
				interfaces.OPERATION_TYPE_EXECUTE, detail); err != nil {
				return nil, "", err
			}
		case interfaces.CAPABILITY_TYPE_FUNCTION:
			if strings.TrimSpace(capability.CapabilityID) == "" {
				return nil, "", fmt.Errorf("capability binding %s has no tool id", capability.ID)
			}
			if err := add(interfaces.KNProxyBindingTypeCapability, capability.ID, boxType(capability.OwnerID), capability.OwnerID,
				interfaces.OPERATION_TYPE_EXECUTE, detail); err != nil {
				return nil, "", err
			}
		case interfaces.CAPABILITY_TYPE_MCP_TOOL:
			if strings.TrimSpace(capability.CapabilityID) == "" {
				return nil, "", fmt.Errorf("capability binding %s has no MCP tool name", capability.ID)
			}
			if err := add(interfaces.KNProxyBindingTypeCapability, capability.ID, "mcp", capability.OwnerID,
				interfaces.OPERATION_TYPE_EXECUTE, detail); err != nil {
				return nil, "", err
			}
		default:
			return nil, "", fmt.Errorf("capability binding %s has unsupported type %s",
				capability.ID, capability.CapabilityType)
		}
	}

	sort.Slice(sources, func(i, j int) bool {
		left := strings.Join([]string{sources[i].SourceID, sources[i].ResourceType, sources[i].ResourceID, sources[i].Operation}, "\x00")
		right := strings.Join([]string{sources[j].SourceID, sources[j].ResourceType, sources[j].ResourceID, sources[j].Operation}, "\x00")
		return left < right
	})
	sort.Slice(bindings, func(i, j int) bool {
		left := strings.Join([]string{bindings[i].Type, bindings[i].ID, bindings[i].TargetType, bindings[i].TargetID, bindings[i].Detail}, "\x00")
		right := strings.Join([]string{bindings[j].Type, bindings[j].ID, bindings[j].TargetType, bindings[j].TargetID, bindings[j].Detail}, "\x00")
		return left < right
	})
	canonical, err := json.Marshal(bindings)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(canonical)
	return sources, "sha256:" + hex.EncodeToString(digest[:]), nil
}

// buildTypedProxyGrantSources resolves toolbox kinds from execution factory data.
func (kns *knowledgeNetworkService) buildTypedProxyGrantSources(ctx context.Context, kn *interfaces.KN) ([]interfaces.ProxyGrantSourceSpec, string, error) {
	return kns.buildTypedProxyGrantSourcesWithCapabilities(ctx, kn, nil)
}

func (kns *knowledgeNetworkService) buildTypedProxyGrantSourcesWithCapabilities(ctx context.Context, kn *interfaces.KN, capabilities []*interfaces.CapabilityBinding) ([]interfaces.ProxyGrantSourceSpec, string, error) {
	if kn == nil {
		return nil, "", fmt.Errorf("knowledge network is required")
	}
	boxTools := map[string]map[string]bool{}
	addTool := func(boxID, toolID string) {
		if boxID == "" || toolID == "" {
			return
		}
		if boxTools[boxID] == nil {
			boxTools[boxID] = map[string]bool{}
		}
		boxTools[boxID][toolID] = true
	}
	objects := append([]*interfaces.ObjectType(nil), kn.ObjectTypes...)
	actions := append([]*interfaces.ActionType(nil), kn.ActionTypes...)
	for _, group := range kn.ConceptGroups {
		if group != nil {
			objects = append(objects, group.ObjectTypes...)
			actions = append(actions, group.ActionTypes...)
		}
	}
	for _, objectType := range objects {
		if objectType == nil {
			continue
		}
		for _, property := range objectType.LogicProperties {
			if property != nil && property.DataSource != nil && property.DataSource.Type == interfaces.LOGIC_PROPERTY_TYPE_TOOL && property.DataSource.BoxID != "" && property.DataSource.ToolID != "" {
				addTool(property.DataSource.BoxID, property.DataSource.ToolID)
			}
		}
	}
	for _, action := range actions {
		if action != nil && action.ActionSource.Type == interfaces.ACTION_SOURCE_TYPE_TOOL && action.ActionSource.BoxID != "" && action.ActionSource.ToolID != "" {
			addTool(action.ActionSource.BoxID, action.ActionSource.ToolID)
		}
	}
	for _, capability := range capabilities {
		if capability != nil && capability.KNID == kn.KNID && capability.Branch == interfaces.MAIN_BRANCH && capability.CapabilityType == interfaces.CAPABILITY_TYPE_FUNCTION {
			addTool(capability.OwnerID, capability.CapabilityID)
		}
	}
	if len(boxTools) == 0 {
		return buildProxyGrantSourcesWithCapabilities(kn, capabilities, map[string]string{})
	}
	if kns.aoa == nil {
		return nil, "", fmt.Errorf("execution factory access is unavailable")
	}
	boxTypes := map[string]string{}
	for boxID, expectedTools := range boxTools {
		tools, err := kns.aoa.ListBoxTools(ctx, boxID)
		if err != nil {
			return nil, "", err
		}
		for _, tool := range tools {
			if tool == nil || !expectedTools[tool.ToolID] {
				continue
			}
			switch tool.BoxMetadataType {
			case interfaces.EXEC_BOX_METADATA_TYPE_OPENAPI:
				boxTypes[boxID] = "tool_box"
			case interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION:
				boxTypes[boxID] = "function"
			default:
				return nil, "", fmt.Errorf("box %s has unsupported metadata type", boxID)
			}
			delete(expectedTools, tool.ToolID)
		}
		if len(expectedTools) > 0 {
			return nil, "", fmt.Errorf("box %s has missing bound tools", boxID)
		}
	}
	return buildProxyGrantSourcesWithCapabilities(kn, capabilities, boxTypes)
}

func indirectRelationProxyResource(mappingRules any) (*interfaces.ResourceInfo, bool, error) {
	switch rules := mappingRules.(type) {
	case *interfaces.InDirectMapping:
		if rules == nil {
			return nil, true, nil
		}
		return rules.BackingDataSource, true, nil
	case interfaces.InDirectMapping:
		return rules.BackingDataSource, true, nil
	case map[string]any:
		if _, exists := rules["backing_data_source"]; !exists {
			return nil, false, nil
		}
		data, err := json.Marshal(rules)
		if err != nil {
			return nil, true, err
		}
		var decoded interfaces.InDirectMapping
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil, true, err
		}
		return decoded.BackingDataSource, true, nil
	default:
		return nil, false, nil
	}
}

func stableProxySourceID(knID, bindingType, bindingID string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{knID, bindingType, bindingID}, "\x00")))
	return hex.EncodeToString(digest[:])
}
