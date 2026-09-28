// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
)

func registerMockLogicViewService(t *testing.T, service interfaces.LogicViewService) {
	t.Helper()
	previous := resourcelogic.GetLogicViewService()
	resourcelogic.SetLogicViewService(service)
	t.Cleanup(func() { resourcelogic.SetLogicViewService(previous) })
}

func TestQuerySourcePageVectorConditionErrorProvenance(t *testing.T) {
	for _, tc := range []struct {
		name       string
		stored     bool
		statusCode int
	}{
		{name: "stored condition", stored: true, statusCode: http.StatusInternalServerError},
		{name: "request condition", statusCode: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCS := mock_interfaces.NewMockCatalogService(ctrl)
			mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
			mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
				Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
			mockMFS.EXPECT().GetModelByID(gomock.Any(), "removed-model").Return(nil, nil)
			rds := &resourceDataService{cs: mockCS, mfs: mockMFS}
			resource := &interfaces.Resource{
				ID: "source", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable,
				LocalIndexName: "source-index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
				IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "removed-model"},
				SchemaDefinition: []*interfaces.Property{{Name: "embedding", Type: interfaces.DataType_Vector,
					Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}}}},
			}
			condition := &interfaces.FilterCondCfg{Name: "embedding", Operation: filter_condition.OperationKnnVector,
				ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const, Value: "query text"}}
			params := &interfaces.ResourceDataQueryParams{FilterCondCfg: condition}
			if tc.stored {
				params.FixedFilterCondCfg = condition
			}
			rows, total, err := rds.QuerySourcePage(context.Background(), resource, params)
			assert.Nil(t, rows)
			assert.Zero(t, total)
			var httpErr *rest.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tc.statusCode, httpErr.HTTPCode)
		})
	}
}

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
	ctrl := gomock.NewController(t)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	rs.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil).Times(2)
	rds := &resourceDataService{rs: rs}
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
		_, err := rds.QueryWithPaging(context.Background(), resource, params)

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_MetadataUnavailable, httpErr.BaseError.ErrorCode)
	}
}

func TestResourceDataServiceQueryWithPagingRequiresQueryDataPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	rds := &resourceDataService{rs: rs}
	denied := rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden)
	rs.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_QUERY_DATA).Return(denied)

	_, err := rds.QueryWithPaging(context.Background(), &interfaces.Resource{ID: "resource-1"},
		&interfaces.ResourceDataQueryParams{})

	require.ErrorIs(t, err, denied)
}

