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
	"math"
	"net/http"
	"slices"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/index/opensearch"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
)

// These fixtures exercise query orchestration, not connector schema projection.
// Cases that verify projected capabilities provide explicit manager responses.
func newResourceDataLocalIndexManagerMock(ctrl *gomock.Controller) *mock_interfaces.MockLocalIndexManager {
	manager := mock_interfaces.NewMockLocalIndexManager(ctrl)
	manager.EXPECT().SchemaForQuery(gomock.Any()).DoAndReturn(func(schema []*interfaces.Property) ([]*interfaces.Property, error) {
		projected := make([]*interfaces.Property, 0, len(schema))
		for _, source := range schema {
			if source == nil {
				continue
			}
			if source.Type == interfaces.DataType_Binary || source.Type == interfaces.DataType_Json || source.Type == interfaces.DataType_Other {
				continue
			}
			field := *source
			field.OriginalName = source.Name
			field.Filterable = new(true)
			if field.Type == interfaces.DataType_LargeInteger {
				field.Type = interfaces.DataType_String
			}
			projected = append(projected, &field)
		}
		for _, field := range resourceDataGeneratedFieldsFixture(schema) {
			projected = append(projected, field)
		}
		return projected, nil
	}).AnyTimes()
	manager.EXPECT().GeneratedFields(gomock.Any()).DoAndReturn(func(schema []*interfaces.Property) (map[string]*interfaces.Property, error) {
		return resourceDataGeneratedFieldsFixture(schema), nil
	}).AnyTimes()
	return manager
}

// resourceDataGeneratedFieldsFixture 为查询编排测试提供显式的生成字段元数据。
func resourceDataGeneratedFieldsFixture(schema []*interfaces.Property) map[string]*interfaces.Property {
	generated := map[string]*interfaces.Property{}
	for _, source := range schema {
		if source == nil {
			continue
		}
		for _, feature := range source.Features {
			if feature.FeatureType == interfaces.PropertyFeatureType_Vector && feature.RefProperty == "" && (source.Type == interfaces.DataType_String || source.Type == interfaces.DataType_Text) {
				name := source.Name + "_vector"
				generated[name] = &interfaces.Property{Name: name, Type: interfaces.DataType_Vector, Filterable: new(true), Features: []interfaces.PropertyFeature{feature}}
			}
		}
	}
	return generated
}

func registerMockLogicalViewService(t *testing.T, service interfaces.LogicalViewService) {
	t.Helper()
	previous := resourcelogic.GetLogicalViewService()
	resourcelogic.SetLogicalViewService(service)
	t.Cleanup(func() { resourcelogic.SetLogicalViewService(previous) })
}

func TestMapQueryError(t *testing.T) {
	for _, invalidCode := range []string{verrors.VegaBackend_Resource_InvalidParameter, verrors.VegaBackend_Query_InvalidParameter} {
		t.Run(invalidCode, func(t *testing.T) {
			for _, tc := range []struct {
				name     string
				cause    error
				code     string
				status   int
				metadata map[string]any
			}{
				{name: "source read forbidden", cause: interfaces.NewSourceReadForbiddenError(errors.New("private driver diagnostic")), status: http.StatusForbidden, code: verrors.VegaBackend_Resource_SourceReadForbidden},
				{name: "source permission precedes stored condition provenance", cause: interfaces.NewStoredConditionBuildError(interfaces.NewSourceReadForbiddenError(errors.New("private driver diagnostic"))), status: http.StatusForbidden, code: verrors.VegaBackend_Resource_SourceReadForbidden},
				{name: "execution budget", cause: interfaces.NewQueryExecutionBudgetError("row_scan", errors.New("private driver diagnostic")), status: http.StatusUnprocessableEntity, code: verrors.VegaBackend_Query_ExecutionBudgetExceeded, metadata: map[string]any{"budget": "row_scan"}},
				{name: "stored execution budget", cause: interfaces.NewStoredConditionBuildError(interfaces.NewQueryExecutionBudgetError("row_scan", errors.New("private driver diagnostic"))), status: http.StatusUnprocessableEntity, code: verrors.VegaBackend_Query_ExecutionBudgetExceeded, metadata: map[string]any{"budget": "row_scan"}},
				{name: "time range becomes HTTP 400", cause: interfaces.NewTemporalRangeError("created", "date_nanos", 2147483647, "day"), code: verrors.VegaBackend_Resource_QueryTimeOutOfRange, metadata: map[string]any{"field": "created", "field_type": "date_nanos", "interval": int64(2147483647), "unit": "day"}},
				{name: "stored time range becomes HTTP 400", cause: interfaces.NewStoredConditionBuildError(interfaces.NewTemporalRangeError("created", "date_nanos", 2147483647, "day")), code: verrors.VegaBackend_Resource_QueryTimeOutOfRange, metadata: map[string]any{"field": "created", "field_type": "date_nanos", "interval": int64(2147483647), "unit": "day"}},
				{name: "keyword limit", cause: interfaces.NewKeywordLimitError("status", 256), code: verrors.VegaBackend_Resource_QueryValueExceedsKeywordLimit,
					metadata: map[string]any{"field": "status", "limit": 256}},
				{name: "unknown output", cause: interfaces.NewOutputFieldError(interfaces.OutputFieldUnknown, "missing"), code: verrors.VegaBackend_Query_InvalidParameter_OutputField},
				{name: "unsupported output", cause: interfaces.NewOutputFieldError(interfaces.OutputFieldUnsupported, "blob"), code: verrors.VegaBackend_Query_UnsupportedOutputField},
				{name: "no queryable output", cause: interfaces.NewOutputFieldError(interfaces.OutputFieldNoneQueryable, ""), code: verrors.VegaBackend_Query_NoQueryableFields},
				{name: "generic condition", cause: interfaces.NewConditionBuildError("invalid condition"), code: invalidCode},
				{name: "unsupported operation", cause: interfaces.NewUnsupportedOperationError("regex", errors.New("unsupported by this channel")), code: invalidCode},
				{name: "source query input", cause: interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterUnknownColumn, errors.New("private driver diagnostic")), code: invalidCode},
				{name: "stored generic condition", cause: interfaces.NewStoredConditionBuildError(interfaces.NewConditionBuildError("invalid saved condition"))},
				{name: "stored keyword", cause: interfaces.NewStoredConditionBuildError(interfaces.NewKeywordLimitError("status", 256))},
				{name: "stored output", cause: interfaces.NewStoredConditionBuildError(interfaces.NewOutputFieldError(interfaces.OutputFieldUnknown, "missing"))},
				{name: "already mapped", cause: httperrors.NewHTTPError(t.Context(), http.StatusConflict, verrors.VegaBackend_Catalog_IsDisabled), status: http.StatusConflict, code: verrors.VegaBackend_Catalog_IsDisabled},
				{name: "unknown failure", cause: errors.New("private driver diagnostic")},
			} {
				t.Run(tc.name, func(t *testing.T) {
					err := fmt.Errorf("query execution wrapper: %w", tc.cause)
					mapped := MapQueryError(t.Context(), err, invalidCode)
					if tc.code == "" {
						assert.Nil(t, mapped)
						return
					}
					var httpErr *httperrors.HTTPError
					require.ErrorAs(t, mapped, &httpErr)
					status := tc.status
					if status == 0 {
						status = http.StatusBadRequest
					}
					assert.Equal(t, status, httpErr.HTTPCode)
					assert.Equal(t, tc.code, httpErr.ErrorCode)
					assert.Equal(t, tc.metadata, httpErr.Metadata)
					assert.NotContains(t, httpErr.Error(), "private driver diagnostic")
					assert.NotContains(t, httpErr.Error(), "query execution wrapper")
				})
			}
		})
	}
}

func TestQuerySourcePageVectorConditionErrorProvenance(t *testing.T) {
	for _, tc := range []struct {
		name             string
		stored           bool
		modelFailure     bool
		embeddingFailure bool
		statusCode       int
	}{
		{name: "stored condition", stored: true, statusCode: http.StatusInternalServerError},
		{name: "request condition", statusCode: http.StatusInternalServerError},
		{name: "model lookup fails", modelFailure: true, statusCode: http.StatusInternalServerError},
		{name: "embedding service fails", embeddingFailure: true, statusCode: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCS := mock_interfaces.NewMockCatalogService(ctrl)
			mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
			mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
				Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
			switch {
			case tc.modelFailure:
				mockMFS.EXPECT().GetModelByID(gomock.Any(), "removed-model").Return(nil, errors.New("private model service failure"))
			case tc.embeddingFailure:
				model := &interfaces.SmallModel{ModelID: "removed-model"}
				mockMFS.EXPECT().GetModelByID(gomock.Any(), "removed-model").Return(model, nil)
				mockMFS.EXPECT().GetVector(gomock.Any(), model, []string{"query text"}).Return(nil, errors.New("private embedding service failure"))
			default:
				mockMFS.EXPECT().GetModelByID(gomock.Any(), "removed-model").Return(nil, nil)
			}
			rds := &resourceDataService{cs: mockCS, mfs: mockMFS, lim: newResourceDataLocalIndexManagerMock(ctrl)}
			resource := &interfaces.Resource{
				ID: "source", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable,
				LocalIndexName: "source-index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
				IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "removed-model"},
				SchemaDefinition: []*interfaces.Property{{Name: "embedding", Type: interfaces.DataType_Vector,
					Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}}}},
			}
			condition := &interfaces.FilterCondCfg{Field: "embedding", Operation: filter_condition.OperationKnnVector,
				ValueFrom: interfaces.ValueFrom_Const, Value: "query text"}
			params := &interfaces.ResourceDataQueryParams{FilterCondCfg: condition}
			if tc.stored {
				params.FixedFilterCondCfg = condition
			}
			rowsResult, err := rds.QuerySourcePage(context.Background(), resource, params)
			var rows []map[string]any
			var total int64
			if rowsResult != nil {
				rows = rowsResult.Entries
				total = rowsResult.TotalCount
			}
			assert.Nil(t, rows)
			assert.Zero(t, total)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tc.statusCode, httpErr.HTTPCode)
			assert.NotContains(t, httpErr.ErrorDetails, "private")
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

	t.Run("managed-index unavailable fields remain valid output fields", func(t *testing.T) {
		for _, category := range []string{interfaces.ResourceCategoryTable, interfaces.ResourceCategoryDataset} {
			for _, typ := range []string{interfaces.DataType_Binary, interfaces.DataType_Other} {
				resource := &interfaces.Resource{Category: category, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "value", Type: typ}, {Name: "json", Type: interfaces.DataType_Json}}}
				for _, selected := range [][]string{{"value"}, {"id", "value"}} {
					params := &interfaces.ResourceDataQueryParams{OutputFields: selected}
					require.NoError(t, (&resourceDataService{}).validateOutputFieldsParams(resource, params))
					assert.Equal(t, selected, params.OutputFields)
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

func TestQuerySourcePageSearchPrerequisitesPrecedeOutputFields(t *testing.T) {
	for _, operation := range []string{filter_condition.OperationMatch, filter_condition.OperationMatchPhrase, filter_condition.OperationMultiMatch, filter_condition.OperationKnnVector} {
		for _, ignore := range []bool{false, true} {
			for _, nested := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/ignore_index=%t/nested=%t", operation, ignore, nested), func(t *testing.T) {
					ctrl := gomock.NewController(t)
					catalogs := mock_interfaces.NewMockCatalogService(ctrl)
					catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true}, nil)
					resource := &interfaces.Resource{ID: "resource", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "title", Type: interfaces.DataType_Text}}}
					if ignore {
						resource.LocalIndexName = "index"
						resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
					}
					condition := &interfaces.FilterCondCfg{Field: "title", Operation: operation, Value: "search text"}
					if nested {
						condition = &interfaces.FilterCondCfg{Operation: "and", SubConds: []*interfaces.FilterCondCfg{condition}}
					}
					params := &interfaces.ResourceDataQueryParams{IgnoreLocalIndex: &ignore, OutputFields: []string{"id", "_score"}, FilterCondCfg: condition}
					models := mock_interfaces.NewMockModelFactoryService(ctrl)
					rowsResult, err := (&resourceDataService{cs: catalogs, mfs: models, lim: newResourceDataLocalIndexManagerMock(ctrl)}).QuerySourcePage(context.Background(), resource, params)
					var rows []map[string]any
					var total int64
					if rowsResult != nil {
						rows = rowsResult.Entries
						total = rowsResult.TotalCount
					}
					var httpErr *httperrors.HTTPError
					require.ErrorAs(t, err, &httpErr)
					assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
					assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.ErrorCode)
					if ignore {
						assert.Contains(t, httpErr.ErrorDetails, "ignore_local_index")
					} else if operation == filter_condition.OperationKnnVector {
						assert.Contains(t, httpErr.ErrorDetails, "build one before vector search")
					} else {
						assert.Contains(t, httpErr.ErrorDetails, "build one before full-text search")
					}
					assert.NotContains(t, httpErr.ErrorDetails, "unknown output field")
					assert.Nil(t, rows)
					assert.Zero(t, total)
				})
			}
		}
	}
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
				page, err := rds.QuerySourcePage(context.Background(), resource, params)
				var rows []map[string]any
				var total int64
				if page != nil {
					rows = page.Entries
					total = page.TotalCount
				}
				var output *interfaces.OutputFieldError
				require.ErrorAs(t, err, &output)
				assert.Equal(t, interfaces.OutputFieldUnknown, output.Reason)
				assert.Equal(t, "missing", output.Field)
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
		{interfaces.OutputFieldUnsupported, "", true},
	} {
		t.Run(string(tc.reason), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			catalogs := mock_interfaces.NewMockCatalogService(ctrl)
			factory := mock_interfaces.NewMockConnectorFactory(ctrl)
			connector := mock_interfaces.NewMockTableConnector(ctrl)
			resource := &interfaces.Resource{ID: "resource", Enabled: true, CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{{Name: "value", Type: interfaces.DataType_String}}}
			catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: "mariadb"}, nil)
			factory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).Return(connector, nil)
			connector.EXPECT().Connect(gomock.Any()).Return(nil)
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			var cause error = interfaces.NewOutputFieldError(tc.reason, "value")
			if tc.stored {
				cause = interfaces.NewStoredConditionBuildError(cause)
			}
			connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(nil, fmt.Errorf("build query: %w", cause))
			rds := &resourceDataService{cs: catalogs, cf: factory}
			if tc.stored {
				page, err := rds.QuerySourcePage(t.Context(), resource, &interfaces.ResourceDataQueryParams{})
				var rows []map[string]any
				var total int64
				if page != nil {
					rows = page.Entries
					total = page.TotalCount
				}
				var stored *interfaces.StoredConditionBuildError
				require.ErrorAs(t, err, &stored)
				require.ErrorIs(t, err, cause)
				assert.Nil(t, rows)
				assert.Zero(t, total)
			} else {
				result, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, &interfaces.ResourceDataQueryParams{})
				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
				assert.Equal(t, tc.code, httpErr.ErrorCode)
				assert.Nil(t, result)
			}
		})
	}
	t.Run("index source preserves wrapped stored condition failures", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		catalogs := mock_interfaces.NewMockCatalogService(ctrl)
		factory := mock_interfaces.NewMockConnectorFactory(ctrl)
		connector := mock_interfaces.NewMockIndexConnector(ctrl)
		resource := &interfaces.Resource{ID: "resource", CatalogID: "catalog", Category: interfaces.ResourceCategoryIndex,
			SourceIdentifier: "physical-index",
			SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text}}}
		fixed := &interfaces.FilterCondCfg{Field: "title", Operation: filter_condition.OperationEqual,
			ValueFrom: interfaces.ValueFrom_Const, Value: "example"}
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: fixed, FixedFilterCondCfg: fixed}
		catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).
			Return(&interfaces.Catalog{Enabled: true, ConnectorType: "opensearch"}, nil)
		factory.EXPECT().CreateConnectorInstance(gomock.Any(), "opensearch", gomock.Any()).Return(connector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		cause := interfaces.NewConditionBuildError("text field has no keyword feature")
		storedCause := interfaces.NewStoredConditionBuildError(cause)
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource.SourceIdentifier, resource, params).
			Return(nil, fmt.Errorf("compile index query: %w", storedCause))
		service := &resourceDataService{cs: catalogs, cf: factory}

		rowsResult5, err := service.QuerySourcePage(context.Background(), resource, params)
		var rows []map[string]any
		var total int64
		if rowsResult5 != nil {
			rows = rowsResult5.Entries
			total = rowsResult5.TotalCount
		}

		assert.Nil(t, rows)
		assert.Zero(t, total)
		var stored *interfaces.StoredConditionBuildError
		require.ErrorAs(t, err, &stored)
		assert.Same(t, storedCause, stored)
		assert.ErrorIs(t, err, cause)
	})

}

