// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package vega_backend

import (
	"context"
	"sync"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

var (
	vbServiceOnce     sync.Once
	vbServiceInstance interfaces.VegaBackendService
)

type vegaBackendService struct {
	appSetting *common.AppSetting
	vba        interfaces.VegaBackendAccess
}

// NewVegaBackendService creates the vega-backend service used by BKN business logic.
func NewVegaBackendService(appSetting *common.AppSetting, vba interfaces.VegaBackendAccess) interfaces.VegaBackendService {
	vbServiceOnce.Do(func() {
		vbServiceInstance = &vegaBackendService{
			appSetting: appSetting,
			vba:        vba,
		}
	})
	return vbServiceInstance
}

func (vbs *vegaBackendService) GetCatalogByID(ctx context.Context, id string) (*interfaces.Catalog, error) {
	return vbs.vba.GetCatalogByID(ctx, id)
}

func (vbs *vegaBackendService) CreateCatalog(ctx context.Context, req *interfaces.CatalogRequest) (*interfaces.Catalog, error) {
	return vbs.vba.CreateCatalog(ctx, req)
}

func (vbs *vegaBackendService) GetResourceByID(ctx context.Context, id string) (*interfaces.VegaResource, error) {
	return vbs.vba.GetResourceByID(ctx, id)
}

func (vbs *vegaBackendService) CreateResource(ctx context.Context, req *interfaces.VegaResource) error {
	return vbs.vba.CreateResource(ctx, req)
}

func (vbs *vegaBackendService) DeleteResource(ctx context.Context, id string) error {
	return vbs.vba.DeleteResource(ctx, id)
}

func (vbs *vegaBackendService) QueryResourceData(ctx context.Context, resourceID string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	return vbs.vba.QueryResourceData(ctx, resourceID, params)
}

func (vbs *vegaBackendService) WriteDatasetDocuments(ctx context.Context, datasetID string, documents []map[string]any) error {
	return vbs.vba.WriteDatasetDocuments(ctx, datasetID, documents)
}

func (vbs *vegaBackendService) DeleteDatasetDocumentByID(ctx context.Context, datasetID string, docID string) error {
	return vbs.vba.DeleteDatasetDocumentByID(ctx, datasetID, docID)
}

func (vbs *vegaBackendService) DeleteDatasetDocumentsByQuery(ctx context.Context, datasetID string, filterCondition map[string]any) error {
	return vbs.vba.DeleteDatasetDocumentsByQuery(ctx, datasetID, filterCondition)
}
