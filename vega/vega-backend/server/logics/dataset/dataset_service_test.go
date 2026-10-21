// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dataset

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

func TestDatasetServiceIndexLifecycle(t *testing.T) {
	ctx := context.Background()
	resource := &interfaces.Resource{
		ID:               "dataset-1",
		SourceIdentifier: "source",
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: "integer"}},
	}

	t.Run("create", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().CreateIndex(gomock.Any(), gomock.Any(), resource.SchemaDefinition, map[string]string{"resource_id": "dataset-1"}).
			DoAndReturn(func(_ context.Context, indexName string, _ []*interfaces.Property, _ map[string]string) error {
				assert.Regexp(t, `^vega-dataset-[0-9a-f-]+$`, indexName)
				return nil
			})

		require.NoError(t, ds.Create(ctx, resource))
		assert.Regexp(t, `^vega-dataset-[0-9a-f-]+$`, resource.LocalIndexName)
		assert.Equal(t, interfaces.ResourceLocalIndexStatusAvailable, resource.LocalIndexStatus)
	})

	t.Run("create persists disabled JSON indexing", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		resource := &interfaces.Resource{ID: "dataset-json", SchemaDefinition: []*interfaces.Property{
			{Name: "payload", Type: interfaces.DataType_Json, Attributes: map[string]any{"enabled": true, "source": "jsonb"}},
		}}
		lim.EXPECT().CreateIndex(gomock.Any(), gomock.Any(), gomock.Any(), map[string]string{"resource_id": "dataset-json"}).
			DoAndReturn(func(_ context.Context, _ string, schema []*interfaces.Property, _ map[string]string) error {
				assert.Equal(t, map[string]any{"enabled": false, "source": "jsonb"}, schema[0].Attributes)
				return nil
			})
		require.NoError(t, ds.Create(ctx, resource))
		assert.Equal(t, false, resource.SchemaDefinition[0].Attributes["enabled"])
	})

	t.Run("create wraps index error", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().CreateIndex(gomock.Any(), gomock.Any(), resource.SchemaDefinition, map[string]string{"resource_id": "dataset-1"}).Return(errors.New("create failed"))

		err := ds.Create(ctx, resource)

		assertHTTPError(t, err)
		assert.Contains(t, err.Error(), "create failed")
	})

	t.Run("update uses historical source-id index name", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		resource.LocalIndexName = "vega-dataset-index-1"
		lim.EXPECT().UpdateIndex(gomock.Any(), "vega-dataset-index-1", resource.SchemaDefinition).Return(nil)

		require.NoError(t, ds.Update(ctx, resource))
	})

	t.Run("delete skips missing index", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		resource.LocalIndexName = "dataset-1"
		lim.EXPECT().CheckIndexExist(gomock.Any(), resource.LocalIndexName).Return(false, nil)

		require.NoError(t, ds.Delete(ctx, resource))
	})

	t.Run("delete existing index", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		resource.LocalIndexName = "dataset-1"
		lim.EXPECT().CheckIndexExist(gomock.Any(), resource.LocalIndexName).Return(true, nil)
		lim.EXPECT().DeleteIndex(gomock.Any(), resource.LocalIndexName).Return(nil)

		require.NoError(t, ds.Delete(ctx, resource))
	})
}

