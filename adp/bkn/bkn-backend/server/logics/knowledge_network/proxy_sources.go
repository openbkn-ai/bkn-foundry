// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
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
func buildProxyGrantSources(kn *interfaces.KN) ([]interfaces.ProxyGrantSourceSpec, string, error) {
	if kn == nil || strings.TrimSpace(kn.KNID) == "" {
		return nil, "", fmt.Errorf("knowledge network is required")
	}

	objectResources := make(map[string]string, len(kn.ObjectTypes))
	sources := make([]interfaces.ProxyGrantSourceSpec, 0)
	bindings := make([]proxyModelBinding, 0)
	seen := make(map[string]struct{})

	add := func(bindingType, bindingID, resourceType, resourceID, operation, detail string) error {
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
		bindings = append(bindings, proxyModelBinding{
			Type: bindingType, ID: bindingID, TargetType: resourceType, TargetID: resourceID, Detail: detail,
		})
		return nil
	}

	for _, objectType := range kn.ObjectTypes {
		if objectType == nil || objectType.DataSource == nil || strings.TrimSpace(objectType.DataSource.ID) == "" {
			continue
		}
		if strings.TrimSpace(objectType.DataSource.Type) != interfaces.DATA_SOURCE_TYPE_RESOURCE {
			return nil, "", fmt.Errorf("object type %s has unsupported data source type", objectType.OTID)
		}
		resourceID := strings.TrimSpace(objectType.DataSource.ID)
		objectResources[objectType.OTID] = resourceID
		if err := add(interfaces.MODULE_TYPE_OBJECT_TYPE, objectType.OTID, "resource", resourceID,
			interfaces.OPERATION_TYPE_VIEW_DETAIL, "schema"); err != nil {
			return nil, "", err
		}
		if err := add(interfaces.MODULE_TYPE_OBJECT_TYPE, objectType.OTID, "resource", resourceID,
			interfaces.OPERATION_TYPE_QUERY_DATA, "data"); err != nil {
			return nil, "", err
		}
	}

	for _, relationType := range kn.RelationTypes {
		if relationType == nil {
			continue
		}
		for _, objectTypeID := range []string{relationType.SourceObjectTypeID, relationType.TargetObjectTypeID} {
			resourceID := objectResources[objectTypeID]
			if resourceID == "" {
				return nil, "", fmt.Errorf("relation type %s references object type %s without a resource binding",
					relationType.RTID, objectTypeID)
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
		resourceID := objectResources[scopeRef]
		if resourceID == "" {
			return nil, "", fmt.Errorf("metric %s references scope %s without a resource binding", metric.ID, metric.ScopeRef)
		}
		if err := add(interfaces.MODULE_TYPE_METRIC, metric.ID, "resource", resourceID,
			interfaces.OPERATION_TYPE_QUERY_DATA, scopeType+":"+scopeRef); err != nil {
			return nil, "", err
		}
	}

	for _, actionType := range kn.ActionTypes {
		if actionType == nil || strings.TrimSpace(actionType.ActionSource.Type) == "" {
			continue
		}
		switch strings.TrimSpace(actionType.ActionSource.Type) {
		case interfaces.ACTION_SOURCE_TYPE_TOOL:
			if err := add(interfaces.MODULE_TYPE_ACTION_TYPE, actionType.ATID, "tool_box",
				actionType.ActionSource.BoxID, interfaces.OPERATION_TYPE_EXECUTE,
				"tool:"+actionType.ActionSource.ToolID); err != nil {
				return nil, "", err
			}
		case interfaces.ACTION_SOURCE_TYPE_MCP:
			if err := add(interfaces.MODULE_TYPE_ACTION_TYPE, actionType.ATID, "mcp",
				actionType.ActionSource.McpID, interfaces.OPERATION_TYPE_EXECUTE,
				"tool:"+actionType.ActionSource.ToolName); err != nil {
				return nil, "", err
			}
		default:
			return nil, "", fmt.Errorf("action type %s has unsupported action source type", actionType.ATID)
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

func stableProxySourceID(knID, bindingType, bindingID string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{knID, bindingType, bindingID}, "\x00")))
	return hex.EncodeToString(digest[:])
}
