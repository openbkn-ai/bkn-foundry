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

//go:generate mockgen -source ../interfaces/object_type_service.go -destination ../interfaces/mock/mock_object_type_service.go
type ObjectTypeService interface {
	CheckObjectTypeExistByID(ctx context.Context, knID string, branch string, otID string) (string, bool, error)
	CheckObjectTypeExistByName(ctx context.Context, knID string, branch string, otName string) (string, bool, error)
	CreateObjectTypes(ctx context.Context, tx *sql.Tx, objectTypes []*ObjectType, mode string, needCreateConceptGroupRelation bool, strictMode bool) ([]string, error)
	ListObjectTypes(ctx context.Context, tx *sql.Tx, query ObjectTypesQueryParams) ([]*ObjectType, int, error)
	ListObjectTypeSummaries(ctx context.Context, tx *sql.Tx, query ObjectTypesQueryParams) ([]*ObjectType, int, error)
	GetObjectTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, otIDs []string) ([]*ObjectType, error)
	// FilterObjectTypesForRead applies the caller's property-level visibility to public object metadata.
	FilterObjectTypesForRead(ctx context.Context, knID string, objectTypes []*ObjectType) ([]*ObjectType, error)
	UpdateObjectType(ctx context.Context, tx *sql.Tx, objectType *ObjectType, strictMode bool) error
	// UpdateDataProperties persists data properties after request validation.
	UpdateDataProperties(ctx context.Context, objectType *ObjectType, dataProperties []*DataProperty) error
	DeleteObjectTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, otIDs []string) error

	GetObjectTypeByID(ctx context.Context, tx *sql.Tx, knID string, branch string, otID string) (*ObjectType, error)
	GetAllObjectTypesByKnID(ctx context.Context, knID string, branch string) (map[string]*ObjectType, error)
	GetObjectTypeIDsByKnID(ctx context.Context, knID string, branch string) ([]string, error)
	DeleteObjectTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) error
	GetObjectTypeSampleData(ctx context.Context, knID string, branch string, otID string, query ObjectTypeSampleDataQueryParams) (*ObjectTypeSampleData, error)

	SearchObjectTypes(ctx context.Context, query *ConceptsQuery) (ObjectTypes, error)

	// Get basic object-type information without translation-dependent resources.
	GetObjectTypesMapByIDs(ctx context.Context, knID string, branch string, otIDs []string, needPropMap bool) (map[string]*ObjectType, error)

	// GetObjectTypeResources reads the vega resource bound to each resource-bound object type, keyed
	// by object type ID, the same way object type details are enriched: as the caller, and for a
	// resource vega refuses to the caller, as the knowledge network's proxy for current published
	// bindings. An object type whose resource cannot be read is absent. Callers must pass only
	// object types the caller is already authorized to read.
	GetObjectTypeResources(ctx context.Context, objectTypes []*ObjectType) map[string]*VegaResource

	// Write object types to the index.
	InsertDatasetData(ctx context.Context, objectTypes []*ObjectType) error

	// ValidateObjectTypes validates dependency existence only and does not persist data.
	// When batch is nil, behavior matches history (DB existence only). For full-KN / concept-group preflight, pass an index from batchindex.CollectKNFromPayload / batchindex.CollectFromConceptGroups.
	ValidateObjectTypes(ctx context.Context, knID string, branch string, objectTypes []*ObjectType, strictMode bool, batch *BatchIDIndex, mode string) error
}
