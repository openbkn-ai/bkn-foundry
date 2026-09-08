// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
	"vega-backend/logics"
	"vega-backend/logics/sync_checkpoint"
)

func TestBuildBatchCursorFilter(t *testing.T) {
	filter := buildBatchCursorFilter(
		[]string{"customer_id", "id"},
		[]interfaces.KeyValue{{Key: "customer_id", Value: "customer-1"}, {Key: "id", Value: 100}},
	)

	require.Equal(t, "or", filter.Operation)
	require.Len(t, filter.SubConds, 2)
	assert.Equal(t, &interfaces.FilterCondCfg{
		Operation: "and",
		SubConds: []*interfaces.FilterCondCfg{
			{Name: "customer_id", Operation: "gt", ValueOptCfg: interfaces.ValueOptCfg{Value: "customer-1", ValueFrom: interfaces.ValueFrom_Const}},
		},
	}, filter.SubConds[0])
	assert.Equal(t, &interfaces.FilterCondCfg{
		Operation: "and",
		SubConds: []*interfaces.FilterCondCfg{
			{Name: "customer_id", Operation: "==", ValueOptCfg: interfaces.ValueOptCfg{Value: "customer-1", ValueFrom: interfaces.ValueFrom_Const}},
			{Name: "id", Operation: "gt", ValueOptCfg: interfaces.ValueOptCfg{Value: 100, ValueFrom: interfaces.ValueFrom_Const}},
		},
	}, filter.SubConds[1])
}

func TestBuildBatchCursorFilterAppendsPrimaryKeyForSameIncrementalValue(t *testing.T) {
	keys := sync_checkpoint.EffectiveCursorFields([]string{"ingested_at"}, []string{"id"})
	filter := buildBatchCursorFilter(keys, []interfaces.KeyValue{{Key: "ingested_at", Value: "T1"}, {Key: "id", Value: int64(1000)}})

	require.Equal(t, []string{"ingested_at", "id"}, keys)
	require.Len(t, filter.SubConds, 2)
	assert.Equal(t, "ingested_at", filter.SubConds[1].SubConds[0].Name)
	assert.Equal(t, "==", filter.SubConds[1].SubConds[0].Operation)
	assert.Equal(t, "id", filter.SubConds[1].SubConds[1].Name)
	assert.Equal(t, "gt", filter.SubConds[1].SubConds[1].Operation)
	assert.Equal(t, int64(1000), filter.SubConds[1].SubConds[1].ValueOptCfg.Value)
}

func TestBatchCursorReadsAllSameIncrementalValueAcrossPages(t *testing.T) {
	for _, count := range []int{999, 1000, 1001, 1500} {
		t.Run(fmt.Sprintf("%d same values", count), func(t *testing.T) {
			first := min(count, 1000)
			cursor := []interfaces.KeyValue{{Key: "ingested_at", Value: "T1"}, {Key: "id", Value: int64(first)}}
			filter := buildBatchCursorFilter(sync_checkpoint.EffectiveCursorFields([]string{"ingested_at"}, []string{"id"}), cursor)
			second := make([]int64, 0, count-first)
			for id := int64(1); id <= int64(count); id++ {
				if id > cursor[1].Value.(int64) {
					second = append(second, id)
				}
			}
			assert.Len(t, filter.SubConds, 2)
			assert.Len(t, second, count-first)
			assert.Equal(t, count, first+len(second))
		})
	}

	t.Run("999 earlier rows plus two equal boundary rows", func(t *testing.T) {
		type cursorRow struct {
			time string
			id   int64
		}
		rows := make([]cursorRow, 0, 1001)
		for id := int64(1); id <= 999; id++ {
			rows = append(rows, cursorRow{time: "T0", id: id})
		}
		rows = append(rows, cursorRow{time: "T1", id: 1000}, cursorRow{time: "T1", id: 1001})
		cursor := rows[999]
		remaining := make([]cursorRow, 0, 1)
		for _, row := range rows {
			if row.time > cursor.time || row.time == cursor.time && row.id > cursor.id {
				remaining = append(remaining, row)
			}
		}
		assert.Equal(t, []cursorRow{{time: "T1", id: 1001}}, remaining)
	})
}

