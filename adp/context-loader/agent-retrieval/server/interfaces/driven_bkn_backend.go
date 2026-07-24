// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
)

// KnOperationType Business knowledge network operator
type KnOperationType string

const (
	KnOperationTypeAnd            KnOperationType = "and"       // AND
	KnOperationTypeOr             KnOperationType = "or"        // OR
	KnOperationTypeEqual          KnOperationType = "=="        // Equal
	KnOperationTypeNotEqual       KnOperationType = "!="        // Not Equal
	KnOperationTypeGreater        KnOperationType = ">"         // Greater than
	KnOperationTypeLess           KnOperationType = "<"         // Less than
	KnOperationTypeGreaterOrEqual KnOperationType = ">="        // Greater than or equal
	KnOperationTypeLessOrEqual    KnOperationType = "<="        // Less than or equal
	KnOperationTypeIn             KnOperationType = "in"        // in
	KnOperationTypeNotIn          KnOperationType = "not_in"    // not_in
	KnOperationTypeLike           KnOperationType = "like"      // like
	KnOperationTypeNotLike        KnOperationType = "not_like"  // not_like
	KnOperationTypeRange          KnOperationType = "range"     // range
	KnOperationTypeOutRange       KnOperationType = "out_range" // out_range
	KnOperationTypeExist          KnOperationType = "exist"     // exist
	KnOperationTypeNotExist       KnOperationType = "not_exist" // not_exist
	KnOperationTypeRegex          KnOperationType = "regex"     // regex
	KnOperationTypeMatch          KnOperationType = "match"     // match
	KnOperationTypeKnn            KnOperationType = "knn"       // knn
)

// LogicPropertyType Logic property type
type LogicPropertyType string

const (
	LogicPropertyTypeMetric   LogicPropertyType = "metric"   // Metric type
	LogicPropertyTypeOperator LogicPropertyType = "operator" // Operator type
)

type KnBaseError struct {
	ErrorCode               string         `json:"error_code"`    // Error code
	Description             string         `json:"description"`   // Error description
	Solution                string         `json:"solution"`      // Solution
	ErrorLink               string         `json:"error_link"`    // Error link
	ErrorDetails            interface{}    `json:"error_details"` // Detail content
	DescriptionTemplateData map[string]any `json:"-"`             // Description parameters
	SolutionTemplateData    map[string]any `json:"-"`             // Solution parameters
}

type ResourceInfo struct {
	Type string `json:"type"` // Data source type
	ID   string `json:"id"`   // Data view ID
	Name string `json:"name"` // View name
}

type SimpleObjectType struct {
	OTID   string `json:"id"`
	OTName string `json:"name"`
	Icon   string `json:"icon"`
	Color  string `json:"color"`
}

// DataProperty Data property structure definition
type DataProperty struct {
	// Name is the property name. Can only contain lowercase letters, numbers, underscores (_),
	// hyphens (-), and cannot start with underscore or hyphen
	Name                string            `json:"name"`
	DisplayName         string            `json:"display_name,omitempty"`         // Property display name
	Type                string            `json:"type"`                           // Property data type. In addition to view field types, there are metric, objective, event, trace, log, operator
	Comment             string            `json:"comment,omitempty"`              // Comment
	MappedField         any               `json:"mapped_field,omitempty"`         // View field info
	ConditionOperations []KnOperationType `json:"condition_operations,omitempty"` // List of query condition operators supported by this data property
}

// LogicPropertyDef Logic property definition (extracted from object type definition)
type LogicPropertyDef struct {
	Name        string              `json:"name"`
	DisplayName string              `json:"display_name,omitempty"`
	Type        LogicPropertyType   `json:"type"` // Logic property type: metric or operator
	Comment     string              `json:"comment,omitempty"`
	DataSource  map[string]any      `json:"data_source,omitempty"`
	Parameters  []PropertyParameter `json:"parameters,omitempty"`
}

// PropertyParameter Property parameter definition
type PropertyParameter struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	ValueFrom        string `json:"value_from"` // "input", "property", "const"
	Value            any    `json:"value,omitempty"`
	IfSystemGenerate bool   `json:"if_system_generate,omitempty"`
	Comment          string `json:"comment,omitempty"`
}

// ObjectType Object type structure definition
type ObjectType struct {
	ModuleType      string              `json:"module_type"` // Module type
	ID              string              `json:"id"`          // Object ID
	Name            string              `json:"name"`        // Object name
	Tags            []string            `json:"tags"`        // Tags
	Comment         string              `json:"comment"`     // Comment
	Score           float64             `json:"_score"`      // Score
	DataSource      *ResourceInfo       `json:"data_source"`
	DataProperties  []*DataProperty     `json:"data_properties,omitempty"`  // Data properties
	LogicProperties []*LogicPropertyDef `json:"logic_properties,omitempty"` // Logic properties
	PrimaryKeys     []string            `json:"primary_keys"`               // Primary key fields
}

