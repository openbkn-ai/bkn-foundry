// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	cond "ontology-query/common/condition"
)

const (
	// Object-type data sources aligned with bkn-backend.
	DATA_SOURCE_TYPE_RESOURCE = "resource"

	// Logical property types.
	LOGIC_PROPERTY_TYPE_METRIC = "metric"
	LOGIC_PROPERTY_TYPE_TOOL   = "tool"

	// Logic-property parameter source types.
	LOGIC_PARAMS_VALUE_FROM_PROP  = "property"
	LOGIC_PARAMS_VALUE_FROM_INPUT = "input"
	LOGIC_PARAMS_VALUE_FROM_CONST = "const"
	LOGIC_PARAMS_VALUE_FROM_PARAM = "param" // RiskFunction parameter value sourced from a RiskType parameter; value is ParamDef.name.

	// USE_SEARCH_AFTER
	USE_SEARCH_AFTER_TRUE = true

	// System field constants.
	SYSTEM_PROPERTY_INSTANCE_ID       = "_instance_id"
	SYSTEM_PROPERTY_INSTANCE_IDENTITY = "_instance_identity"
	SYSTEM_PROPERTY_DISPLAY           = "_display"
)

// Object search request body.
type ObjectQueryBaseOnObjectType struct {
	Condition  map[string]any `json:"condition,omitempty"`
	Properties []string       `json:"properties,omitempty"`
	PageQuery

	KNID            string        `json:"-"`
	Branch          string        `json:"-"`
	ObjectTypeID    string        `json:"-"`
	ActualCondition *cond.CondCfg `json:"-"`
	CommonQueryParameters

	// Compatibility fields for validating identities and property sets in property queries.
	ObjectQueryInfo *ObjectQueryInfo `json:"-"`
}

type ObjectQueryInfo struct {
	InstanceIdentity []map[string]any `json:"_instance_identity,omitempty"`
	Properties       []string         `json:"properties,omitempty"`
}

type Objects struct {
	ObjectType      *ObjectType      `json:"object_type,omitempty"`
	Datas           []map[string]any `json:"datas"`
	TotalCount      int64            `json:"total_count,omitempty"`
	SearchAfter     []any            `json:"search_after,omitempty"`
	OverallMs       int64            `json:"overall_ms"`
	SearchFromIndex bool             `json:"search_from_index"` // Whether to query the index.
}

// Calculation parameters for metric properties.
type MetricProperty struct {
	PropertyType         string         `json:"property_type"`
	MappingSourceId      string         `json:"mapping_source_id"`
	HasAnyUnfilledParams bool           `json:"has_any_unfilled_params"`
	Parameters           MetricFilters  `json:"parameters"`
	DynamicParams        map[string]any `json:"dynamic_params"`
}

// ToolProperty holds execution parameters for a ToolBox-backed logical property.
type ToolProperty struct {
	PropertyType         string         `json:"property_type"`
	MappingSourceId      string         `json:"mapping_source_id"`
	HasAnyUnfilledParams bool           `json:"has_any_unfilled_params"`
	Parameters           map[string]any `json:"parameters"`
	DynamicParams        map[string]any `json:"dynamic_params"`
}

type MetricFilters struct {
	Filters []Filter `json:"filters"`
}

type Filter struct {
	Name      string `json:"name"`
	Operation string `json:"operation"`
	Value     any    `json:"value"`
}

type CommonQueryParameters struct {
	IncludeTypeInfo         bool
	IncludeLogicParams      bool
	IgnoringStore           bool
	ExcludeSystemProperties []string
}

type ObjectTypeWithKeyField struct {
	OTID            string              `json:"id" mapstructure:"id"`
	OTName          string              `json:"name" mapstructure:"name"`
	DataSource      *ResourceInfo       `json:"data_source" mapstructure:"data_source"`
	DataProperties  []cond.DataProperty `json:"data_properties,omitempty" mapstructure:"data_properties,omitempty"`
	LogicProperties []*LogicProperty    `json:"logic_properties,omitempty" mapstructure:"logic_properties,omitempty"`
	PrimaryKeys     []string            `json:"primary_keys" mapstructure:"primary_keys"`
	DisplayKey      string              `json:"display_key" mapstructure:"display_key"`

	// Compatibility fields for path-based subgraph query requests.
	Condition       map[string]any `json:"condition,omitempty" mapstructure:"condition,omitempty"`
	ActualCondition *cond.CondCfg  `json:"-"` // Filter for each object type in the path.
	PageQuery                      // Pagination for each object type in the path.
}