func TestResourceDataServiceQueryWithPagingPreservesLogicViewQuerySource(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	lvs := mock_interfaces.NewMockLogicViewService(ctrl)
	registerMockLogicViewService(t, lvs)
	rds := &resourceDataService{rs: rs}
	view := &interfaces.Resource{ID: "view-1", Category: interfaces.ResourceCategoryLogicView,
		Enabled: true, SchemaDefinition: []*interfaces.Property{{Name: "name"}}}
	params := &interfaces.ResourceDataQueryParams{}
	rs.EXPECT().CheckResourcePermission(gomock.Any(), view.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
	lvs.EXPECT().QueryWithPaging(gomock.Any(), view, params).Return(&interfaces.ResourceDataQueryResult{
		Entries: []map[string]any{{"name": "alice"}}, QuerySource: interfaces.ResourceQuerySourceLocalIndex,
	}, nil)

	result, err := rds.QueryWithPaging(context.Background(), view, params)
	require.NoError(t, err)
	assert.Equal(t, interfaces.ResourceQuerySourceLocalIndex, result.QuerySource)
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
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
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
				{Name: "blob", Type: interfaces.DataType_Binary},
				{Name: "native_value", Type: interfaces.DataType_Other},
			},
		}
		params := &interfaces.ResourceDataQueryParams{}
		wantRows := []map[string]any{{"name": "openbkn"}}

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockLIM.EXPECT().ListDocuments(gomock.Any(), resource.LocalIndexName, resource, params).
			Return(wantRows, int64(1), nil)

		rows, total, err := rds.QuerySourcePage(context.Background(), resource, params)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, "openbkn", rows[0]["name"])
		assert.Equal(t, interfaces.ResourceValue{Mode: interfaces.ResourceValueModeUnavailable}, rows[0]["blob"])
		assert.Equal(t, interfaces.ResourceValue{Mode: interfaces.ResourceValueModeUnavailable}, rows[0]["native_value"])
	})

	t.Run("query table with local index answers 400 for a condition the index cannot build", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
		rds := &resourceDataService{cs: mockCS, lim: mockLIM}
		resource := &interfaces.Resource{
			ID: "resource-1", Enabled: true, CatalogID: "catalog-1",
			Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			LocalIndexName:   "vega-build-resource-1-task-1",
			SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text}},
		}
		params := &interfaces.ResourceDataQueryParams{}
		cause := fmt.Errorf("failed to build filter query: %w",
			interfaces.NewConditionBuildError("text field title has no keyword feature; re-save the resource configuration and rebuild the local index, or use match"))

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockLIM.EXPECT().ListDocuments(gomock.Any(), resource.LocalIndexName, resource, params).
			Return(nil, int64(0), cause)

		_, _, err := rds.query(context.Background(), resource, params)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.BaseError.ErrorCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "re-save the resource configuration")
	})

	t.Run("query table with local index answers 500 for a stored condition the index cannot build", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
		rds := &resourceDataService{cs: mockCS, lim: mockLIM}
		resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1",
			Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			LocalIndexName: "source-index", SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text}}}
		fixed := &interfaces.FilterCondCfg{Name: "title", Operation: filter_condition.OperationEqual,
			ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const, Value: "example"}}
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: fixed, FixedFilterCondCfg: fixed}
		mockCS.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).
			Return(&interfaces.Catalog{Enabled: true}, nil)
		mockLIM.EXPECT().ListDocuments(gomock.Any(), resource.LocalIndexName, resource, params).Return(nil, int64(0),
			interfaces.NewStoredConditionBuildError(interfaces.NewConditionBuildError("text field has no keyword feature")))
		rows, total, err := rds.QuerySourcePage(context.Background(), resource, params)
		assert.Nil(t, rows)
		assert.Zero(t, total)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
	})

	t.Run("force source bypasses local index and returns binary metadata", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockTableConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := &interfaces.Resource{
			ID:               "resource-1",
			Enabled:          true,
			CatalogID:        "catalog-1",
			Category:         interfaces.ResourceCategoryTable,
			LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			LocalIndexName:   "vega-build-resource-1-task-1",
			SchemaDefinition: []*interfaces.Property{{Name: "blob", Type: interfaces.DataType_Binary}},
		}
		ignoreLocalIndex := true
		binaryMode := interfaces.BinaryModeMetadata
		params := &interfaces.ResourceDataQueryParams{IgnoreLocalIndex: &ignoreLocalIndex, BinaryMode: &binaryMode}

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, params).
			Return(&interfaces.QueryResult{Entries: []map[string]any{{"blob": int64(12)}}, Total: 1}, nil)

		rows, total, err := rds.QuerySourcePage(context.Background(), resource, params)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		length := int64(12)
		assert.Equal(t, interfaces.ResourceValue{Mode: interfaces.ResourceValueModeMetadata, ByteLength: &length}, rows[0]["blob"])
	})

	t.Run("source page returns binary content envelope", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockTableConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1",
			Category:         interfaces.ResourceCategoryTable,
			SchemaDefinition: []*interfaces.Property{{Name: "blob", Type: interfaces.DataType_Binary}}}
		binaryMode := interfaces.BinaryModeContent
		params := &interfaces.ResourceDataQueryParams{BinaryMode: &binaryMode, OutputFields: []string{"blob"}}
		mockCS.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).
			Return(&interfaces.Catalog{Enabled: true}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, params).
			Return(&interfaces.QueryResult{Entries: []map[string]any{{"blob": []byte{1, 2, 3}}}, Total: 1}, nil)

		rows, total, err := rds.QuerySourcePage(context.Background(), resource, params)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		length := int64(3)
		data := "AQID"
		assert.Equal(t, interfaces.ResourceValue{Mode: interfaces.ResourceValueModeContent,
			ByteLength: &length, Data: &data}, rows[0]["blob"])
	})

	t.Run("query dataset passes the dataset service HTTP error through", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockDS := mock_interfaces.NewMockDatasetService(ctrl)
		rds := &resourceDataService{cs: mockCS, ds: mockDS}
		resource := &interfaces.Resource{
			ID: "dataset-1", Enabled: true, CatalogID: "catalog-1",
			Category: interfaces.ResourceCategoryDataset, LocalIndexName: "vega-dataset-index-1",
			SchemaDefinition: []*interfaces.Property{{Name: "body", Type: interfaces.DataType_Text}},
		}
		params := &interfaces.ResourceDataQueryParams{}
		downstream := rest.NewHTTPError(context.Background(), http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails("text field body has no keyword feature; re-save the resource configuration and rebuild the local index, or use match")

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockDS.EXPECT().ListDocuments(gomock.Any(), resource, gomock.Any()).Return(nil, int64(0), downstream)

		_, _, err := rds.query(context.Background(), resource, params)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.BaseError.ErrorCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "re-save the resource configuration")
	})

	t.Run("query dataset builds actual filter condition and delegates", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockDS := mock_interfaces.NewMockDatasetService(ctrl)
		rds := &resourceDataService{cs: mockCS, ds: mockDS}
		resource := &interfaces.Resource{
			ID:             "dataset-1",
			Enabled:        true,
			CatalogID:      "catalog-1",
			Category:       interfaces.ResourceCategoryDataset,
			LocalIndexName: "vega-dataset-index-1",
			SchemaDefinition: []*interfaces.Property{
				{Name: "name", OriginalName: "source_name", Type: interfaces.DataType_String},
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

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockDS.EXPECT().ListDocuments(gomock.Any(), resource, params).
			DoAndReturn(func(ctx context.Context, gotResource *interfaces.Resource,
				gotParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
				require.NotNil(t, gotParams.ActualFilterCond)
				assert.Equal(t, "==", gotParams.ActualFilterCond.GetOperation())
				equal, ok := gotParams.ActualFilterCond.(*filter_condition.EqualCond)
				require.True(t, ok)
				assert.Equal(t, "name", equal.Lfield.OriginalName)
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
		registerMockLogicViewService(t, mockLVS)
		rds := &resourceDataService{cs: mockCS}
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

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
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

	t.Run("query logic view passes the logic view service HTTP error through", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockLVS := mock_interfaces.NewMockLogicViewService(ctrl)
		registerMockLogicViewService(t, mockLVS)
		rds := &resourceDataService{cs: mockCS}
		resource := &interfaces.Resource{
			ID: "logic-view-1", Enabled: true, CatalogID: "catalog-1",
			Category:         interfaces.ResourceCategoryLogicView,
			SchemaDefinition: []*interfaces.Property{{Name: "body", Type: interfaces.DataType_Text}},
		}
		params := &interfaces.ResourceDataQueryParams{}
		downstream := rest.NewHTTPError(context.Background(), http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails("text field body has no keyword feature; re-save the resource configuration")

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockLVS.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).Return(nil, downstream)

		_, _, err := rds.query(context.Background(), resource, params)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.BaseError.ErrorCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "re-save the resource configuration")
	})
}

func TestNormalizeResourceValuesRespectOutputFields(t *testing.T) {
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
		{Name: "id", Type: interfaces.DataType_Integer},
		{Name: "blob", Type: interfaces.DataType_Binary},
		{Name: "native_value", Type: interfaces.DataType_Other},
	}}
	params := &interfaces.ResourceDataQueryParams{OutputFields: []string{"id"}}

	sourceRows := []map[string]any{{"id": int64(1)}}
	normalizedSourceRows, err := normalizeBinaryQueryValues(sourceRows, resource, params)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"id": int64(1)}, normalizedSourceRows[0])

	indexedRows := normalizeIndexedUnavailableQueryValues([]map[string]any{{"id": int64(1)}}, resource, params)
	assert.Equal(t, map[string]any{"id": int64(1)}, indexedRows[0])
}

