// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
	"vega-backend/logics/filter_condition"
	resourcelogic "vega-backend/logics/resource"
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

func TestEnsureResourceQueryableMetadata(t *testing.T) {
	tests := []struct {
		name        string
		resource    *interfaces.Resource
		wantError   bool
		wantDetails string
	}{
		{
			name: "rejects empty schema after discover failure for any resource category",
			resource: &interfaces.Resource{
				ID:                 "resource-1",
				Enabled:            true,
				Category:           interfaces.ResourceCategoryFileset,
				LastDiscoverStatus: interfaces.DiscoverStatusError,
			},
			wantError:   true,
			wantDetails: "resource metadata discovery failed; refresh the resource schema before querying",
		},
		{
			name: "rejects empty schema after a successful discovery observation",
			resource: &interfaces.Resource{
				ID:                 "fileset-1",
				Enabled:            true,
				Category:           interfaces.ResourceCategoryFileset,
				LastDiscoverStatus: interfaces.DiscoverStatusUnchanged,
			},
			wantError:   true,
			wantDetails: "resource schema definition is empty; refresh the resource schema before querying",
		},
		{
			name: "allows last known schema after discover failure",
			resource: &interfaces.Resource{
				ID:                 "resource-1",
				Enabled:            true,
				Category:           interfaces.ResourceCategoryTable,
				LastDiscoverStatus: interfaces.DiscoverStatusError,
				SchemaDefinition:   []*interfaces.Property{{Name: "id"}},
			},
		},
		{
			name: "rejects a missing resource even when its previous schema remains",
			resource: &interfaces.Resource{
				ID:                 "resource-1",
				Enabled:            true,
				Category:           interfaces.ResourceCategoryDataset,
				LastDiscoverStatus: interfaces.DiscoverStatusMissing,
				SchemaDefinition:   []*interfaces.Property{{Name: "id"}},
			},
			wantError:   true,
			wantDetails: "resource is missing from its source; run discovery and restore the source resource before querying",
		},
		{
			name: "allows restored resource metadata",
			resource: &interfaces.Resource{
				ID:                 "resource-1",
				Enabled:            true,
				Category:           interfaces.ResourceCategoryDataset,
				LastDiscoverStatus: interfaces.DiscoverStatusRestored,
				SchemaDefinition:   []*interfaces.Property{{Name: "id"}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resourcelogic.EnsureResourceQueryable(context.Background(), test.resource)
			if !test.wantError {
				require.NoError(t, err)
				return
			}

			var httpErr *rest.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
			assert.Equal(t, verrors.VegaBackend_Resource_MetadataUnavailable, httpErr.BaseError.ErrorCode)
			assert.Equal(t, test.wantDetails, httpErr.BaseError.ErrorDetails)
		})
	}
}

func TestEnsureResourceQueryableDoesNotExposeStatusMessage(t *testing.T) {
	resource := &interfaces.Resource{
		ID:                 "resource-1",
		Enabled:            true,
		Category:           interfaces.ResourceCategoryFileset,
		LastDiscoverStatus: interfaces.DiscoverStatusError,
		StatusMessage:      "discover metadata failed: syntax error at or near LATERAL",
	}

	_, err := resourcelogic.EnsureResourceQueryable(context.Background(), resource)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.NotContains(t, httpErr.BaseError.ErrorDetails, resource.StatusMessage)
	assert.NotContains(t, httpErr.BaseError.ErrorDetails, "syntax error at or near LATERAL")
}

func TestResourceDataServiceQueryWithPagingRejectsUnavailableTableMetadata(t *testing.T) {
	resource := &interfaces.Resource{
		ID:                 "resource-1",
		Enabled:            true,
		Category:           interfaces.ResourceCategoryFileset,
		LastDiscoverStatus: interfaces.DiscoverStatusError,
	}

	for _, params := range []*interfaces.ResourceDataQueryParams{
		{},
		{Paging: interfaces.PagingRequest{Cursor: "existing-cursor"}},
	} {
		_, err := (&resourceDataService{}).QueryWithPaging(context.Background(), resource, params)

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_MetadataUnavailable, httpErr.BaseError.ErrorCode)
	}
}

