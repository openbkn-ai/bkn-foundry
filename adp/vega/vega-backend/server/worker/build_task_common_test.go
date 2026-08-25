// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
	"vega-backend/logics"
	resourcelogic "vega-backend/logics/resource"
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

func TestPrepareFullBuildIndex(t *testing.T) {
	t.Run("empty mark rebuild deletes existing task index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		resource := workerTestResource()
		task := workerTestFullTask(t, resource)
		indexName := logics.BuildIndexName(resource.ID, task.ID)

		lim.EXPECT().CheckIndexExist(gomock.Any(), indexName).Return(true, nil)
		lim.EXPECT().DeleteIndex(gomock.Any(), indexName).Return(nil)
		lim.EXPECT().CreateIndex(gomock.Any(), indexName, gomock.Any()).Return(nil)

		require.NoError(t, recreateManagedLocalIndex(context.Background(), lim, indexName, task, resource))
	})

	t.Run("nonempty mark cannot resume a missing task index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		indexName := logics.BuildIndexName("r1", "t1")
		lim.EXPECT().CheckIndexExist(gomock.Any(), indexName).Return(false, nil)

		err := requireManagedLocalIndex(context.Background(), lim, indexName)
		require.ErrorContains(t, err, "cannot resume full build")
	})
}

func TestCompleteFullBuildTask(t *testing.T) {
	t.Run("completes task and resource update atomically", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ts := vmock.NewMockBuildTaskService(ctrl)
		resource := workerTestResource()
		resource.LocalIndexName = "old-index"
		task := workerTestFullTask(t, resource)
		mark := `{"mode":"batch","cursor":[{"key":"id","value":10}]}`

		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		mock.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, "r1").Return(resource, nil)
		rs.EXPECT().InternalUpdateLocalIndexState(gomock.Any(), txMatcher, "r1",
			interfaces.ResourceLocalIndexStatusAvailable, "new-index", mark).Return(true, nil)
		ts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, "t1").Return(true, nil)
		mock.ExpectCommit()

		err = (&batchBuildWorker{rs: rs, bts: ts}).completeFullBuildTask(context.Background(), resource, task, "new-index", mark)

		require.NoError(t, err)
		assert.Equal(t, interfaces.ResourceLocalIndexStatusAvailable, resource.LocalIndexStatus)
		assert.Equal(t, "new-index", resource.LocalIndexName)
		assert.Equal(t, mark, resource.SyncMark)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("finishes concurrent stop request", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ts := vmock.NewMockBuildTaskService(ctrl)
		resource := workerTestResource()
		resource.LocalIndexName = "old-index"
		task := workerTestFullTask(t, resource)
		mark := `{"mode":"batch","cursor":[]}`

		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		mock.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, "r1").Return(resource, nil)
		rs.EXPECT().InternalUpdateLocalIndexState(gomock.Any(), txMatcher, "r1",
			interfaces.ResourceLocalIndexStatusAvailable, "new-index", mark).Return(true, nil)
		ts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, "t1").Return(false, nil)
		mock.ExpectRollback()
		ts.EXPECT().InternalMarkStopped(gomock.Any(), "t1").Return(true, nil)

		err = (&batchBuildWorker{rs: rs, bts: ts}).completeFullBuildTask(context.Background(), resource, task, "new-index", mark)

		require.NoError(t, err)
		assert.Equal(t, "old-index", resource.LocalIndexName)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("does not write stale resource snapshot when build completes", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ts := vmock.NewMockBuildTaskService(ctrl)
		staleBuildResource := workerTestResource()
		staleBuildResource.Description = "old description"
		current := workerTestResource()
		current.Description = "new description"
		task := workerTestFullTask(t, current)
		mark := `{"mode":"batch","cursor":[]}`

		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		mock.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, "r1").Return(current, nil)
		rs.EXPECT().InternalUpdateLocalIndexState(gomock.Any(), txMatcher, "r1",
			interfaces.ResourceLocalIndexStatusAvailable, "new-index", mark).Return(true, nil)
		ts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, "build-task-1").Return(true, nil)
		mock.ExpectCommit()

		task.ID = "build-task-1"
		require.NoError(t, (&batchBuildWorker{rs: rs, bts: ts}).completeFullBuildTask(
			context.Background(), staleBuildResource, task, "new-index", mark))
		assert.Equal(t, "new-index", staleBuildResource.LocalIndexName)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("marks task failed when resource index config changed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ts := vmock.NewMockBuildTaskService(ctrl)
		resource := workerTestResource()
		task := workerTestFullTask(t, resource)
		resource.IndexConfig.BuildKeyFields = []string{"updated_at"}
		resource.SchemaDefinition = []*interfaces.Property{{Name: "updated_at", Type: interfaces.DataType_Timestamp}}

		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		mock.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, "r1").Return(resource, nil)
		ts.EXPECT().InternalMarkFailed(gomock.Any(), txMatcher, "t1", gomock.Any()).Return(true, nil)
		mock.ExpectCommit()

		err = (&batchBuildWorker{rs: rs, bts: ts}).completeFullBuildTask(
			context.Background(), resource, task, "new-index", `{"mode":"batch","cursor":[]}`)

		require.ErrorIs(t, err, errBuildTaskMarkedFailed)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func workerTestResource() *interfaces.Resource {
	return &interfaces.Resource{
		ID:          "r1",
		Category:    interfaces.ResourceCategoryTable,
		IndexConfig: &interfaces.ResourceIndexConfig{BuildKeyFields: []string{"id"}},
		SchemaDefinition: []*interfaces.Property{
			{Name: "id", Type: interfaces.DataType_Integer},
		},
	}
}

func workerTestFullTask(t *testing.T, resource *interfaces.Resource) *interfaces.BuildTask {
	t.Helper()
	fields, err := resourcelogic.SnapshotBuildTaskIndexConfigFields(resource)
	require.NoError(t, err)
	return &interfaces.BuildTask{
		ID:          "t1",
		ResourceID:  resource.ID,
		Mode:        interfaces.BuildTaskModeBatch,
		ExecuteType: interfaces.BuildTaskExecuteTypeFull,
		IndexConfig: &interfaces.BuildTaskIndexConfig{
			IndexConfigContract: interfaces.IndexConfigContract{
				BuildKeyFields: []string{"id"},
				Fields:         fields,
			},
		},
	}
}