func TestDatasetServiceDocumentOperations(t *testing.T) {
	ctx := context.Background()
	resource := &interfaces.Resource{ID: "dataset-1", LocalIndexName: "dataset-1"}
	params := &interfaces.ResourceDataQueryParams{}
	docs := []map[string]any{{"id": 1}}

	t.Run("list documents", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().ListDocuments(gomock.Any(), "dataset-1", resource, params).Return(docs, int64(1), nil)

		got, total, err := ds.ListDocuments(ctx, resource, params)

		require.NoError(t, err)
		assert.Equal(t, docs, got)
		assert.Equal(t, int64(1), total)
	})

	t.Run("count documents requests an exact total", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().ListDocuments(gomock.Any(), "dataset-1", resource, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ *interfaces.Resource, got *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
				assert.True(t, got.NeedTotal)
				assert.Equal(t, 1, got.Paging.Limit)
				return nil, 42, nil
			})

		total, err := ds.CountDocuments(ctx, resource)

		require.NoError(t, err)
		assert.Equal(t, int64(42), total)
	})

	t.Run("list documents preserves keyword limit code and metadata", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().ListDocuments(gomock.Any(), "dataset-1", resource, params).Return(nil, int64(0), fmt.Errorf("build: %w", interfaces.NewKeywordLimitError("status", 256)))
		rows, total, err := ds.ListDocuments(ctx, resource, params)
		assert.Nil(t, rows)
		assert.Zero(t, total)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, "VegaBackend.Resource.QueryValueExceedsKeywordLimit", httpErr.ErrorCode)
		assert.Equal(t, map[string]any{"field": "status", "limit": 256}, httpErr.Metadata)
	})

	t.Run("list documents answers 400 for a condition the index cannot build", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		cause := fmt.Errorf("failed to build filter query: %w",
			interfaces.NewConditionBuildError("text field body has no keyword feature; re-save the resource configuration and rebuild the local index, or use match"))
		lim.EXPECT().ListDocuments(gomock.Any(), "dataset-1", resource, params).Return(nil, int64(0), cause)

		_, _, err := ds.ListDocuments(ctx, resource, params)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.ErrorCode)
		assert.Contains(t, httpErr.ErrorDetails, "re-save the resource configuration")
	})

	t.Run("get documents preserves positions for ignored missing documents", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().GetDocuments(gomock.Any(), "dataset-1", []string{"doc-1", "missing"}).
			Return([]map[string]any{{"id": "doc-1"}, nil}, nil)

		got, err := ds.GetDocuments(ctx, resource, []string{"doc-1", "missing"}, true)

		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": "doc-1"}, nil}, got)
	})

	t.Run("get documents preserves dataset JSON types", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		resource := &interfaces.Resource{ID: "dataset-1", LocalIndexName: "dataset-1", Category: interfaces.ResourceCategoryDataset,
			SchemaDefinition: []*interfaces.Property{{Name: "payload", Type: interfaces.DataType_Json}}}
		values := []any{map[string]any{"id": "creator"}, []any{"value", true}, int64(42), true, "{incomplete", nil}
		documents := make([]map[string]any, len(values)+1)
		ids := make([]string, len(documents))
		for i := range documents {
			ids[i] = fmt.Sprintf("doc-%d", i)
			if i < len(values) {
				documents[i] = map[string]any{"payload": values[i]}
			}
		}
		lim.EXPECT().GetDocuments(gomock.Any(), "dataset-1", ids).Return(documents, nil)

		got, err := ds.GetDocuments(ctx, resource, ids, true)

		require.NoError(t, err)
		require.Len(t, got, len(documents))
		for i, value := range values {
			assert.Equal(t, value, got[i]["payload"])
		}
		assert.Nil(t, got[len(values)])
	})

	t.Run("get documents allows resource-level query permission", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		ds := &datasetService{lim: lim, rs: rs}
		resource := &interfaces.Resource{ID: "dataset-1", CatalogID: "catalog-1", LocalIndexName: "dataset-1"}

		rs.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID,
			interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
		lim.EXPECT().GetDocuments(gomock.Any(), resource.LocalIndexName, []string{"doc-1"}).
			Return([]map[string]any{{"id": "doc-1"}}, nil)

		documents, err := ds.GetDocuments(ctx, resource, []string{"doc-1"}, false)

		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": "doc-1"}}, documents)
	})

	t.Run("get documents rejects missing documents by default", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().GetDocuments(gomock.Any(), "dataset-1", []string{"missing"}).Return([]map[string]any{nil}, nil)

		_, err := ds.GetDocuments(ctx, resource, []string{"missing"}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "document missing not found")
	})

	t.Run("get documents requires query_data permission", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ds := &datasetService{rs: rs}
		denied := httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID,
			interfaces.OPERATION_TYPE_QUERY_DATA).Return(denied)

		_, err := ds.GetDocuments(ctx, resource, []string{"doc-1"}, false)

		require.ErrorIs(t, err, denied)
	})

	t.Run("delete documents", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().GetDocuments(gomock.Any(), "dataset-1", []string{"doc-1", "doc-2"}).
			Return([]map[string]any{{"id": "doc-1"}, {"id": "doc-2"}}, nil)
		lim.EXPECT().DeleteDocuments(gomock.Any(), "dataset-1", []string{"doc-1", "doc-2"}).Return(nil)

		require.NoError(t, ds.DeleteDocuments(ctx, resource, []string{"doc-1", "doc-2"}, false))
	})

	t.Run("tolerant delete skips missing documents", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().GetDocuments(gomock.Any(), "dataset-1", []string{"doc-1", "missing"}).
			Return([]map[string]any{{"id": "doc-1"}, nil}, nil)
		lim.EXPECT().DeleteDocuments(gomock.Any(), "dataset-1", []string{"doc-1"}).Return(nil)

		require.NoError(t, ds.DeleteDocuments(ctx, resource, []string{"doc-1", "missing"}, true))
	})

	t.Run("tolerant delete succeeds when all documents are missing", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().GetDocuments(gomock.Any(), "dataset-1", []string{"missing"}).Return([]map[string]any{nil}, nil)

		require.NoError(t, ds.DeleteDocuments(ctx, resource, []string{"missing"}, true))
	})

	t.Run("creates a document with a service generated id", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().CreateDocuments(gomock.Any(), "dataset-1", []map[string]any{{"title": "one"}}).
			Return([]string{"doc-1"}, nil)

		docID, err := ds.CreateDocument(ctx, resource, map[string]any{"title": "one"})

		require.NoError(t, err)
		assert.Equal(t, "doc-1", docID)
	})

	t.Run("rejects caller supplied document id before materializing or writing", func(t *testing.T) {
		ds, _ := newDatasetServiceMock(t)

		_, err := ds.CreateDocument(ctx, resource, map[string]any{"_id": "doc-1"})

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	})

	t.Run("requires data_write permission for document mutations", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		ds := &datasetService{lim: lim, rs: rs}
		denied := httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("Access denied: insufficient permissions for[data_write]")
		rs.EXPECT().CheckResourcePermission(gomock.Any(), resource.ID,
			interfaces.OPERATION_TYPE_DATA_WRITE).Return(denied)

		err := ds.DeleteDocuments(ctx, resource, []string{"doc-1"}, false)

		require.ErrorIs(t, err, denied)
	})

	t.Run("delete by query rejects stored JSON as missing field", func(t *testing.T) {
		ds, _ := newDatasetServiceMock(t)
		resource := &interfaces.Resource{ID: "dataset-1", LocalIndexName: "dataset-1", SchemaDefinition: []*interfaces.Property{{Name: "payload", Type: interfaces.DataType_Json}}}
		for _, operation := range []string{filter_condition.OperationNotExist, filter_condition.OperationNull} {
			_, err := ds.DeleteDocumentsByQuery(ctx, resource, &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{Name: "payload", Operation: operation}})
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
			assert.Contains(t, httpErr.ErrorDetails, "not found")
		}
	})

	t.Run("delete by query wraps error", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		resource := &interfaces.Resource{
			ID:             "dataset-1",
			LocalIndexName: "dataset-1",
			SchemaDefinition: []*interfaces.Property{{
				Name: "id",
				Type: interfaces.DataType_Integer,
			}},
		}
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{
			Name:      "id",
			Operation: filter_condition.OperationEqual,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     1,
		}}
		lim.EXPECT().DeleteDocumentsByQuery(gomock.Any(), "dataset-1", resource, params).Return(nil, errors.New("delete failed"))

		_, err := ds.DeleteDocumentsByQuery(ctx, resource, params)

		assertHTTPError(t, err)
		assert.Contains(t, err.Error(), "completion is unknown")
		assert.NotContains(t, err.Error(), "delete failed")
	})

	t.Run("delete by query defaults nested value sources before index access", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		resource := &interfaces.Resource{
			ID: "dataset-1", LocalIndexName: "dataset-1",
			SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_String}},
		}
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationAnd,
			SubConds: []*interfaces.FilterCondCfg{{
				Name: "title", Operation: filter_condition.OperationLike,
				Value: "old",
			}},
		}}
		lim.EXPECT().DeleteDocumentsByQuery(gomock.Any(), "dataset-1", resource, params).
			DoAndReturn(func(_ context.Context, _ string, _ *interfaces.Resource, got *interfaces.ResourceDataQueryParams) (*interfaces.DeleteByQueryResult, error) {
				assert.Equal(t, interfaces.ValueFrom_Const, got.FilterCondCfg.SubConds[0].ValueFrom)
				require.NotNil(t, got.ActualFilterCond)
				return &interfaces.DeleteByQueryResult{}, nil
			})

		_, err := ds.DeleteDocumentsByQuery(ctx, resource, params)
		require.NoError(t, err)
	})

	t.Run("delete by query distinguishes invalid operations before index access", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			operation string
			nested    bool
			code      string
		}{
			{"unsupported root", "unknown", false, verrors.VegaBackend_UnsupportFilterConditionOperation},
			{"unsupported child", "unknown", true, verrors.VegaBackend_UnsupportFilterConditionOperation},
			{"missing root", "", false, verrors.VegaBackend_NullParameter_FilterConditionOperation},
			{"missing child", "", true, verrors.VegaBackend_NullParameter_FilterConditionOperation},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ds, _ := newDatasetServiceMock(t)
				resource := &interfaces.Resource{ID: "dataset-1", LocalIndexName: "dataset-1"}
				condition := &interfaces.FilterCondCfg{
					Name: "title", Operation: tc.operation,
					Value: "old",
				}
				if tc.nested {
					condition = &interfaces.FilterCondCfg{
						Operation: filter_condition.OperationAnd, SubConds: []*interfaces.FilterCondCfg{condition},
					}
				}
				params := &interfaces.ResourceDataQueryParams{FilterCondCfg: condition}

				_, err := ds.DeleteDocumentsByQuery(ctx, resource, params)

				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
				assert.Equal(t, tc.code, httpErr.ErrorCode)
				assert.Nil(t, params.ActualFilterCond)
			})
		}
	})

	t.Run("delete by query rejects unsupported value source before index access", func(t *testing.T) {
		ds, _ := newDatasetServiceMock(t)
		resource := &interfaces.Resource{
			ID: "dataset-1", LocalIndexName: "dataset-1",
			SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_String}},
		}
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{
			Name: "title", Operation: filter_condition.OperationEqual,
			ValueFrom: "unknown", Value: "old",
		}}

		_, err := ds.DeleteDocumentsByQuery(ctx, resource, params)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_InvalidParameter_FilterConditionValueFrom, httpErr.ErrorCode)
		assert.Contains(t, httpErr.ErrorDetails, "value_from")
		assert.Nil(t, params.ActualFilterCond)
	})

	t.Run("delete by query rejects an empty filter without calling the index", func(t *testing.T) {
		ds, _ := newDatasetServiceMock(t)

		_, err := ds.DeleteDocumentsByQuery(ctx, resource, &interfaces.ResourceDataQueryParams{
			FilterCondCfg: &interfaces.FilterCondCfg{},
		})

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.ErrorDetails, "non-empty filter")
	})

	for _, operation := range []string{filter_condition.OperationAnd, filter_condition.OperationOr} {
		t.Run("delete by query rejects an "+operation+" condition containing only empty filters", func(t *testing.T) {
			ds, _ := newDatasetServiceMock(t)

			_, err := ds.DeleteDocumentsByQuery(ctx, resource, &interfaces.ResourceDataQueryParams{
				FilterCondCfg: &interfaces.FilterCondCfg{
					Operation: operation,
					SubConds:  []*interfaces.FilterCondCfg{{}},
				},
			})

			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
			assert.Contains(t, httpErr.ErrorDetails, "no effective child conditions")
		})
	}

	t.Run("delete by query resolves resource generated fields before local index access", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		resource := &interfaces.Resource{
			ID:             "dataset-1",
			LocalIndexName: "dataset-1",
			SchemaDefinition: []*interfaces.Property{{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
				}},
			}},
		}
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{
			Name:      "content_vector",
			Operation: filter_condition.OperationKnnVector,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     []float32{0.1, 0.2},
		}}
		lim.EXPECT().DeleteDocumentsByQuery(gomock.Any(), "dataset-1", resource, params).
			DoAndReturn(func(_ context.Context, _ string, _ *interfaces.Resource, got *interfaces.ResourceDataQueryParams) (*interfaces.DeleteByQueryResult, error) {
				require.NotNil(t, got.ActualFilterCond)
				assert.Equal(t, filter_condition.OperationKnnVector, got.ActualFilterCond.GetOperation())
				return &interfaces.DeleteByQueryResult{}, nil
			})

		_, err := ds.DeleteDocumentsByQuery(ctx, resource, params)
		require.NoError(t, err)
	})

	t.Run("delete by query returns bad request for an invalid condition", func(t *testing.T) {
		ds, _ := newDatasetServiceMock(t)
		resource := &interfaces.Resource{
			ID:             "dataset-1",
			LocalIndexName: "dataset-1",
			SchemaDefinition: []*interfaces.Property{{
				Name: "content",
				Type: interfaces.DataType_Text,
			}},
		}
		params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{
			Name:      "missing",
			Operation: filter_condition.OperationEqual,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     "value",
		}}

		_, err := ds.DeleteDocumentsByQuery(ctx, resource, params)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.ErrorCode)
	})
}