func TestQuerySourcePagePreservesUnavailableIndexOutput(t *testing.T) {
	for _, typ := range []string{interfaces.DataType_Binary, interfaces.DataType_Other} {
		for _, selected := range [][]string{nil, {}, {"value"}, {"id", "value"}, {"id"}} {
			t.Run(fmt.Sprintf("%s/output=%v", typ, selected), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				catalogs := mock_interfaces.NewMockCatalogService(ctrl)
				index := newResourceDataLocalIndexManagerMock(ctrl)
				catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true}, nil)
				resource := &interfaces.Resource{CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "value", Type: typ}}}
				params := &interfaces.ResourceDataQueryParams{OutputFields: selected}
				stored := map[string]any{}
				if len(selected) == 0 || slices.Contains(selected, "id") {
					stored["id"] = 1
				}
				index.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).Return(&interfaces.ResourceDataQueryResult{Entries: []map[string]any{stored}, TotalCount: int64(1)}, nil)
				rowsResult, err := (&resourceDataService{cs: catalogs, lim: index}).QuerySourcePage(context.Background(), resource, params)
				var rows []map[string]any
				var total int64
				if rowsResult != nil {
					rows = rowsResult.Entries
					total = rowsResult.TotalCount
				}
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Equal(t, int64(1), total)
				if len(selected) == 0 || slices.Contains(selected, "value") {
					assert.Equal(t, interfaces.ResourceValue{Mode: interfaces.ResourceValueModeUnavailable}, rows[0]["value"])
				} else {
					assert.NotContains(t, rows[0], "value")
				}
				assert.Equal(t, selected, params.OutputFields)
			})
		}
	}
}

func TestQuerySourcePageProjectsAggregateOutput(t *testing.T) {
	for _, channel := range []string{"source", "local_index"} {
		t.Run(channel, func(t *testing.T) {
			for _, selected := range [][]string{nil, {}, {"region"}, {"total"}, {"region", "total"}} {
				ctrl := gomock.NewController(t)
				catalogs := mock_interfaces.NewMockCatalogService(ctrl)
				catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: "mariadb"}, nil)
				rds := &resourceDataService{cs: catalogs}
				resource := &interfaces.Resource{CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "region", Type: interfaces.DataType_String}, {Name: "total", Type: interfaces.DataType_Binary}}}
				params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "region"}}, Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "total"}, OutputFields: selected}
				original := []map[string]any{{"region": "east", "total": int64(2)}, {"region": nil, "total": int64(2)}}
				if channel == "local_index" {
					resource.LocalIndexName = "index"
					resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
					index := newResourceDataLocalIndexManagerMock(ctrl)
					rds.lim = index
					index.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).Return(&interfaces.ResourceDataQueryResult{Entries: original, TotalCount: int64(42)}, nil)
				} else {
					factory := mock_interfaces.NewMockConnectorFactory(ctrl)
					connector := mock_interfaces.NewMockTableConnector(ctrl)
					rds.cf = factory
					factory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).Return(connector, nil)
					connector.EXPECT().Connect(gomock.Any()).Return(nil)
					connector.EXPECT().Close(gomock.Any()).Return(nil)
					connector.EXPECT().ExecuteQuery(gomock.Any(), resource, params).Return(&interfaces.ResourceDataQueryResult{Entries: original, TotalCount: 42}, nil)
				}
				rowsResult, err := rds.QuerySourcePage(context.Background(), resource, params)
				var rows []map[string]any
				var total int64
				if rowsResult != nil {
					rows = rowsResult.Entries
					total = rowsResult.TotalCount
				}
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

			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
			assert.Equal(t, verrors.VegaBackend_Resource_MetadataUnavailable, httpErr.ErrorCode)
			assert.Equal(t, test.wantDetails, httpErr.ErrorDetails)
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
	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.NotContains(t, httpErr.ErrorDetails, resource.StatusMessage)
	assert.NotContains(t, httpErr.ErrorDetails, "syntax error at or near LATERAL")
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

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_MetadataUnavailable, httpErr.ErrorCode)
	}
}