func TestBatchCursorKeepsCompositePrimaryFieldsForResume(t *testing.T) {
	keys := sync_checkpoint.EffectiveCursorFields(
		[]string{"updated_at", "tenant_id"}, []string{"tenant_id", "id"},
	)
	cursor := []interfaces.KeyValue{{Key: "updated_at", Value: "T1"}, {Key: "tenant_id", Value: "tenant-a"}, {Key: "id", Value: int64(1000)}}
	filter := buildBatchCursorFilter(keys, cursor)

	require.Equal(t, []string{"updated_at", "tenant_id", "id"}, keys)
	require.Len(t, filter.SubConds, 3)
	assert.Equal(t, "id", filter.SubConds[2].SubConds[2].Name)
	assert.Equal(t, "gt", filter.SubConds[2].SubConds[2].Operation)
	assert.Equal(t, int64(1000), filter.SubConds[2].SubConds[2].ValueOptCfg.Value)
}

func TestBatchBuildWorkerHandleTask(t *testing.T) {
	t.Run("does not switch local index when build fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bbw := &batchBuildWorker{bts: bts, lim: lim}

		resource := workerTestResource()
		resource.LocalIndexName = buildIndexName("r1", "old-task")
		task := workerTestFullTask(t, resource)
		task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
		task.IndexName = resource.LocalIndexName
		task.Status = interfaces.BuildTaskStatusPending
		lim.EXPECT().CheckIndexExist(gomock.Any(), buildIndexName("r1", "old-task")).
			Return(false, errors.New("opensearch unavailable"))
		bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "t1",
			"prepare local index failed: check local index exist failed: opensearch unavailable").
			Return(true, nil)

		require.NoError(t, bbw.Run(context.Background(), task, resource, &interfaces.Catalog{Enabled: true}))
		assert.Equal(t, buildIndexName("r1", "old-task"), resource.LocalIndexName)
	})

}