// RelationType Relation type structure definition
type RelationType struct {
	ModuleType string   `json:"module_type"` // Module type
	ID         string   `json:"id"`          // Relation type ID
	Name       string   `json:"name"`        // Relation type name
	Tags       []string `json:"tags"`        // Tags
	Comment    string   `json:"comment"`     // Comment
	Score      float64  `json:"_score"`      // Score

	SourceObjectTypeID string `json:"source_object_type_id"`        // Source object type ID
	TargetObjectTypeID string `json:"target_object_type_id"`        // Target object type ID
	SourceObjectType   any    `json:"source_object_type,omitempty"` // Provide name when viewing details
	TargetObjectType   any    `json:"target_object_type,omitempty"` // Provide name when viewing details
	MappingRules       any    `json:"mapping_rules,omitempty"`      // Mapping rules based on type, direct corresponds to []Mapping structure
	Type               string `json:"type"`                         // Relation type
}

// ActionType Action type structure definition
type ActionType struct {
	ModuleType string   `json:"module_type"` // Module type
	ID         string   `json:"id"`          // Action type ID
	Name       string   `json:"name"`        // Action type name
	Tags       []string `json:"tags"`        // Tags
	Comment    string   `json:"comment"`     // Comment
	Score      float64  `json:"_score"`      // Score

	ObjectTypeID string `json:"object_type_id"` // Object type ID bound to action type
}

// ConceptGroup BKN concept group structure used by exported knowledge-network detail.
type ConceptGroup struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	ObjectTypeIDs []string        `json:"object_type_ids,omitempty"`
	ObjectTypes   []*ObjectType   `json:"object_types,omitempty"`
	RelationTypes []*RelationType `json:"relation_types,omitempty"`
	ActionTypes   []*ActionType   `json:"action_types,omitempty"`
}

type KnCondValueFrom string

const (
	CondValueFromConst KnCondValueFrom = "const"
)

type KnCondLimitKey string

const (
	CondLimitKeyK           KnCondLimitKey = "k"            // Pagination key
	CondLimitKeyMinScore    KnCondLimitKey = "min_score"    // Min score
	CondLimitKeyMinDistance KnCondLimitKey = "min_distance" // Min distance
)

// KnCondition Retrieval condition
type KnCondition struct {
	Field         string          `json:"field"`          // Field name
	Operation     KnOperationType `json:"operation"`      // Operator
	SubConditions []*KnCondition  `json:"sub_conditions"` // Sub filtering conditions
	Value         any             `json:"value"`          // Field value
	ValueFrom     KnCondValueFrom `json:"value_from"`     // Field value source
	LimitKey      KnCondLimitKey  `json:"limit_key"`
	LimitValue    any             `json:"limit_value"`
}

type KnSortParams struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

// QueryConceptsReq Query concepts request
type QueryConceptsReq struct {
	KnID          string          `json:"-"`                        // Knowledge network ID
	ConceptGroups []string        `json:"concept_groups,omitempty"` // Concept group IDs
	Cond          *KnCondition    `json:"condition"`                // Retrieval condition
	Sort          []*KnSortParams `json:"sort"`
	Limit         int             `json:"limit"`      // Return count, default 10. Range 1-10000
	NeedTotal     bool            `json:"need_total"` // Whether total count is needed, default false
}

// Concepts Retrieved concepts list
type Concepts struct {
	Entries     []any `json:"entries"`
	TotalCount  int64 `json:"total_count,omitempty"`
	SearchAfter []any `json:"search_after,omitempty"`
	OverallMs   int64 `json:"overall_ms"`
}

// ObjectTypeConcepts Object type concepts list
type ObjectTypeConcepts struct {
	Entries    []*ObjectType `json:"entries"`               // Object type data
	TotalCount int64         `json:"total_count,omitempty"` // Total count
}

// RelationTypeConcepts Relation type concepts list
type RelationTypeConcepts struct {
	Entries    []*RelationType `json:"entries"`               // Relation type data
	TotalCount int64           `json:"total_count,omitempty"` // Total count
}

// ActionTypeConcepts Action type concepts list
type ActionTypeConcepts struct {
	Entries    []*ActionType `json:"entries"`               // Action type data
	TotalCount int64         `json:"total_count,omitempty"` // Total count
}

