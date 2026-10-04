// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import "strings"

var businessRefPrefixes = map[string]string{
	"knowledge_network": "kn",
	"object_type":       "object",
	"object_instance":   "object_instance",
	"property":          "property",
	"relation_type":     "relation",
	"metric":            "metric",
	"logic":             "logic",
	"function":          "function",
	"action_type":       "action_type",
	"action_instance":   "action_instance",
	"data_resource":     "resource",
}

var businessRefMinSegments = map[string]int{
	"knowledge_network": 2,
	"object_type":       3,
	"object_instance":   4,
	"property":          4,
	"relation_type":     3,
	"metric":            3,
	"logic":             3,
	"function":          3,
	"action_type":       3,
	"action_instance":   4,
	"data_resource":     2,
}

// DeriveToolBusinessRefs records targets named by structured request fields.
// MCP and managed HTTP calls share this request tier; observed evidence can
// provide the resolved versions and schema IDs later. Caller declarations and
// query text are never used to infer a target.
func DeriveToolBusinessRefs(toolName string, input map[string]any, currentKNID string) []BusinessRef {
	if currentKNID == "" {
		return nil
	}
	if inputKNID := businessRefString(input["kn_id"]); inputKNID != "" && inputKNID != currentKNID {
		return nil
	}
	refs := []BusinessRef{{RefType: "knowledge_network", RefID: "kn:" + currentKNID, Version: "unversioned"}}
	switch toolName {
	case "search_instance":
		// Semantic instance search can legitimately return no object type. The
		// addressed network is still a deterministic request scope.
		return refs
	case "query_object_instance", "explore_subgraph":
		field := "ot_id"
		if toolName == "explore_subgraph" {
			field = "source_object_type_id"
		}
		objectID := businessRefString(input[field])
		if objectID == "" {
			return nil
		}
		return append(refs, BusinessRef{RefType: "object_type", RefID: "object:" + currentKNID + ":" + objectID, Version: "unversioned"})
	case "query_instance_subgraph":
		// Keep only the fixed, typed path fields. Do not recursively scan query
		// conditions or infer targets from arbitrary IDs.
		paths, ok := input["relation_type_paths"].([]any)
		if !ok {
			return refs
		}
		seen := map[string]bool{"kn:" + currentKNID: true}
		for _, rawPath := range paths {
			path, ok := rawPath.(map[string]any)
			if !ok {
				continue
			}
			if objects, ok := path["object_types"].([]any); ok {
				for _, rawObject := range objects {
					object, ok := rawObject.(map[string]any)
					if !ok {
						continue
					}
					id := businessRefString(object["id"])
					refID := "object:" + currentKNID + ":" + id
					if id != "" && !seen[refID] && len(seen) < 65 {
						seen[refID] = true
						refs = append(refs, BusinessRef{RefType: "object_type", RefID: refID, Version: "unversioned"})
					}
				}
			}
			if relations, ok := path["relation_types"].([]any); ok {
				for _, rawRelation := range relations {
					relation, ok := rawRelation.(map[string]any)
					if !ok {
						continue
					}
					id := businessRefString(relation["relation_type_id"])
					refID := "relation:" + currentKNID + ":" + id
					if id != "" && !seen[refID] && len(seen) < 65 {
						seen[refID] = true
						refs = append(refs, BusinessRef{RefType: "relation_type", RefID: refID, Version: "unversioned"})
					}
				}
			}
		}
		return refs
	case "get_kn_detail", "get_object_types", "get_relation_types", "run_cypher":
		// Schema lookups may name display names; Cypher resolves model IDs in
		// the compiler. Only the known network is authoritative at this tier.
		return refs
	case "query_metric":
		metricID := businessRefString(input["metric_id"])
		if metricID == "" {
			return nil
		}
		return append(refs, BusinessRef{RefType: "metric", RefID: "metric:" + currentKNID + ":" + metricID, Version: "unversioned"})
	default:
		return nil
	}
}

// ParseBusinessRefs accepts only canonical, bounded declarations that belong
// to the knowledge network addressed by the current operation. Both MCP and
// REST call this function so an evidence declaration has one contract.
func ParseBusinessRefs(value any, currentKNID string) ([]BusinessRef, *APIError) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok || len(items) > 64 {
		return nil, invalidBusinessRefError()
	}
	refs := make([]BusinessRef, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		declaration, ok := item.(map[string]any)
		if !ok {
			return nil, invalidBusinessRefError()
		}
		for field := range declaration {
			if field != "ref_type" && field != "ref_id" && field != "version" {
				return nil, invalidBusinessRefError()
			}
		}
		refType := strings.TrimSpace(businessRefString(declaration["ref_type"]))
		refID := strings.TrimSpace(businessRefString(declaration["ref_id"]))
		prefix, registered := businessRefPrefixes[refType]
		parts := strings.Split(refID, ":")
		if !registered || len(refID) > 512 || len(parts) < businessRefMinSegments[refType] || parts[0] != prefix {
			return nil, invalidBusinessRefError()
		}
		for _, part := range parts {
			if strings.TrimSpace(part) == "" {
				return nil, invalidBusinessRefError()
			}
		}
		if refType != "data_resource" && (currentKNID == "" || len(parts) < 2 || parts[1] != currentKNID) {
			return nil, invalidBusinessRefError()
		}
		version := strings.TrimSpace(businessRefString(declaration["version"]))
		if version == "" {
			version = "unversioned"
		}
		if len(version) > 128 {
			return nil, invalidBusinessRefError()
		}
		key := refType + "\x00" + refID + "\x00" + version
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, BusinessRef{RefType: refType, RefID: refID, Version: version})
	}
	return refs, nil
}

func invalidBusinessRefError() *APIError {
	return &APIError{
		Code: "invalid_business_ref", Message: "business_refs must use canonical identifiers from the current knowledge network",
		RequiredAction: "correct_business_refs",
	}
}

func businessRefString(value any) string {
	text, _ := value.(string)
	return text
}