func TestBatchBuildWorkerExecuteBuild(t *testing.T) {
	t.Run("does not invoke model when index creation fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bbw := &batchBuildWorker{lim: lim}
		resource := &interfaces.Resource{
			ID: "r1",
			SchemaDefinition: []*interfaces.Property{
				{
					Name: "content", Type: interfaces.DataType_String,
					Features: []interfaces.PropertyFeature{{FeatureType: interfaces.DataType_Vector}},
				},
			},
		}
		buildTask := &interfaces.BuildTask{
			ID: "t1", ExecuteType: interfaces.BuildTaskExecuteTypeFull, IndexName: buildIndexName("r1", "t1"),
			IndexConfig: &interfaces.BuildTaskIndexConfig{Features: map[string]interfaces.BuildTaskFieldIndexFeature{
				"content": {Vector: &interfaces.SmallModel{ModelID: "m1", EmbeddingDim: 3}},
			}},
		}

		lim.EXPECT().CheckIndexExist(gomock.Any(), buildIndexName("r1", "t1")).Return(false, nil)
		lim.EXPECT().CreateIndex(gomock.Any(), buildIndexName("r1", "t1"), gomock.Any(), gomock.Any()).
			Return(errors.New("opensearch unavailable"))

		err := bbw.executeBuild(context.Background(), &interfaces.Catalog{ID: "c1"}, resource, buildTask)
		require.Error(t, err)
		assert.ErrorContains(t, err, "prepare local index failed: opensearch unavailable")
	})

	t.Run("empty full build publishes an established empty checkpoint", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		resource := workerTestResource()
		task := workerTestFullTask(t, resource)
		indexName := buildIndexName(resource.ID, task.ID)
		bbw := &batchBuildWorker{lim: lim, bts: bts, rs: rs, cf: cf}

		db, mockDB, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		lim.EXPECT().CheckIndexExist(gomock.Any(), indexName).Return(false, nil)
		lim.EXPECT().CreateIndex(gomock.Any(), indexName, gomock.Any(), gomock.Any()).Return(nil)
		var progressMarks []string
		var totalCounts []int64
		bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress) (bool, error) {
				if progress.TotalCount != nil {
					totalCounts = append(totalCounts, *progress.TotalCount)
				}
				if progress.SyncedMark != nil {
					progressMarks = append(progressMarks, *progress.SyncedMark)
				}
				return true, nil
			}).Times(2)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(&interfaces.QueryResult{Total: 0}, nil)
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		mockDB.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(resource, nil)
		rs.EXPECT().InternalUpdateLocalIndexState(gomock.Any(), txMatcher, resource.ID,
			interfaces.ResourceLocalIndexStatusAvailable, indexName, `{"mode":"batch","cursor":[]}`).Return(true, nil)
		bts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, task.ID).Return(true, nil)
		mockDB.ExpectCommit()

		err = bbw.executeBuild(context.Background(), &interfaces.Catalog{ID: "c1", ConnectorType: "mysql"}, resource, task)

		require.NoError(t, err)
		assert.Equal(t, []int64{0}, totalCounts)
		assert.Equal(t, []string{"", `{"mode":"batch","cursor":[]}`}, progressMarks)
		assert.Equal(t, `{"mode":"batch","cursor":[]}`, resource.SyncMark)
		require.NoError(t, mockDB.ExpectationsWereMet())
	})

	t.Run("resumed incremental keeps cumulative total and advances checkpoints atomically", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		resource := workerTestResource()
		resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{Name: "payload", Type: interfaces.DataType_Json})
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
		resource.LocalIndexName = "current-index"
		resource.SyncMark = `{"mode":"batch","cursor":[{"key":"id","value":8000}]}`
		task := workerTestFullTask(t, resource)
		task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
		task.IndexName = resource.LocalIndexName
		task.Status = interfaces.BuildTaskStatusRunning
		task.SyncedMark = resource.SyncMark
		task.SyncedCount = 8000
		bbw := &batchBuildWorker{lim: lim, bts: bts, rs: rs, cf: cf}

		db, mockDB, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		lim.EXPECT().CheckIndexExist(gomock.Any(), "current-index").Return(true, nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		queryCount := 0
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Times(73).DoAndReturn(
			func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
				queryCount++
				if queryCount == 73 {
					return &interfaces.QueryResult{}, nil
				}
				require.NotNil(t, params.FilterCondCfg)
				assert.Equal(t, 1000, params.Limit)
				assert.Equal(t, queryCount == 1, params.NeedTotal)
				entries := make([]map[string]any, 1000)
				firstID := int64(8001 + (queryCount-1)*1000)
				for index := range entries {
					entries[index] = map[string]any{
						"id":      firstID + int64(index),
						"payload": `{"region":"cn"}`,
					}
				}
				result := &interfaces.QueryResult{Entries: entries}
				if queryCount == 1 {
					result.Total = 72000
				}
				return result, nil
			})
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil).Times(73)
		bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress) (bool, error) {
				require.NotNil(t, progress.TotalCount)
				assert.EqualValues(t, 80000, *progress.TotalCount)
				return true, nil
			})
		indexedBatches := 0
		lim.EXPECT().IndexDocuments(gomock.Any(), "current-index", gomock.Any()).Times(72).DoAndReturn(
			func(_ context.Context, _ string, documents map[string]map[string]any) ([]string, error) {
				require.Len(t, documents, 1000)
				for _, document := range documents {
					assert.Equal(t, map[string]any{"region": "cn"}, document["payload"])
				}
				indexedBatches++
				return nil, nil
			})
		newMark := `{"mode":"batch","cursor":[{"key":"id","value":80000}]}`
		for range 72 {
			mockDB.ExpectBegin()
			mockDB.ExpectCommit()
		}
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).AnyTimes().DoAndReturn(
			func(context.Context, *sql.Tx, string) (*interfaces.Resource, error) {
				require.Positive(t, indexedBatches, "checkpoint transaction must start after OpenSearch write")
				return resource, nil
			})
		var finalProgress interfaces.BuildTaskProgress
		bts.EXPECT().InternalSetProgress(gomock.Any(), txMatcher, task.ID, gomock.Any()).Times(72).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress) (bool, error) {
				finalProgress = progress
				return true, nil
			})
		rs.EXPECT().InternalUpdateLocalIndexState(gomock.Any(), txMatcher, resource.ID,
			interfaces.ResourceLocalIndexStatusAvailable, "current-index", gomock.Any()).Return(true, nil).Times(72)
		bts.EXPECT().InternalMarkCompleted(gomock.Any(), nil, task.ID).Return(true, nil)

		err = bbw.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)

		require.NoError(t, err)
		require.NotNil(t, finalProgress.SyncedMark)
		assert.Equal(t, newMark, *finalProgress.SyncedMark)
		require.NotNil(t, finalProgress.SyncedCount)
		assert.EqualValues(t, 80000, *finalProgress.SyncedCount)
		assert.Equal(t, newMark, task.SyncedMark)
		assert.Equal(t, newMark, resource.SyncMark)
		require.NoError(t, mockDB.ExpectationsWereMet())
	})

	t.Run("fresh incremental keeps connector total without a cursor", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		resource := workerTestResource()
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
		resource.LocalIndexName = "current-index"
		resource.SyncMark = `{"mode":"batch","cursor":[]}`
		task := workerTestFullTask(t, resource)
		task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
		task.IndexName = resource.LocalIndexName
		task.Status = interfaces.BuildTaskStatusRunning
		task.SyncedMark = resource.SyncMark
		bbw := &batchBuildWorker{lim: lim, bts: bts, rs: rs, cf: cf}

		db, mockDB, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		lim.EXPECT().CheckIndexExist(gomock.Any(), "current-index").Return(true, nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
				assert.Nil(t, params.FilterCondCfg)
				return &interfaces.QueryResult{Total: 1, Entries: []map[string]any{{"id": int64(1)}}}, nil
			})
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress) (bool, error) {
				require.NotNil(t, progress.TotalCount)
				assert.EqualValues(t, 1, *progress.TotalCount)
				return true, nil
			})
		lim.EXPECT().IndexDocuments(gomock.Any(), "current-index", gomock.Any()).Return(nil, nil)
		mockDB.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(resource, nil)
		bts.EXPECT().InternalSetProgress(gomock.Any(), txMatcher, task.ID, gomock.Any()).Return(true, nil)
		rs.EXPECT().InternalUpdateLocalIndexState(gomock.Any(), txMatcher, resource.ID,
			interfaces.ResourceLocalIndexStatusAvailable, "current-index", gomock.Any()).Return(true, nil)
		mockDB.ExpectCommit()
		bts.EXPECT().InternalMarkCompleted(gomock.Any(), nil, task.ID).Return(true, nil)

		err = bbw.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)

		require.NoError(t, err)
		require.NoError(t, mockDB.ExpectationsWereMet())
	})

	t.Run("resumed full build keeps cumulative total", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		resource := workerTestResource()
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
		resource.LocalIndexName = "current-index"
		resource.SyncMark = `{"mode":"batch","cursor":[{"key":"id","value":8}]}`
		task := workerTestFullTask(t, resource)
		task.IndexName = resource.LocalIndexName
		task.Status = interfaces.BuildTaskStatusRunning
		task.SyncedMark = resource.SyncMark
		task.SyncedCount = 8
		bbw := &batchBuildWorker{lim: lim, bts: bts, rs: rs, cf: cf}

		db, mockDB, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		lim.EXPECT().CheckIndexExist(gomock.Any(), "current-index").Return(true, nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
				require.NotNil(t, params.FilterCondCfg)
				return &interfaces.QueryResult{Total: 2, Entries: []map[string]any{{"id": int64(9)}, {"id": int64(10)}}}, nil
			})
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		progressCalls := 0
		bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any()).Times(2).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress) (bool, error) {
				progressCalls++
				if progressCalls == 1 {
					require.NotNil(t, progress.TotalCount)
					assert.EqualValues(t, 10, *progress.TotalCount)
				} else {
					require.NotNil(t, progress.SyncedCount)
					assert.EqualValues(t, 10, *progress.SyncedCount)
				}
				return true, nil
			})
		lim.EXPECT().IndexDocuments(gomock.Any(), "current-index", gomock.Any()).Return(nil, nil)
		newMark := `{"mode":"batch","cursor":[{"key":"id","value":10}]}`
		mockDB.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(resource, nil)
		rs.EXPECT().InternalUpdateLocalIndexState(gomock.Any(), txMatcher, resource.ID,
			interfaces.ResourceLocalIndexStatusAvailable, "current-index", newMark).Return(true, nil)
		bts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, task.ID).Return(true, nil)
		mockDB.ExpectCommit()

		err = bbw.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)

		require.NoError(t, err)
		require.NoError(t, mockDB.ExpectationsWereMet())
	})

	t.Run("incremental with no new rows completes with initialized checkpoint", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		resource := workerTestResource()
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
		resource.LocalIndexName = "current-index"
		resource.SyncMark = `{"mode":"batch","cursor":[{"key":"id","value":10}]}`
		task := workerTestFullTask(t, resource)
		task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
		task.IndexName = resource.LocalIndexName
		task.Status = interfaces.BuildTaskStatusRunning
		task.SyncedMark = resource.SyncMark
		bbw := &batchBuildWorker{lim: lim, bts: bts, cf: cf}

		lim.EXPECT().CheckIndexExist(gomock.Any(), "current-index").Return(true, nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(&interfaces.QueryResult{}, nil)
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		bts.EXPECT().InternalMarkCompleted(gomock.Any(), nil, task.ID).Return(true, nil)

		err := bbw.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)

		require.NoError(t, err)
		assert.Equal(t, resource.SyncMark, task.SyncedMark)
	})
}

