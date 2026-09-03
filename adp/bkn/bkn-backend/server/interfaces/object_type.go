// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"

	"bkn-backend/interfaces/data_type"
)

const (
	MAX_PROPERTY_NUM = 1000
	// Logical property types.
	LOGIC_PROPERTY_TYPE_METRIC = "metric"
	LOGIC_PROPERTY_TYPE_TOOL   = "tool"
)

var (
	OBJECT_TYPE_SORT = map[string]string{
		"name":        "f_name",
		"update_time": "f_update_time",
	}

	// Primary key property types can only be integer, unsigned integer, string, or text.
	ValidPrimaryKeyTypes = map[string]bool{
		data_type.DATATYPE_INTEGER:          true,
		data_type.DATATYPE_UNSIGNED_INTEGER: true,
		data_type.DATATYPE_STRING:           true,
		data_type.DATATYPE_TEXT:             true,
	}

	// Display property types support integer, unsigned integer, float, decimal, string, text, date, timestamp, time, datetime, and boolean.
	ValidDisplayKeyTypes = map[string]bool{
		data_type.DATATYPE_INTEGER:          true,
		data_type.DATATYPE_UNSIGNED_INTEGER: true,
		data_type.DATATYPE_FLOAT:            true,
		data_type.DATATYPE_DECIMAL:          true,
		data_type.DATATYPE_STRING:           true,
		data_type.DATATYPE_TEXT:             true,
		data_type.DATATYPE_DATE:             true,
		data_type.DATATYPE_TIMESTAMP:        true,
		data_type.DATATYPE_TIME:             true,
		data_type.DATATYPE_DATETIME:         true,
		data_type.DATATYPE_BOOLEAN:          true,
	}

	// Logical resource types must be valid. metric and tool are currently supported.
	ValidLogicSourceTypes = map[string]bool{
		LOGIC_PROPERTY_TYPE_METRIC: true,
		LOGIC_PROPERTY_TYPE_TOOL:   true,
	}

	// Valid property types are integer, unsigned integer, float, decimal, string, text, date, timestamp, time, datetime, boolean, binary, json, vector, point, shape, and ip.
	ValidDataPropertyTypes = map[string]bool{
		data_type.DATATYPE_INTEGER:          true,
		data_type.DATATYPE_UNSIGNED_INTEGER: true,
		data_type.DATATYPE_STRING:           true,
		data_type.DATATYPE_FLOAT:            true,
		data_type.DATATYPE_DECIMAL:          true,
		data_type.DATATYPE_TEXT:             true,
		data_type.DATATYPE_DATE:             true,
		data_type.DATATYPE_TIMESTAMP:        true,
		data_type.DATATYPE_TIME:             true,
		data_type.DATATYPE_DATETIME:         true,
		data_type.DATATYPE_BOOLEAN:          true,
		data_type.DATATYPE_BINARY:           true,
		data_type.DATATYPE_JSON:             true,
		data_type.DATATYPE_VECTOR:           true,
		data_type.DATATYPE_POINT:            true,
		data_type.DATATYPE_SHAPE:            true,
		data_type.DATATYPE_IP:               true,
	}
)

type ObjectTypeWithKeyField struct {
	OTID            string           `json:"id" mapstructure:"id"`
	OTName          string           `json:"name" mapstructure:"name"`
	DataSource      *ResourceInfo    `json:"data_source" mapstructure:"data_source"`
	DataProperties  []*DataProperty  `json:"data_properties,omitempty" mapstructure:"data_properties"`
	LogicProperties []*LogicProperty `json:"logic_properties,omitempty" mapstructure:"logic_properties"`
	PrimaryKeys     []string         `json:"primary_keys" mapstructure:"primary_keys"`
	DisplayKey      string           `json:"display_key" mapstructure:"display_key"`
	IncrementalKey  string           `json:"incremental_key" mapstructure:"incremental_key"`
	// ConditionOperations []string         `json:"condition_operations"`
}