func newDatasetServiceMock(t *testing.T) (*datasetService, *vmock.MockLocalIndexManager) {
	t.Helper()

	ctrl := gomock.NewController(t)
	lim := vmock.NewMockLocalIndexManager(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	rs.EXPECT().CheckResourcePermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return &datasetService{lim: lim, rs: rs}, lim
}

func assertHTTPError(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)
	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.NotEmpty(t, httpErr.ErrorCode)
}

func TestDatasetServiceBulkReplaceDocuments(t *testing.T) {
	ctx := context.Background()
	t.Run("uses supplied resource and preserves individual results", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		ds := &datasetService{lim: lim}
		resource := &interfaces.Resource{ID: "dataset", Category: interfaces.ResourceCategoryDataset, LocalIndexName: "index"}

		input := []map[string]any{
			{"_id": "ok", "new": "value"},
			{"_id": "conflict"},
		}
		lim.EXPECT().IndexDocuments(ctx, "index", []interfaces.DocumentWrite{{ID: "ok", Document: map[string]any{"new": "value"}}, {ID: "conflict", Document: map[string]any{}}}).Return([]interfaces.DocumentWriteResult{
			{ID: "ok", Status: 201}, {ID: "conflict", Status: 409, ErrorCode: interfaces.DocumentConflict},
		}, nil)
		results, err := ds.BulkReplaceDocuments(ctx, resource, input)
		require.NoError(t, err)
		require.Len(t, results, 2)
		assert.Equal(t, 201, results[0].Status)
		assert.Equal(t, interfaces.DocumentConflict, results[1].ErrorCode)
		assert.Equal(t, map[string]any{"_id": "ok", "new": "value"}, input[0])
	})

	t.Run("duplicate IDs reject entire batch before inference or writes", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		mfs := vmock.NewMockModelFactoryService(ctrl)
		ds := &datasetService{lim: lim, mfs: mfs}
		resource := &interfaces.Resource{LocalIndexName: "index", IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}}}}}
		result, err := ds.BulkReplaceDocuments(ctx, resource, []map[string]any{
			{"_id": "valid", "content": "hello"}, {"_id": "duplicate"}, {"_id": "duplicate"},
		})
		require.Nil(t, result)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, 400, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_InvalidParameter_RequestBody, httpErr.ErrorCode)
	})
	t.Run("write error is returned for entire request", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		resource := &interfaces.Resource{Category: interfaces.ResourceCategoryDataset, LocalIndexName: "index"}

		writeErr := errors.New("index unavailable")
		lim.EXPECT().IndexDocuments(ctx, "index", gomock.Any()).Return(nil, writeErr)
		results, err := (&datasetService{lim: lim}).BulkReplaceDocuments(ctx, resource, []map[string]any{{"_id": "doc"}})
		require.ErrorIs(t, err, writeErr)
		assert.Nil(t, results)
	})

	for _, tc := range []struct {
		name  string
		entry map[string]any
	}{
		{"null entry", nil}, {"missing ID", map[string]any{}}, {"null ID", map[string]any{"_id": nil}},
		{"numeric ID", map[string]any{"_id": 123}}, {"empty ID", map[string]any{"_id": ""}},
		{"blank ID", map[string]any{"_id": " \t\n"}},
		{"oversized ID", map[string]any{"_id": strings.Repeat("a", 513)}},
		{"oversized UTF8 ID", map[string]any{"_id": strings.Repeat("界", 171)}},
		{"invalid UTF8 ID", map[string]any{"_id": string([]byte{0xff})}},
	} {
		t.Run(tc.name+" rejects entire batch", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ds := &datasetService{lim: vmock.NewMockLocalIndexManager(ctrl), mfs: vmock.NewMockModelFactoryService(ctrl)}
			resource := &interfaces.Resource{LocalIndexName: "index", IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}}}}}
			results, err := ds.BulkReplaceDocuments(ctx, resource, []map[string]any{{"_id": "valid", "content": "hello"}, tc.entry})
			assert.Nil(t, results)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, 400, httpErr.HTTPCode)
			assert.Equal(t, verrors.VegaBackend_InvalidParameter_RequestBody, httpErr.ErrorCode)
		})
	}

	t.Run("constructs writes from materialized documents", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		mfs := vmock.NewMockModelFactoryService(ctrl)
		ds := &datasetService{lim: lim, mfs: mfs}
		resource := &interfaces.Resource{LocalIndexName: "index", IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}}}}}
		model := &interfaces.SmallModel{ModelID: "model"}
		mfs.EXPECT().GetModelByID(ctx, "model").Return(model, nil)
		mfs.EXPECT().GetVector(ctx, model, []string{"hello"}).Return([]*interfaces.VectorResp{{Vector: []float32{1, 2}}}, nil)
		lim.EXPECT().IndexDocuments(ctx, "index", []interfaces.DocumentWrite{{ID: "doc", Document: map[string]any{"content": "hello", "content_vector": []float32{1, 2}}}}).Return([]interfaces.DocumentWriteResult{{ID: "doc", Status: 201}}, nil)
		entries := []map[string]any{{"_id": "doc", "content": "hello"}}
		results, err := ds.BulkReplaceDocuments(ctx, resource, entries)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, 201, results[0].Status)
		assert.NotContains(t, entries[0], "content_vector")
	})

	for _, failure := range []string{"invalid vector", "registry", "inference"} {
		t.Run(failure+" prevents entire batch write", func(t *testing.T) {
			core, logs := observer.New(zap.ErrorLevel)
			sharedLogger := logger.GetLogger()
			original := *sharedLogger
			*sharedLogger = *zap.New(core).Sugar()
			t.Cleanup(func() { *sharedLogger = original })
			ctrl := gomock.NewController(t)
			lim := vmock.NewMockLocalIndexManager(ctrl)
			mfs := vmock.NewMockModelFactoryService(ctrl)
			resource := &interfaces.Resource{LocalIndexName: "index", IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}}}}}
			entries := []map[string]any{{"_id": "explicit", "content_vector": []float32{1, 2}}, {"_id": "infer", "content": "hello"}}
			status := 500
			if failure == "invalid vector" {
				entries = append(entries, map[string]any{"_id": "bad", "content_vector": []float32{1}})
				status = 400
			} else {
				model := &interfaces.SmallModel{ModelID: "model"}
				if failure == "registry" {
					mfs.EXPECT().GetModelByID(ctx, "model").Return(nil, errors.New("secret registry detail"))
				} else {
					mfs.EXPECT().GetModelByID(ctx, "model").Return(model, nil)
					mfs.EXPECT().GetVector(ctx, model, []string{"hello"}).Return(nil, errors.New("secret model detail"))
				}
			}
			results, err := (&datasetService{lim: lim, mfs: mfs}).BulkReplaceDocuments(ctx, resource, entries)
			assert.Nil(t, results)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, status, httpErr.HTTPCode)
			if status == 400 {
				assert.Empty(t, logs.All())
			} else {
				require.Len(t, logs.All(), 1)
				assert.Contains(t, logs.All()[0].Message, "secret")
			}
			assert.NotContains(t, httpErr.Error(), "secret")
			assert.NotContains(t, entries[1], "content_vector")
		})
	}

}