func TestNormalizeResourceValuesDoesNotAddFieldsToAggregateResults(t *testing.T) {
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
		{Name: "blob", Type: interfaces.DataType_Binary},
		{Name: "native_value", Type: interfaces.DataType_Other},
	}}
	params := &interfaces.ResourceDataQueryParams{
		Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count"},
	}

	sourceRows := []map[string]any{{"__value": int64(1)}}
	normalizedSourceRows, err := normalizeBinaryQueryValues(sourceRows, resource, params)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"__value": int64(1)}, normalizedSourceRows[0])

	indexedRows := normalizeIndexedUnavailableQueryValues([]map[string]any{{"__value": int64(1)}}, resource, params)
	assert.Equal(t, map[string]any{"__value": int64(1)}, indexedRows[0])
}

func TestResourceDataServiceRejectsIndexAggregationCursor(t *testing.T) {
	rds := &resourceDataService{}
	_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(context.Background()), &interfaces.Resource{
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
			assert.Equal(t, tt.want, resourceDataPaginationCategory(tt.resource, &interfaces.ResourceDataQueryParams{}))
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
		LocalIndexName:   "vega-dataset-index-1",
		SchemaDefinition: []*interfaces.Property{{Name: "id"}},
	}
	mockCS.EXPECT().InternalGetByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockDS.EXPECT().ListDocuments(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		Return(nil, int64(0), nil)

	_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(context.Background()), resource, &interfaces.ResourceDataQueryParams{
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
	mockCS.EXPECT().InternalGetByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockDS.EXPECT().ListDocuments(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		Return(nil, int64(0), nil)

	_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(context.Background()), resource, &interfaces.ResourceDataQueryParams{
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
		LocalIndexName:   "vega-dataset-index-1",
		SchemaDefinition: []*interfaces.Property{{Name: "id"}},
	}
	params := &interfaces.ResourceDataQueryParams{
		Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1},
		Sort:   []*interfaces.SortField{{Field: "id", Direction: "asc"}},
	}
	mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Times(2).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	firstPage := true
	mockDS.EXPECT().ListDocuments(gomock.Any(), resource, gomock.Any()).Times(2).
		DoAndReturn(func(_ context.Context, _ *interfaces.Resource, pageParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			assert.Equal(t, 1, pageParams.Paging.Limit)
			if firstPage {
				firstPage = false
				assert.Empty(t, pageParams.SearchAfter)
				pageParams.SearchAfter = []any{"sort-1"}
				return []map[string]any{{"id": 1}}, 0, nil
			}
			assert.Equal(t, []any{"sort-1"}, pageParams.SearchAfter)
			return nil, 0, nil
		})

	first, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(context.Background()), resource, params)
	require.NoError(t, err)
	require.NotNil(t, first.Paging.NextCursor)
	final, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(context.Background()), resource, &interfaces.ResourceDataQueryParams{
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
type codedQueryError struct{ code int }

func (e codedQueryError) Error() string { return "source database error" }
func (e codedQueryError) Code() int     { return e.code }

func TestQueryClassifiesUnsupportedOperations(t *testing.T) {
	newResource := func(category string) *interfaces.Resource {
		return &interfaces.Resource{
			ID: "resource-1", CatalogID: "catalog-1", Category: category, Enabled: true,
			SchemaDefinition: []*interfaces.Property{{Name: "name", Type: interfaces.DataType_String}},
		}
	}
	unsupported := interfaces.NewUnsupportedOperationError("regex", filter_condition.QueryChannelSQL)

	t.Run("connector source permission error becomes HTTP 403", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockTableConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := newResource(interfaces.ResourceCategoryTable)

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypeMariaDB}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeMariaDB, gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).
			Return(nil, fmt.Errorf("execute source query: %w", interfaces.NewSourceReadForbiddenError(errors.New("source database error"))))

		rows, total, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		assert.Nil(t, rows)
		assert.Zero(t, total)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_SourceReadForbidden, httpErr.BaseError.ErrorCode)
		assert.NotContains(t, httpErr.Error(), "source database error")
	})

	t.Run("index connector source permission error becomes HTTP 403", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockIndexConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := newResource(interfaces.ResourceCategoryIndex)

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource.SourceIdentifier, resource, gomock.Any()).
			Return(nil, interfaces.NewSourceReadForbiddenError(errors.New("source database error")))

		_, _, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_SourceReadForbidden, httpErr.BaseError.ErrorCode)
	})

	t.Run("index condition build error becomes HTTP 400", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockIndexConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := newResource(interfaces.ResourceCategoryIndex)
		cause := fmt.Errorf("failed to build filter query: %w", interfaces.NewConditionBuildError("text field body has no keyword feature"))

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource.SourceIdentifier, resource, gomock.Any()).Return(nil, cause)

		rows, total, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		assert.Nil(t, rows)
		assert.Zero(t, total)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.BaseError.ErrorCode)
		assert.Contains(t, httpErr.Error(), "text field body has no keyword feature")
	})

	t.Run("fileset connector source permission error becomes HTTP 403", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockFilesetConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := newResource(interfaces.ResourceCategoryFileset)

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).
			Return(nil, interfaces.NewSourceReadForbiddenError(errors.New("source database error")))

		_, _, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_SourceReadForbidden, httpErr.BaseError.ErrorCode)
	})

	t.Run("raw driver code is not classified by the service", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockTableConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := newResource(interfaces.ResourceCategoryTable)

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypeHANA}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeHANA, gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(nil, codedQueryError{258})

		_, _, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
	})

	t.Run("表分支的 400 不再被上层压成 500", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockTableConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := newResource(interfaces.ResourceCategoryTable)

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
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

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).
			Return(nil, interfaces.NewUnsupportedOperationError("regex", filter_condition.QueryChannelFileset))

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

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
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
