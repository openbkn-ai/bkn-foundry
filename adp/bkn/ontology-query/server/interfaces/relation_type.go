// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import cond "ontology-query/common/condition"

const (
	RELATION_TYPE_DIRECT              = "direct"
	RELATION_TYPE_FILTERED_CROSS_JOIN = "filtered_cross_join"
)

type RelationType struct {
	RTID               string `json:"id"`
	RTName             string `json:"name"`
	SourceObjectTypeID string `json:"source_object_type_id"`
	TargetObjectTypeID string `json:"target_object_type_id"`
	Type               string `json:"type"`
	MappingRules       any    `json:"mapping_rules"` // Mapping shape depends on type; direct uses []Mapping.
}

// Indirect relationship.
type InDirectMapping struct {
	BackingDataSource  *ResourceInfo `json:"backing_data_source"`
	SourceMappingRules []Mapping     `json:"source_mapping_rules"`
	TargetMappingRules []Mapping     `json:"target_mapping_rules"`
}

// Direct relationship.
type Mapping struct {
	SourceProp SimpleProperty `json:"source_property"`
	TargetProp SimpleProperty `json:"target_property"`
}

type SimpleProperty struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// FilteredCrossJoinMapping rules for relation type filtered_cross_join (per-side conditions, no key mapping).
type FilteredCrossJoinMapping struct {
	SourceCondition *cond.CondCfg `json:"source_condition"`
	TargetCondition *cond.CondCfg `json:"target_condition"`
}
