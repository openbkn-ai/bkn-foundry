// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
)

// Request body for retrieving an object subgraph by source, direction, and path length.
type PathsQueryBaseOnSource struct {
	ConceptGroups     []string `json:"concept_groups,omitempty"`
	SourceObjecTypeId string   `json:"source_object_type_id"`
	Direction         string   `json:"direction"`
	PathLength        int      `json:"path_length"`

	KNID string `json:"-"`
	// IncludeTypeInfo bool   `json:"-"`
}

// Relationship-type list query parameters.
type RelationTypesQuery struct {
	// Query by one object-type ID for backward compatibility.
	SourceObjectTypeID string `json:"source_object_type_id,omitempty"`
	TargetObjectTypeID string `json:"target_object_type_id,omitempty"`

	// Query by multiple object-type IDs.
	SourceObjectTypeIDs []string `json:"source_object_type_ids,omitempty"`
	TargetObjectTypeIDs []string `json:"target_object_type_ids,omitempty"`
}

//go:generate mockgen -source ../interfaces/ontology_manager_access.go -destination ../interfaces/mock/mock_ontology_manager_access.go
type OntologyManagerAccess interface {
	GetObjectType(ctx context.Context, knID string, branch string, otId string) (ObjectType, bool, error)
	// GetMetricDefinition loads metric definition from bkn-backend GET .../metrics/{metric_id} (same base URL as GetObjectType).
	GetMetricDefinition(ctx context.Context, knID string, branch string, metricID string) (*MetricDefinition, bool, error)
	GetRelationType(ctx context.Context, knID string, branch string, rtId string) (RelationType, bool, error)
	GetActionType(ctx context.Context, knID string, branch string, atId string) (ActionType, map[string]any, bool, error)
	GetRelationTypePathsBaseOnSource(ctx context.Context, knID string, branch string, query PathsQueryBaseOnSource) ([]RelationTypePath, error)
	ListRelationTypes(ctx context.Context, knID string, branch string, query RelationTypesQuery) ([]RelationType, error)
	GetRiskTypesByIDs(ctx context.Context, knID string, branch string, riskTypeIDs []string) ([]RiskType, error)
}