type ObjectType struct {
	ObjectTypeWithKeyField `mapstructure:",squash"`
	CommonInfo             `mapstructure:",squash"`

	KNID   string `json:"kn_id" mapstructure:"kn_id"`
	Branch string `json:"branch" mapstructure:"branch"`

	Status *ObjectTypeStatus `json:"status,omitempty" mapstructure:"status"`

	Creator    AccountInfo `json:"creator" mapstructure:"creator"`
	CreateTime int64       `json:"create_time" mapstructure:"create_time"`
	Updater    AccountInfo `json:"updater" mapstructure:"updater"`
	UpdateTime int64       `json:"update_time" mapstructure:"update_time"`

	ModuleType string `json:"module_type"`
}

type ObjectTypeStatus struct {
	IncrementalKey   string `json:"incremental_key" mapstructure:"incremental_key"`
	IncrementalValue string `json:"incremental_value" mapstructure:"incremental_value"`
	Index            string `json:"index" mapstructure:"index"`
	IndexAvailable   bool   `json:"index_available" mapstructure:"index_available"`
	DocCount         int64  `json:"doc_count" mapstructure:"doc_count"`
	StorageSize      int64  `json:"storage_size" mapstructure:"storage_size"`
	UpdateTime       int64  `json:"update_time" mapstructure:"update_time"`
}

type LogicProperty struct {
	Name        string        `json:"name" mapstructure:"name"`
	DisplayName string        `json:"display_name" mapstructure:"display_name"`
	Type        string        `json:"type" mapstructure:"type"`
	Comment     string        `json:"comment" mapstructure:"comment"`
	Index       bool          `json:"index" mapstructure:"index"`
	DataSource  *ResourceInfo `json:"data_source" mapstructure:"data_source"`
	Parameters  []Parameter   `json:"parameters" mapstructure:"parameters"`
}

type Parameter struct {
	Name      string  `json:"name" mapstructure:"name"`
	Type      string  `json:"type" mapstructure:"type"`     // Parameter type.
	Source    string  `json:"source" mapstructure:"source"` // Source type.
	Operation string  `json:"operation,omitempty" mapstructure:"operation,omitempty"`
	ValueFrom string  `json:"value_from,omitempty" mapstructure:"value_from,omitempty"`
	Value     any     `json:"value,omitempty" mapstructure:"value,omitempty"`
	Comment   *string `json:"comment,omitempty" mapstructure:"comment"` // Parameter note used when assigning live metric inputs to object-type metric properties.
	Required  bool    `json:"required,omitempty" mapstructure:"required,omitempty"`
	Default   any     `json:"default,omitempty" mapstructure:"default,omitempty"`
}

// DynamicParams is the dynamic_params structure for metric properties.
type MetricPropertyDynamicParams struct {
	Start              *int64           `json:"start,omitempty"`
	End                *int64           `json:"end,omitempty"`
	Instant            *bool            `json:"instant,omitempty"`
	Step               *string          `json:"step,omitempty"`
	AnalysisDimensions []string         `json:"analysis_dimensions,omitempty"`
	OrderByFields      []OrderField     `json:"order_by_fields,omitempty"`
	HavingCondition    *HavingCondition `json:"having_condition,omitempty"`
	Metrics            *Metrics         `json:"metrics,omitempty"`
	// Additional dynamic parameters defined by the metric property are handled by mapstructure or custom UnmarshalJSON.
}

// Object property-value request body.
type ObjectPropertyValueQuery struct {
	InstanceIdentities []map[string]any          `json:"_instance_identities,omitempty"`
	Properties         []string                  `json:"properties,omitempty"`
	DynamicParams      map[string]map[string]any `json:"dynamic_params"`

	KNID         string `json:"-"`
	Branch       string `json:"-"`
	ObjectTypeID string `json:"-"`
	CommonQueryParameters
}
