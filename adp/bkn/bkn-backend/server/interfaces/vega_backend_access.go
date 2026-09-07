// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// VegaBackendAccess defines the interface for accessing vega-backend service
//
//go:generate mockgen -source ../interfaces/vega_backend_access.go -destination ../interfaces/mock/mock_vega_backend_access.go
type VegaBackendAccess interface {
	// GetCatalogByID gets catalog by ID
	GetCatalogByID(ctx context.Context, id string) (*Catalog, error)

	// CreateCatalog creates a new catalog
	CreateCatalog(ctx context.Context, req *CatalogRequest) (*Catalog, error)

	// GetResourceByID gets resource by ID
	GetResourceByID(ctx context.Context, id string) (*VegaResource, error)

	// CreateResource creates a new resource
	CreateResource(ctx context.Context, req *VegaResource) error

	// DeleteResource deletes a resource by ID
	DeleteResource(ctx context.Context, id string) error

	// QueryResourceData queries data from a vega Resource (same HTTP contract as dataset resource data API).
	QueryResourceData(ctx context.Context, resourceID string, params *ResourceDataQueryParams) (*DatasetQueryResponse, error)

	// WriteDatasetDocument writes one document to a dataset.
	WriteDatasetDocument(ctx context.Context, datasetID, docID string, document map[string]any) error

	// DeleteDatasetDocumentByID deletes a document by ID from dataset
	DeleteDatasetDocumentByID(ctx context.Context, datasetID string, docID string) error

	// DeleteDatasetDocumentsByQuery deletes documents by query condition from dataset
	DeleteDatasetDocumentsByQuery(ctx context.Context, datasetID string, filterCondition map[string]any) error
}
