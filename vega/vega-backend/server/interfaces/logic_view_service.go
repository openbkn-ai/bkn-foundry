// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
)

const (
	LogicType_Derived   = "derived"
	LogicType_Composite = "composite"

	//Configuration items of features
	PropertyFeatureType_Keyword  = "keyword"
	PropertyFeatureType_Fulltext = "fulltext"
	PropertyFeatureType_Vector   = "vector"

	LogicDefinitionNodeType_Resource = "resource"
	LogicDefinitionNodeType_Join     = "join"
	LogicDefinitionNodeType_Union    = "union"
	LogicDefinitionNodeType_Sql      = "sql"
	LogicDefinitionNodeType_Output   = "output"

	// The type of join
	JoinType_Inner = "inner"
	JoinType_Left  = "left"
	JoinType_Right = "right"
	// JoinType_FullOuter = "full outer"

	// The type of union
	UnionType_All      = "all"
	UnionType_Distinct = "distinct"

	// MaxRecursionDepth is the maximum nested depth of the logical view to prevent stack overflow caused by circular references
	MaxRecursionDepth = 10
)

var (
	LogicDefinitionNodeTypeMap = map[string]struct{}{
		LogicDefinitionNodeType_Resource: {},
		LogicDefinitionNodeType_Join:     {},
		LogicDefinitionNodeType_Union:    {},
		LogicDefinitionNodeType_Sql:      {},
		LogicDefinitionNodeType_Output:   {},
	}

	JoinTypeMap = map[string]struct{}{
		JoinType_Inner: {},
		JoinType_Left:  {},
		JoinType_Right: {},
	}

	UnionTypeMap = map[string]struct{}{
		UnionType_All:      {},
		UnionType_Distinct: {},
	}

	PropertyFeatureTypeMap = map[string]struct{}{
		PropertyFeatureType_Keyword:  {},
		PropertyFeatureType_Fulltext: {},
		PropertyFeatureType_Vector:   {},
	}
)

type LogicView struct {
	Resource
	IsSingleSource bool                 `json:"is_single_source,omitempty" mapstructure:"-"`
	RefResources   map[string]*Resource `json:"ref_resources,omitempty" mapstructure:"-"`
}

// LogicDefinitionNode represents the nodes in the graph
type LogicDefinitionNode struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Inputs       []string        `json:"inputs"`
	Config       map[string]any  `json:"config"`
	OutputFields []*ViewProperty `json:"output_fields"`
}

// Node configuration with the node type of resource
type ResourceNodeCfg struct {
	ResourceID string         `json:"resource_id" mapstructure:"resource_id"`
	Filters    *FilterCondCfg `json:"filters,omitempty" mapstructure:"filters"`
	Distinct   bool           `json:"distinct" mapstructure:"distinct"`
	Resource   *Resource      `json:"resource,omitempty" mapstructure:"resource"`
}

// Node configuration with the node type "join"
type JoinNodeCfg struct {
	JoinType string         `json:"join_type" mapstructure:"join_type"`
	JoinOn   []*JoinOn      `json:"join_on" mapstructure:"join_on"`
	Filters  *FilterCondCfg `json:"filters,omitempty" mapstructure:"filters"`
}

// JoinOn configures a join predicate.
type JoinOn struct {
	LeftField  string `json:"left_field" mapstructure:"left_field"`   // Pass the field name.
	RightField string `json:"right_field" mapstructure:"right_field"` // Pass the field name.
	Operator   string `json:"operator" mapstructure:"operator"`
}

// Node configuration with the node type of union
type UnionNodeCfg struct {
	UnionType string         `json:"union_type" mapstructure:"union_type"`
	Filters   *FilterCondCfg `json:"filters,omitempty" mapstructure:"filters"`
}

type SQLNodeCfg struct {
	SQL string `json:"sql" mapstructure:"sql"`
}

// OutputFieldRef represents the elements of the from array in the Union alignment mode
type OutputFieldRef struct {
	From     string `json:"from"`
	FromNode string `json:"from_node"`
}

// Logical view field
type ViewProperty struct {
	Property
	From     string            `json:"from,omitempty"`      // Join mapping mode: Source field name (when from is string)
	FromNode string            `json:"from_node,omitempty"` // Join mapping mode: Source node ID
	FromList []*OutputFieldRef `json:"-"`                   // Union alignment mode: Multi-source aligned array (when from is array)
}