func TestBatchBuildWorkerRejectsIncrementalCheckpointWhenIndexChanged(t *testing.T) {
	ctrl := gomock.NewController(t)
	bts := vmock.NewMockBuildTaskService(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	resource := workerTestResource()
	resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
	resource.LocalIndexName = "current-index"
	resource.SyncMark = `{"mode":"batch","cursor":[]}`
	current := *resource
	current.LocalIndexName = "replacement-index"
	task := workerTestFullTask(t, resource)
	task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
	newMark := `{"mode":"batch","cursor":[{"key":"id","value":1}]}`
	progress := interfaces.BuildTaskProgress{SyncedMark: &newMark}
	bbw := &batchBuildWorker{bts: bts, rs: rs}

	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDB := logics.DB
	logics.DB = db
	defer func() { logics.DB = oldDB }()

	mockDB.ExpectBegin()
	txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
	rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(&current, nil)
	mockDB.ExpectRollback()

	err = bbw.commitIncrementalProgress(context.Background(), resource, task,
		resource.LocalIndexName, resource.SyncMark, newMark, progress)

	require.ErrorContains(t, err, "resource local index changed during incremental build")
	assert.Equal(t, `{"mode":"batch","cursor":[]}`, resource.SyncMark)
	require.NoError(t, mockDB.ExpectationsWereMet())
}

func TestBatchBuildWorkerReadsSameIncrementalValueAcrossPages(t *testing.T) {
	ctrl := gomock.NewController(t)
	lim := vmock.NewMockLocalIndexManager(ctrl)
	bts := vmock.NewMockBuildTaskService(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	cf := vmock.NewMockConnectorFactory(ctrl)
	connector := vmock.NewMockTableConnector(ctrl)
	resource := workerTestResource()
	resource.SchemaDefinition = append(resource.SchemaDefinition,
		&interfaces.Property{Name: "ingested_at", Type: interfaces.DataType_Timestamp})
	resource.IndexConfig.IncrementalFields = []string{"ingested_at"}
	resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
	resource.LocalIndexName = "current-index"
	resource.SyncMark = `{"mode":"batch","cursor":[{"key":"ingested_at","value":"T0"},{"key":"id","value":20}]}`
	task := workerTestFullTask(t, resource)
	task.IndexConfig.IncrementalFields = []string{"ingested_at"}
	task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
	task.IndexName = resource.LocalIndexName
	task.Status = interfaces.BuildTaskStatusRunning
	task.SyncedMark = resource.SyncMark
	bbw := &batchBuildWorker{lim: lim, bts: bts, rs: rs, cf: cf}

	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDB := logics.DB
	logics.DB = db
	defer func() { logics.DB = oldDB }()

	lim.EXPECT().CheckIndexExist(gomock.Any(), "current-index").Return(true, nil)
	cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
	connector.EXPECT().Connect(gomock.Any()).Return(nil)
	sourceRows := make([]map[string]any, 1500)
	for i := range sourceRows {
		sourceRows[i] = map[string]any{"id": int64(i + 21), "ingested_at": "T1"}
	}
	queryCount := 0
	connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Times(2).DoAndReturn(
		func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
			queryCount++
			require.Len(t, params.Sort, 2)
			assert.Equal(t, "ingested_at", params.Sort[0].Field)
			assert.Equal(t, "id", params.Sort[1].Field)
			require.NotNil(t, params.FilterCondCfg)
			if queryCount == 2 {
				require.Len(t, params.FilterCondCfg.SubConds, 2)
				idCondition := params.FilterCondCfg.SubConds[1].SubConds[1]
				assert.Equal(t, "id", idCondition.Name)
				assert.Equal(t, "gt", idCondition.Operation)
				assert.Equal(t, int64(1020), idCondition.ValueOptCfg.Value)
			}
			cursorTime := params.FilterCondCfg.SubConds[0].SubConds[0].ValueOptCfg.Value.(string)
			cursorID := params.FilterCondCfg.SubConds[1].SubConds[1].ValueOptCfg.Value.(int64)
			entries := make([]map[string]any, 0, params.Limit)
			for _, row := range sourceRows {
				rowTime := row["ingested_at"].(string)
				rowID := row["id"].(int64)
				if rowTime > cursorTime || rowTime == cursorTime && rowID > cursorID {
					entries = append(entries, row)
					if len(entries) == params.Limit {
						break
					}
				}
			}
			result := &interfaces.QueryResult{Entries: entries}
			if queryCount == 1 {
				result.Total = int64(len(sourceRows))
			}
			return result, nil
		})
	connector.EXPECT().Close(gomock.Any()).Return(nil)
	bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil).Times(2)
	bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress) (bool, error) {
			require.NotNil(t, progress.TotalCount)
			assert.EqualValues(t, 1500, *progress.TotalCount)
			return true, nil
		})
	batchSizes := make([]int, 0, 2)
	lim.EXPECT().IndexDocuments(gomock.Any(), "current-index", gomock.Any()).Times(2).DoAndReturn(
		func(_ context.Context, _ string, documents map[string]map[string]any) ([]string, error) {
			batchSizes = append(batchSizes, len(documents))
			return nil, nil
		})
	for range 2 {
		mockDB.ExpectBegin()
		mockDB.ExpectCommit()
	}
	txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
	rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Times(2).Return(resource, nil)
	var finalProgress interfaces.BuildTaskProgress
	bts.EXPECT().InternalSetProgress(gomock.Any(), txMatcher, task.ID, gomock.Any()).Times(2).DoAndReturn(
		func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress) (bool, error) {
			finalProgress = progress
			return true, nil
		})
	rs.EXPECT().InternalUpdateLocalIndexState(gomock.Any(), txMatcher, resource.ID,
		interfaces.ResourceLocalIndexStatusAvailable, "current-index", gomock.Any()).Return(true, nil).Times(2)
	bts.EXPECT().InternalMarkCompleted(gomock.Any(), nil, task.ID).Return(true, nil)

	err = bbw.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)

	require.NoError(t, err)
	assert.Equal(t, []int{1000, 500}, batchSizes)
	require.NotNil(t, finalProgress.SyncedCount)
	assert.EqualValues(t, 1500, *finalProgress.SyncedCount)
	require.NotNil(t, finalProgress.SyncedMark)
	assert.JSONEq(t, `{"mode":"batch","cursor":[{"key":"ingested_at","value":"T1"},{"key":"id","value":1520}]}`, *finalProgress.SyncedMark)
	require.NoError(t, mockDB.ExpectationsWereMet())
}

