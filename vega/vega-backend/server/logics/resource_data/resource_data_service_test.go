// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"context"
	"encoding/json"
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

func registerMockLogicalViewService(t *testing.T, service interfaces.LogicalViewService) {
	t.Helper()
	previous := resourcelogic.GetLogicalViewService()
	resourcelogic.SetLogicalViewService(service)
	t.Cleanup(func() { resourcelogic.SetLogicalViewService(previous) })
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

func TestResourceDataServiceValidateOutputFieldsParams(t *testing.T) {
	for _, category := range []string{interfaces.ResourceCategoryTable, interfaces.ResourceCategoryIndex, interfaces.ResourceCategoryDataset, interfaces.ResourceCategoryFileset, interfaces.ResourceCategoryLogicalView} {
		t.Run(category, func(t *testing.T) {
			resource := &interfaces.Resource{Category: category, SchemaDefinition: []*interfaces.Property{nil, {Name: "id"}}}
			for _, fields := range [][]string{{"missing"}, {"id", "missing"}, {""}} {
				params := &interfaces.ResourceDataQueryParams{OutputFields: fields}
				before := append([]string(nil), fields...)
				err := (&resourceDataService{}).validateOutputFieldsParams(resource, params)
				var projection *interfaces.OutputFieldError
				require.ErrorAs(t, err, &projection)
				assert.Equal(t, interfaces.OutputFieldUnknown, projection.Reason)
				assert.Equal(t, before, params.OutputFields)
			}
			for _, fields := range [][]string{nil, {}, {"id"}} {
				require.NoError(t, (&resourceDataService{}).validateOutputFieldsParams(resource, &interfaces.ResourceDataQueryParams{OutputFields: fields}))
			}
		})
	}
	t.Run("aggregate result aliases follow the current query", func(t *testing.T) {
		rds := &resourceDataService{}
		resource := &interfaces.Resource{Category: interfaces.ResourceCategoryTable}
		for _, alias := range []string{"", "total"} {
			field := alias
			if field == "" {
				field = "__value"
			}
			params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Aggr: "count", Alias: alias}, OutputFields: []string{field}}
			require.NoError(t, rds.validateOutputFieldsParams(resource, params))
			params.OutputFields = []string{"unrelated_alias"}
			require.Error(t, rds.validateOutputFieldsParams(resource, params))
		}
	})

	t.Run("explicit managed-index projection checks read capability", func(t *testing.T) {
		for _, category := range []string{interfaces.ResourceCategoryTable, interfaces.ResourceCategoryDataset} {
			for _, typ := range []string{interfaces.DataType_Binary, interfaces.DataType_Other} {
				resource := &interfaces.Resource{Category: category, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "value", Type: typ}, {Name: "json", Type: interfaces.DataType_Json}}}
				for _, selected := range [][]string{{"value"}, {"id", "value"}} {
					params := &interfaces.ResourceDataQueryParams{OutputFields: selected}
					var output *interfaces.OutputFieldError
					require.ErrorAs(t, (&resourceDataService{}).validateOutputFieldsParams(resource, params), &output)
					assert.Equal(t, interfaces.OutputFieldUnsupported, output.Reason)
					assert.Equal(t, "value", output.Field)
				}
				for _, selected := range [][]string{nil, {}, {"id"}, {"json"}} {
					require.NoError(t, (&resourceDataService{}).validateOutputFieldsParams(resource, &interfaces.ResourceDataQueryParams{OutputFields: selected}))
				}
				ignore := true
				if category == interfaces.ResourceCategoryTable {
					require.NoError(t, (&resourceDataService{}).validateOutputFieldsParams(resource, &interfaces.ResourceDataQueryParams{OutputFields: []string{"value"}, IgnoreLocalIndex: &ignore}))
				}
			}
		}
	})
	t.Run("aggregate output contains only grouping dimensions and its actual alias", func(t *testing.T) {
		resource := &interfaces.Resource{Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{{Name: "id"}, {Name: "region"}, {Name: "value", Type: interfaces.DataType_Other}}}
		params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "region"}}, Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "total"}}
		for _, name := range []string{"id", "value", "__value", "_score"} {
			params.OutputFields = []string{name}
			require.Error(t, (&resourceDataService{}).validateOutputFieldsParams(resource, params), name)
		}
		for _, fields := range [][]string{nil, {}, {"region"}, {"total"}, {"region", "total"}} {
			params.OutputFields = fields
			require.NoError(t, (&resourceDataService{}).validateOutputFieldsParams(resource, params))
		}
		params.Aggregation = nil
		params.Having = &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 0}
		params.OutputFields = []string{"region", "__value"}
		require.NoError(t, (&resourceDataService{}).validateOutputFieldsParams(resource, params))
		params.Having = nil
		params.OutputFields = []string{"__value"}
		require.Error(t, (&resourceDataService{}).validateOutputFieldsParams(resource, params))
	})

	t.Run("score belongs to the query channel", func(t *testing.T) {
		rds := &resourceDataService{}
		params := &interfaces.ResourceDataQueryParams{OutputFields: []string{"_score"}}
		require.NoError(t, rds.validateOutputFieldsParams(&interfaces.Resource{Category: interfaces.ResourceCategoryIndex}, params))
		require.Error(t, rds.validateOutputFieldsParams(&interfaces.Resource{Category: interfaces.ResourceCategoryTable}, params))
		require.NoError(t, rds.validateOutputFieldsParams(&interfaces.Resource{Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index"}, params))
		ignore := true
		params.IgnoreLocalIndex = &ignore
		require.Error(t, rds.validateOutputFieldsParams(&interfaces.Resource{Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index"}, params))
	})
}

func TestQuerySourcePageInvalidOutputFields(t *testing.T) {
	for _, category := range []string{interfaces.ResourceCategoryTable, interfaces.ResourceCategoryIndex, interfaces.ResourceCategoryDataset, interfaces.ResourceCategoryFileset, interfaces.ResourceCategoryLogicalView} {
		t.Run(category, func(t *testing.T) {
			for _, fields := range [][]string{{"missing"}, {"id", "missing"}} {
				ctrl := gomock.NewController(t)
				catalogs := mock_interfaces.NewMockCatalogService(ctrl)
				catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true}, nil)
				rds := &resourceDataService{cs: catalogs}
				resource := &interfaces.Resource{ID: "resource", CatalogID: "catalog", Category: category, SchemaDefinition: []*interfaces.Property{{Name: "id"}}}
				params := &interfaces.ResourceDataQueryParams{OutputFields: fields}
				rows, total, err := rds.QuerySourcePage(context.Background(), resource, params)
				var httpErr *rest.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
				assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter_OutputField, httpErr.BaseError.ErrorCode)
				assert.Nil(t, rows)
				assert.Zero(t, total)
				assert.Equal(t, fields, params.OutputFields)
			}
		})
	}
}

func TestQuerySourcePageClassifiesOutputFieldErrors(t *testing.T) {
	for _, tc := range []struct {
		reason interfaces.OutputFieldErrorReason
		code   string
		stored bool
	}{
		{interfaces.OutputFieldUnknown, verrors.VegaBackend_Query_InvalidParameter_OutputField, false},
		{interfaces.OutputFieldUnsupported, verrors.VegaBackend_Query_UnsupportedOutputField, false},
		{interfaces.OutputFieldNoneQueryable, verrors.VegaBackend_Query_NoQueryableFields, false},
		{interfaces.OutputFieldUnsupported, verrors.VegaBackend_Resource_InternalError, true},
	} {
		t.Run(string(tc.reason), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			catalogs := mock_interfaces.NewMockCatalogService(ctrl)
			factory := mock_interfaces.NewMockConnectorFactory(ctrl)
			connector := mock_interfaces.NewMockTableConnector(ctrl)
			resource := &interfaces.Resource{ID: "resource", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable}
			catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: interfaces.ConnectorTypeMariaDB}, nil)
			factory.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeMariaDB, gomock.Any()).Return(connector, nil)
			connector.EXPECT().Connect(gomock.Any()).Return(nil)
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			var cause error = interfaces.NewOutputFieldError(tc.reason, "value")
			if tc.stored {
				cause = interfaces.NewStoredConditionBuildError(cause)
			}
			connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(nil, fmt.Errorf("build query: %w", cause))
			rds := &resourceDataService{cs: catalogs, cf: factory}
			rows, total, err := rds.QuerySourcePage(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
			var httpErr *rest.HTTPError
			require.ErrorAs(t, err, &httpErr)
			status := http.StatusBadRequest
			if tc.stored {
				status = http.StatusInternalServerError
			}
			assert.Equal(t, status, httpErr.HTTPCode)
			assert.Equal(t, tc.code, httpErr.BaseError.ErrorCode)
			assert.Nil(t, rows)
			assert.Zero(t, total)
		})
	}
}