// UnmarshalJSON custom deserialization handles five forms of output_fields
func (v *ViewProperty) UnmarshalJSON(data []byte) error {
	// 1. Detect whether it is a pure string (wildcard mode "*" or projection mode "field_a")
	var s string
	if err := sonic.Unmarshal(data, &s); err == nil {
		v.Name = s
		return nil
	}

	// 2. Detect whether it is an object
	var raw map[string]sonic.NoCopyRawMessage
	if err := sonic.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Decode the fields of the base class Property (Name, Type, DisplayName, OriginalName, Description, Features)
	type PropertyAlias Property
	var propAlias PropertyAlias
	if err := sonic.Unmarshal(data, &propAlias); err != nil {
		return err
	}
	v.Property = Property(propAlias)

	// Decode from_node
	if rawFromNode, ok := raw["from_node"]; ok {
		_ = sonic.Unmarshal(rawFromNode, &v.FromNode)
	}

	// Decoding from: It could be string (mapping mode) or array (alignment mode)
	if rawFrom, ok := raw["from"]; ok {
		// Try string
		var fromStr string
		if err := sonic.Unmarshal(rawFrom, &fromStr); err == nil {
			v.From = fromStr
		} else {
			// Try array
			var fromList []*OutputFieldRef
			if err := sonic.Unmarshal(rawFrom, &fromList); err == nil {
				v.FromList = fromList
			}
		}
	}

	return nil
}

// MarshalJSON custom serialization, to simplify output and conform to five forms
func (v *ViewProperty) MarshalJSON() ([]byte, error) {
	// If there is only the Name and no other metadata or mapping information, serialize it as a pure string (form 1 & 2)
	// Judgment condition: The Name is non-empty, and other key fields such as Type, From, FromNode, FromList, and DisplayName are all empty
	if v.Name != "" && v.Type == "" && v.From == "" && v.FromNode == "" &&
		len(v.FromList) == 0 && v.DisplayName == "" && v.OriginalName == "" &&
		v.Description == "" && len(v.Features) == 0 {
		return sonic.Marshal(v.Name)
	}

	// Otherwise, serialize it as an object (form 3, 4, 5)
	type Alias ViewProperty
	// Use an auxiliary structure to handle the polymorphic output of the from field
	tmp := struct {
		*Alias
		From any `json:"from,omitempty"`
	}{
		Alias: (*Alias)(v),
	}

	if len(v.FromList) > 0 {
		tmp.From = v.FromList
	} else if v.From != "" {
		tmp.From = v.From
	}

	return sonic.Marshal(tmp)
}

func (v *ViewProperty) String() string {
	return fmt.Sprintf("ViewProperty{name: %s, type: %s, from: %s, from_node: %s, from_list_len: %d}",
		v.Name, v.Type, v.From, v.FromNode, len(v.FromList))
}

type DSLCfg struct {
	From           int              `json:"from"`
	Size           int              `json:"size"`
	Sort           []map[string]any `json:"sort,omitempty"`
	TrackScores    bool             `json:"track_scores,omitempty"`
	TrackTotalHits bool             `json:"track_total_hits,omitempty"`
	SearchAfter    []any            `json:"search_after,omitempty"`
	Query          struct {
		Bool struct {
			Should         []any `json:"should,omitempty"`
			Filter         []any `json:"filter,omitempty"`
			Must           []any `json:"must,omitempty"`
			MinShouldMatch int   `json:"minimum_should_match,omitempty"`
		} `json:"bool"`
	} `json:"query"`
	Pit *struct {
		ID        string `json:"id,omitempty"`
		KeepAlive string `json:"keep_alive,omitempty"`
	} `json:"pit,omitempty"`
}

func (dsl DSLCfg) String() string {
	bytes, _ := sonic.MarshalIndent(dsl, "", "  ")
	return string(bytes)
}

type SearchAfterParams struct {
	SearchAfter  []any  `json:"search_after"`
	PitID        string `json:"pit_id"`
	PitKeepAlive string `json:"pit_keep_alive"`
}

//go:generate mockgen -source ../interfaces/logic_view_service.go -destination ../interfaces/mock/mock_logic_view_service.go
type LogicViewService interface {
	// QueryWithPaging queries logic-view data and returns cursor paging state when supported.
	QueryWithPaging(ctx context.Context, resource *Resource, params *ResourceDataQueryParams) (*ResourceDataQueryResult, error)
}
