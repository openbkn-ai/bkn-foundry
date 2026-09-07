// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dataset

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
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

	t.Run("get documents preserves positions for ignored missing documents", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().GetDocuments(gomock.Any(), "dataset-1", []string{"doc-1", "missing"}).
			Return([]map[string]any{{"id": "doc-1"}, nil}, nil)

		got, err := ds.GetDocuments(ctx, resource, []string{"doc-1", "missing"}, true)

		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": "doc-1"}, nil}, got)
	})

	t.Run("get documents rejects missing documents by default", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().GetDocuments(gomock.Any(), "dataset-1", []string{"missing"}).Return([]map[string]any{nil}, nil)

		_, err := ds.GetDocuments(ctx, resource, []string{"missing"}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "document missing not found")
	})

	t.Run("delete documents", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().DeleteDocuments(gomock.Any(), "dataset-1", []string{"doc-1", "doc-2"}).Return(nil)

		require.NoError(t, ds.DeleteDocuments(ctx, resource, []string{"doc-1", "doc-2"}))
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
	return &datasetService{lim: lim}, lim
}

func assertHTTPError(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.NotEmpty(t, httpErr.BaseError.ErrorCode)
}