func TestQuerySourcePageRejectsUnavailableIndexOutput(t *testing.T) {
	for _, typ := range []string{interfaces.DataType_Binary, interfaces.DataType_Other} {
		t.Run(typ, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			catalogs := mock_interfaces.NewMockCatalogService(ctrl)
			index := mock_interfaces.NewMockLocalIndexManager(ctrl)
			catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true}, nil)
			calls := 0
			index.EXPECT().ListDocuments(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, string, *interfaces.Resource, *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
				calls++
				return []map[string]any{{"id": 1}}, 1, nil
			}).AnyTimes()
			resource := &interfaces.Resource{CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "value", Type: typ}}}
			params := &interfaces.ResourceDataQueryParams{OutputFields: []string{"id", "value"}}
			rows, total, err := (&resourceDataService{cs: catalogs, lim: index}).QuerySourcePage(context.Background(), resource, params)
			var httpErr *rest.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
			assert.Equal(t, verrors.VegaBackend_Query_UnsupportedOutputField, httpErr.BaseError.ErrorCode)
			assert.Nil(t, rows)
			assert.Zero(t, total)
			assert.Zero(t, calls)
			assert.Equal(t, []string{"id", "value"}, params.OutputFields)
		})
	}
}

func TestQuerySourcePageProjectsAggregateOutput(t *testing.T) {
	for _, channel := range []string{"source", "local_index"} {
		t.Run(channel, func(t *testing.T) {
			for _, selected := range [][]string{nil, {}, {"region"}, {"total"}, {"region", "total"}} {
				ctrl := gomock.NewController(t)
				catalogs := mock_interfaces.NewMockCatalogService(ctrl)
				catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: interfaces.ConnectorTypeMariaDB}, nil)
				rds := &resourceDataService{cs: catalogs}
				resource := &interfaces.Resource{CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "region", Type: interfaces.DataType_String}, {Name: "total", Type: interfaces.DataType_Binary}}}
				params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "region"}}, Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "total"}, OutputFields: selected}
				original := []map[string]any{{"region": "east", "total": int64(2)}, {"region": nil, "total": int64(2)}}
				if channel == "local_index" {
					resource.LocalIndexName = "index"
					resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
					index := mock_interfaces.NewMockLocalIndexManager(ctrl)
					rds.lim = index
					index.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).Return(original, int64(42), nil)
				} else {
					factory := mock_interfaces.NewMockConnectorFactory(ctrl)
					connector := mock_interfaces.NewMockTableConnector(ctrl)
					rds.cf = factory
					factory.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeMariaDB, gomock.Any()).Return(connector, nil)
					connector.EXPECT().Connect(gomock.Any()).Return(nil)
					connector.EXPECT().Close(gomock.Any()).Return(nil)
					connector.EXPECT().ExecuteQuery(gomock.Any(), resource, params).Return(&interfaces.QueryResult{Entries: original, Total: 42}, nil)
				}
				rows, total, err := rds.QuerySourcePage(context.Background(), resource, params)
				require.NoError(t, err)
				require.Len(t, rows, 2)
				assert.Equal(t, int64(42), total)
				fields := selected
				if len(fields) == 0 {
					fields = []string{"region", "total"}
				}
				for i, row := range rows {
					assert.Len(t, row, len(fields))
					for _, field := range fields {
						assert.Contains(t, row, field)
						assert.Equal(t, original[i][field], row[field])
					}
				}
				assert.Equal(t, []map[string]any{{"region": "east", "total": int64(2)}, {"region": nil, "total": int64(2)}}, original)
				assert.Equal(t, selected, params.OutputFields)
			}
		})
	}
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