func TestReconcileTaskFulltextFeatures(t *testing.T) {
	t.Run("errors when task field is missing from schema features", func(t *testing.T) {
		schema := []*interfaces.Property{
			{Name: "team_name", Type: interfaces.DataType_String},
			{Name: "team_code", Type: interfaces.DataType_String},
		}
		task := buildTaskWithFulltext("team_name", "ik_max_word")

		err := validateTaskFulltextFeatures(schema, task)

		require.Error(t, err)
		assert.Contains(t, err.Error(), `build task fulltext field "team_name"`)
	})

	t.Run("errors when schema has stale field", func(t *testing.T) {
		schema := []*interfaces.Property{
			{Name: "team_name", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
			}},
			{Name: "federation_name", Type: interfaces.DataType_String},
		}
		task := buildTaskWithFulltext("federation_name", "standard")

		err := validateTaskFulltextFeatures(schema, task)

		require.Error(t, err)
		assert.Contains(t, err.Error(), `resource schema fulltext field "team_name"`)
	})

	t.Run("errors when explicit analyzer differs", func(t *testing.T) {
		schema := []*interfaces.Property{
			{Name: "x", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
			}},
		}
		task := buildTaskWithFulltext("x", "ik_max_word")

		err := validateTaskFulltextFeatures(schema, task)

		require.Error(t, err)
		assert.Contains(t, err.Error(), `does not match build task analyzer`)
	})

	t.Run("applies task analyzer when schema omits analyzer", func(t *testing.T) {
		schema := []*interfaces.Property{
			{Name: "x", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
			}},
		}
		task := buildTaskWithFulltext("x", "ik_max_word")

		err := validateTaskFulltextFeatures(schema, task)

		require.NoError(t, err)
		assert.Equal(t, "ik_max_word", schema[0].Features[0].Config["analyzer"])
	})
}

