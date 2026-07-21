// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

func TestResourceDataServicePrepareOutputFieldsParams(t *testing.T) {
	t.Run("prepare output fields params filters undefined fields", func(t *testing.T) {
		rds := &resourceDataService{}
		resource := &interfaces.Resource{
			Category: interfaces.ResourceCategoryTable,
			SchemaDefinition: []*interfaces.Property{
				{Name: "name"},
				{Name: "age"},
			},
		}
		params := &interfaces.ResourceDataQueryParams{
			OutputFields: []string{"name", "missing", "age"},
		}

		rds.prepareOutputFieldsParams(resource, params)

		expected := []string{"name", "age"}
		assert.Equal(t, expected, params.OutputFields)
	})

	t.Run("prepare output fields params index keeps score", func(t *testing.T) {
		rds := &resourceDataService{}
		resource := &interfaces.Resource{
			Category: interfaces.ResourceCategoryIndex,
			SchemaDefinition: []*interfaces.Property{
				{Name: "name"},
			},
		}
		params := &interfaces.ResourceDataQueryParams{
			OutputFields: []string{"name", "_score", "missing"},
		}

		rds.prepareOutputFieldsParams(resource, params)

		expected := []string{"name", "_score"}
		assert.Equal(t, expected, params.OutputFields)
	})
}

func TestResourceDataServiceQuery(t *testing.T) {
	t.Run("query rejects disabled catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		rds := &resourceDataService{cs: mockCS}
		resource := &interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
		}
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

		_, _, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		assertCatalogDisabledError(t, err)
	})

	t.Run("query table with local index uses local index manager", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
		rds := &resourceDataService{cs: mockCS, lim: mockLIM}
		resource := &interfaces.Resource{
			ID:             "resource-1",
			CatalogID:      "catalog-1",
			Category:       interfaces.ResourceCategoryTable,
			LocalIndexName: "vega-build-resource-1-task-1",
			SchemaDefinition: []*interfaces.Property{
				{Name: "name"},
			},
		}
		params := &interfaces.ResourceDataQueryParams{}
		wantRows := []map[string]any{{"name": "openbkn"}}

		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockLIM.EXPECT().ListDocuments(gomock.Any(), resource.LocalIndexName, resource, params).
			Return(wantRows, int64(1), nil)

		rows, total, err := rds.query(context.Background(), resource, params)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, wantRows, rows)
	})

	t.Run("query dataset builds actual filter condition and delegates", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockDS := mock_interfaces.NewMockDatasetService(ctrl)
		rds := &resourceDataService{cs: mockCS, ds: mockDS}
		resource := &interfaces.Resource{
			ID:        "dataset-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryDataset,
			SchemaDefinition: []*interfaces.Property{
				{Name: "name", Type: interfaces.DataType_String},
			},
		}
		params := &interfaces.ResourceDataQueryParams{
			FilterCondCfg: &interfaces.FilterCondCfg{
				Name:      "name",
				Operation: "==",
				ValueOptCfg: interfaces.ValueOptCfg{
					ValueFrom: interfaces.ValueFrom_Const,
					Value:     "alice",
				},
			},
		}
		wantRows := []map[string]any{{"name": "alice"}}

		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockDS.EXPECT().ListDocuments(gomock.Any(), "dataset-1", resource, params).
			DoAndReturn(func(ctx context.Context, resourceID string, gotResource *interfaces.Resource,
				gotParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
				require.NotNil(t, gotParams.ActualFilterCond)
				assert.Equal(t, "==", gotParams.ActualFilterCond.GetOperation())
				return wantRows, int64(1), nil
			})

		rows, total, err := rds.query(context.Background(), resource, params)

		require.NoError(t, err)
		assert.Equal(t, wantRows, rows)
		assert.Equal(t, int64(1), total)
	})

	t.Run("query logic view filters sort and output fields before delegating", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockLVS := mock_interfaces.NewMockLogicViewService(ctrl)
		rds := &resourceDataService{cs: mockCS, lvs: mockLVS}
		resource := &interfaces.Resource{
			ID:        "logic-view-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryLogicView,
			SchemaDefinition: []*interfaces.Property{
				{Name: "name"},
			},
		}
		params := &interfaces.ResourceDataQueryParams{
			Sort: []*interfaces.SortField{
				{Field: "name", Direction: "asc"},
				{Field: "missing", Direction: "desc"},
			},
			OutputFields: []string{"name", "missing"},
		}
		wantRows := []map[string]any{{"name": "alice"}}

		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockLVS.EXPECT().QueryWithPaging(gomock.Any(), resource, params).
			DoAndReturn(func(ctx context.Context, gotResource *interfaces.Resource,
				gotParams *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				assert.Equal(t, []*interfaces.SortField{{Field: "name", Direction: "asc"}}, gotParams.Sort)
				assert.Equal(t, []string{"name"}, gotParams.OutputFields)
				return &interfaces.ResourceDataQueryResult{Entries: wantRows, TotalCount: 1, Paging: &interfaces.PagingResponse{}}, nil
			})

		rows, total, err := rds.query(context.Background(), resource, params)

		require.NoError(t, err)
		assert.Equal(t, wantRows, rows)
		assert.Equal(t, int64(1), total)
	})
}

func TestResourceDataServiceRejectsIndexAggregationCursor(t *testing.T) {
	rds := &resourceDataService{}
	_, err := rds.QueryWithPaging(context.Background(), &interfaces.Resource{
		ID:       "index-1",
		Category: interfaces.ResourceCategoryIndex,
	}, &interfaces.ResourceDataQueryParams{
		Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 10},
		Sort:   []*interfaces.SortField{{Field: "timestamp", Direction: "desc"}},
		GroupBy: []*interfaces.GroupByItem{
			{Property: "category"},
		},
	})
	require.Error(t, err)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.BaseError.ErrorCode)
}

func TestResourceDataServicePrepareSortParams(t *testing.T) {
	t.Run("keeps schema aggregation and group fields", func(t *testing.T) {
		rds := &resourceDataService{}
		resource := &interfaces.Resource{
			SchemaDefinition: []*interfaces.Property{
				{Name: "name"},
				{Name: "age"},
			},
		}
		params := &interfaces.ResourceDataQueryParams{
			Sort: []*interfaces.SortField{
				{Field: "name", Direction: "asc"},
				{Field: "missing", Direction: "desc"},
				{Field: "__value", Direction: "desc"},
				{Field: "group_name", Direction: "asc"},
				{Field: "total", Direction: "desc"},
			},
			Aggregation: &interfaces.Aggregation{
				Alias: "total",
			},
			GroupBy: []*interfaces.GroupByItem{
				{Property: "group_name"},
			},
		}

		got := rds.prepareSortParams(resource, params)

		require.Same(t, params, got)
		assert.Equal(t, []*interfaces.SortField{
			{Field: "name", Direction: "asc"},
			{Field: "__value", Direction: "desc"},
			{Field: "group_name", Direction: "asc"},
			{Field: "total", Direction: "desc"},
		}, got.Sort)
	})

	t.Run("returns nil or original params for nil inputs", func(t *testing.T) {
		rds := &resourceDataService{}
		params := &interfaces.ResourceDataQueryParams{}

		assert.Nil(t, rds.prepareSortParams(nil, nil))
		assert.Same(t, params, rds.prepareSortParams(nil, params))
		assert.Nil(t, rds.prepareSortParams(&interfaces.Resource{}, nil))
	})
}

func assertCatalogDisabledError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)

	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Catalog_IsDisabled, httpErr.BaseError.ErrorCode)
}