func TestResourceDataServiceQueryWithPagingPreservesLogicalViewQuerySource(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	lvs := mock_interfaces.NewMockLogicalViewService(ctrl)
	registerMockLogicalViewService(t, lvs)
	rds := &resourceDataService{rs: rs}
	view := &interfaces.Resource{ID: "view-1", Category: interfaces.ResourceCategoryLogicalView,
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

func TestResourceDataServiceQueryWithPagingProjectsAggregateOutput(t *testing.T) {
	ctrl := gomock.NewController(t)
	resources := mock_interfaces.NewMockResourceService(ctrl)
	views := mock_interfaces.NewMockLogicalViewService(ctrl)
	registerMockLogicalViewService(t, views)
	rds := &resourceDataService{rs: resources}
	view := &interfaces.Resource{ID: "view", Category: interfaces.ResourceCategoryLogicalView, Enabled: true, SchemaDefinition: []*interfaces.Property{{Name: "id"}, {Name: "region"}}}
	params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "region"}}, Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "total"}, OutputFields: []string{"total"}}
	rows := []map[string]any{{"region": "east", "total": int64(2)}}
	next := "cursor"
	paging := &interfaces.PagingResponse{NextCursor: &next}
	resources.EXPECT().CheckResourcePermission(gomock.Any(), view.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
	views.EXPECT().QueryWithPaging(gomock.Any(), view, params).Return(&interfaces.ResourceDataQueryResult{Entries: rows, TotalCount: 42, QuerySource: interfaces.ResourceQuerySourceLocalIndex, Paging: paging}, nil)
	result, err := rds.QueryWithPaging(context.Background(), view, params)
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"total": int64(2)}}, result.Entries)
	assert.Equal(t, int64(42), result.TotalCount)
	assert.Same(t, paging, result.Paging)
	assert.Equal(t, interfaces.ResourceQuerySourceLocalIndex, result.QuerySource)
	assert.Equal(t, []map[string]any{{"region": "east", "total": int64(2)}}, rows)
}