func buildTaskWithFulltext(field string, analyzer string) *interfaces.BuildTask {
	return &interfaces.BuildTask{
		IndexConfig: &interfaces.BuildTaskIndexConfig{
			Features: map[string]interfaces.BuildTaskFieldIndexFeature{
				field: {Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: analyzer}},
			},
		},
	}
}

func TestValidateTaskEmbeddingFeatures(t *testing.T) {
	t.Run("errors when task field is missing from schema features", func(t *testing.T) {
		schema := []*interfaces.Property{{Name: "title", Type: interfaces.DataType_String}}

		err := validateTaskEmbeddingFeatures(schema, buildTaskWithVector("title"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), `build task embedding field "title"`)
	})

	t.Run("errors when schema has stale field", func(t *testing.T) {
		schema := []*interfaces.Property{{
			Name: "title",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{
				{FeatureName: "vector", FeatureType: interfaces.PropertyFeatureType_Vector},
			},
		}}

		err := validateTaskEmbeddingFeatures(schema, &interfaces.BuildTask{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), `resource schema embedding field "title"`)
	})

	t.Run("passes when schema and task match", func(t *testing.T) {
		schema := []*interfaces.Property{{
			Name: "title",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{
				{FeatureName: "vector", FeatureType: interfaces.PropertyFeatureType_Vector},
			},
		}}

		err := validateTaskEmbeddingFeatures(schema, buildTaskWithVector("title"))

		require.NoError(t, err)
	})
}

