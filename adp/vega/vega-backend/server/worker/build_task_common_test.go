// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
	"vega-backend/logics"
)

func TestUpdateResourceIndexName(t *testing.T) {
	t.Run("updates empty old index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		resource := &interfaces.Resource{ID: "r1"}

		rs.EXPECT().InternalUpdateLocalIndexName(gomock.Any(), nil, "r1", "new-index").DoAndReturn(func(_ context.Context, _ *sql.Tx, id, indexName string) error {
			assert.Equal(t, "r1", id)
			assert.Equal(t, "new-index", indexName)
			return nil
		})

		require.NoError(t, updateResourceIndexName(context.Background(), resource, rs, "new-index"))
	})

	t.Run("skips unchanged index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		resource := &interfaces.Resource{ID: "r1", LocalIndexName: "same-index"}

		require.NoError(t, updateResourceIndexName(context.Background(), resource, rs, "same-index"))
	})

	t.Run("keeps old index after update failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		resource := &interfaces.Resource{ID: "r1", LocalIndexName: "old-index"}

		rs.EXPECT().InternalUpdateLocalIndexName(gomock.Any(), nil, "r1", "new-index").DoAndReturn(func(_ context.Context, _ *sql.Tx, id, indexName string) error {
			assert.Equal(t, "r1", id)
			assert.Equal(t, "new-index", indexName)
			return errors.New("update failed")
		})

		err := updateResourceIndexName(context.Background(), resource, rs, "new-index")

		require.Error(t, err)
		assert.Equal(t, "old-index", resource.LocalIndexName)
	})
}

func TestGenerateDocumentID(t *testing.T) {
	keys := []interfaces.KeyValue{{Key: "tenant_id", Value: "tenant-1"}, {Key: "id", Value: 42}}

	docID, err := generateDocumentID(keys)
	require.NoError(t, err)
	assert.Len(t, docID, 64)

	sameDocID, err := generateDocumentID(keys)
	require.NoError(t, err)
	assert.Equal(t, docID, sameDocID)

	reorderedDocID, err := generateDocumentID([]interfaces.KeyValue{{Key: "id", Value: 42}, {Key: "tenant_id", Value: "tenant-1"}})
	require.NoError(t, err)
	assert.NotEqual(t, docID, reorderedDocID)

	documentKeys, err := extractKeyValues([]string{"tenant_id", "id"}, map[string]any{"tenant_id": "tenant-2", "id": 99})
	require.NoError(t, err)
	docID, err = generateDocumentID(documentKeys)
	require.NoError(t, err)
	assert.Len(t, docID, 64)

	first, err := generateDocumentID([]interfaces.KeyValue{{Key: "tenant_id", Value: "a-b"}, {Key: "id", Value: "c"}})
	require.NoError(t, err)
	second, err := generateDocumentID([]interfaces.KeyValue{{Key: "tenant_id", Value: "a"}, {Key: "id", Value: "b-c"}})
	require.NoError(t, err)
	assert.NotEqual(t, first, second)

	_, err = extractKeyValues([]string{"tenant_id", "id"}, map[string]any{"tenant_id": "tenant-2"})
	require.ErrorContains(t, err, `build key field "id" is missing`)
}

func TestKeyValueJSONUsesOrderedCursorFormat(t *testing.T) {
	mark, err := json.Marshal([]interfaces.KeyValue{
		{Key: "id", Value: 42},
		{Key: "tenant_id", Value: "tenant-1"},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `[{"key":"id","value":42},{"key":"tenant_id","value":"tenant-1"}]`, string(mark))

	var restored []interfaces.KeyValue
	require.NoError(t, json.Unmarshal(mark, &restored))
	require.Len(t, restored, 2)
	assert.Equal(t, "id", restored[0].Key)
	assert.Equal(t, float64(42), restored[0].Value)
	assert.Equal(t, "tenant_id", restored[1].Key)
	assert.Equal(t, "tenant-1", restored[1].Value)
}

func TestCompleteBuildTaskWithoutEmbedding(t *testing.T) {
	t.Run("completes task and resource update atomically", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ts := vmock.NewMockBuildTaskService(ctrl)
		resource := &interfaces.Resource{ID: "r1", LocalIndexName: "old-index"}

		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		mock.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalUpdateLocalIndexName(gomock.Any(), txMatcher, "r1", "new-index").
			DoAndReturn(func(_ context.Context, _ *sql.Tx, id, indexName string) error {
				assert.Equal(t, "r1", id)
				assert.Equal(t, "new-index", indexName)
				return nil
			})
		ts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, "t1").Return(true, nil)
		mock.ExpectCommit()

		err = completeBuildTaskWithoutEmbedding(context.Background(), resource, rs, ts, "t1", "new-index")

		require.NoError(t, err)
		assert.Equal(t, "new-index", resource.LocalIndexName)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("finishes concurrent stop request", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ts := vmock.NewMockBuildTaskService(ctrl)
		resource := &interfaces.Resource{ID: "r1", LocalIndexName: "old-index"}

		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		mock.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalUpdateLocalIndexName(gomock.Any(), txMatcher, "r1", "new-index").Return(nil)
		ts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, "t1").Return(false, nil)
		mock.ExpectRollback()
		ts.EXPECT().InternalMarkStopped(gomock.Any(), "t1").Return(true, nil)

		err = completeBuildTaskWithoutEmbedding(context.Background(), resource, rs, ts, "t1", "new-index")

		require.NoError(t, err)
		assert.Equal(t, "old-index", resource.LocalIndexName)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("does not write stale resource snapshot when build completes", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ts := vmock.NewMockBuildTaskService(ctrl)
		staleBuildResource := &interfaces.Resource{
			ID:               "r1",
			Name:             "supply_chain.material_entity",
			SourceIdentifier: "supply_chain.material_entity",
			Description:      "source material description",
			SchemaDefinition: []*interfaces.Property{{
				Name:                "material_id",
				DisplayName:         "material_id",
				Description:         "source material identifier",
				OriginalDescription: "source material identifier",
			}},
		}

		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		mock.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalUpdateLocalIndexName(gomock.Any(), txMatcher, "r1", "new-index").DoAndReturn(
			func(_ context.Context, _ *sql.Tx, id, indexName string) error {
				assert.Equal(t, "r1", id)
				assert.Equal(t, "new-index", indexName)
				return nil
			},
		)
		ts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, "build-task-1").Return(true, nil)
		mock.ExpectCommit()

		require.NoError(t, completeBuildTaskWithoutEmbedding(context.Background(), staleBuildResource, rs, ts, "build-task-1", "new-index"))
		assert.Equal(t, "new-index", staleBuildResource.LocalIndexName)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