func TestResourceDataServiceQuery(t *testing.T) {
	t.Run("public table query preserves local index score and cursor position", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		catalogs := mock_interfaces.NewMockCatalogService(ctrl)
		resources := mock_interfaces.NewMockResourceService(ctrl)
		index := mock_interfaces.NewMockLocalIndexManager(ctrl)
		resource := &interfaces.Resource{ID: "table-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable,
			Enabled: true, Status: interfaces.ResourceStatusActive,
			LocalIndexName: "table-index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}}}
		params := &interfaces.ResourceDataQueryParams{NeedTotal: true,
			Paging:       interfaces.PagingRequest{Mode: interfaces.PagingModeSingle, Limit: 2},
			Sort:         []*interfaces.SortField{{Field: "_score", Direction: "desc"}, {Field: "id", Direction: "asc"}},
			OutputFields: []string{"id", "_score"}, SearchAfter: []any{0.875, 1}}
		resources.EXPECT().CheckResourcePermission(gomock.Any(), "table-1", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
		catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{Enabled: true}, nil)
		index.EXPECT().ListDocuments(gomock.Any(), "table-index", resource, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, _ *interfaces.Resource, actual *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
				assert.Equal(t, []*interfaces.SortField{{Field: "_score", Direction: "desc"}, {Field: "id", Direction: "asc"}}, actual.Sort)
				assert.Equal(t, []string{"id", "_score"}, actual.OutputFields)
				assert.Equal(t, []any{0.875, 1}, actual.SearchAfter)
				actual.SearchAfter = []any{0.875, 2}
				return []map[string]any{{"id": 2, "_score": 0.875}}, 3, nil
			})
		result, err := (&resourceDataService{rs: resources, cs: catalogs, lim: index}).QueryWithPaging(context.Background(), resource, params)
		require.NoError(t, err)
		assert.Equal(t, interfaces.ResourceQuerySourceLocalIndex, result.QuerySource)
		assert.Equal(t, []map[string]any{{"id": 2, "_score": 0.875}}, result.Entries)
		assert.Equal(t, int64(3), result.TotalCount)
		assert.Equal(t, []any{0.875, 2}, params.SearchAfter)
	})
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

	t.Run("query logical view filters sort and preserves valid output fields before delegating", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockLVS := mock_interfaces.NewMockLogicalViewService(ctrl)
		registerMockLogicalViewService(t, mockLVS)
		rds := &resourceDataService{cs: mockCS}
		resource := &interfaces.Resource{
			ID:        "logic-view-1",
			Enabled:   true,
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryLogicalView,
			SchemaDefinition: []*interfaces.Property{
				{Name: "name"},
			},
		}
		params := &interfaces.ResourceDataQueryParams{
			Sort: []*interfaces.SortField{
				{Field: "name", Direction: "asc"},
				{Field: "missing", Direction: "desc"},
			},
			OutputFields: []string{"name"},
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

	t.Run("query logical view passes the logical view service HTTP error through", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockLVS := mock_interfaces.NewMockLogicalViewService(ctrl)
		registerMockLogicalViewService(t, mockLVS)
		rds := &resourceDataService{cs: mockCS}
		resource := &interfaces.Resource{
			ID: "logic-view-1", Enabled: true, CatalogID: "catalog-1",
			Category:         interfaces.ResourceCategoryLogicalView,
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
	unsupported := interfaces.NewUnsupportedOperationError("regex", fmt.Errorf("the sql query channel does not support this operation"))

	t.Run("table connector unknown column becomes HTTP 400 without driver details", func(t *testing.T) {
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
			Return(nil, fmt.Errorf("execute source query: %w", interfaces.NewSourceQueryInvalidParameterError(
				interfaces.SourceQueryInvalidParameterUnknownColumn, errors.New("private driver column details"))))

		rows, total, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		assert.Nil(t, rows)
		assert.Zero(t, total)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.BaseError.ErrorCode)
		assert.NotContains(t, httpErr.Error(), "private driver column details")
	})

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
			Return(nil, interfaces.NewUnsupportedOperationError("regex", fmt.Errorf("the fileset query channel does not support this operation")))

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

func TestResourceDataServiceQueryRejectsUnavailableIndexFields(t *testing.T) {
	for _, fieldType := range []string{interfaces.DataType_Json, interfaces.DataType_Binary, interfaces.DataType_Other} {
		for _, stored := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stored=%v", fieldType, stored), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				cs := mock_interfaces.NewMockCatalogService(ctrl)
				lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
				cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{Enabled: true}, nil)
				service := &resourceDataService{cs: cs, lim: lim}
				resource := tableResource("index")
				resource.CatalogID = "catalog-1"
				resource.SchemaDefinition = []*interfaces.Property{{Name: "payload", Type: fieldType}}
				cfg := &interfaces.FilterCondCfg{Name: "payload", Operation: "exist"}
				params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg}
				expected := http.StatusBadRequest
				if stored {
					params.FixedFilterCondCfg = cfg
					expected = http.StatusInternalServerError
				}
				rows, total, err := service.QuerySourcePage(context.Background(), resource, params)
				require.Nil(t, rows)
				assert.Zero(t, total)
				var httpErr *rest.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, expected, httpErr.HTTPCode)
				if !stored {
					assert.Contains(t, httpErr.BaseError.ErrorDetails, "not found")
				}
			})
		}
	}
}