func TestResourceDataServiceQuery(t *testing.T) {
	t.Run("query rejects disabled catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		rds := &resourceDataService{cs: mockCS}
		resource := &interfaces.Resource{
			ID:        "resource-1",
			Enabled:   true,
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
			ID:               "resource-1",
			Enabled:          true,
			CatalogID:        "catalog-1",
			Category:         interfaces.ResourceCategoryTable,
			LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			LocalIndexName:   "vega-build-resource-1-task-1",
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
			Enabled:   true,
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
			Enabled:   true,
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
		ID:               "index-1",
		Enabled:          true,
		Category:         interfaces.ResourceCategoryIndex,
		SchemaDefinition: []*interfaces.Property{{Name: "category"}},
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

func TestResourceDataPaginationCategoryUsesPhysicalEngine(t *testing.T) {
	tests := []struct {
		name     string
		resource *interfaces.Resource
		want     string
	}{
		{name: "index", resource: &interfaces.Resource{Category: interfaces.ResourceCategoryIndex}, want: interfaces.ResourceCategoryIndex},
		{name: "dataset", resource: &interfaces.Resource{Category: interfaces.ResourceCategoryDataset}, want: interfaces.ResourceCategoryIndex},
		{name: "local index table", resource: &interfaces.Resource{Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index-1"}, want: interfaces.ResourceCategoryIndex},
		{name: "rds table", resource: &interfaces.Resource{Category: interfaces.ResourceCategoryTable}, want: interfaces.ResourceCategoryTable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resourceDataPaginationCategory(tt.resource))
		})
	}
}

func TestResourceDataServiceRejectsOpenSearchCursorWithoutSort(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	rds := &resourceDataService{cs: mockCS, ds: mockDS}
	resource := &interfaces.Resource{
		ID:               "dataset-1",
		Enabled:          true,
		CatalogID:        "catalog-1",
		Category:         interfaces.ResourceCategoryDataset,
		SchemaDefinition: []*interfaces.Property{{Name: "id"}},
	}
	mockCS.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockDS.EXPECT().ListDocuments(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		Return(nil, int64(0), nil)

	_, err := rds.QueryWithPaging(context.Background(), resource, &interfaces.ResourceDataQueryParams{
		Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1},
	})
	require.Error(t, err)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
}

func TestResourceDataServiceRejectsOpenSearchFirstPageWindowOverflow(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	rds := &resourceDataService{cs: mockCS, ds: mockDS}
	resource := &interfaces.Resource{
		ID:               "dataset-1",
		Enabled:          true,
		CatalogID:        "catalog-1",
		Category:         interfaces.ResourceCategoryDataset,
		SchemaDefinition: []*interfaces.Property{{Name: "id"}},
	}
	mockCS.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockDS.EXPECT().ListDocuments(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		Return(nil, int64(0), nil)

	_, err := rds.QueryWithPaging(context.Background(), resource, &interfaces.ResourceDataQueryParams{
		Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Offset: interfaces.MaxPageLimit, Limit: 1},
		Sort:   []*interfaces.SortField{{Field: "id", Direction: "asc"}},
	})
	require.Error(t, err)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
}

func TestDatasetCursorUsesSearchAfterPagination(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	rds := &resourceDataService{cs: mockCS, ds: mockDS}
	resource := &interfaces.Resource{
		ID:               "dataset-1",
		Enabled:          true,
		CatalogID:        "catalog-1",
		Category:         interfaces.ResourceCategoryDataset,
		SchemaDefinition: []*interfaces.Property{{Name: "id"}},
	}
	params := &interfaces.ResourceDataQueryParams{
		Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1},
		Sort:   []*interfaces.SortField{{Field: "id", Direction: "asc"}},
	}
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).Times(2).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	firstPage := true
	mockDS.EXPECT().ListDocuments(gomock.Any(), "dataset-1", resource, gomock.Any()).Times(2).
		DoAndReturn(func(_ context.Context, _ string, _ *interfaces.Resource, pageParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			assert.Equal(t, 1, pageParams.Limit)
			if firstPage {
				firstPage = false
				assert.Empty(t, pageParams.SearchAfter)
				pageParams.SearchAfter = []any{"sort-1"}
				return []map[string]any{{"id": 1}}, 0, nil
			}
			assert.Equal(t, []any{"sort-1"}, pageParams.SearchAfter)
			return nil, 0, nil
		})

	first, err := rds.QueryWithPaging(context.Background(), resource, params)
	require.NoError(t, err)
	require.NotNil(t, first.Paging.NextCursor)
	final, err := rds.QueryWithPaging(context.Background(), resource, &interfaces.ResourceDataQueryParams{
		Paging: interfaces.PagingRequest{Cursor: *first.Paging.NextCursor},
	})
	require.NoError(t, err)
	assert.Nil(t, final.Paging.NextCursor)
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

// TestQueryClassifiesUnsupportedOperations 覆盖两个之前被抹平的分型点。
//
// 之前的形态是：QueryData 里判出的 400 被 query() 无条件重包成 500，而 fileset
// 分支根本没做判断——两处加起来，anyshare / mariadb 那几个连接器返回
// UnsupportedOperationError 的改动一点行为变化都没有，调用方拿到的仍是
// 「数据资源内部错误」，ontology-query 继续判成依赖故障。
func TestQueryClassifiesUnsupportedOperations(t *testing.T) {
	newResource := func(category string) *interfaces.Resource {
		return &interfaces.Resource{
			ID: "resource-1", CatalogID: "catalog-1", Category: category, Enabled: true,
			SchemaDefinition: []*interfaces.Property{{Name: "name", Type: interfaces.DataType_String}},
		}
	}
	unsupported := filter_condition.NewUnsupportedOperationError("regex", filter_condition.QueryChannelSQL)

	t.Run("表分支的 400 不再被上层压成 500", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockTableConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := newResource(interfaces.ResourceCategoryTable)

		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(nil, unsupported)

		_, _, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		assertUnsupportedOperationHTTPError(t, err)
	})

	t.Run("fileset 分支也要分型", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockFilesetConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := newResource(interfaces.ResourceCategoryFileset)

		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).
			Return(nil, filter_condition.NewUnsupportedOperationError("regex", filter_condition.QueryChannelFileset))

		_, _, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		assertUnsupportedOperationHTTPError(t, err)
	})

	t.Run("真正的下游故障仍然是 500", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockTableConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := newResource(interfaces.ResourceCategoryTable)

		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).
			Return(nil, errors.New("connection reset by peer"))

		_, _, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		var httpErr *rest.HTTPError
		require.True(t, errors.As(err, &httpErr), "want an HTTPError, got %v", err)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
	})
}

func assertUnsupportedOperationHTTPError(t *testing.T, err error) {
	t.Helper()
	var httpErr *rest.HTTPError
	require.True(t, errors.As(err, &httpErr), "want an HTTPError, got %v", err)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode,
		"算子不支持是请求侧问题：包成 500 会让 ontology-query 判成依赖故障")
	assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.BaseError.ErrorCode)
}
