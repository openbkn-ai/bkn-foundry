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
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
	"vega-backend/logics/filter_condition"
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

	t.Run("list documents answers 400 for a condition the index cannot build", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		// Wrapped the way the connector wraps it; this layer is the first to turn it into an
		// HTTPError, which has no Unwrap, so the classification must happen right here.
		cause := fmt.Errorf("failed to build filter query: %w",
			filter_condition.NewConditionBuildError("text field body has no keyword feature, cannot be used for comparison"))
		lim.EXPECT().ListDocuments(gomock.Any(), "dataset-1", resource, params).Return(nil, int64(0), cause)

		_, _, err := ds.ListDocuments(ctx, resource, params)

		var httpErr *rest.HTTPError
		require.True(t, errors.As(err, &httpErr))
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_InvalidParameter, httpErr.BaseError.ErrorCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "no keyword feature")
	})

	t.Run("get documents preserves positions for ignored missing documents", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().GetDocuments(gomock.Any(), "dataset-1", []string{"doc-1", "missing"}).
			Return([]map[string]any{{"id": "doc-1"}, nil}, nil)

		got, err := ds.GetDocuments(ctx, resource, []string{"doc-1", "missing"}, true)

		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": "doc-1"}, nil}, got)
	})

	t.Run("get documents allows resource-level query permission", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		ds := &datasetService{lim: lim, cs: cs, ps: ps}
		resource := &interfaces.Resource{ID: "dataset-1", CatalogID: "catalog-1", LocalIndexName: "dataset-1"}

		cs.EXPECT().InternalCatalogIDSet(gomock.Any()).Return(map[string]struct{}{}, nil)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			ID:   resource.ID,
		}, []string{interfaces.OPERATION_TYPE_QUERY_DATA}).Return(nil)
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
		cs := vmock.NewMockCatalogService(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		ds := &datasetService{cs: cs, ps: ps}
		denied := rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden)
		cs.EXPECT().InternalCatalogIDSet(gomock.Any()).Return(map[string]struct{}{}, nil)
		gomock.InOrder(
			ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
				Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
				ID:   resource.ID,
			}, []string{interfaces.OPERATION_TYPE_QUERY_DATA}).Return(denied),
			ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
				Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
				ID:   resource.CatalogID,
			}, []string{interfaces.OPERATION_TYPE_QUERY_DATA}).Return(denied),
		)

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

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	})

	t.Run("requires catalog resource_manage permission for document mutations", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		ds := &datasetService{lim: lim, cs: cs, ps: ps}
		denied := rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails("Access denied: insufficient permissions for[resource_manage]")
		cs.EXPECT().InternalCatalogIDSet(gomock.Any()).Return(map[string]struct{}{}, nil)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			ID:   resource.CatalogID,
		}, []string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}).Return(denied)

		err := ds.DeleteDocuments(ctx, resource, []string{"doc-1"}, false)

		require.ErrorIs(t, err, denied)
	})

	t.Run("delete by query wraps error", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().DeleteDocumentsByQuery(gomock.Any(), "dataset-1", resource, params).Return(errors.New("delete failed"))

		err := ds.DeleteDocumentsByQuery(ctx, resource, params)

		assertHTTPError(t, err)
		assert.Contains(t, err.Error(), "delete failed")
	})
}

func newDatasetServiceMock(t *testing.T) (*datasetService, *vmock.MockLocalIndexManager) {
	t.Helper()

	ctrl := gomock.NewController(t)
	lim := vmock.NewMockLocalIndexManager(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	ps := vmock.NewMockPermissionService(ctrl)
	cs.EXPECT().InternalCatalogIDSet(gomock.Any()).Return(map[string]struct{}{}, nil).AnyTimes()
	ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return &datasetService{lim: lim, cs: cs, ps: ps}, lim
}

func assertHTTPError(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.NotEmpty(t, httpErr.BaseError.ErrorCode)
}