func TestResourceDataServiceQueryDoris(t *testing.T) {
	for _, connectorType := range []string{interfaces.ConnectorTypeDoris, interfaces.ConnectorTypeMariaDB} {
		for _, indexed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s indexed=%v", connectorType, indexed), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				cs := mock_interfaces.NewMockCatalogService(ctrl)
				cf := mock_interfaces.NewMockConnectorFactory(ctrl)
				lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
				service := &resourceDataService{cs: cs, cf: cf, lim: lim}
				resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{{Name: "wide", Type: interfaces.DataType_String, OriginalType: "LARGEINT", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword}}}, {Name: "payload", Type: interfaces.DataType_Json}}}
				if indexed {
					resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
					resource.LocalIndexName = "test-index"
				}
				params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{Name: "wide", Operation: "eq", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: "const", Value: "+00100"}}}
				wantValue := "+00100"
				wantRows := []map[string]any{{"wide": "100", "payload": map[string]any{"id": json.Number("9007199254740993")}}}
				cs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: connectorType}, nil)
				check := func(prepared *interfaces.ResourceDataQueryParams) {
					assert.Equal(t, wantValue, prepared.FilterCondCfg.Value)
					actual, ok := prepared.ActualFilterCond.(*filter_condition.EqualCond)
					require.True(t, ok)
					assert.Equal(t, wantValue, actual.Value)
				}
				if indexed {
					lim.EXPECT().ListDocuments(gomock.Any(), resource.LocalIndexName, resource, gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _ *interfaces.Resource, prepared *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
						check(prepared)
						prepared.SearchAfter = []any{"100"}
						return wantRows, 1, nil
					})
				} else {
					conn := mock_interfaces.NewMockTableConnector(ctrl)
					cf.EXPECT().CreateConnectorInstance(gomock.Any(), connectorType, gomock.Any()).Return(conn, nil)
					conn.EXPECT().Connect(gomock.Any()).Return(nil)
					conn.EXPECT().Close(gomock.Any()).Return(nil)
					conn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).DoAndReturn(func(_ context.Context, _ *interfaces.Resource, prepared *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
						check(prepared)
						return &interfaces.QueryResult{Entries: wantRows, Total: 1}, nil
					})
				}
				rows, total, err := service.QuerySourcePage(context.Background(), resource, params)
				require.NoError(t, err)
				assert.Equal(t, int64(1), total)
				assert.Equal(t, wantRows, rows)
				if connectorType == interfaces.ConnectorTypeDoris {
					assert.Equal(t, "+00100", params.FilterCondCfg.Value)
				}
				if indexed {
					assert.Equal(t, []any{"100"}, params.SearchAfter)
				}
			})
		}
	}
	t.Run("JSON content queries never execute", func(t *testing.T) {
		for _, cfg := range []*interfaces.FilterCondCfg{
			{Name: "payload.id", Operation: "eq", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: "const", Value: "1"}},
		} {
			ctrl := gomock.NewController(t)
			cs := mock_interfaces.NewMockCatalogService(ctrl)
			service := &resourceDataService{cs: cs, cf: mock_interfaces.NewMockConnectorFactory(ctrl), lim: mock_interfaces.NewMockLocalIndexManager(ctrl)}
			resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "test-index", SchemaDefinition: []*interfaces.Property{{Name: "wide", Type: interfaces.DataType_String, OriginalType: "LARGEINT"}, {Name: "payload", Type: interfaces.DataType_Json}}}
			cs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: interfaces.ConnectorTypeDoris}, nil)
			rows, total, err := service.QuerySourcePage(context.Background(), resource, &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg})
			assert.Nil(t, rows)
			assert.Zero(t, total)
			var httpErr *rest.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		}
	})
}