func TestDatasetServiceDeleteDocumentsByQueryIncompleteResult(t *testing.T) {
	ds, lim := newDatasetServiceMock(t)
	resource := &interfaces.Resource{ID: "dataset", LocalIndexName: "index", SchemaDefinition: []*interfaces.Property{{Name: "kind", Type: interfaces.DataType_String}}}
	params := &interfaces.ResourceDataQueryParams{FilterCondCfg: &interfaces.FilterCondCfg{Name: "kind", Operation: filter_condition.OperationEqual2, ValueOptCfg: interfaces.ValueOptCfg{Value: "old"}}}
	progress := &interfaces.DeleteByQueryResult{Deleted: 2, TimedOut: true, VersionConflicts: 1, Failures: []interfaces.DocumentWriteResult{{ID: "", Status: 400, ErrorCode: interfaces.DocumentInvalid}}}
	lim.EXPECT().DeleteDocumentsByQuery(gomock.Any(), "index", resource, params).Return(progress, interfaces.NewDeleteByQueryIncompleteError(progress))
	result, err := ds.DeleteDocumentsByQuery(context.Background(), resource, params)
	require.Same(t, progress, result)
	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, verrors.VegaBackend_Dataset_DeleteIncomplete, httpErr.ErrorCode)
	assert.Equal(t, 500, httpErr.HTTPCode)
	assert.Same(t, progress, httpErr.ErrorDetails)
}

