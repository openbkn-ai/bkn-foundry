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
