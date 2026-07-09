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

	"github.com/openbkn-ai/bkn-comm-go/rest"
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
		lim.EXPECT().CreateIndex(gomock.Any(), "dataset-1", resource.SchemaDefinition).Return(nil)

		require.NoError(t, ds.Create(ctx, resource))
	})

	t.Run("create wraps index error", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().CreateIndex(gomock.Any(), "dataset-1", resource.SchemaDefinition).Return(errors.New("create failed"))

		err := ds.Create(ctx, resource)

		assertHTTPError(t, err)
		assert.Contains(t, err.Error(), "create failed")
	})

	t.Run("update uses historical source-id index name", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().UpdateIndex(gomock.Any(), "source-dataset-1", resource.SchemaDefinition).Return(nil)

		require.NoError(t, ds.Update(ctx, resource))
	})

	t.Run("delete skips missing index", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().CheckExist(gomock.Any(), "dataset-1").Return(false, nil)

		require.NoError(t, ds.Delete(ctx, "dataset-1"))
	})

	t.Run("delete existing index", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().CheckExist(gomock.Any(), "dataset-1").Return(true, nil)
		lim.EXPECT().DeleteIndex(gomock.Any(), "dataset-1").Return(nil)

		require.NoError(t, ds.Delete(ctx, "dataset-1"))
	})

	t.Run("check exist wraps error", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().CheckExist(gomock.Any(), "dataset-1").Return(false, errors.New("check failed"))

		got, err := ds.CheckExist(ctx, "dataset-1")

		assert.False(t, got)
		assertHTTPError(t, err)
	})
}

func TestDatasetServiceDocumentOperations(t *testing.T) {
	ctx := context.Background()
	resource := &interfaces.Resource{ID: "dataset-1"}
	params := &interfaces.ResourceDataQueryParams{}
	docs := []map[string]any{{"id": 1}}

	t.Run("list documents", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().ListDocuments(gomock.Any(), "dataset-1", resource, params).Return(docs, int64(1), nil)

		got, total, err := ds.ListDocuments(ctx, "dataset-1", resource, params)

		require.NoError(t, err)
		assert.Equal(t, docs, got)
		assert.Equal(t, int64(1), total)
	})

	t.Run("create documents", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().CreateDocuments(gomock.Any(), "dataset-1", docs).Return([]string{"doc-1"}, nil)

		got, err := ds.CreateDocuments(ctx, "dataset-1", docs)

		require.NoError(t, err)
		assert.Equal(t, []string{"doc-1"}, got)
	})

	t.Run("get document", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		doc := map[string]any{"id": 1}
		lim.EXPECT().GetDocument(gomock.Any(), "dataset-1", "doc-1").Return(doc, nil)

		got, err := ds.GetDocument(ctx, "dataset-1", "doc-1")

		require.NoError(t, err)
		assert.Equal(t, doc, got)
	})

	t.Run("delete document", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().DeleteDocument(gomock.Any(), "dataset-1", "doc-1").Return(nil)

		require.NoError(t, ds.DeleteDocument(ctx, "dataset-1", "doc-1"))
	})

	t.Run("upsert documents returns raw error by current contract", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().UpsertDocuments(gomock.Any(), "dataset-1", docs).Return([]string{"doc-1"}, errors.New("upsert failed"))

		got, err := ds.UpsertDocuments(ctx, "dataset-1", docs)

		require.Error(t, err)
		assert.Equal(t, []string{"doc-1"}, got)
		assert.Contains(t, err.Error(), "upsert failed")
	})

	t.Run("delete documents", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().DeleteDocuments(gomock.Any(), "dataset-1", "doc-1,doc-2").Return(nil)

		require.NoError(t, ds.DeleteDocuments(ctx, "dataset-1", "doc-1,doc-2"))
	})

	t.Run("delete by query wraps error", func(t *testing.T) {
		ds, lim := newDatasetServiceMock(t)
		lim.EXPECT().DeleteDocumentsByQuery(gomock.Any(), "dataset-1", resource, params).Return(errors.New("delete failed"))

		err := ds.DeleteDocumentsByQuery(ctx, "dataset-1", resource, params)

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