// object type
type ObjectType struct {
	ObjectTypeWithKeyField `mapstructure:",squash"`
	CommonInfo             `mapstructure:",squash"`

	KNID          string          `json:"kn_id" mapstructure:"kn_id"`
	Branch        string          `json:"branch" mapstructure:"branch"`
	ConceptGroups []*ConceptGroup `json:"concept_groups,omitempty" mapstructure:"concept_groups"`

	Status *ObjectTypeStatus `json:"status,omitempty" mapstructure:"status"`

	Creator    AccountInfo `json:"creator" mapstructure:"creator"`
	CreateTime int64       `json:"create_time" mapstructure:"create_time"`
	Updater    AccountInfo `json:"updater" mapstructure:"updater"`
	UpdateTime int64       `json:"update_time" mapstructure:"update_time"`

	ModuleType string   `json:"module_type" mapstructure:"module_type"`
	Operations []string `json:"operations,omitempty"`

	PropertyMap  map[string]string `json:"-"` // Map from property name to display name
	IfNameModify bool              `json:"-"`

	// Vector.
	Vector []float32 `json:"_vector,omitempty"`
	Score  *float64  `json:"_score,omitempty"` // OpenSearch score used by concept search
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

type SimpleObjectType struct {
	OTID   string `json:"id" mapstructure:"id"`
	OTName string `json:"name" mapstructure:"name"`
	Branch string `json:"branch" mapstructure:"branch"`
	Icon   string `json:"icon" mapstructure:"icon"`
	Color  string `json:"color" mapstructure:"color"`
}

type DataProperty struct {
	Name        string `json:"name" mapstructure:"name"`
	DisplayName string `json:"display_name" mapstructure:"display_name"`
	Type        string `json:"type" mapstructure:"type"`
	Comment     string `json:"comment" mapstructure:"comment"`

	MappedField *Field `json:"mapped_field,omitempty" mapstructure:"mapped_field,omitempty"`

	ConditionOperations []string `json:"condition_operations,omitempty"` // Operations supported by string fields

	retiredIndexConfigProvided bool
}

// UnmarshalJSON records the removed index_config field without reintroducing it to the public model.
func (p *DataProperty) UnmarshalJSON(data []byte) error {
	type dataPropertyAlias DataProperty
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var value dataPropertyAlias
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = DataProperty(value)
	if rawIndexConfig, ok := raw["index_config"]; ok && string(rawIndexConfig) != "null" {
		p.retiredIndexConfigProvided = true
	}
	return nil
}

// HasRetiredIndexConfig reports whether the request contained the retired property-level index_config field.
func (p *DataProperty) HasRetiredIndexConfig() bool {
	return p != nil && p.retiredIndexConfigProvided
}

type LogicProperty struct {
	Name        string `json:"name" mapstructure:"name"`
	DisplayName string `json:"display_name" mapstructure:"display_name"`
	Type        string `json:"type" mapstructure:"type"`
	Comment     string `json:"comment" mapstructure:"comment"`
	// Index        bool          `json:"index" mapstructure:"index"`
	DataSource   *ResourceInfo `json:"data_source,omitempty" mapstructure:"data_source"`
	Parameters   []Parameter   `json:"parameters,omitempty" mapstructure:"parameters"`
	AnalysisDims []Field       `json:"analysis_dimensions,omitempty"`
}

type Field struct {
	Name        string  `json:"name" mapstructure:"name"`                                     // Technical name
	Type        string  `json:"type,omitempty" mapstructure:"type,omitempty"`                 // Field type
	DisplayName string  `json:"display_name,omitempty" mapstructure:"display_name,omitempty"` // Display name
	Comment     *string `json:"comment,omitempty"`                                            // Field comment from the view
}

type Parameter struct {
	Name        string  `json:"name" mapstructure:"name"`
	Type        string  `json:"type" mapstructure:"type"`                               // Parameter type
	Source      string  `json:"source,omitempty" mapstructure:"source,omitempty"`       // Source type
	Operation   string  `json:"operation,omitempty" mapstructure:"operation,omitempty"` // Operator for a metric property
	ValueFrom   string  `json:"value_from,omitempty" mapstructure:"value_from,omitempty"`
	Value       any     `json:"value,omitempty" mapstructure:"value,omitempty"`
	IfSystemGen *bool   `json:"if_system_generate,omitempty" mapstructure:"if_system_generate,omitempty"`
	Comment     *string `json:"comment,omitempty" mapstructure:"comment,omitempty"` // Parameter comment supplied by the metric read
	Required    bool    `json:"required,omitempty" mapstructure:"required,omitempty"`
	Default     any     `json:"default,omitempty" mapstructure:"default,omitempty"`
}

type SimpleProperty struct {
	Name        string `json:"name" mapstructure:"name"`
	DisplayName string `json:"display_name" mapstructure:"display_name"`
}

// Object type pagination query.
type ObjectTypesQueryParams struct {
	PaginationQueryParameters
	NamePattern string
	Tag         string
	Branch      string
	KNID        string
	OTIDS       []string // Filter by object type IDs
}

// Object search list.
type ObjectTypes struct {
	Entries    []*ObjectType `json:"entries"`
	TotalCount int64         `json:"total_count,omitempty"`
	NextCursor *string       `json:"next_cursor,omitempty"`
	OverallMs  int64         `json:"overall_ms"`
}

type ObjectTypeSampleDataColumn struct {
	DataIndex string `json:"data_index"`
	Title     string `json:"title"`
}

type ObjectTypeSampleDataQueryParams struct {
	Offset      int   `json:"offset,omitempty"`
	Limit       int   `json:"limit,omitempty"`
	NeedTotal   bool  `json:"need_total,omitempty"`
	SearchAfter []any `json:"search_after,omitempty"`
}

type ObjectTypeSampleData struct {
	Columns     []*ObjectTypeSampleDataColumn `json:"columns"`
	Entries     []map[string]any              `json:"entries"`
	Name        string                        `json:"name"`
	TotalCount  int64                         `json:"total_count,omitempty"`
	SearchAfter []any                         `json:"search_after,omitempty"`
}