func TestResourceDataServiceQueryJSON(t *testing.T) {
	for _, category := range []string{interfaces.ResourceCategoryTable, interfaces.ResourceCategoryDataset} {
		for _, stored := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s stored=%v", category, stored), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				cs := mock_interfaces.NewMockCatalogService(ctrl)
				service := &resourceDataService{cs: cs, cf: mock_interfaces.NewMockConnectorFactory(ctrl), lim: mock_interfaces.NewMockLocalIndexManager(ctrl), ds: mock_interfaces.NewMockDatasetService(ctrl)}
				resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Category: category, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "test-index", SchemaDefinition: []*interfaces.Property{{Name: "payload", Type: interfaces.DataType_Json}}}
				cs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: interfaces.ConnectorTypePostgreSQL}, nil)
				cfg := &interfaces.FilterCondCfg{Name: "payload.id", Operation: "eq", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: "const", Value: "1"}}
				params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg}
				wantStatus := http.StatusBadRequest
				if stored {
					params.FixedFilterCondCfg = cfg
					wantStatus = http.StatusInternalServerError
				}
				rows, total, err := service.QuerySourcePage(context.Background(), resource, params)
				assert.Nil(t, rows)
				assert.Zero(t, total)
				var httpErr *rest.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, wantStatus, httpErr.HTTPCode)
			})
		}
	}
}
