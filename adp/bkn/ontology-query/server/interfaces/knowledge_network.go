// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	cond "ontology-query/common/condition"
	"sync"
)

var (
	DIRECTION_MAP = map[string]bool{
		DIRECTION_FORWARD:       true,
		DIRECTION_BACKWARD:      true,
		DIRECTION_BIDIRECTIONAL: true,
	}
)

// Request body for retrieving an object subgraph by source, direction, and path length.
type SubGraphQueryBaseOnSource struct {
	ConceptGroups     []string       `json:"concept_groups,omitempty"`
	SourceObjecTypeId string         `json:"source_object_type_id"`
	Condition         map[string]any `json:"condition,omitempty"`
	Direction         string         `json:"direction"`
	PathLength        int            `json:"path_length"`
	// IncludeIncompletePath: when true, include relation paths that already have at least one traversed edge
	// but the conceptual type path is not fully completed (e.g. exploration stops with no next hop).
	// When false, those incomplete paths are omitted. Paths with zero edges are never returned.
	IncludeIncompletePath bool `json:"include_incomplete_path,omitempty"`
	PageQuery

	ActualCondition *cond.CondCfg `json:"-"`
	KNID            string        `json:"-"`
	Branch          string        `json:"-"`
	CommonQueryParameters
	*PathQuotaManager
	BatchQueryState
}

// Path-based subgraph query request body.
type SubGraphQueryBaseOnTypePath struct {
	Paths  QueryRelationTypePaths
	KNID   string
	Branch string
	CommonQueryParameters
}

// Request body for building a relationship subgraph from object instances.
type SubGraphQueryBaseOnObjects struct {
	Entries []InputObjectInstance `json:"entries"`
	KNID    string                `json:"-"`
	Branch  string                `json:"-"`
	CommonQueryParameters
}

// Input object instance.
type InputObjectInstance struct {
	ObjectTypeID     string         `json:"object_type_id"`
	InstanceIdentity map[string]any `json:"_instance_identity"`
}

type QueryRelationTypePaths struct {
	TypePaths []QueryRelationTypePath `json:"relation_type_paths"`
}

type QueryRelationTypePath struct {
	ObjectTypes []ObjectTypeWithKeyField `json:"object_types"` // The key is the object-type ID.
	Edges       []TypeEdge               `json:"relation_types"`
	Limit       int                      `json:"limit"` // Number of results returned for the current path.
	// ObjectTypes   []ObjectTypeWithKeyField `json:"-"`     // The key is the object-type ID.
}

// type ObjectTypeInRequestPath struct {
// 	OTID      string   `json:"id"`
// 	Condition *CondCfg `json:"condition,omitempty"` // Filter for the starting object type.
// 	PageQuery          // Sorting and limit for the path's starting object type.
// }

// Path quota management policy.
type PathQuotaManager struct {
	TotalLimit         int64    `json:"-"` // Total path limit, currently configured globally as 10,000.
	GlobalCount        int64    `json:"-"` // Number of object paths added globally.
	UsedQuota          sync.Map `json:"-"` // Consumed quota.
	RequestPathTypeNum int      `json:"-"` // Number of conceptual paths in the current request.
}

// Intermediate state for batched queries.
type BatchQueryState struct {
	Visited   map[string]bool `json:"-"`
	BatchSize int             `json:"-"`
}

// Object subgraph response body.
type ObjectSubGraph struct {
	Objects           map[string]ObjectInfoInSubgraph `json:"objects"`
	IsolatedObjects   map[string]ObjectInfoInSubgraph `json:"isolated_objects,omitempty"`
	RelationPaths     []RelationPath                  `json:"relation_paths"`
	TotalCount        int64                           `json:"total_count,omitempty"`
	SearchAfter       []any                           `json:"search_after,omitempty"`
	CuurentPathNumber int                             `json:"current_path_number,omitempty"`
	OverallMs         int64                           `json:"overall_ms"`
}

// Path-based query response body.
type PathsEntries struct {
	Entries []ObjectSubGraph `json:"entries"`
}

type ObjectSystemInfo struct {
	InstanceID       any            `json:"_instance_id"`
	InstanceIdentity map[string]any `json:"_instance_identity"`
	Display          any            `json:"_display"`
}

// Object information in an object subgraph.
type ObjectInfoInSubgraph struct {
	ObjectSystemInfo
	ObjectTypeId   string         `json:"object_type_id"`
	ObjectTypeName string         `json:"object_type_name"`
	Properties     map[string]any `json:"properties"`
}

// Path composed of relationship instances.
type RelationPath struct {
	Relations []Relation `json:"relations"`
	Length    int        `json:"length"`
}

// Relationship instance.
type Relation struct {
	RelationTypeId   string `json:"relation_type_id"`
	RelationTypeName string `json:"relation_type_name"`
	SourceObjectId   string `json:"source_object_id"`
	TargetObjectId   string `json:"target_object_id"`
}

// Conceptual path returned by the ontology engine.
type RelationTypePath struct {
	ObjectTypes []ObjectTypeWithKeyField `json:"object_types"`
	TypeEdges   []TypeEdge               `json:"relation_types"`
	Length      int                      `json:"length"`

	ID int `json:"-"` // Conceptual-path ID used by subsequent object-path quota enforcement.
}

type TypeEdge struct {
	RelationTypeId     string       `json:"relation_type_id"`
	RelationType       RelationType `json:"relation_type"`
	SourceObjectTypeId string       `json:"source_object_type_id"`
	TargetObjectTypeId string       `json:"target_object_type_id"`
	Direction          string       `json:"direction"`
}

type LevelObject struct {
	ObjectID   string
	ObjectUK   map[string]any // Object primary key.
	ObjectData map[string]any
	ObjectType *ObjectType
	PathFrom   string // Origin object used to construct the path.
}

type LevelObjectWithPath struct {
	LevelObject
	Paths []RelationPath // All paths from the starting object to the current object.
}