func TestValidateBuildTaskSchemaFeatures(t *testing.T) {
	tests := []struct {
		name     string
		category string
		schema   []*interfaces.Property
		wantErr  string
	}{
		{
			name:     "dataset rejects ref property",
			category: interfaces.ResourceCategoryDataset,
			schema: []*interfaces.Property{
				{Name: "content_keyword", Type: interfaces.DataType_String},
				{Name: "content", Type: interfaces.DataType_Text,
					Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "content_keyword"}}},
			},
			wantErr: "must not set ref_property",
		},
		{
			name:     "rejects feature unsupported by property type",
			category: interfaces.ResourceCategoryTable,
			schema: []*interfaces.Property{{
				Name: "id", Type: interfaces.DataType_Integer,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword}},
			}},
			wantErr: "does not support feature type",
		},
		{
			name:     "rejects mismatched ref property type",
			category: interfaces.ResourceCategoryTable,
			schema: []*interfaces.Property{
				{Name: "embedding", Type: interfaces.DataType_Vector},
				{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "embedding"}}},
			},
			wantErr: "incompatible with feature type",
		},
		{
			name:     "accepts optional ref property on original resource",
			category: interfaces.ResourceCategoryTable,
			schema: []*interfaces.Property{{
				Name: "content", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBuildTaskSchemaFeatures(tt.category, tt.schema)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func buildTaskWithVector(field string) *interfaces.BuildTask {
	return &interfaces.BuildTask{
		IndexConfig: &interfaces.BuildTaskIndexConfig{
			Features: map[string]interfaces.BuildTaskFieldIndexFeature{
				field: {Vector: &interfaces.SmallModel{ModelID: "m1", EmbeddingDim: 1024}},
			},
		},
	}
}

func TestBuildLocalIndexSchemaAppliesTaskIndexConfigWithoutMutatingResourceSchema(t *testing.T) {
	t.Run("single analyzer and vector field", func(t *testing.T) {
		res := &interfaces.Resource{ID: "r1", SchemaDefinition: []*interfaces.Property{
			{Name: "title", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
			}},
			{Name: "body", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "vector", FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 1024}},
			}},
		}}
		task := &interfaces.BuildTask{
			ID: "t1",
			IndexConfig: &interfaces.BuildTaskIndexConfig{
				Features: map[string]interfaces.BuildTaskFieldIndexFeature{
					"title": {Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}},
					"body":  {Vector: &interfaces.SmallModel{ModelID: "m1", EmbeddingDim: 1024}},
				},
			},
		}

		schema, err := buildLocalIndexSchema(task, res)
		require.NoError(t, err)

		require.Len(t, schema[0].Features, 1)
		assert.Equal(t, interfaces.PropertyFeatureType_Fulltext, schema[0].Features[0].FeatureType)
		assert.Equal(t, "ik_max_word", schema[0].Features[0].Config["analyzer"])
		require.Len(t, schema, 2)
		require.Len(t, schema[1].Features, 1)
		assert.Equal(t, 1024, schema[1].Features[0].Config["dimension"])
		assert.Nil(t, res.SchemaDefinition[0].Features[0].Config)
		assert.Len(t, res.SchemaDefinition[1].Features, 1)
	})

	t.Run("keeps different analyzers and vector dimensions per field", func(t *testing.T) {
		res := &interfaces.Resource{ID: "r1", SchemaDefinition: []*interfaces.Property{
			{Name: "title", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
				{FeatureName: "vector", FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 768}},
			}},
			{Name: "body", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
				{FeatureName: "vector", FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 1024}},
			}},
		}}
		task := &interfaces.BuildTask{
			ID: "t1",
			IndexConfig: &interfaces.BuildTaskIndexConfig{
				Features: map[string]interfaces.BuildTaskFieldIndexFeature{
					"title": {
						Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"},
						Vector:   &interfaces.SmallModel{ModelID: "m1", EmbeddingDim: 768},
					},
					"body": {
						Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "standard"},
						Vector:   &interfaces.SmallModel{ModelID: "m2", EmbeddingDim: 1024},
					},
				},
			},
		}

		schema, err := buildLocalIndexSchema(task, res)
		require.NoError(t, err)

		assert.Equal(t, "ik_max_word", schema[0].Features[0].Config["analyzer"])
		assert.Equal(t, "standard", schema[1].Features[0].Config["analyzer"])
		vectorDimensions := map[string]any{
			"title_vector": schema[0].Features[1].Config["dimension"],
			"body_vector":  schema[1].Features[1].Config["dimension"],
		}
		assert.Equal(t, map[string]any{
			"title_vector": 768,
			"body_vector":  1024,
		}, vectorDimensions)
		assert.Nil(t, res.SchemaDefinition[0].Features[0].Config)
		assert.Nil(t, res.SchemaDefinition[1].Features[0].Config)
	})
}

