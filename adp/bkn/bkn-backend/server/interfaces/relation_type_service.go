// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"
)

//go:generate mockgen -source ../interfaces/relation_type_service.go -destination ../interfaces/mock/mock_relation_type_service.go
type RelationTypeService interface {
	CheckRelationTypeExistByID(ctx context.Context, knID string, branch string, rtID string) (string, bool, error)
	CreateRelationTypes(ctx context.Context, tx *sql.Tx, relationTypes []*RelationType, mode string, validateDependency bool) ([]string, error)
	ListRelationTypes(ctx context.Context, query RelationTypesQueryParams) ([]*RelationType, int, error)
	ListRelationTypeSummaries(ctx context.Context, query RelationTypesQueryParams) ([]*RelationType, int, error)
	GetRelationTypesByIDs(ctx context.Context, knID string, branch string, rtIDs []string) ([]*RelationType, error)
	UpdateRelationType(ctx context.Context, tx *sql.Tx, relationType *RelationType, strictMode bool) error
	DeleteRelationTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, rtIDs []string) error

	GetRelationTypeIDsByKnID(ctx context.Context, knID string, branch string) ([]string, error)
	// GetAllRelationTypesByKnID reads every relation type of one network branch without any
	// permission filtering. It is for internal callers that apply their own checks, as the
	// Cypher compiler does with query_data; never return its result to a caller as it is.
	GetAllRelationTypesByKnID(ctx context.Context, knID string, branch string) ([]*RelationType, error)
	DeleteRelationTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) error

	SearchRelationTypes(ctx context.Context, query *ConceptsQuery) (RelationTypes, error)

	// Write relation types to the index.
	InsertDatasetData(ctx context.Context, relationTypes []*RelationType) error

	// ValidateRelationTypes validates dependency existence only and does not persist data.
	ValidateRelationTypes(ctx context.Context, knID string, branch string, relationTypes []*RelationType, strictMode bool, batch *BatchIDIndex, mode string) error

	// CheckOverwriteDeclarations is the first step of an overwrite import and writes nothing: see ObjectTypeService.
	CheckOverwriteDeclarations(ctx context.Context, knID string, branch string, relationTypes []*RelationType, mode string) error
}