func TestResourceDataServiceQueryWithPagingRequiresQueryDataPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	rds := &resourceDataService{rs: rs}
	denied := httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)
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
	t.Run("非法逻辑字段向量在模型和索引调用前拒绝", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			value any
		}{
			{"number", float64(42)},
			{"boolean", true},
			{"object", map[string]any{"x": 1}},
			{"string elements", []any{"0.1", "0.2"}},
		} {
			for _, nested := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/nested=%v", tc.name, nested), func(t *testing.T) {
					ctrl := gomock.NewController(t)
					catalogs := mock_interfaces.NewMockCatalogService(ctrl)
					models := mock_interfaces.NewMockModelFactoryService(ctrl)
					models.EXPECT().GetModelByID(gomock.Any(), gomock.Any()).Times(0)
					models.EXPECT().GetVector(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
					indexes := newResourceDataLocalIndexManagerMock(ctrl)
					resource := &interfaces.Resource{ID: "resource", Enabled: true, Status: interfaces.ResourceStatusActive, CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
						SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}}}}}
					cfg := &interfaces.FilterCondCfg{Field: "title", Operation: filter_condition.OperationKnnVector, ValueFrom: interfaces.ValueFrom_Const, Value: tc.value}
					if nested {
						cfg = &interfaces.FilterCondCfg{Operation: filter_condition.OperationAnd, SubConds: []*interfaces.FilterCondCfg{cfg}}
					}
					params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg}
					catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true}, nil)
					called := false
					indexes.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).DoAndReturn(func(context.Context, string, *interfaces.Resource, *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
						called = true
						return nil, errors.New("invalid backend vector payload")
					}).AnyTimes()
					result, err := (&resourceDataService{cs: catalogs, lim: indexes, mfs: models}).QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, params)
					var httpErr *httperrors.HTTPError
					require.ErrorAs(t, err, &httpErr)
					assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
					assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.ErrorCode)
					assert.Nil(t, result)
					assert.False(t, called, "invalid vector queries must not reach the index backend")
				})
			}
		}
	})
	t.Run("模型非法数值向量保留服务端错误且不调用索引", func(t *testing.T) {
		for _, vector := range [][]float32{nil, {}, {0.1, float32(math.NaN())}, {0.1, float32(math.Inf(1))}} {
			t.Run(fmt.Sprint(vector), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				catalogs := mock_interfaces.NewMockCatalogService(ctrl)
				indexes := newResourceDataLocalIndexManagerMock(ctrl)
				models := mock_interfaces.NewMockModelFactoryService(ctrl)
				model := &interfaces.SmallModel{ModelID: "model"}
				resource := &interfaces.Resource{ID: "resource", Enabled: true, Status: interfaces.ResourceStatusActive, CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
					IndexConfig:      &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"},
					SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}}}}}
				cfg := &interfaces.FilterCondCfg{Field: "title", Operation: filter_condition.OperationKnnVector, ValueFrom: interfaces.ValueFrom_Const, Value: "query text"}
				catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true}, nil)
				models.EXPECT().GetModelByID(gomock.Any(), "model").Return(model, nil)
				models.EXPECT().GetVector(gomock.Any(), model, []string{"query text"}).Return([]*interfaces.VectorResp{{Vector: vector}}, nil)
				called := false
				indexes.EXPECT().ListDocuments(gomock.Any(), "index", resource, gomock.Any()).DoAndReturn(func(context.Context, string, *interfaces.Resource, *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
					called = true
					return nil, errors.New("invalid backend vector payload")
				}).AnyTimes()
				result, err := (&resourceDataService{cs: catalogs, lim: indexes, mfs: models}).QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg})
				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
				assert.Nil(t, result)
				assert.False(t, called)
				assert.Equal(t, "title", cfg.Field)
				assert.Equal(t, "query text", cfg.Value)
			})
		}
	})
	t.Run("向量解析与续页复用执行树并分别保留固定和发布全文范围", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
		catalogs := mock_interfaces.NewMockCatalogService(ctrl)
		models := mock_interfaces.NewMockModelFactoryService(ctrl)
		feature := interfaces.PropertyFeature{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}
		content := &interfaces.Property{Name: "content", Type: interfaces.DataType_Text, Filterable: new(false), Features: []interfaces.PropertyFeature{feature}}
		hidden := &interfaces.Property{Name: "hidden", Type: interfaces.DataType_Text, Filterable: new(false)}
		vector := &interfaces.Property{Name: "content_vector", Type: interfaces.DataType_Vector, Filterable: new(true), Features: []interfaces.PropertyFeature{feature}}
		resource := &interfaces.Resource{ID: "table", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
			Enabled: true, Status: interfaces.ResourceStatusActive, LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			SchemaDefinition: []*interfaces.Property{content, hidden}, IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}}
		queryFields := map[string]*interfaces.Property{"content": content, "hidden": hidden, "content_vector": vector}
		requestWildcard := &interfaces.FilterCondCfg{Operation: "match", Value: "query", RemainCfg: map[string]any{"fields": []any{"*"}}}
		request := &interfaces.FilterCondCfg{Field: "content", Operation: "knn_vector", Value: "search", SubConds: []*interfaces.FilterCondCfg{requestWildcard}}
		requestActual, err := filter_condition.NewFilterCondition(t.Context(), request, queryFields)
		require.NoError(t, err)
		require.NoError(t, filter_condition.ResolveFulltextWildcardFields(requestActual, []string{"content"}))
		fixed := &interfaces.FilterCondCfg{Operation: "match_phrase", Value: "stored", RemainCfg: map[string]any{"fields": []any{"*"}}}
		fixedActual, err := filter_condition.NewFilterCondition(t.Context(), fixed, queryFields)
		require.NoError(t, err)
		merged := &interfaces.FilterCondCfg{Operation: "and", SubConds: []*interfaces.FilterCondCfg{fixed, request}}
		prepared := &filter_condition.AndCond{Cfg: merged, SubConds: []interfaces.FilterCondition{fixedActual, requestActual}}
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: merged, FixedFilterCondCfg: fixed,
			ActualFilterCond: prepared, FixedActualFilterCond: fixedActual}
		indexes.EXPECT().SchemaForQuery(resource.SchemaDefinition).Return([]*interfaces.Property{content, hidden, vector}, nil).Times(2)
		indexes.EXPECT().GeneratedFields(resource.SchemaDefinition).Return(map[string]*interfaces.Property{"content_vector": vector}, nil)
		catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil).Times(2)
		model := &interfaces.SmallModel{ModelID: "model"}
		models.EXPECT().GetModelByID(gomock.Any(), "model").Return(model, nil)
		models.EXPECT().GetVector(gomock.Any(), model, []string{"search"}).Return([]*interfaces.VectorResp{{Vector: []float32{0.1, 0.2}}}, nil)
		indexes.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).DoAndReturn(
			func(ctx context.Context, _ string, _ *interfaces.Resource, query *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				assert.Same(t, prepared, query.ActualFilterCond)
				assert.Same(t, fixedActual, query.FixedActualFilterCond)
				knn := requestActual.(*filter_condition.KnnVectorCond)
				assert.Same(t, request, knn.Cfg)
				assert.Equal(t, "content_vector", knn.FilterFieldName)
				assert.Equal(t, []float32{0.1, 0.2}, knn.Cfg.Value)
				assert.Equal(t, []*interfaces.Property{content}, knn.SubConds[0].(*filter_condition.MatchCond).Fields)
				assert.Contains(t, fixedActual.(*filter_condition.MatchPhraseCond).Fields, hidden)
				dsl, err := (&opensearch.OpenSearchConnector{}).ConvertFilterCondition(knn, []*interfaces.Property{content, hidden, vector})
				require.NoError(t, err)
				encoded, err := json.Marshal(dsl)
				require.NoError(t, err)
				assert.Contains(t, string(encoded), "content_vector")
				assert.NotContains(t, string(encoded), "hidden")
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"content": "row"}}}, nil
			}).Times(2)
		service := &resourceDataService{cs: catalogs, lim: indexes, mfs: models}
		for range 2 {
			result, err := service.QuerySourcePage(t.Context(), resource, params)
			require.NoError(t, err)
			assert.Equal(t, []map[string]any{{"content": "row"}}, result.Entries)
		}
		assert.Equal(t, []any{"*"}, requestWildcard.RemainCfg["fields"])
		assert.Equal(t, []any{"*"}, fixed.RemainCfg["fields"])
		assert.False(t, *content.Filterable)
		assert.False(t, *hidden.Filterable)
	})
	t.Run("执行已绑定的全文字段范围而不重新展开原始通配", func(t *testing.T) {
		for _, operation := range []string{"match", "match_phrase", "multi_match"} {
			t.Run(operation, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
				catalogs := mock_interfaces.NewMockCatalogService(ctrl)
				resources := mock_interfaces.NewMockResourceService(ctrl)
				published := &interfaces.Property{Name: "published", Type: interfaces.DataType_Text, Filterable: new(false)}
				hidden := &interfaces.Property{Name: "hidden", Type: interfaces.DataType_Text, Filterable: new(false)}
				resource := &interfaces.Resource{ID: "table", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
					Enabled: true, Status: interfaces.ResourceStatusActive, LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
					SchemaDefinition: []*interfaces.Property{published, hidden}}
				cfg := &interfaces.FilterCondCfg{Operation: operation, Value: "query", RemainCfg: map[string]any{"fields": []any{"*"}}}
				var prepared interfaces.FilterCondition
				switch operation {
				case "match":
					prepared = &filter_condition.MatchCond{Cfg: cfg, Fields: []*interfaces.Property{published}}
				case "match_phrase":
					prepared = &filter_condition.MatchPhraseCond{Cfg: cfg, Fields: []*interfaces.Property{published}}
				case "multi_match":
					prepared = &filter_condition.MultiMatchCond{Cfg: cfg, Fields: []*interfaces.Property{published}}
				}
				params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg, ActualFilterCond: prepared}
				indexes.EXPECT().SchemaForQuery(resource.SchemaDefinition).Return(resource.SchemaDefinition, nil)
				resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
				catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
				indexes.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).DoAndReturn(
					func(ctx context.Context, _ string, _ *interfaces.Resource, query *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
						assert.Same(t, prepared, query.ActualFilterCond)
						dsl, err := (&opensearch.OpenSearchConnector{}).ConvertFilterCondition(query.ActualFilterCond, resource.SchemaDefinition)
						require.NoError(t, err)
						encoded, err := json.Marshal(dsl)
						require.NoError(t, err)
						assert.Contains(t, string(encoded), "published")
						assert.NotContains(t, string(encoded), "hidden")
						return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"published": "row"}}}, nil
					})
				result, err := (&resourceDataService{rs: resources, cs: catalogs, lim: indexes}).QueryWithPaging(t.Context(), resource, params)
				require.NoError(t, err)
				assert.Equal(t, []map[string]any{{"published": "row"}}, result.Entries)
				assert.Equal(t, []any{"*"}, cfg.RemainCfg["fields"])
			})
		}
	})
	t.Run("不完整 LIKE 转义在查询下游前拒绝", func(t *testing.T) {
		for _, operation := range []string{"like", "not_like"} {
			for _, fixed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/fixed=%v", operation, fixed), func(t *testing.T) {
					ctrl := gomock.NewController(t)
					catalogs := mock_interfaces.NewMockCatalogService(ctrl)
					resources := mock_interfaces.NewMockResourceService(ctrl)
					connectors := mock_interfaces.NewMockConnectorFactory(ctrl)
					indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
					resource := &interfaces.Resource{ID: "native-index", CatalogID: "catalog", Category: interfaces.ResourceCategoryIndex,
						Enabled: true, Status: interfaces.ResourceStatusActive,
						SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_String, Filterable: new(true)}}}
					cfg := &interfaces.FilterCondCfg{Field: "title", Operation: operation, Value: `trailing\`}
					params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg}
					if fixed {
						params.FixedFilterCondCfg = cfg
					}
					resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
					catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
					result, err := (&resourceDataService{rs: resources, cs: catalogs, cf: connectors, lim: indexes}).QueryWithPaging(t.Context(), resource, params)
					var httpErr *httperrors.HTTPError
					require.ErrorAs(t, err, &httpErr)
					if fixed {
						assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
					} else {
						assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
						assert.Contains(t, httpErr.ErrorDetails, "escape")
					}
					assert.Nil(t, result)
					assert.Nil(t, params.ActualFilterCond)
					assert.Nil(t, params.FixedActualFilterCond)
					assert.Equal(t, `trailing\`, cfg.Value)
				})
			}
		}
	})
	t.Run("已有执行条件不绕过原始条件的开关校验", func(t *testing.T) {
		for _, operation := range []string{"match", "match_phrase", "multi_match"} {
			for _, scenario := range []string{"expanded", "explicit", "sibling", "rhs", "fixed", "nonfulltext"} {
				t.Run(operation+"/"+scenario, func(t *testing.T) {
					ctrl := gomock.NewController(t)
					indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
					catalogs := mock_interfaces.NewMockCatalogService(ctrl)
					resources := mock_interfaces.NewMockResourceService(ctrl)
					field := &interfaces.Property{Name: "title", OriginalName: "source_title", Type: interfaces.DataType_Text, Filterable: new(false)}
					projected := *field
					projected.OriginalName = field.Name
					resource := &interfaces.Resource{ID: "table", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
						Enabled: true, Status: interfaces.ResourceStatusActive, LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
						SchemaDefinition: []*interfaces.Property{field}}
					cfg := &interfaces.FilterCondCfg{Operation: operation, Value: "query",
						RemainCfg: map[string]any{"fields": []any{"*"}}}
					prepared, err := filter_condition.NewFilterCondition(t.Context(), cfg, map[string]*interfaces.Property{"title": &projected})
					require.NoError(t, err)
					params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg, ActualFilterCond: prepared}
					explicit := &interfaces.FilterCondCfg{Field: "title", Operation: "eq", Value: "query"}
					switch scenario {
					case "explicit":
						cfg.RemainCfg["fields"] = []any{"title"}
					case "sibling":
						params.FilterCondCfg = &interfaces.FilterCondCfg{Operation: "and", SubConds: []*interfaces.FilterCondCfg{cfg, explicit}}
					case "rhs":
						cfg.ValueFrom, cfg.Value = interfaces.ValueFrom_Field, "title"
					case "fixed":
						params.FixedFilterCondCfg = explicit
					case "nonfulltext":
						cfg.Operation, cfg.Field = "eq", "title"
					}
					indexes.EXPECT().SchemaForQuery(resource.SchemaDefinition).Return([]*interfaces.Property{&projected}, nil)
					resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
					catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
					if scenario == "expanded" {
						indexes.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).DoAndReturn(
							func(_ context.Context, _ string, _ *interfaces.Resource, query *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
								assert.Same(t, prepared, query.ActualFilterCond)
								assert.NotNil(t, query.ActualFilterCond)
								dsl, err := (&opensearch.OpenSearchConnector{}).ConvertFilterCondition(query.ActualFilterCond, []*interfaces.Property{&projected})
								require.NoError(t, err)
								assert.Contains(t, dsl, operation)
								return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"title": "result"}}}, nil
							})
					}
					result, err := (&resourceDataService{rs: resources, cs: catalogs, lim: indexes}).QueryWithPaging(t.Context(), resource, params)
					if scenario == "expanded" {
						require.NoError(t, err)
						assert.Equal(t, []map[string]any{{"title": "result"}}, result.Entries)
					} else {
						var httpErr *httperrors.HTTPError
						require.ErrorAs(t, err, &httpErr)
						if scenario == "fixed" {
							assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
						} else {
							assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
							assert.Contains(t, httpErr.ErrorDetails, "not filterable")
						}
						assert.Nil(t, result)
						assert.Same(t, prepared, params.ActualFilterCond)
					}
					assert.False(t, *field.Filterable)
					assert.Equal(t, "source_title", field.OriginalName)
					assert.Empty(t, field.Features)
				})
			}
		}
	})
	t.Run("wildcard retains logical and generated fields without exposing feature subfields", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
		catalogs := mock_interfaces.NewMockCatalogService(ctrl)
		resources := mock_interfaces.NewMockResourceService(ctrl)
		main := &interfaces.Property{Name: "field", OriginalName: "field", Type: interfaces.DataType_Text, Filterable: new(false),
			Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, FeatureName: "raw"}, {FeatureType: interfaces.PropertyFeatureType_Vector}}}
		raw := &interfaces.Property{Name: "field.raw", OriginalName: "field.raw", Type: interfaces.DataType_String, Filterable: new(true)}
		vector := &interfaces.Property{Name: "field_vector", OriginalName: "field_vector", Type: interfaces.DataType_Vector, Filterable: new(true)}
		resource := &interfaces.Resource{ID: "table", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
			Enabled: true, Status: interfaces.ResourceStatusActive, LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			SchemaDefinition: []*interfaces.Property{main}}
		indexes.EXPECT().SchemaForQuery(resource.SchemaDefinition).Return([]*interfaces.Property{main, raw, vector}, nil)
		indexes.EXPECT().GeneratedFields(gomock.Any()).Times(0)
		resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
		catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{Operation: "multi_match", Value: "query", RemainCfg: map[string]any{"fields": []any{"*"}}}}
		indexes.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).DoAndReturn(
			func(_ context.Context, _ string, _ *interfaces.Resource, query *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				actual, ok := query.ActualFilterCond.(*filter_condition.MultiMatchCond)
				require.True(t, ok)
				names := make([]string, len(actual.Fields))
				for i, field := range actual.Fields {
					names[i] = field.Name
				}
				assert.ElementsMatch(t, []string{"field", "field_vector"}, names)
				return &interfaces.ResourceDataQueryResult{}, nil
			})
		_, err := (&resourceDataService{rs: resources, cs: catalogs, lim: indexes}).QueryWithPaging(t.Context(), resource, params)
		require.NoError(t, err)
		assert.False(t, *main.Filterable)
	})
	t.Run("native index and forced source do not borrow managed physical capability", func(t *testing.T) {
		for _, category := range []string{interfaces.ResourceCategoryIndex, interfaces.ResourceCategoryTable} {
			for _, physicalReference := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/physical=%v", category, physicalReference), func(t *testing.T) {
					ctrl := gomock.NewController(t)
					indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
					catalogs := mock_interfaces.NewMockCatalogService(ctrl)
					resources := mock_interfaces.NewMockResourceService(ctrl)
					connectors := mock_interfaces.NewMockConnectorFactory(ctrl)
					main := &interfaces.Property{Name: "field", Type: interfaces.DataType_String, Filterable: new(false),
						Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext, FeatureName: "search"}}}
					resource := &interfaces.Resource{ID: "resource", CatalogID: "catalog", Category: category, Enabled: true,
						Status: interfaces.ResourceStatusActive, LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, SchemaDefinition: []*interfaces.Property{main}}
					cfg := &interfaces.FilterCondCfg{Field: "field", Operation: "match", Value: "query"}
					if physicalReference {
						cfg.Field, cfg.Operation = "field.search", "eq"
					}
					params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg}
					if category == interfaces.ResourceCategoryTable {
						params.IgnoreLocalIndex = new(true)
					}
					resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
					catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
					result, err := (&resourceDataService{rs: resources, cs: catalogs, lim: indexes, cf: connectors}).QueryWithPaging(t.Context(), resource, params)
					var httpErr *httperrors.HTTPError
					require.ErrorAs(t, err, &httpErr)
					assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
					assert.Nil(t, result)
					if physicalReference {
						assert.Contains(t, httpErr.ErrorDetails, "not found")
					} else {
						assert.Contains(t, httpErr.ErrorDetails, "not filterable")
					}
				})
			}
		}
	})

	t.Run("managed filters check the physical field selected by the feature", func(t *testing.T) {
		for _, tc := range []struct {
			name, kind, operation, feature, featureName, physicalName, physicalType string
			parent, child, allowed                                                  bool
		}{
			{"string fulltext uses enabled child", interfaces.DataType_String, "match", interfaces.PropertyFeatureType_Fulltext, "search", "field.search", interfaces.DataType_Text, false, true, true},
			{"string fulltext rejects disabled child", interfaces.DataType_String, "match", interfaces.PropertyFeatureType_Fulltext, "search", "field.search", interfaces.DataType_Text, true, false, false},
			{"text exact uses enabled keyword", interfaces.DataType_Text, "eq", interfaces.PropertyFeatureType_Keyword, "raw", "field.raw", interfaces.DataType_String, false, true, true},
			{"text exact rejects disabled keyword", interfaces.DataType_Text, "eq", interfaces.PropertyFeatureType_Keyword, "raw", "field.raw", interfaces.DataType_String, true, false, false},
			{"string exact cannot borrow fulltext capability", interfaces.DataType_String, "eq", interfaces.PropertyFeatureType_Fulltext, "search", "field.search", interfaces.DataType_Text, false, true, false},
			{"text fulltext cannot borrow keyword capability", interfaces.DataType_Text, "match", interfaces.PropertyFeatureType_Keyword, "raw", "field.raw", interfaces.DataType_String, false, true, false},
			{"knn uses enabled generated vector", interfaces.DataType_String, "knn_vector", interfaces.PropertyFeatureType_Vector, "embedding", "field_vector", interfaces.DataType_Vector, false, true, true},
			{"knn rejects disabled generated vector before model lookup", interfaces.DataType_String, "knn_vector", interfaces.PropertyFeatureType_Vector, "embedding", "field_vector", interfaces.DataType_Vector, true, false, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
				models := mock_interfaces.NewMockModelFactoryService(ctrl)
				catalogs := mock_interfaces.NewMockCatalogService(ctrl)
				resources := mock_interfaces.NewMockResourceService(ctrl)
				feature := interfaces.PropertyFeature{FeatureType: tc.feature, FeatureName: tc.featureName}
				if tc.feature == interfaces.PropertyFeatureType_Vector {
					feature.Config = map[string]any{"dimension": 2}
				}
				main := &interfaces.Property{Name: "field", OriginalName: "field", Type: tc.kind, Filterable: &tc.parent, Features: []interfaces.PropertyFeature{feature}}
				physical := &interfaces.Property{Name: tc.physicalName, OriginalName: tc.physicalName, Type: tc.physicalType, Filterable: &tc.child}
				generated := map[string]*interfaces.Property{}
				if tc.feature == interfaces.PropertyFeatureType_Vector {
					physical.Features = []interfaces.PropertyFeature{feature}
					generated[tc.physicalName] = physical
				}
				resource := &interfaces.Resource{ID: "table", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
					Enabled: true, Status: interfaces.ResourceStatusActive, LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
					IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{main}}
				indexes.EXPECT().SchemaForQuery(resource.SchemaDefinition).Return([]*interfaces.Property{main, physical}, nil)
				generatedCalls := 0
				if tc.operation == "knn_vector" && tc.allowed {
					generatedCalls = 1
					model := &interfaces.SmallModel{ModelID: "model"}
					models.EXPECT().GetModelByID(gomock.Any(), "model").Return(model, nil)
					models.EXPECT().GetVector(gomock.Any(), model, []string{"query"}).Return([]*interfaces.VectorResp{{Vector: []float32{1, 2}}}, nil)
				}
				indexes.EXPECT().GeneratedFields(resource.SchemaDefinition).Return(generated, nil).Times(generatedCalls)
				resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
				catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
				cfg := &interfaces.FilterCondCfg{Field: "field", Operation: tc.operation, Value: "query"}
				params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg, OutputFields: []string{"field"}}
				if tc.allowed {
					indexes.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).DoAndReturn(
						func(_ context.Context, _ string, _ *interfaces.Resource, query *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
							require.NotNil(t, query.ActualFilterCond)
							if tc.operation == "knn_vector" {
								assert.Equal(t, "field_vector", query.FilterCondCfg.Field)
							} else {
								assert.Equal(t, "field", query.FilterCondCfg.Field)
							}
							return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"field": "returned"}}}, nil
						})
				}
				result, err := (&resourceDataService{rs: resources, cs: catalogs, lim: indexes, mfs: models}).QueryWithPaging(t.Context(), resource, params)
				if tc.allowed {
					require.NoError(t, err)
					assert.Equal(t, []map[string]any{{"field": "returned"}}, result.Entries)
				} else {
					var httpErr *httperrors.HTTPError
					require.ErrorAs(t, err, &httpErr)
					assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
					assert.Contains(t, httpErr.ErrorDetails, "not filterable")
					assert.Nil(t, result)
				}
				assert.Equal(t, tc.parent, *main.Filterable)
				assert.Equal(t, tc.child, *physical.Filterable)
			})
		}
	})

	t.Run("LIM metadata failures retain query error classification before model or backend calls", func(t *testing.T) {
		for _, failure := range []struct {
			name   string
			cause  error
			status int
		}{
			{"decimal mapping", interfaces.NewDecimalIndexError("amount", "invalid_scale"), http.StatusBadRequest},
			{"unknown mapping", errors.New("private mapping diagnostic"), http.StatusInternalServerError},
		} {
			t.Run(failure.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
				models := mock_interfaces.NewMockModelFactoryService(ctrl)
				catalogs := mock_interfaces.NewMockCatalogService(ctrl)
				resources := mock_interfaces.NewMockResourceService(ctrl)
				connectors := mock_interfaces.NewMockConnectorFactory(ctrl)
				resource := &interfaces.Resource{ID: "table", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
					Enabled: true, Status: interfaces.ResourceStatusActive, LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
					SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text}}}
				resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
				catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
				indexes.EXPECT().SchemaForQuery(resource.SchemaDefinition).Return(nil, failure.cause)
				indexes.EXPECT().GeneratedFields(gomock.Any()).Times(0)
				params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{Field: "title", Operation: "knn_vector", Value: "query"}}
				result, err := (&resourceDataService{rs: resources, cs: catalogs, cf: connectors, lim: indexes, mfs: models}).QueryWithPaging(t.Context(), resource, params)
				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, failure.status, httpErr.HTTPCode)
				assert.Nil(t, result)
				assert.Nil(t, params.ActualFilterCond)
				if failure.status == http.StatusBadRequest {
					assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.ErrorCode)
					assert.Equal(t, "amount", httpErr.Metadata["field"])
					assert.Equal(t, "invalid_scale", httpErr.Metadata["reason"])
				} else {
					assert.NotContains(t, httpErr.ErrorDetails, "private mapping diagnostic")
				}
			})
		}
	})

	t.Run("injected LIM query schema can reject a source-enabled field", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		catalogs := mock_interfaces.NewMockCatalogService(ctrl)
		resources := mock_interfaces.NewMockResourceService(ctrl)
		indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
		models := mock_interfaces.NewMockModelFactoryService(ctrl)
		resource := &interfaces.Resource{ID: "table", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
			Enabled: true, Status: interfaces.ResourceStatusActive, LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text, Filterable: new(true)}}}
		projected := *resource.SchemaDefinition[0]
		projected.Filterable = new(false)
		indexes.EXPECT().SchemaForQuery(resource.SchemaDefinition).Return([]*interfaces.Property{&projected}, nil)
		indexes.EXPECT().GeneratedFields(gomock.Any()).Times(0)
		resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
		catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{Field: "title", Operation: "knn_vector", Value: "query"}}
		result, err := (&resourceDataService{rs: resources, cs: catalogs, lim: indexes, mfs: models}).QueryWithPaging(t.Context(), resource, params)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.ErrorDetails, "not filterable")
		assert.Nil(t, result)
		assert.True(t, *resource.SchemaDefinition[0].Filterable)
	})

	t.Run("dataset feature capability requires an available index", func(t *testing.T) {
		for _, status := range []string{"", interfaces.ResourceLocalIndexStatusUnavailable, interfaces.ResourceLocalIndexStatusStale, interfaces.ResourceLocalIndexStatusAvailable} {
			for _, indexName := range []string{"", "dataset-index"} {
				t.Run(status+"/"+indexName, func(t *testing.T) {
					ctrl := gomock.NewController(t)
					catalogs := mock_interfaces.NewMockCatalogService(ctrl)
					resources := mock_interfaces.NewMockResourceService(ctrl)
					datasets := mock_interfaces.NewMockDatasetService(ctrl)
					resource := &interfaces.Resource{ID: "dataset", CatalogID: "catalog", Category: interfaces.ResourceCategoryDataset,
						Enabled: true, Status: interfaces.ResourceStatusActive, LocalIndexName: indexName, LocalIndexStatus: status,
						SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text, Filterable: new(false)}}}
					params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{Field: "title", Operation: "match", Value: "query"}}
					resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
					catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
					available := status == interfaces.ResourceLocalIndexStatusAvailable && indexName != ""
					if available {
						datasets.EXPECT().ListDocuments(gomock.Any(), resource, params).Return(&interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"title": "result"}}}, nil)
					}
					result, err := (&resourceDataService{rs: resources, cs: catalogs, ds: datasets, lim: newResourceDataLocalIndexManagerMock(ctrl)}).QueryWithPaging(t.Context(), resource, params)
					if available {
						require.NoError(t, err)
						assert.Equal(t, "result", result.Entries[0]["title"])
					} else {
						var httpErr *httperrors.HTTPError
						require.ErrorAs(t, err, &httpErr)
						assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
						assert.Contains(t, httpErr.ErrorDetails, "not filterable")
						assert.Nil(t, result)
					}
					assert.False(t, *resource.SchemaDefinition[0].Filterable)
				})
			}
		}
	})

	t.Run("native index rejects disabled fields before connector creation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		catalogs := mock_interfaces.NewMockCatalogService(ctrl)
		resources := mock_interfaces.NewMockResourceService(ctrl)
		connectors := mock_interfaces.NewMockConnectorFactory(ctrl)
		indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
		resource := &interfaces.Resource{ID: "native-index", CatalogID: "catalog", Category: interfaces.ResourceCategoryIndex,
			SourceIdentifier: "physical-index", Enabled: true, Status: interfaces.ResourceStatusActive,
			SchemaDefinition: []*interfaces.Property{
				{Name: "title", Type: interfaces.DataType_String, Filterable: new(false)},
			}}
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{
			Field: "title", Operation: filter_condition.OperationEqual, Value: "protected",
		}}
		resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
		catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).
			Return(&interfaces.Catalog{Enabled: true, ConnectorType: "opensearch"}, nil)
		result, err := (&resourceDataService{rs: resources, cs: catalogs, cf: connectors, lim: indexes}).QueryWithPaging(t.Context(), resource, params)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.ErrorCode)
		assert.Contains(t, httpErr.ErrorDetails, "not filterable")
		assert.Nil(t, result)
		assert.Nil(t, params.ActualFilterCond)
		// Neither connector creation nor a managed local-index call is permitted.
	})

	t.Run("filterability follows the execution schema and preserves projection", func(t *testing.T) {
		for _, indexed := range []bool{false, true} {
			for _, operation := range []string{"plain", "nested", "right field", "multi field", "match", "match phrase", "vector", "vector ordinary subfilter", "generated vector", "projection", "wildcard"} {
				t.Run(fmt.Sprintf("index=%v/%s", indexed, operation), func(t *testing.T) {
					ctrl := gomock.NewController(t)
					catalogs := mock_interfaces.NewMockCatalogService(ctrl)
					resources := mock_interfaces.NewMockResourceService(ctrl)
					indexes := newResourceDataLocalIndexManagerMock(ctrl)
					connectors := mock_interfaces.NewMockConnectorFactory(ctrl)
					models := mock_interfaces.NewMockModelFactoryService(ctrl)
					resource := &interfaces.Resource{ID: "table", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
						Enabled: true, Status: interfaces.ResourceStatusActive,
						SchemaDefinition: []*interfaces.Property{
							{Name: "allowed", Type: interfaces.DataType_String},
							{Name: "blocked", Type: interfaces.DataType_String, Filterable: new(false), Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext}, {FeatureType: interfaces.PropertyFeatureType_Vector}}},
						}}
					if indexed {
						resource.LocalIndexName = "table-index"
						resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
						resource.IndexConfig = &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}
					}
					shouldSucceed := indexed || operation == "projection" || operation == "wildcard"
					params := &interfaces.ResourceDataQueryParams{OutputFields: []string{"blocked"}}
					switch operation {
					case "plain":
						params.FilterCondCfg = &interfaces.FilterCondCfg{Field: "blocked", Operation: "eq", Value: "x"}
					case "nested":
						params.FilterCondCfg = &interfaces.FilterCondCfg{Operation: "and", SubConds: []*interfaces.FilterCondCfg{{Operation: "or", SubConds: []*interfaces.FilterCondCfg{{Field: "blocked", Operation: "null"}}}}}
					case "right field":
						params.FilterCondCfg = &interfaces.FilterCondCfg{Field: "allowed", Operation: "eq", ValueFrom: interfaces.ValueFrom_Field, Value: "blocked"}
					case "multi field":
						params.FilterCondCfg = &interfaces.FilterCondCfg{Operation: "multi_match", Value: "x", RemainCfg: map[string]any{"fields": []any{"allowed", "blocked"}}}
					case "match":
						params.FilterCondCfg = &interfaces.FilterCondCfg{Field: "blocked", Operation: "match", Value: "x"}
					case "match phrase":
						params.FilterCondCfg = &interfaces.FilterCondCfg{Field: "blocked", Operation: "match_phrase", Value: "x"}
					case "vector", "vector ordinary subfilter":
						params.FilterCondCfg = &interfaces.FilterCondCfg{Field: "blocked", Operation: "knn_vector", Value: "x"}
						if operation == "vector ordinary subfilter" {
							params.FilterCondCfg.SubConds = []*interfaces.FilterCondCfg{{Field: "blocked", Operation: "eq", Value: "x"}}
						}
						if indexed {
							model := &interfaces.SmallModel{ModelID: "model"}
							models.EXPECT().GetModelByID(gomock.Any(), "model").Return(model, nil)
							models.EXPECT().GetVector(gomock.Any(), model, []string{"x"}).Return([]*interfaces.VectorResp{{Vector: []float32{1, 2}}}, nil)
						}
					case "wildcard":
						if !indexed {
							return
						}
						params.FilterCondCfg = &interfaces.FilterCondCfg{Operation: "multi_match", Value: "x", RemainCfg: map[string]any{"fields": []any{"*"}}}
					case "generated vector":
						if !indexed {
							return
						}
						params.FilterCondCfg = &interfaces.FilterCondCfg{Field: "blocked_vector", Operation: "knn_vector", Value: []float32{1, 2}}
					}
					resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
					catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
					rows := []map[string]any{{"blocked": "still returned"}}
					if shouldSucceed {
						if indexed {
							indexes.EXPECT().ListDocuments(gomock.Any(), resource.LocalIndexName, resource, gomock.Any()).DoAndReturn(
								func(_ context.Context, _ string, _ *interfaces.Resource, actual *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
									if operation == "wildcard" {
										match, ok := actual.ActualFilterCond.(*filter_condition.MultiMatchCond)
										require.True(t, ok)
										names := make([]string, len(match.Fields))
										for i, property := range match.Fields {
											names[i] = property.Name
										}
										assert.ElementsMatch(t, []string{"allowed", "blocked", "blocked_vector"}, names)
									}
									if operation == "vector" || operation == "vector ordinary subfilter" || operation == "generated vector" {
										actualVector, ok := actual.ActualFilterCond.(*filter_condition.KnnVectorCond)
										require.True(t, ok)
										assert.Equal(t, "blocked_vector", actualVector.FilterFieldName)
									}
									return &interfaces.ResourceDataQueryResult{Entries: rows}, nil
								})
						} else {
							source := mock_interfaces.NewMockTableConnector(ctrl)
							connectors.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(source, nil)
							source.EXPECT().Connect(gomock.Any()).Return(nil)
							source.EXPECT().Close(gomock.Any()).Return(nil)
							source.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(&interfaces.ResourceDataQueryResult{Entries: rows}, nil)
						}
					}
					result, err := (&resourceDataService{rs: resources, cs: catalogs, lim: indexes, cf: connectors, mfs: models}).QueryWithPaging(context.Background(), resource, params)
					if shouldSucceed {
						require.NoError(t, err)
						assert.Equal(t, rows, result.Entries)
						assert.False(t, *resource.SchemaDefinition[1].Filterable)
					} else {
						require.Error(t, err)
						assert.Nil(t, result)
						var httpErr *httperrors.HTTPError
						require.ErrorAs(t, err, &httpErr)
						assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
						assert.Contains(t, httpErr.ErrorDetails, "not filterable")
						// No expectation permits a connector, index, or model call.
					}
				})
			}
		}
	})

	for _, tc := range []struct {
		name       string
		allow      *bool
		ignore     *bool
		wantSource bool
		cause      error
	}{
		{name: "default distinct uses available index"},
		{name: "approximate distinct uses available index", allow: new(true)},
		{name: "exact distinct prefers source", allow: new(false), wantSource: true},
		{name: "exact distinct prefers source over explicit index allowance", allow: new(false), ignore: new(false), wantSource: true},
		{name: "explicit source remains source", allow: new(true), ignore: new(true), wantSource: true},
		{name: "exact source failure does not fall back to index", allow: new(false), wantSource: true, cause: interfaces.NewSourceReadForbiddenError(errors.New("source read denied"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			catalogs := mock_interfaces.NewMockCatalogService(ctrl)
			resources := mock_interfaces.NewMockResourceService(ctrl)
			indexes := newResourceDataLocalIndexManagerMock(ctrl)
			connectors := mock_interfaces.NewMockConnectorFactory(ctrl)
			source := mock_interfaces.NewMockTableConnector(ctrl)
			resource := &interfaces.Resource{ID: "table", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
				Enabled: true, Status: interfaces.ResourceStatusActive,
				LocalIndexName: "table-index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
				SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}}}
			params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count_distinct", AllowApproximate: tc.allow}, IgnoreLocalIndex: tc.ignore}
			resources.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
			catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
			rows := []map[string]any{{"__value": int64(15020)}}
			if tc.wantSource {
				connectors.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(source, nil)
				source.EXPECT().Connect(gomock.Any()).Return(nil)
				source.EXPECT().Close(gomock.Any()).Return(nil)
				if tc.cause != nil {
					source.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(nil, tc.cause)
				} else {
					source.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(&interfaces.ResourceDataQueryResult{Entries: rows}, nil)
				}
			} else {
				indexes.EXPECT().ListDocuments(gomock.Any(), resource.LocalIndexName, resource, gomock.Any()).DoAndReturn(
					func(_ context.Context, _ string, _ *interfaces.Resource, actual *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
						return &interfaces.ResourceDataQueryResult{Entries: rows, TotalCount: 0, IsApproximate: true}, nil
					})
			}
			got, err := (&resourceDataService{rs: resources, cs: catalogs, lim: indexes, cf: connectors}).QueryWithPaging(context.Background(), resource, params)
			if tc.cause != nil {
				require.Nil(t, got)
				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, rows, got.Entries)
			assert.Equal(t, !tc.wantSource, got.IsApproximate)
			if tc.wantSource {
				assert.Equal(t, interfaces.ResourceQuerySourceSource, got.QuerySource)
				assert.Equal(t, interfaces.ResourceCategoryTable, resourceDataPaginationCategory(resource, params))
			} else {
				assert.Equal(t, interfaces.ResourceQuerySourceLocalIndex, got.QuerySource)
			}
			assert.Same(t, tc.ignore, params.IgnoreLocalIndex)
		})
	}
	t.Run("public table query preserves local index score and cursor position", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		catalogs := mock_interfaces.NewMockCatalogService(ctrl)
		resources := mock_interfaces.NewMockResourceService(ctrl)
		index := newResourceDataLocalIndexManagerMock(ctrl)
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
			func(_ context.Context, _ string, _ *interfaces.Resource, actual *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				var resultSearchAfter []any

				assert.Equal(t, []*interfaces.SortField{{Field: "_score", Direction: "desc"}, {Field: "id", Direction: "asc"}}, actual.Sort)
				assert.Equal(t, []string{"id", "_score"}, actual.OutputFields)
				assert.Equal(t, []any{0.875, 1}, actual.SearchAfter)
				resultSearchAfter = []any{0.875, 2}
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 2, "_score": 0.875}}, TotalCount: 3, SearchAfter: resultSearchAfter}, nil
			})
		result, err := (&resourceDataService{rs: resources, cs: catalogs, lim: index}).QueryWithPaging(context.Background(), resource, params)
		require.NoError(t, err)
		assert.Equal(t, interfaces.ResourceQuerySourceLocalIndex, result.QuerySource)
		assert.Equal(t, []map[string]any{{"id": 2, "_score": 0.875}}, result.Entries)
		assert.Equal(t, int64(3), result.TotalCount)
		assert.Equal(t, []any{0.875, 1}, params.SearchAfter)
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

		_, err := rds.query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
		assertCatalogDisabledError(t, err)
	})

	t.Run("query table with local index uses local index manager", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockLIM := newResourceDataLocalIndexManagerMock(ctrl)
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
			Return(&interfaces.ResourceDataQueryResult{Entries: wantRows, TotalCount: int64(1)}, nil)

		rowsResult, err := rds.QuerySourcePage(context.Background(), resource, params)
		var rows []map[string]any
		var total int64
		if rowsResult != nil {
			rows = rowsResult.Entries
			total = rowsResult.TotalCount
		}
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, "openbkn", rows[0]["name"])
		assert.Equal(t, interfaces.ResourceValue{Mode: interfaces.ResourceValueModeUnavailable}, rows[0]["blob"])
		assert.Equal(t, interfaces.ResourceValue{Mode: interfaces.ResourceValueModeUnavailable}, rows[0]["native_value"])
	})

	t.Run("query table with local index answers 400 for a condition the index cannot build", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockLIM := newResourceDataLocalIndexManagerMock(ctrl)
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
			Return(nil, cause)

		_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, params)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.ErrorCode)
		assert.Contains(t, httpErr.ErrorDetails, "re-save the resource configuration")
	})

	t.Run("query table with local index preserves stored condition build failures", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockLIM := newResourceDataLocalIndexManagerMock(ctrl)
		rds := &resourceDataService{cs: mockCS, lim: mockLIM}
		resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1",
			Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			LocalIndexName: "source-index", SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text}}}
		fixed := &interfaces.FilterCondCfg{Field: "title", Operation: filter_condition.OperationEqual,
			ValueFrom: interfaces.ValueFrom_Const, Value: "example"}
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: fixed, FixedFilterCondCfg: fixed}
		mockCS.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).
			Return(&interfaces.Catalog{Enabled: true}, nil)
		mockLIM.EXPECT().ListDocuments(gomock.Any(), resource.LocalIndexName, resource, params).Return(nil, interfaces.NewStoredConditionBuildError(interfaces.NewConditionBuildError("text field has no keyword feature")))
		rowsResult9, err := rds.QuerySourcePage(context.Background(), resource, params)
		var rows []map[string]any
		var total int64
		if rowsResult9 != nil {
			rows = rowsResult9.Entries
			total = rowsResult9.TotalCount
		}
		assert.Nil(t, rows)
		assert.Zero(t, total)
		var stored *interfaces.StoredConditionBuildError
		require.ErrorAs(t, err, &stored)
		var build *interfaces.ConditionBuildError
		require.ErrorAs(t, stored, &build)
		assert.Equal(t, "text field has no keyword feature", build.Reason)
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
			Return(&interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"blob": int64(12)}}, TotalCount: 1}, nil)

		rowsResult10, err := rds.QuerySourcePage(context.Background(), resource, params)
		var rows []map[string]any
		var total int64
		if rowsResult10 != nil {
			rows = rowsResult10.Entries
			total = rowsResult10.TotalCount
		}
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
			Return(&interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"blob": []byte{1, 2, 3}}}, TotalCount: 1}, nil)

		rowsResult11, err := rds.QuerySourcePage(context.Background(), resource, params)
		var rows []map[string]any
		var total int64
		if rowsResult11 != nil {
			rows = rowsResult11.Entries
			total = rowsResult11.TotalCount
		}
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
		rds := &resourceDataService{cs: mockCS, ds: mockDS, lim: newResourceDataLocalIndexManagerMock(ctrl)}
		resource := &interfaces.Resource{
			ID: "dataset-1", Enabled: true, CatalogID: "catalog-1",
			Category: interfaces.ResourceCategoryDataset, LocalIndexName: "vega-dataset-index-1",
			SchemaDefinition: []*interfaces.Property{{Name: "body", Type: interfaces.DataType_Text}},
		}
		params := &interfaces.ResourceDataQueryParams{}
		downstream := httperrors.NewHTTPError(context.Background(), http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails("text field body has no keyword feature; re-save the resource configuration and rebuild the local index, or use match")

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockDS.EXPECT().ListDocuments(gomock.Any(), resource, gomock.Any()).Return(nil, downstream)

		_, err := rds.query(context.Background(), resource, params)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.ErrorCode)
		assert.Contains(t, httpErr.ErrorDetails, "re-save the resource configuration")
	})

	t.Run("query dataset builds actual filter condition and delegates", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockDS := mock_interfaces.NewMockDatasetService(ctrl)
		rds := &resourceDataService{cs: mockCS, ds: mockDS, lim: newResourceDataLocalIndexManagerMock(ctrl)}
		resource := &interfaces.Resource{
			ID:               "dataset-1",
			Enabled:          true,
			CatalogID:        "catalog-1",
			Category:         interfaces.ResourceCategoryDataset,
			LocalIndexName:   "vega-dataset-index-1",
			LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			SchemaDefinition: []*interfaces.Property{
				{Name: "name", OriginalName: "source_name", Type: interfaces.DataType_String},
			},
		}
		params := &interfaces.ResourceDataQueryParams{
			FilterCondCfg: &interfaces.FilterCondCfg{
				Field:     "name",
				Operation: "==",
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "alice",
			},
		}
		wantRows := []map[string]any{{"name": "alice"}}

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockDS.EXPECT().ListDocuments(gomock.Any(), resource, params).
			DoAndReturn(func(ctx context.Context, gotResource *interfaces.Resource,
				gotParams *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				require.NotNil(t, gotParams.ActualFilterCond)
				assert.Equal(t, "==", gotParams.ActualFilterCond.GetOperation())
				equal, ok := gotParams.ActualFilterCond.(*filter_condition.EqualCond)
				require.True(t, ok)
				assert.Equal(t, "name", equal.Lfield.OriginalName)
				return &interfaces.ResourceDataQueryResult{Entries: wantRows, TotalCount: int64(1)}, nil
			})

		rowsResult12, err := rds.query(context.Background(), resource, params)
		var rows []map[string]any
		var total int64
		if rowsResult12 != nil {
			rows = rowsResult12.Entries
			total = rowsResult12.TotalCount
		}

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

		rowsResult13, err := rds.query(context.Background(), resource, params)
		var rows []map[string]any
		var total int64
		if rowsResult13 != nil {
			rows = rowsResult13.Entries
			total = rowsResult13.TotalCount
		}

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
		downstream := httperrors.NewHTTPError(context.Background(), http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails("text field body has no keyword feature; re-save the resource configuration")

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockLVS.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).Return(nil, downstream)

		_, err := rds.query(context.Background(), resource, params)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.ErrorCode)
		assert.Contains(t, httpErr.ErrorDetails, "re-save the resource configuration")
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
	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.ErrorCode)
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
	rds := &resourceDataService{cs: mockCS, ds: mockDS, lim: newResourceDataLocalIndexManagerMock(ctrl)}
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
		Return(&interfaces.ResourceDataQueryResult{}, nil)

	_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(context.Background()), resource, &interfaces.ResourceDataQueryParams{
		Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1},
	})
	require.Error(t, err)
	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
}

func TestResourceDataServiceRejectsOpenSearchFirstPageWindowOverflow(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	rds := &resourceDataService{cs: mockCS, ds: mockDS, lim: newResourceDataLocalIndexManagerMock(ctrl)}
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
		Return(&interfaces.ResourceDataQueryResult{}, nil)

	_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(context.Background()), resource, &interfaces.ResourceDataQueryParams{
		Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Offset: interfaces.MaxPageLimit, Limit: 1},
		Sort:   []*interfaces.SortField{{Field: "id", Direction: "asc"}},
	})
	require.Error(t, err)
	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
}

func TestDatasetCursorUsesSearchAfterPagination(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	rds := &resourceDataService{cs: mockCS, ds: mockDS, lim: newResourceDataLocalIndexManagerMock(ctrl)}
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
		DoAndReturn(func(_ context.Context, _ *interfaces.Resource, pageParams *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			var resultSearchAfter2 []any

			assert.Equal(t, 1, pageParams.Paging.Limit)
			if firstPage {
				firstPage = false
				assert.Empty(t, pageParams.SearchAfter)
				resultSearchAfter2 = []any{"sort-1"}
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}}, TotalCount: 0, SearchAfter: resultSearchAfter2}, nil
			}
			assert.Equal(t, []any{"sort-1"}, pageParams.SearchAfter)
			return &interfaces.ResourceDataQueryResult{Entries: nil, TotalCount: 0, SearchAfter: resultSearchAfter2}, nil
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
	t.Run("preserves implicit count ordering", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 0}, Sort: []*interfaces.SortField{{Field: "__value", Direction: interfaces.DESC_DIRECTION}}}
		before, err := json.Marshal(params)
		require.NoError(t, err)
		got := (&resourceDataService{}).prepareSortParams(&interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_Integer}}}, params)
		assert.Equal(t, []*interfaces.SortField{{Field: "__value", Direction: interfaces.DESC_DIRECTION}}, got.Sort)
		after, err := json.Marshal(params)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})

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

	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Catalog_IsDisabled, httpErr.ErrorCode)
}

// TestQueryClassifiesUnsupportedOperations 覆盖两个之前被抹平的分型点。
//
// 之前的形态是：QueryData 里判出的 400 被 query() 无条件重包成 500，而 fileset
// 分支根本没做判断——两处加起来，anyshare / mariadb 那几个连接器返回
// UnsupportedOperationError 的改动一点行为变化都没有，调用方拿到的仍是
// 「数据资源内部错误」，ontology-query 继续判成依赖故障。
type codedQueryError struct{ code int }

func (e *codedQueryError) Error() string { return "source database error" }
func (e *codedQueryError) Code() int     { return e.code }

func TestQueryClassifiesUnsupportedOperations(t *testing.T) {
	t.Run("temporal errors stay typed internally and become HTTP 400 publicly", func(t *testing.T) {
		for _, category := range []string{interfaces.ResourceCategoryIndex, interfaces.ResourceCategoryTable} {
			for _, mode := range []interfaces.PagingMode{"internal", interfaces.PagingModeSingle, interfaces.PagingModeCursor} {
				for _, stored := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/mode=%s/stored=%v", category, mode, stored), func(t *testing.T) {
						ctrl := gomock.NewController(t)
						catalogs := mock_interfaces.NewMockCatalogService(ctrl)
						resources := mock_interfaces.NewMockResourceService(ctrl)
						resource := &interfaces.Resource{ID: "resource", Enabled: true, CatalogID: "catalog", Category: category, SourceIdentifier: "index",
							SchemaDefinition: []*interfaces.Property{{Name: "created", Type: interfaces.DataType_Datetime}}}
						catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: "index"}, nil)
						temporal := interfaces.NewTemporalRangeError("created", "date_nanos", 2147483647, "day")
						var cause error = temporal
						if stored {
							cause = interfaces.NewStoredConditionBuildError(cause)
						}
						wrapped := fmt.Errorf("private source: %w", cause)
						service := &resourceDataService{cs: catalogs, rs: resources}
						if category == interfaces.ResourceCategoryIndex {
							connectors := mock_interfaces.NewMockConnectorFactory(ctrl)
							index := mock_interfaces.NewMockIndexConnector(ctrl)
							connectors.EXPECT().CreateConnectorInstance(gomock.Any(), "index", gomock.Any()).Return(index, nil)
							index.EXPECT().Connect(gomock.Any()).Return(nil)
							index.EXPECT().Close(gomock.Any()).Return(nil)
							index.EXPECT().ExecuteQuery(gomock.Any(), "index", resource, gomock.Any()).Return(nil, wrapped)
							service.cf = connectors
						} else {
							index := newResourceDataLocalIndexManagerMock(ctrl)
							resource.LocalIndexName = "index"
							resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
							index.EXPECT().ListDocuments(gomock.Any(), "index", resource, gomock.Any()).Return(nil, wrapped)
							service.lim = index
						}
						params := &interfaces.ResourceDataQueryParams{}
						if mode != "internal" {
							params.Paging = interfaces.PagingRequest{Mode: mode, Limit: 1}
							params.Sort = []*interfaces.SortField{{Field: "created", Direction: "asc"}}
							resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
							result, err := service.QueryWithPaging(t.Context(), resource, params)
							assert.Nil(t, result)
							var httpErr *httperrors.HTTPError
							require.ErrorAs(t, err, &httpErr)
							assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
							assert.Equal(t, verrors.VegaBackend_Resource_QueryTimeOutOfRange, httpErr.ErrorCode)
							assert.Equal(t, "created", httpErr.Metadata["field"])
							assert.NotContains(t, httpErr.Error(), "private source")
							return
						}
						page, err := service.QuerySourcePage(t.Context(), resource, params)
						var rows []map[string]any
						var total int64
						if page != nil {
							rows = page.Entries
							total = page.TotalCount
						}
						assert.Nil(t, rows)
						assert.Zero(t, total)
						var got *interfaces.TemporalRangeError
						require.ErrorAs(t, err, &got)
						assert.Same(t, temporal, got)
						var fixed *interfaces.StoredConditionBuildError
						assert.Equal(t, stored, errors.As(err, &fixed))
					})
				}
			}
		}
	})

	t.Run("cursor continuation uses its cached source channel for error mapping", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		catalogs := mock_interfaces.NewMockCatalogService(ctrl)
		connectors := mock_interfaces.NewMockConnectorFactory(ctrl)
		connector := mock_interfaces.NewMockTableConnector(ctrl)
		resource := &interfaces.Resource{ID: "mapper-cursor-table", CatalogID: "catalog", Enabled: true, Category: interfaces.ResourceCategoryTable,
			LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}}}
		catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true}, nil).Times(2)
		connectors.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).Return(connector, nil).Times(2)
		connector.EXPECT().Connect(gomock.Any()).Return(nil).Times(2)
		connector.EXPECT().Close(gomock.Any()).Return(nil).Times(2)
		gomock.InOrder(
			connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(&interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}, {"id": 2}}}, nil),
			connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ *interfaces.Resource, actual *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
					require.NotNil(t, actual.IgnoreLocalIndex)
					assert.True(t, *actual.IgnoreLocalIndex)
					return nil, interfaces.NewConditionBuildError("invalid source query condition")
				}),
		)
		service := &resourceDataService{cs: catalogs, cf: connectors}
		ctx := interfaces.WithTrustedProxyRead(context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "mapper-cursor-owner"}))
		ignore := true
		first, err := service.QueryWithPaging(ctx, resource, &interfaces.ResourceDataQueryParams{
			IgnoreLocalIndex: &ignore, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1},
			Sort: []*interfaces.SortField{{Field: "id", Direction: "asc"}},
		})
		require.NoError(t, err)
		require.NotNil(t, first)
		require.NotNil(t, first.Paging.NextCursor)
		result, err := service.QueryWithPaging(ctx, resource, &interfaces.ResourceDataQueryParams{
			Paging: interfaces.PagingRequest{Cursor: *first.Paging.NextCursor},
		})
		assert.Nil(t, result)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.ErrorCode)
	})
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
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: "mariadb"}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).
			Return(nil, fmt.Errorf("execute source query: %w", interfaces.NewSourceQueryInvalidParameterError(
				interfaces.SourceQueryInvalidParameterUnknownColumn, errors.New("private driver column details"))))

		result, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, &interfaces.ResourceDataQueryParams{})
		assert.Nil(t, result)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.ErrorCode)
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
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: "mariadb"}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).
			Return(nil, fmt.Errorf("execute source query: %w", interfaces.NewSourceReadForbiddenError(errors.New("source database error"))))

		result, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, &interfaces.ResourceDataQueryParams{})
		assert.Nil(t, result)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_SourceReadForbidden, httpErr.ErrorCode)
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

		_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, &interfaces.ResourceDataQueryParams{})
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_SourceReadForbidden, httpErr.ErrorCode)
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

		result, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, &interfaces.ResourceDataQueryParams{})
		assert.Nil(t, result)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.ErrorCode)
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

		_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, &interfaces.ResourceDataQueryParams{})
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_SourceReadForbidden, httpErr.ErrorCode)
	})

	t.Run("raw driver code is not classified by the service", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCF := mock_interfaces.NewMockConnectorFactory(ctrl)
		mockConn := mock_interfaces.NewMockTableConnector(ctrl)
		rds := &resourceDataService{cs: mockCS, cf: mockCF}
		resource := newResource(interfaces.ResourceCategoryTable)

		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: "hana"}, nil)
		mockCF.EXPECT().CreateConnectorInstance(gomock.Any(), "hana", gomock.Any()).Return(mockConn, nil)
		mockConn.EXPECT().Connect(gomock.Any()).Return(nil)
		mockConn.EXPECT().Close(gomock.Any()).Return(nil)
		mockConn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(nil, &codedQueryError{258})

		_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, &interfaces.ResourceDataQueryParams{})
		var httpErr *httperrors.HTTPError
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

		_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, &interfaces.ResourceDataQueryParams{})
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

		_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, &interfaces.ResourceDataQueryParams{})
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

		_, err := rds.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, &interfaces.ResourceDataQueryParams{})
		var httpErr *httperrors.HTTPError
		require.True(t, errors.As(err, &httpErr), "want an HTTPError, got %v", err)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
	})
}

func assertUnsupportedOperationHTTPError(t *testing.T, err error) {
	t.Helper()
	var httpErr *httperrors.HTTPError
	require.True(t, errors.As(err, &httpErr), "want an HTTPError, got %v", err)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode,
		"算子不支持是请求侧问题：包成 500 会让 ontology-query 判成依赖故障")
	assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.ErrorCode)
}

func TestResourceDataServiceQueryRejectsUnavailableIndexFields(t *testing.T) {
	for _, fieldType := range []string{interfaces.DataType_Json, interfaces.DataType_Binary, interfaces.DataType_Other} {
		for _, stored := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stored=%v", fieldType, stored), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				cs := mock_interfaces.NewMockCatalogService(ctrl)
				lim := newResourceDataLocalIndexManagerMock(ctrl)
				cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{Enabled: true}, nil)
				service := &resourceDataService{cs: cs, lim: lim}
				resource := tableResource("index")
				resource.CatalogID = "catalog-1"
				resource.SchemaDefinition = []*interfaces.Property{{Name: "payload", Type: fieldType}}
				cfg := &interfaces.FilterCondCfg{Field: "payload", Operation: "exist"}
				params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg}
				if stored {
					params.FixedFilterCondCfg = cfg
				}
				rowsResult, err := service.QuerySourcePage(context.Background(), resource, params)
				var rows []map[string]any
				var total int64
				if rowsResult != nil {
					rows = rowsResult.Entries
					total = rowsResult.TotalCount
				}
				require.Nil(t, rows)
				assert.Zero(t, total)
				if stored {
					var storedErr *interfaces.StoredConditionBuildError
					require.ErrorAs(t, err, &storedErr)
					require.NotNil(t, storedErr.Cause)
					assert.Contains(t, storedErr.Cause.Error(), "not found")
					return
				}
				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
				if !stored {
					assert.Contains(t, httpErr.ErrorDetails, "not found")
				}
			})
		}
	}
}

func TestResourceDataServiceQueryDoris(t *testing.T) {
	t.Run("large integer source ranges reach connector", func(t *testing.T) {
		for _, op := range []string{"range", "out_range", "between"} {
			ctrl := gomock.NewController(t)
			cs := mock_interfaces.NewMockCatalogService(ctrl)
			cf := mock_interfaces.NewMockConnectorFactory(ctrl)
			conn := mock_interfaces.NewMockTableConnector(ctrl)
			service := &resourceDataService{cs: cs, cf: cf}
			resource := &interfaces.Resource{ID: "large-resource", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
				LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
				SchemaDefinition: []*interfaces.Property{{Name: "wide", Type: interfaces.DataType_LargeInteger, OriginalType: "LARGEINT"}}}
			ignore := true
			bounds := []any{"-170141183460469231731687303715884105728", "170141183460469231731687303715884105727"}
			params := &interfaces.ResourceDataQueryParams{IgnoreLocalIndex: &ignore, NeedTotal: true,
				FilterCondCfg: &interfaces.FilterCondCfg{Field: "wide", Operation: op, Value: bounds}}
			cs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: "doris"}, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "doris", gomock.Any()).Return(conn, nil)
			conn.EXPECT().Connect(gomock.Any()).Return(nil)
			conn.EXPECT().Close(gomock.Any()).Return(nil)
			conn.EXPECT().ExecuteQuery(gomock.Any(), resource, params).DoAndReturn(func(_ context.Context, _ *interfaces.Resource, prepared *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				require.NotNil(t, prepared.ActualFilterCond)
				assert.Equal(t, op, prepared.ActualFilterCond.GetOperation())
				assert.Equal(t, bounds, prepared.FilterCondCfg.Value)
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"wide": bounds[1]}}, TotalCount: 1}, nil
			})
			rowsResult, err := service.QuerySourcePage(context.Background(), resource, params)
			var rows []map[string]any
			var total int64
			if rowsResult != nil {
				rows = rowsResult.Entries
				total = rowsResult.TotalCount
			}
			require.NoError(t, err)
			assert.Equal(t, int64(1), total)
			assert.Equal(t, bounds[1], rows[0]["wide"])
		}
	})
	for _, connectorType := range []string{"doris", "mariadb"} {
		for _, indexed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s indexed=%v", connectorType, indexed), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				cs := mock_interfaces.NewMockCatalogService(ctrl)
				cf := mock_interfaces.NewMockConnectorFactory(ctrl)
				lim := newResourceDataLocalIndexManagerMock(ctrl)
				service := &resourceDataService{cs: cs, cf: cf, lim: lim}
				resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{{Name: "wide", Type: interfaces.DataType_String, OriginalType: "VARCHAR", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword}}}, {Name: "payload", Type: interfaces.DataType_Json}}}
				if connectorType == "doris" {
					resource.SchemaDefinition[0].Type = interfaces.DataType_LargeInteger
					resource.SchemaDefinition[0].OriginalType = "LARGEINT"
				}
				if indexed {
					resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
					resource.LocalIndexName = "test-index"
				}
				params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{Field: "wide", Operation: "eq", ValueFrom: "const", Value: "+00100"}}
				wantValue := "+00100"
				wantRows := []map[string]any{{"wide": "100", "payload": map[string]any{"id": json.Number("9007199254740993")}}}
				cs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: connectorType}, nil)
				check := func(prepared *interfaces.ResourceDataQueryParams) {
					assert.Equal(t, wantValue, prepared.FilterCondCfg.Value)
					actual, ok := prepared.ActualFilterCond.(*filter_condition.EqualCond)
					require.True(t, ok)
					assert.Equal(t, wantValue, actual.Value)
					if connectorType == "doris" && !indexed {
						assert.Equal(t, interfaces.DataType_LargeInteger, actual.Lfield.Type)
					} else {
						assert.Equal(t, interfaces.DataType_String, actual.Lfield.Type)
					}
				}
				if indexed {
					lim.EXPECT().ListDocuments(gomock.Any(), resource.LocalIndexName, resource, gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _ *interfaces.Resource, prepared *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
						var resultSearchAfter3 []any

						check(prepared)
						resultSearchAfter3 = []any{"100"}
						return &interfaces.ResourceDataQueryResult{Entries: wantRows, TotalCount: 1, SearchAfter: resultSearchAfter3}, nil
					})
				} else {
					conn := mock_interfaces.NewMockTableConnector(ctrl)
					cf.EXPECT().CreateConnectorInstance(gomock.Any(), connectorType, gomock.Any()).Return(conn, nil)
					conn.EXPECT().Connect(gomock.Any()).Return(nil)
					conn.EXPECT().Close(gomock.Any()).Return(nil)
					conn.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).DoAndReturn(func(_ context.Context, _ *interfaces.Resource, prepared *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
						check(prepared)
						return &interfaces.ResourceDataQueryResult{Entries: wantRows, TotalCount: 1}, nil
					})
				}
				rowsResult19, err := service.QuerySourcePage(context.Background(), resource, params)
				var rows []map[string]any
				var total int64
				if rowsResult19 != nil {
					rows = rowsResult19.Entries
					total = rowsResult19.TotalCount
				}
				require.NoError(t, err)
				assert.Equal(t, int64(1), total)
				assert.Equal(t, wantRows, rows)
				if connectorType == "doris" {
					assert.Equal(t, "+00100", params.FilterCondCfg.Value)
				}
				if indexed {
					assert.Equal(t, []any{"100"}, rowsResult19.SearchAfter)
					assert.Empty(t, params.SearchAfter)
				}
			})
		}
	}
	t.Run("JSON content queries never execute", func(t *testing.T) {
		for _, cfg := range []*interfaces.FilterCondCfg{
			{Field: "payload.id", Operation: "eq", ValueFrom: "const", Value: "1"},
		} {
			ctrl := gomock.NewController(t)
			cs := mock_interfaces.NewMockCatalogService(ctrl)
			service := &resourceDataService{cs: cs, cf: mock_interfaces.NewMockConnectorFactory(ctrl), lim: newResourceDataLocalIndexManagerMock(ctrl)}
			resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "test-index", SchemaDefinition: []*interfaces.Property{{Name: "wide", Type: interfaces.DataType_LargeInteger, OriginalType: "LARGEINT"}, {Name: "payload", Type: interfaces.DataType_Json}}}
			cs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: "doris"}, nil)
			rowsResult20, err := service.QuerySourcePage(context.Background(), resource, &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg})
			var rows []map[string]any
			var total int64
			if rowsResult20 != nil {
				rows = rowsResult20.Entries
				total = rowsResult20.TotalCount
			}
			assert.Nil(t, rows)
			assert.Zero(t, total)
			var httpErr *httperrors.HTTPError
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
				service := &resourceDataService{cs: cs, cf: mock_interfaces.NewMockConnectorFactory(ctrl), lim: newResourceDataLocalIndexManagerMock(ctrl), ds: mock_interfaces.NewMockDatasetService(ctrl)}
				resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Category: category, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "test-index", SchemaDefinition: []*interfaces.Property{{Name: "payload", Type: interfaces.DataType_Json}}}
				cs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true, ConnectorType: "postgresql"}, nil)
				cfg := &interfaces.FilterCondCfg{Field: "payload.id", Operation: "eq", ValueFrom: "const", Value: "1"}
				params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg}
				if stored {
					params.FixedFilterCondCfg = cfg
				}
				rowsResult, err := service.QuerySourcePage(context.Background(), resource, params)
				var rows []map[string]any
				var total int64
				if rowsResult != nil {
					rows = rowsResult.Entries
					total = rowsResult.TotalCount
				}
				assert.Nil(t, rows)
				assert.Zero(t, total)
				if stored {
					var storedErr *interfaces.StoredConditionBuildError
					require.ErrorAs(t, err, &storedErr)
					require.NotNil(t, storedErr.Cause)
					assert.Contains(t, storedErr.Cause.Error(), "not found")
					return
				}
				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
			})
		}
	}
}

func TestNormalizeBinaryQueryValuesPreservesUnavailable(t *testing.T) {
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "blob", Type: interfaces.DataType_Binary}}}
	for _, mode := range []string{interfaces.BinaryModeMetadata, interfaces.BinaryModeContent} {
		t.Run(mode, func(t *testing.T) {
			unavailable := interfaces.ResourceValue{Mode: interfaces.ResourceValueModeUnavailable, Reason: interfaces.ResourceValueReasonNotRequested}
			entries := []map[string]any{{"blob": unavailable}, {"blob": nil}, {}}
			got, err := normalizeBinaryQueryValues(entries, resource, &interfaces.ResourceDataQueryParams{BinaryMode: &mode})
			require.NoError(t, err)
			assert.Equal(t, unavailable, got[0]["blob"])
			assert.Contains(t, got[1], "blob")
			assert.Nil(t, got[1]["blob"])
			assert.NotContains(t, got[2], "blob")
		})
	}
}

func TestResourceDataServiceQuerySourcePageVectorDimensions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		value      any
		downstream error
		status     int
	}{
		{"raw mismatch", []any{0.1, 0.2, 0.3}, nil, 400},
		{"matching raw vector", make([]float32, 1024), nil, 0},
		{"text vector", "query text", nil, 0},
		{"backend outage", make([]float32, 1024), errors.New("backend unavailable"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			catalogs := mock_interfaces.NewMockCatalogService(ctrl)
			index := newResourceDataLocalIndexManagerMock(ctrl)
			models := mock_interfaces.NewMockModelFactoryService(ctrl)
			resource := &interfaces.Resource{ID: "resource", Enabled: true, CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index", IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": float64(1024)}}}}}}
			field := "title_vector"
			if _, text := tc.value.(string); text {
				field = "title"
				model := &interfaces.SmallModel{ModelID: "model"}
				models.EXPECT().GetModelByID(gomock.Any(), "model").Return(model, nil)
				models.EXPECT().GetVector(gomock.Any(), model, []string{"query text"}).Return([]*interfaces.VectorResp{{Vector: make([]float32, 1024)}}, nil)
			}
			cfg := &interfaces.FilterCondCfg{Field: field, Operation: filter_condition.OperationKnnVector, ValueFrom: interfaces.ValueFrom_Const, Value: tc.value}
			params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg}
			catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{ID: "catalog", Enabled: true}, nil)
			if tc.name != "raw mismatch" {
				index.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).Return(&interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"title": "example"}}, TotalCount: int64(1)}, tc.downstream)
			}
			result, err := (&resourceDataService{cs: catalogs, lim: index, mfs: models}).QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, params)
			if tc.status == 0 {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, int64(1), result.TotalCount)
				require.Len(t, result.Entries, 1)
				assert.Equal(t, "title_vector", cfg.Field)
				return
			}
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tc.status, httpErr.HTTPCode)
			assert.Nil(t, result)
			if tc.status == 400 {
				assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.ErrorCode)
				assert.Contains(t, httpErr.ErrorDetails, "3")
				assert.Contains(t, httpErr.ErrorDetails, "1024")
			}
		})
	}
}

func TestResourceDataServiceQuerySourcePageLegacyVectorDimensions(t *testing.T) {
	for _, tc := range []struct {
		name           string
		text           bool
		backendFailure bool
	}{
		{"raw success", false, false}, {"text success", true, false},
		{"raw dimension failure", false, true}, {"model dimension failure", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			catalogs := mock_interfaces.NewMockCatalogService(ctrl)
			index := newResourceDataLocalIndexManagerMock(ctrl)
			models := mock_interfaces.NewMockModelFactoryService(ctrl)
			resource := &interfaces.Resource{ID: "resource", Enabled: true, CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index", IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}}}}}
			cfg := &interfaces.FilterCondCfg{Field: "title_vector", Operation: filter_condition.OperationKnnVector, ValueFrom: interfaces.ValueFrom_Const, Value: make([]float32, 3)}
			if tc.text {
				cfg.Field = "title"
				cfg.Value = "query text"
				model := &interfaces.SmallModel{ModelID: "model"}
				models.EXPECT().GetModelByID(gomock.Any(), "model").Return(model, nil)
				models.EXPECT().GetVector(gomock.Any(), model, []string{"query text"}).Return([]*interfaces.VectorResp{{Vector: make([]float32, 3)}}, nil)
			}
			params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg}
			catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{ID: "catalog", Enabled: true}, nil)
			var backendErr error
			expectedRows := []map[string]any{{"title": "example"}}
			if tc.backendFailure {
				backendErr = errors.New("search failed: [400 Bad Request] Query vector has invalid dimension: 3. Dimension should be: 1024")
			}
			index.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).Return(&interfaces.ResourceDataQueryResult{Entries: expectedRows, TotalCount: 1}, backendErr)
			result, err := (&resourceDataService{cs: catalogs, lim: index, mfs: models}).QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, params)
			if !tc.backendFailure {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, expectedRows, result.Entries)
				require.NotNil(t, result)
				assert.Equal(t, int64(1), result.TotalCount)
				return
			}
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
			assert.Equal(t, verrors.VegaBackend_Resource_InternalError, httpErr.ErrorCode)
			assert.Nil(t, result)
		})
	}
}

func TestQuerySourcePageTemporalConfigurationErrors(t *testing.T) {
	t.Setenv("TZ", "invalid/zone")
	for _, stored := range []bool{false, true} {
		ctrl := gomock.NewController(t)
		catalogs := mock_interfaces.NewMockCatalogService(ctrl)
		catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true}, nil)
		svc := &resourceDataService{cs: catalogs}
		field := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime}
		source := &interfaces.Resource{CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{field}}
		cfg := &interfaces.FilterCondCfg{Field: "created", Operation: ">=", Value: "2026-10-19 00:00:00"}
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: cfg}
		if stored {
			params.FixedFilterCondCfg = cfg
		}
		rowsResult, err := svc.QuerySourcePage(context.Background(), source, params)
		var rows []map[string]any
		var total int64
		if rowsResult != nil {
			rows = rowsResult.Entries
			total = rowsResult.TotalCount
		}
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_InternalError, httpErr.ErrorCode)
		assert.Nil(t, rows)
		assert.Zero(t, total)
	}
}

func TestQuerySourcePageHavingTemporalErrors(t *testing.T) {
	for _, tc := range []struct {
		name, zone string
		value      any
		status     int
	}{
		{"date-only datetime", "UTC", "2026-10-19", http.StatusBadRequest},
		{"invalid array element", "UTC", []any{"2026-10-19T00:00:00Z", "bad"}, http.StatusBadRequest},
		{"server timezone", "invalid/zone", "2026-10-19 00:00:00", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TZ", tc.zone)
			ctrl := gomock.NewController(t)
			catalogs := mock_interfaces.NewMockCatalogService(ctrl)
			catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true}, nil)
			svc := &resourceDataService{cs: catalogs}
			field := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime}
			source := &interfaces.Resource{CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{field}}
			params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "created", Aggr: "max"}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: tc.value}}
			rowsResult, err := svc.QuerySourcePage(context.Background(), source, params)
			var rows []map[string]any
			var total int64
			if rowsResult != nil {
				rows = rowsResult.Entries
				total = rowsResult.TotalCount
			}
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tc.status, httpErr.HTTPCode)
			assert.Nil(t, rows)
			assert.Zero(t, total)
		})
	}
}

func TestResourceDataServiceQuerySourcePageKeywordLimit(t *testing.T) {
	for _, stored := range []bool{false, true} {
		t.Run(fmt.Sprintf("stored=%v", stored), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			catalogs := mock_interfaces.NewMockCatalogService(ctrl)
			index := newResourceDataLocalIndexManagerMock(ctrl)
			catalogs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{Enabled: true}, nil)
			resource := &interfaces.Resource{Enabled: true, CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index", SchemaDefinition: []*interfaces.Property{{Name: "status", Type: interfaces.DataType_String}}}
			params := &interfaces.ResourceDataQueryParams{}
			var cause error = interfaces.NewKeywordLimitError("status", 256)
			if stored {
				cause = interfaces.NewStoredConditionBuildError(cause)
			}
			index.EXPECT().ListDocuments(gomock.Any(), "index", resource, params).Return(nil, fmt.Errorf("build: %w", cause))
			service := &resourceDataService{cs: catalogs, lim: index}
			if stored {
				page, err := service.QuerySourcePage(t.Context(), resource, params)
				var rows []map[string]any
				var total int64
				if page != nil {
					rows = page.Entries
					total = page.TotalCount
				}
				assert.Nil(t, rows)
				assert.Zero(t, total)
				require.ErrorIs(t, err, cause)
				return
			}
			result, err := service.QueryWithPaging(interfaces.WithTrustedProxyRead(t.Context()), resource, params)
			assert.Nil(t, result)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
			assert.Equal(t, "VegaBackend.Resource.QueryValueExceedsKeywordLimit", httpErr.ErrorCode)
			assert.Equal(t, map[string]any{"field": "status", "limit": 256}, httpErr.Metadata)
			body, marshalErr := json.Marshal(httpErr.BaseError)
			require.NoError(t, marshalErr)
			assert.Contains(t, string(body), `"metadata":{"field":"status","limit":256}`)
		})
	}
}