// MetricType Metric type structure definition
type MetricType struct {
	ID                 string `json:"id"`                            // Metric ID
	Name               string `json:"name"`                          // Metric name
	Comment            string `json:"comment,omitempty"`             // Metric comment
	UnitType           string `json:"unit_type,omitempty"`           // Unit type
	Unit               string `json:"unit,omitempty"`                // Unit
	MetricType         string `json:"metric_type"`                   // Metric type
	ScopeType          string `json:"scope_type"`                    // Scope type
	ScopeRef           string `json:"scope_ref"`                     // Scope ref
	TimeDimension      any    `json:"time_dimension,omitempty"`      // Time dimension
	CalculationFormula any    `json:"calculation_formula"`           // Calculation formula
	AnalysisDimensions any    `json:"analysis_dimensions,omitempty"` // Analysis dimensions
}

// MetricTypeConcepts Metric type concepts list
type MetricTypeConcepts struct {
	Entries    []*MetricType `json:"entries"`               // Metric type data
	TotalCount int64         `json:"total_count,omitempty"` // Total count
}

// KnowledgeNetworkDetail Knowledge network detail with full schema
type KnowledgeNetworkDetail struct {
	ID            string          `json:"id"`             // Knowledge network ID
	Name          string          `json:"name"`           // Knowledge network name
	Comment       string          `json:"comment"`        // Comment/description
	ConceptGroups []*ConceptGroup `json:"concept_groups"` // Concept groups
	ObjectTypes   []*ObjectType   `json:"object_types"`   // Object types
	RelationTypes []*RelationType `json:"relation_types"` // Relation types
	ActionTypes   []*ActionType   `json:"action_types"`   // Action types
}

// Detail levels for get_kn_detail progressive disclosure.
const (
	DetailLevelSummary = "summary" // skeleton + property name/type/comment (default)
	DetailLevelFull    = "full"    // everything, incl. field mappings / operators / mapping rules
)

// Slim trims a get_kn_detail response for progressive disclosure.
//
// It always dedups concept_groups: the exported detail repeats every ObjectType /
// RelationType / ActionType both at the top level (the authoritative arrays every
// consumer reads) and nested inside each ConceptGroup. The nested copies are unused,
// so we drop them and keep only object_type_ids as the group boundary.
//
// Unless level is DetailLevelFull, it also strips the heavy per-property detail —
// data-property field mappings and query operators, logic-property data sources and
// parameters, relation mapping rules — while keeping property name/type/comment so an
// agent still sees the schema shape. Callers fetch the stripped detail on demand via
// get_object_types / get_relation_types.
func (d *KnowledgeNetworkDetail) Slim(level string) {
	if d == nil {
		return
	}
	// Always dedup: nested concept-group instances duplicate the top-level arrays.
	for _, g := range d.ConceptGroups {
		if g == nil {
			continue
		}
		g.ObjectTypes = nil
		g.RelationTypes = nil
		g.ActionTypes = nil
	}
	if level == DetailLevelFull {
		return
	}
	for _, o := range d.ObjectTypes {
		if o == nil {
			continue
		}
		// summary keeps only name+type per property so the array is flat and uniform
		// (TOON then renders it as a compact table); drop display_name/comment and the
		// heavy mapped_field/operators/logic sources. Full detail via get_object_types.
		for _, p := range o.DataProperties {
			if p == nil {
				continue
			}
			p.DisplayName = ""
			p.Comment = ""
			p.MappedField = nil
			p.ConditionOperations = nil
		}
		for _, lp := range o.LogicProperties {
			if lp == nil {
				continue
			}
			lp.DisplayName = ""
			lp.Comment = ""
			lp.DataSource = nil
			lp.Parameters = nil
		}
	}
	for _, r := range d.RelationTypes {
		if r == nil {
			continue
		}
		r.MappingRules = nil
		r.SourceObjectType = nil
		r.TargetObjectType = nil
	}
}

// ObjectTypesResp is the get_object_types response: the requested object types in
// full detail, plus any requested ids that matched nothing.
type ObjectTypesResp struct {
	KnID        string        `json:"kn_id"`
	ObjectTypes []*ObjectType `json:"object_types"`
	Missing     []string      `json:"missing,omitempty"`
}

// RelationTypesResp is the get_relation_types response: the requested relation
// types in full detail, plus any requested ids that matched nothing.
type RelationTypesResp struct {
	KnID          string          `json:"kn_id"`
	RelationTypes []*RelationType `json:"relation_types"`
	Missing       []string        `json:"missing,omitempty"`
}