func TestDatasetServiceReplaceDocumentChecksIndexResults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results []interfaces.DocumentWriteResult
		err     error
		ok      bool
	}{
		{"success", []interfaces.DocumentWriteResult{{ID: "doc", Status: 201}}, nil, true},
		{"item failure", []interfaces.DocumentWriteResult{{ID: "doc", Status: 429}}, nil, false},
		{"missing result", nil, nil, false},
		{"wrong ID", []interfaces.DocumentWriteResult{{ID: "other", Status: 201}}, nil, false},
		{"request failure", nil, errors.New("secret index detail"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rs := vmock.NewMockResourceService(ctrl)
			lim := vmock.NewMockLocalIndexManager(ctrl)
			resource := &interfaces.Resource{ID: "dataset", LocalIndexName: "index"}
			rs.EXPECT().CheckResourcePermission(gomock.Any(), "dataset", interfaces.OPERATION_TYPE_DATA_WRITE).Return(nil)
			lim.EXPECT().IndexDocuments(gomock.Any(), "index", []interfaces.DocumentWrite{{ID: "doc", Document: map[string]any{"content": "hello"}}}).Return(tc.results, tc.err)
			err := (&datasetService{rs: rs, lim: lim}).ReplaceDocument(context.Background(), resource, "doc", map[string]any{"content": "hello"})
			if tc.ok {
				require.NoError(t, err)
			} else {
				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, 500, httpErr.HTTPCode)
				assert.NotContains(t, httpErr.Error(), "secret")
			}
		})
	}
}