func workerAccountFromCtx(ctx context.Context) (interfaces.AccountInfo, bool) {
	ai, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return ai, ok
}

// 从没被 PUT 过的存量资源，库里仍是自引用形状。构建入口抹平之后必须能建索引，否则
// 「push 清掉 condition_operations -> 重建索引 -> 恢复」这条路仍然是断的。
func TestBuildLocalIndexSchemaNormalizesLegacySelfReference(t *testing.T) {
	// schema 里的 fulltext 特征必须同时在构建任务的 index config 里声明，与自引用无关
	fulltextTask := func(field string) *interfaces.BuildTask {
		return &interfaces.BuildTask{
			ID: "t1",
			IndexConfig: &interfaces.BuildTaskIndexConfig{
				Features: map[string]interfaces.BuildTaskFieldIndexFeature{
					field: {Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}},
				},
			},
		}
	}

	tests := []struct {
		name     string
		category string
		task     *interfaces.BuildTask
		props    []*interfaces.Property
	}{
		{
			name:     "fulltext feature referencing its own text field",
			category: interfaces.ResourceCategoryTable,
			task:     fulltextTask("title"),
			props: []*interfaces.Property{{
				Name: "title", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureName: "title_fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "title"}},
			}},
		},
		{
			// keyword 的 ref 类型要求是 string，text 字段上的自引用 keyword 特征
			// 在抹平前会撞上 ref 类型校验，抹平后按「特征作用于属性自身」通过
			name:     "keyword feature referencing its own text field",
			category: interfaces.ResourceCategoryTable,
			task:     &interfaces.BuildTask{ID: "t1"},
			props: []*interfaces.Property{{
				Name: "title", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureName: "title.keyword", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "title"}},
			}},
		},
		{
			// 入口严、存量宽：写入侧对 dataset 的 ref_property 仍然 400（#837 之前就是），
			// 但库里若已有这种行（迁移或直接写库留下的），构建不该因此建不起来
			name:     "any self-reference on a dataset",
			category: interfaces.ResourceCategoryDataset,
			task:     fulltextTask("content"),
			props: []*interfaces.Property{{
				Name: "content", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureName: "content_fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "content"}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &interfaces.Resource{Category: tt.category, SchemaDefinition: tt.props}

			schema, err := buildLocalIndexSchema(tt.task, res)

			require.NoError(t, err)
			require.Len(t, schema, 1)
			assert.Empty(t, schema[0].Features[0].RefProperty)
			// 只动深拷贝，资源行留给下一次更新自愈
			assert.Equal(t, tt.props[0].Name, res.SchemaDefinition[0].Features[0].RefProperty)
		})
	}
}
