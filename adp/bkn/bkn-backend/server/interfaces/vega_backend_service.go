// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/vega_backend_service.go -destination ../interfaces/mock/mock_vega_backend_service.go

// VegaBackendService defines the business-facing operations provided by vega-backend.
type VegaBackendService interface {
	GetCatalogByID(ctx context.Context, id string) (*Catalog, error)
	CreateCatalog(ctx context.Context, req *CatalogRequest) (*Catalog, error)
	GetResourceByID(ctx context.Context, id string) (*VegaResource, error)
	CreateResource(ctx context.Context, req *VegaResource) error
	DeleteResource(ctx context.Context, id string) error
	QueryResourceData(ctx context.Context, resourceID string, params *ResourceDataQueryParams) (*DatasetQueryResponse, error)
	WriteDatasetDocuments(ctx context.Context, datasetID string, documents []map[string]any) error
	DeleteDatasetDocumentByID(ctx context.Context, datasetID string, docID string) error
	DeleteDatasetDocumentsByQuery(ctx context.Context, datasetID string, filterCondition map[string]any) error
}