// FilterObjectTypes returns the object types whose ID (or, as a fallback, Name)
// appears in ids, in request order and de-duplicated, plus the ids that matched
// nothing. This is the drill-down that get_kn_detail's summary level omits; it
// keeps the heavy per-property detail (mapped_field, condition_operations, logic
// sources) so results stay run_sql-capable, but drops the property display_name
// (a UI label of little value to an agent).
func (d *KnowledgeNetworkDetail) FilterObjectTypes(ids []string) (matched []*ObjectType, missing []string) {
	if d == nil {
		return nil, ids
	}
	byKey := make(map[string]*ObjectType, len(d.ObjectTypes)*2)
	for _, o := range d.ObjectTypes {
		if o != nil {
			byKey[o.ID] = o
		}
	}
	for _, o := range d.ObjectTypes {
		if o != nil && o.Name != "" {
			if _, exists := byKey[o.Name]; !exists {
				byKey[o.Name] = o
			}
		}
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		o, ok := byKey[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		if seen[o.ID] {
			continue
		}
		seen[o.ID] = true
		for _, p := range o.DataProperties {
			if p != nil {
				p.DisplayName = ""
			}
		}
		for _, lp := range o.LogicProperties {
			if lp != nil {
				lp.DisplayName = ""
			}
		}
		matched = append(matched, o)
	}
	return matched, missing
}

// FilterRelationTypes mirrors FilterObjectTypes for relation types.
func (d *KnowledgeNetworkDetail) FilterRelationTypes(ids []string) (matched []*RelationType, missing []string) {
	if d == nil {
		return nil, ids
	}
	byKey := make(map[string]*RelationType, len(d.RelationTypes)*2)
	for _, r := range d.RelationTypes {
		if r != nil {
			byKey[r.ID] = r
		}
	}
	for _, r := range d.RelationTypes {
		if r != nil && r.Name != "" {
			if _, exists := byKey[r.Name]; !exists {
				byKey[r.Name] = r
			}
		}
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		r, ok := byKey[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		if seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		matched = append(matched, r)
	}
	return matched, missing
}

// BknBackendAccess BKN backend ontology management interface
// ListKnReq 列出知识网络的查询参数
type ListKnReq struct {
	NamePattern string `json:"name_pattern,omitempty"` // 按名称模糊过滤
	Limit       int    `json:"limit,omitempty"`        // 单页数量，默认 20
	Offset      int    `json:"offset,omitempty"`       // 偏移，用于翻页
	Sort        string `json:"sort,omitempty"`         // 排序字段，默认 update_time
	Direction   string `json:"direction,omitempty"`    // asc / desc，默认 desc
}

// KnBrief 知识网络概要（list 用）
type KnBrief struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	ModuleType     string `json:"module_type,omitempty"`
	BusinessDomain string `json:"business_domain,omitempty"`
}

// ListKnResp 知识网络列表响应
type ListKnResp struct {
	Entries    []*KnBrief `json:"entries"`
	TotalCount int64      `json:"total_count"`
}

type BknBackendAccess interface {
	// GetKnowledgeNetworkDetail Get knowledge network detail with full schema (include_detail=true, mode=export)
	GetKnowledgeNetworkDetail(ctx context.Context, knID string) (*KnowledgeNetworkDetail, error)

	// ListKnowledgeNetworks 列出知识网络（用于发现 kn_id）
	ListKnowledgeNetworks(ctx context.Context, req *ListKnReq) (resp *ListKnResp, err error)

	// SearchObjectTypes Search object types
	SearchObjectTypes(ctx context.Context, query *QueryConceptsReq) (objectTypes *ObjectTypeConcepts, err error)
	// GetObjectTypeDetail Get object type details
	GetObjectTypeDetail(ctx context.Context, knID string, otIds []string, includeDetail bool) ([]*ObjectType, error)

	// SearchRelationTypes Search relation types
	SearchRelationTypes(ctx context.Context, query *QueryConceptsReq) (releationTypes *RelationTypeConcepts, err error)
	// GetRelationTypeDetail Get relation type details
	GetRelationTypeDetail(ctx context.Context, knID string, rtIDs []string, includeDetail bool) ([]*RelationType, error)

	// SearchActionTypes Search action types
	SearchActionTypes(ctx context.Context, query *QueryConceptsReq) (actionTypes *ActionTypeConcepts, err error)
	// GetActionTypeDetail Get action type details
	GetActionTypeDetail(ctx context.Context, knID string, atIDs []string, includeDetail bool) ([]*ActionType, error)

	// SearchMetricTypes Search metric types
	SearchMetricTypes(ctx context.Context, query *QueryConceptsReq) (metricTypes *MetricTypeConcepts, err error)
}
