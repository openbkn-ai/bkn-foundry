// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/sync_checkpoint"
)

func TestBuildBatchCursorFilter(t *testing.T) {
	t.Run("temporal checkpoint round trip", func(t *testing.T) {
		t.Setenv("TZ", "Asia/Shanghai")
		for _, kind := range []string{interfaces.DataType_Datetime, interfaces.DataType_Date, interfaces.DataType_Time} {
			t.Run(kind, func(t *testing.T) {
				field := &interfaces.Property{Name: "cursor", Type: kind, OriginalType: "timestamp without time zone"}
				native := time.Date(2026, 10, 19, 14, 30, 0, 123456789, time.UTC)
				original, err := buildBatchCursorFilter([]string{"cursor"}, []interfaces.KeyValue{{Key: "cursor", Value: native}})
				require.NoError(t, err)
				before, err := filter_condition.NewFilterCondition(context.Background(), original, map[string]*interfaces.Property{"cursor": field})
				require.NoError(t, err)
				mark, err := sync_checkpoint.EncodeBatch([]interfaces.KeyValue{{Key: "cursor", Value: native}})
				require.NoError(t, err)
				restored, err := sync_checkpoint.DecodeBatch(mark)
				require.NoError(t, err)
				require.NoError(t, sync_checkpoint.ValidateCursor(restored, []string{"cursor"}, []*interfaces.Property{field}))
				resumed, err := buildBatchCursorFilter([]string{"cursor"}, restored.Cursor)
				require.NoError(t, err)
				after, err := filter_condition.NewFilterCondition(context.Background(), resumed, map[string]*interfaces.Property{"cursor": field})
				require.NoError(t, err)
				beforeValue := before.(*filter_condition.OrCond).SubConds[0].(*filter_condition.AndCond).SubConds[0].(*filter_condition.GtCond).Value
				afterValue := after.(*filter_condition.OrCond).SubConds[0].(*filter_condition.AndCond).SubConds[0].(*filter_condition.GtCond).Value
				assert.Equal(t, beforeValue, afterValue)
				assert.Equal(t, native, original.SubConds[0].SubConds[0].Value)
			})
		}
	})

	t.Run("resumed cursor keeps source precision", func(t *testing.T) {
		for _, kind := range []string{interfaces.DataType_Datetime, interfaces.DataType_Time} {
			literal := "14:30:00.123456789"
			if kind == interfaces.DataType_Datetime {
				literal = "2026-10-19T14:30:00.123456789Z"
			}
			filter, err := buildBatchCursorFilter([]string{"cursor"}, []interfaces.KeyValue{{Key: "cursor", Value: literal}})
			require.NoError(t, err)
			cond, err := filter_condition.NewFilterCondition(context.Background(), filter, map[string]*interfaces.Property{"cursor": {Name: "cursor", Type: kind}})
			require.NoError(t, err)
			value := cond.(*filter_condition.OrCond).SubConds[0].(*filter_condition.AndCond).SubConds[0].(*filter_condition.GtCond).Value
			if kind == interfaces.DataType_Datetime {
				assert.Equal(t, 123456789, value.(time.Time).Nanosecond())
			} else {
				assert.Equal(t, literal, value)
			}
			assert.Equal(t, literal, filter.SubConds[0].SubConds[0].Value)
		}
	})

	filter, err := buildBatchCursorFilter(
		[]string{"customer_id", "id"},
		[]interfaces.KeyValue{{Key: "customer_id", Value: "customer-1"}, {Key: "id", Value: 100}},
	)
	require.NoError(t, err)

	require.Equal(t, "or", filter.Operation)
	require.Len(t, filter.SubConds, 2)
	assert.Equal(t, &interfaces.FilterCondCfg{
		Operation: "and",
		SubConds: []*interfaces.FilterCondCfg{
			{Field: "customer_id", Operation: "gt", Value: "customer-1", ValueFrom: interfaces.ValueFrom_Const},
		},
	}, filter.SubConds[0])
	assert.Equal(t, &interfaces.FilterCondCfg{
		Operation: "and",
		SubConds: []*interfaces.FilterCondCfg{
			{Field: "customer_id", Operation: "==", Value: "customer-1", ValueFrom: interfaces.ValueFrom_Const},
			{Field: "id", Operation: "gt", Value: 100, ValueFrom: interfaces.ValueFrom_Const},
		},
	}, filter.SubConds[1])
}

func TestBuildBatchCursorFilterRejectsMismatchedCursor(t *testing.T) {
	filter, err := buildBatchCursorFilter(
		[]string{"customer_id", "id"},
		[]interfaces.KeyValue{{Key: "customer_id", Value: "customer-1"}},
	)
	require.Error(t, err)
	assert.Nil(t, filter)
}

func TestBuildBatchCursorFilterAppendsPrimaryKeyForSameIncrementalValue(t *testing.T) {
	keys := sync_checkpoint.EffectiveCursorFields([]string{"ingested_at"}, []string{"id"})
	filter, err := buildBatchCursorFilter(keys, []interfaces.KeyValue{{Key: "ingested_at", Value: "T1"}, {Key: "id", Value: int64(1000)}})
	require.NoError(t, err)

	require.Equal(t, []string{"ingested_at", "id"}, keys)
	require.Len(t, filter.SubConds, 2)
	assert.Equal(t, "ingested_at", filter.SubConds[1].SubConds[0].Field)
	assert.Equal(t, "==", filter.SubConds[1].SubConds[0].Operation)
	assert.Equal(t, "id", filter.SubConds[1].SubConds[1].Field)
	assert.Equal(t, "gt", filter.SubConds[1].SubConds[1].Operation)
	assert.Equal(t, int64(1000), filter.SubConds[1].SubConds[1].Value)
}

func TestBatchCursorReadsAllSameIncrementalValueAcrossPages(t *testing.T) {
	for _, count := range []int{999, 1000, 1001, 1500} {
		t.Run(fmt.Sprintf("%d same values", count), func(t *testing.T) {
			first := min(count, 1000)
			cursor := []interfaces.KeyValue{{Key: "ingested_at", Value: "T1"}, {Key: "id", Value: int64(first)}}
			filter, err := buildBatchCursorFilter(sync_checkpoint.EffectiveCursorFields([]string{"ingested_at"}, []string{"id"}), cursor)
			require.NoError(t, err)
			second := make([]int64, 0, count-first)
			for id := int64(1); id <= int64(count); id++ {
				if matchesStringInt64CursorFilter(t, filter, map[string]any{"ingested_at": "T1", "id": id}) {
					second = append(second, id)
				}
			}
			assert.Len(t, second, count-first)
			assert.Equal(t, count, first+len(second))
		})
	}

	t.Run("999 earlier rows plus two equal boundary rows", func(t *testing.T) {
		rows := make([]map[string]any, 0, 1001)
		for id := int64(1); id <= 999; id++ {
			rows = append(rows, map[string]any{"ingested_at": "T0", "id": id})
		}
		rows = append(rows,
			map[string]any{"ingested_at": "T1", "id": int64(1000)},
			map[string]any{"ingested_at": "T1", "id": int64(1001)})
		filter, err := buildBatchCursorFilter(
			[]string{"ingested_at", "id"},
			[]interfaces.KeyValue{{Key: "ingested_at", Value: "T1"}, {Key: "id", Value: int64(1000)}},
		)
		require.NoError(t, err)
		remaining := make([]map[string]any, 0, 1)
		for _, row := range rows {
			if matchesStringInt64CursorFilter(t, filter, row) {
				remaining = append(remaining, row)
			}
		}
		assert.Equal(t, []map[string]any{{"ingested_at": "T1", "id": int64(1001)}}, remaining)
	})
}

func matchesStringInt64CursorFilter(t *testing.T, filter *interfaces.FilterCondCfg, row map[string]any) bool {
	t.Helper()
	require.Equal(t, "or", filter.Operation)
	for _, branch := range filter.SubConds {
		require.Equal(t, "and", branch.Operation)
		matches := true
		for _, condition := range branch.SubConds {
			actual, exists := row[condition.Field]
			require.True(t, exists, "row is missing cursor field %q", condition.Field)
			switch condition.Operation {
			case "==":
				matches = matches && actual == condition.Value
			case "gt":
				switch expected := condition.Value.(type) {
				case string:
					matches = matches && actual.(string) > expected
				case int64:
					matches = matches && actual.(int64) > expected
				default:
					require.FailNow(t, "unsupported cursor value type", "%T", expected)
				}
			default:
				require.FailNow(t, "unsupported cursor operation", "%s", condition.Operation)
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func TestBatchCursorKeepsCompositePrimaryFieldsForResume(t *testing.T) {
	keys := sync_checkpoint.EffectiveCursorFields(
		[]string{"updated_at", "tenant_id"}, []string{"tenant_id", "id"},
	)
	cursor := []interfaces.KeyValue{{Key: "updated_at", Value: "T1"}, {Key: "tenant_id", Value: "tenant-a"}, {Key: "id", Value: int64(1000)}}
	filter, err := buildBatchCursorFilter(keys, cursor)
	require.NoError(t, err)

	require.Equal(t, []string{"updated_at", "tenant_id", "id"}, keys)
	require.Len(t, filter.SubConds, 3)
	assert.Equal(t, "id", filter.SubConds[2].SubConds[2].Field)
	assert.Equal(t, "gt", filter.SubConds[2].SubConds[2].Operation)
	assert.Equal(t, int64(1000), filter.SubConds[2].SubConds[2].Value)
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
		task.Status = interfaces.BuildTaskStatusRunning
		lim.EXPECT().CheckIndexExist(gomock.Any(), buildIndexName("r1", "old-task")).
			Return(false, errors.New("opensearch unavailable"))
		bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "t1",
			"prepare local index failed: check local index exist failed: opensearch unavailable", []string{interfaces.BuildTaskStatusRunning}).
			Return(true, nil)

		require.NoError(t, bbw.Run(context.Background(), task, resource, &interfaces.Catalog{Enabled: true}))
		assert.Equal(t, buildIndexName("r1", "old-task"), resource.LocalIndexName)
	})

	t.Run("preserves an accepted stop when the full source read fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := &streamTableConnector{vmock.NewMockTableConnector(ctrl), vmock.NewMockTableReadSessionConnector(ctrl)}
		stream := vmock.NewMockQueryPageSession(ctrl)
		resource := workerTestResource()
		resource.LocalIndexName = "published-index"
		task := workerTestFullTask(t, resource)
		task.SyncedMark = `{"mode":"batch","cursor":[{"key":"id","value":20}]}`
		worker := &batchBuildWorker{lim: lim, bts: bts, cf: cf}

		gomock.InOrder(
			lim.EXPECT().CheckIndexExist(gomock.Any(), task.IndexName).Return(true, nil),
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil),
			connector.MockTableConnector.EXPECT().Connect(gomock.Any()).Return(nil),
			bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil),
			connector.MockTableReadSessionConnector.EXPECT().OpenTableRead(gomock.Any(), resource, gomock.Any()).Return(stream, nil),
			stream.EXPECT().ReadTotal(gomock.Any()).Return(nil, nil),
			stream.EXPECT().ReadPage(gomock.Any(), 1000).Return(nil, errors.New("source read failed")),
			stream.EXPECT().Close().Return(nil),
			connector.MockTableConnector.EXPECT().Close(gomock.Any()).Return(nil),
			// Stop has already changed running to stopping, so the failure update matches no rows.
			bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, task.ID, "read full build source page: source read failed",
				[]string{interfaces.BuildTaskStatusRunning}).Return(false, nil),
			bts.EXPECT().InternalMarkStopped(gomock.Any(), nil, task.ID, []string{interfaces.BuildTaskStatusStopping}).Return(true, nil),
		)

		require.NoError(t, worker.Run(t.Context(), task, resource, &interfaces.Catalog{ConnectorType: "mysql"}))
		assert.Equal(t, "published-index", resource.LocalIndexName)
	})
}

type streamTableConnector struct {
	*vmock.MockTableConnector
	*vmock.MockTableReadSessionConnector
}

func TestBatchBuildWorkerExecuteBuild(t *testing.T) {
	t.Run("primary key only full build resumes and publishes without a baseline", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := &streamTableConnector{vmock.NewMockTableConnector(ctrl), vmock.NewMockTableReadSessionConnector(ctrl)}
		reportedTotal := int64(0)
		snapshot := vmock.NewMockQueryPageSession(ctrl)
		resource := workerTestResource()
		resource.IndexConfig.IncrementalFields = nil
		resource.SyncMark = `{"mode":"batch","cursor":[{"key":"id","value":17}]}`
		task := workerTestFullTask(t, resource)
		task.IndexConfig.IncrementalFields = nil
		task.SyncedMark = `{"mode":"batch","cursor":[{"key":"id","value":20}]}`
		task.SyncedCount = 20
		worker := &batchBuildWorker{lim: lim, bts: bts, rs: rs, cf: cf}

		db, mockDB, err := sqlmock.New()
		require.NoError(t, err)
		oldDB := logics.DB
		logics.DB = db
		t.Cleanup(func() {
			logics.DB = oldDB
			_ = db.Close()
		})
		mockDB.ExpectBegin()
		mockDB.ExpectCommit()

		lim.EXPECT().CheckIndexExist(gomock.Any(), task.IndexName).Return(true, nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil).Times(2)
		var taskCheckpoint string
		var totalProgress, syncedProgress []int64
		bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any(), runningOrStoppingStatuses).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress, _ []string) (bool, error) {
				if progress.TotalCount != nil {
					totalProgress = append(totalProgress, *progress.TotalCount)
				}
				if progress.SyncedCount != nil {
					syncedProgress = append(syncedProgress, *progress.SyncedCount)
				}
				if progress.SyncedMark != nil {
					taskCheckpoint = *progress.SyncedMark
				}
				return true, nil
			},
		).Times(3)
		lim.EXPECT().IndexDocuments(gomock.Any(), task.IndexName, gomock.Len(1)).DoAndReturn(successfulIndexDocuments).Times(2)
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		mark := `{"mode":"batch","cursor":[{"key":"id","value":22}]}`
		readCtx := gomock.Cond(func(ctx context.Context) bool { return ctx.Done() == nil })
		gomock.InOrder(
			connector.MockTableConnector.EXPECT().Connect(gomock.Any()).Return(nil),
			connector.MockTableReadSessionConnector.EXPECT().OpenTableRead(readCtx, resource, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (interfaces.QueryPageSession, error) {
					assert.True(t, params.NeedTotal)
					assert.Zero(t, params.Paging.Limit)
					assert.Equal(t, int64(20), params.FilterCondCfg.SubConds[0].SubConds[0].Value)
					return snapshot, nil
				}),
			snapshot.EXPECT().ReadTotal(readCtx).Return(&reportedTotal, nil),
			snapshot.EXPECT().ReadPage(readCtx, 1000).Return(&interfaces.QueryPage{Entries: []map[string]any{{"id": int64(21)}}, TotalCount: &reportedTotal, HasMore: true}, nil),
			snapshot.EXPECT().ReadPage(readCtx, 1000).Return(&interfaces.QueryPage{Entries: []map[string]any{{"id": int64(22)}}}, nil),
			snapshot.EXPECT().Close().Return(nil),
			rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(resource, nil),
			rs.EXPECT().InternalPublishLocalIndex(gomock.Any(), txMatcher, resource.ID, task.IndexName, "", resource.UpdateTime).Return(true, nil),
			bts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, task.ID).Return(true, nil),
			connector.MockTableConnector.EXPECT().Close(gomock.Any()).Return(nil),
		)

		require.NoError(t, worker.executeBuild(t.Context(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task))
		assert.Equal(t, []int64{20}, totalProgress)
		assert.Equal(t, []int64{21, 22}, syncedProgress)
		assert.Equal(t, mark, taskCheckpoint)
		assert.Empty(t, resource.SyncMark)
		assert.Equal(t, interfaces.ResourceLocalIndexStatusAvailable, resource.LocalIndexStatus)
		require.NoError(t, mockDB.ExpectationsWereMet())
	})

	t.Run("full snapshot errors and interruptions release the source without publishing", func(t *testing.T) {
		fail := errors.New("snapshot failed")
		for _, step := range []string{"begin", "count", "read", "close", "stop", "cancel"} {
			t.Run(step, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				lim := vmock.NewMockLocalIndexManager(ctrl)
				bts := vmock.NewMockBuildTaskService(ctrl)
				cf := vmock.NewMockConnectorFactory(ctrl)
				connector := &streamTableConnector{vmock.NewMockTableConnector(ctrl), vmock.NewMockTableReadSessionConnector(ctrl)}
				snapshot := vmock.NewMockQueryPageSession(ctrl)
				resource := workerTestResource()
				resource.LocalIndexName = "published-index"
				task := workerTestFullTask(t, resource)
				task.SyncedMark = `{"mode":"batch","cursor":[{"key":"id","value":20}]}`
				worker := &batchBuildWorker{lim: lim, bts: bts, cf: cf}

				lim.EXPECT().CheckIndexExist(gomock.Any(), task.IndexName).Return(true, nil)
				cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
				connector.MockTableConnector.EXPECT().Connect(gomock.Any()).Return(nil)
				connector.MockTableConnector.EXPECT().Close(gomock.Any()).Return(nil)
				bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
				begin := connector.MockTableReadSessionConnector.EXPECT().OpenTableRead(gomock.Any(), resource, gomock.Any())
				switch step {
				case "begin":
					begin.Return(nil, fail)
				case "count":
					begin.Return(snapshot, nil)
					snapshot.EXPECT().ReadTotal(gomock.Any()).Return(nil, fail)
					snapshot.EXPECT().Close().Return(nil)
				default:
					begin.Return(snapshot, nil)
					snapshot.EXPECT().ReadTotal(gomock.Any()).Return(nil, nil)
					switch step {
					case "read":
						snapshot.EXPECT().ReadPage(gomock.Any(), 1000).Return(nil, fail)
						snapshot.EXPECT().Close().Return(nil)
					case "close":
						snapshot.EXPECT().ReadPage(gomock.Any(), 1000).Return(&interfaces.QueryPage{}, nil)
						snapshot.EXPECT().Close().Return(fail)
					case "stop", "cancel":
						rows := make([]map[string]any, 1000)
						for i := range rows {
							rows[i] = map[string]any{"id": int64(21 + i)}
						}
						snapshot.EXPECT().ReadPage(gomock.Any(), 1000).Return(&interfaces.QueryPage{Entries: rows, HasMore: true}, nil)
						lim.EXPECT().IndexDocuments(gomock.Any(), task.IndexName, gomock.Len(1000)).DoAndReturn(successfulIndexDocuments)
						bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any(), runningOrStoppingStatuses).Return(true, nil).Times(2)
						snapshot.EXPECT().Close().Return(nil)
						status := interfaces.BuildTaskStatusCancelled
						if step == "stop" {
							status = interfaces.BuildTaskStatusStopping
							bts.EXPECT().InternalMarkStopped(gomock.Any(), nil, task.ID, nil).Return(true, nil)
						}
						bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(status, nil)
					}
				}

				err := worker.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)
				if step == "stop" || step == "cancel" {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, fail)
				}
				assert.Equal(t, "published-index", resource.LocalIndexName)
			})
		}
	})
	t.Run("corrupt columns fail before full rebuild index deletion or creation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		mfs := vmock.NewMockModelFactoryService(ctrl)
		worker := &batchBuildWorker{lim: lim, cf: cf, mfs: mfs}
		resource := &interfaces.Resource{LocalIndexName: "index", SourceMetadata: map[string]any{"columns": "invalid metadata"}, SchemaDefinition: []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)"}}}
		task := &interfaces.BuildTask{ID: "task", IndexName: "index", ExecuteType: interfaces.BuildTaskExecuteTypeFull, SyncedCount: 17}
		err := worker.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)
		require.Error(t, err)
		assert.Equal(t, "index", resource.LocalIndexName)
		assert.Empty(t, task.SyncedMark)
		assert.EqualValues(t, 17, task.SyncedCount)
		assert.Nil(t, resource.SchemaDefinition[0].Attributes)
	})
	for _, connectorType := range []string{"mysql", "mariadb", "postgresql", "sqlserver", "oracle", "doris", "hana"} {
		t.Run("exact integer decimal write for "+connectorType, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			lim := vmock.NewMockLocalIndexManager(ctrl)
			bts := vmock.NewMockBuildTaskService(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			resource := &interfaces.Resource{LocalIndexName: "index", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(38,0)"}}}
			mark := `{"mode":"batch","cursor":[{"key":"id","value":8000}]}`
			task := &interfaces.BuildTask{ID: "task", IndexName: "index", ExecuteType: interfaces.BuildTaskExecuteTypeFull, SyncedMark: mark, SyncedCount: 8000,
				IndexConfig: &interfaces.BuildTaskIndexConfig{IndexConfigContract: interfaces.IndexConfigContract{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"id"}}}}
			lim.EXPECT().CheckIndexExist(gomock.Any(), "index").Return(true, nil)
			connector := vmock.NewMockTableConnector(ctrl)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), connectorType, gomock.Any()).Return(connector, nil)
			connector.EXPECT().Connect(gomock.Any()).Return(nil)
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			document := map[string]any{"id": int64(8001), "amount": "9007199254740993"}
			connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(&interfaces.ResourceDataQueryResult{TotalCount: 1, Entries: []map[string]any{document}}, nil)
			bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
			bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any(), runningOrStoppingStatuses).DoAndReturn(func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress, _ []string) (bool, error) {
				assert.Nil(t, progress.SyncedMark)
				assert.Nil(t, progress.SyncedCount)
				return true, nil
			})
			writeError := errors.New("index write failed")
			lim.EXPECT().IndexDocuments(gomock.Any(), "index", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, writes []interfaces.DocumentWrite) ([]interfaces.DocumentWriteResult, error) {
				require.Len(t, writes, 1)
				assert.Equal(t, int64(9007199254740993), writes[0].Document["amount"])
				return nil, writeError
			})
			worker := &batchBuildWorker{lim: lim, bts: bts, cf: cf}
			err := worker.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: connectorType}, resource, task)
			require.ErrorIs(t, err, writeError)
			assert.Equal(t, "9007199254740993", document["amount"])
			assert.Equal(t, mark, task.SyncedMark)
			assert.EqualValues(t, 8000, task.SyncedCount)
		})
	}

	t.Run("invalid decimal values cannot write or advance incremental checkpoints", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		resource := &interfaces.Resource{LocalIndexName: "index", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)"}}}
		mark := `{"mode":"batch","cursor":[{"key":"id","value":8000}]}`
		task := &interfaces.BuildTask{ID: "task", IndexName: "index", ExecuteType: interfaces.BuildTaskExecuteTypeIncremental, SyncedMark: mark, SyncedCount: 8000,
			IndexConfig: &interfaces.BuildTaskIndexConfig{IndexConfigContract: interfaces.IndexConfigContract{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"id"}}}}
		lim.EXPECT().CheckIndexExist(gomock.Any(), "index").Return(true, nil)
		connector := vmock.NewMockTableConnector(ctrl)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(&interfaces.ResourceDataQueryResult{TotalCount: 1, Entries: []map[string]any{{"id": int64(8001), "amount": "1.234"}}}, nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any(), runningOrStoppingStatuses).DoAndReturn(func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress, _ []string) (bool, error) {
			assert.Nil(t, progress.SyncedMark)
			assert.Nil(t, progress.SyncedCount)
			return true, nil
		})
		worker := &batchBuildWorker{lim: lim, bts: bts, cf: cf}
		err := worker.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)
		var failure *interfaces.DecimalIndexError
		require.ErrorAs(t, err, &failure)
		assert.Equal(t, "amount", failure.Field)
		assert.Equal(t, "value_out_of_range", failure.Reason)
		docID, idErr := generateDocumentID([]interfaces.KeyValue{{Key: "id", Value: int64(8001)}})
		require.NoError(t, idErr)
		assert.ErrorContains(t, err, fmt.Sprintf("build document %q:", docID))
		assert.NotContains(t, err.Error(), "1.234")
		assert.Equal(t, mark, task.SyncedMark)
		assert.EqualValues(t, 8000, task.SyncedCount)
	})
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

	t.Run("empty full build replaces the read-only index and publishes an established empty checkpoint", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		resource := workerTestResource()
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusReadOnly
		resource.LocalIndexName = "published-readonly"
		resource.SchemaDefinition = append(resource.SchemaDefinition,
			&interfaces.Property{Name: "attachment", Type: interfaces.DataType_Binary},
			&interfaces.Property{Name: "metadata", Type: interfaces.DataType_Other},
		)
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
		bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any(), runningOrStoppingStatuses).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress, _ []string) (bool, error) {
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
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				assert.Equal(t, []string{"id"}, params.OutputFields)
				return &interfaces.ResourceDataQueryResult{TotalCount: 0}, nil
			},
		)
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		mockDB.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(resource, nil)
		rs.EXPECT().InternalPublishLocalIndex(gomock.Any(), txMatcher, resource.ID,
			indexName, `{"mode":"batch","cursor":[]}`, resource.UpdateTime).Return(true, nil)
		bts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, task.ID).Return(true, nil)
		mockDB.ExpectCommit()

		err = bbw.executeBuild(context.Background(), &interfaces.Catalog{ID: "c1", ConnectorType: "mysql"}, resource, task)

		require.NoError(t, err)
		assert.Equal(t, []int64{0}, totalCounts)
		assert.Equal(t, []string{"", `{"mode":"batch","cursor":[]}`}, progressMarks)
		assert.Equal(t, `{"mode":"batch","cursor":[]}`, resource.SyncMark)
		assert.Equal(t, indexName, resource.LocalIndexName)
		assert.Equal(t, interfaces.ResourceLocalIndexStatusAvailable, resource.LocalIndexStatus)
		require.NoError(t, mockDB.ExpectationsWereMet())
	})

	t.Run("resumed incremental keeps cumulative total and advances checkpoints atomically", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		snapshotConnector := &streamTableConnector{connector, vmock.NewMockTableReadSessionConnector(ctrl)}
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
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(snapshotConnector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		payloads := []struct{ source, stored any }{
			{`{"items":[{"sku":"BOX-01","count":2}]}`, map[string]any{"items": []any{map[string]any{"sku": "BOX-01", "count": json.Number("2")}}}},
			{`{"items":["SENSOR-A","SENSOR-B"]}`, map[string]any{"items": []any{"SENSOR-A", "SENSOR-B"}}},
			{`{"items":[]}`, map[string]any{"items": []any{}}},
			{`{"items":null}`, map[string]any{"items": nil}},
			{"null", nil}, {nil, nil}, {`"null"`, "null"}, {`"scalar"`, "scalar"}, {`""`, ""},
		}
		queryCount := 0
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Times(73).DoAndReturn(
			func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				queryCount++
				if queryCount == 73 {
					return &interfaces.ResourceDataQueryResult{}, nil
				}
				require.NotNil(t, params.FilterCondCfg)
				assert.Equal(t, 1000, params.Paging.Limit)
				assert.Equal(t, queryCount == 1, params.NeedTotal)
				entries := make([]map[string]any, 1000)
				firstID := int64(8001 + (queryCount-1)*1000)
				for index := range entries {
					entries[index] = map[string]any{
						"id":      firstID + int64(index),
						"payload": payloads[(index+queryCount)%len(payloads)].source,
					}
				}
				result := &interfaces.ResourceDataQueryResult{Entries: entries}
				if queryCount == 1 {
					result.TotalCount = 72000
				}
				return result, nil
			})
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil).Times(73)
		bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any(), runningOrStoppingStatuses).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress, _ []string) (bool, error) {
				require.NotNil(t, progress.TotalCount)
				assert.EqualValues(t, 80000, *progress.TotalCount)
				return true, nil
			})
		indexedBatches := 0
		lim.EXPECT().IndexDocuments(gomock.Any(), "current-index", gomock.Any()).Times(72).DoAndReturn(
			func(ctx context.Context, indexName string, documents []interfaces.DocumentWrite) ([]interfaces.DocumentWriteResult, error) {
				require.Len(t, documents, 1000)
				for _, write := range documents {
					document := write.Document
					id := document["id"].(int64)
					expected := payloads[(int(id-8001)%1000+indexedBatches+1)%len(payloads)].stored
					assert.Equal(t, expected, document["payload"])
				}
				indexedBatches++
				return successfulIndexDocuments(ctx, indexName, documents)
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
		bts.EXPECT().InternalSetProgress(gomock.Any(), txMatcher, task.ID, gomock.Any(), runningOrStoppingStatuses).Times(72).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress, _ []string) (bool, error) {
				finalProgress = progress
				return true, nil
			})
		ra := vmock.NewMockResourceAccess(ctrl)
		previousRA := logics.RA
		logics.RA = ra
		t.Cleanup(func() { logics.RA = previousRA })
		ra.EXPECT().CompareAndSetSyncMark(gomock.Any(), txMatcher, resource.ID, "current-index",
			interfaces.ResourceLocalIndexStatusAvailable, gomock.Any(), gomock.Any()).Return(true, nil).Times(72)
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
			func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				assert.Nil(t, params.FilterCondCfg)
				return &interfaces.ResourceDataQueryResult{TotalCount: 1, Entries: []map[string]any{{"id": int64(1)}}}, nil
			})
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any(), runningOrStoppingStatuses).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress, _ []string) (bool, error) {
				require.NotNil(t, progress.TotalCount)
				assert.EqualValues(t, 1, *progress.TotalCount)
				return true, nil
			})
		lim.EXPECT().IndexDocuments(gomock.Any(), "current-index", gomock.Any()).DoAndReturn(successfulIndexDocuments)
		mockDB.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(resource, nil)
		bts.EXPECT().InternalSetProgress(gomock.Any(), txMatcher, task.ID, gomock.Any(), runningOrStoppingStatuses).Return(true, nil)
		ra := vmock.NewMockResourceAccess(ctrl)
		previousRA := logics.RA
		logics.RA = ra
		t.Cleanup(func() { logics.RA = previousRA })
		ra.EXPECT().CompareAndSetSyncMark(gomock.Any(), txMatcher, resource.ID, "current-index",
			interfaces.ResourceLocalIndexStatusAvailable, gomock.Any(), gomock.Any()).Return(true, nil)
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
			func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				require.NotNil(t, params.FilterCondCfg)
				return &interfaces.ResourceDataQueryResult{TotalCount: 2, Entries: []map[string]any{{"id": int64(9)}, {"id": int64(10)}}}, nil
			})
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		progressCalls := 0
		bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any(), runningOrStoppingStatuses).Times(2).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress, _ []string) (bool, error) {
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
		lim.EXPECT().IndexDocuments(gomock.Any(), "current-index", gomock.Any()).DoAndReturn(successfulIndexDocuments)
		newMark := `{"mode":"batch","cursor":[{"key":"id","value":10}]}`
		mockDB.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(resource, nil)
		rs.EXPECT().InternalPublishLocalIndex(gomock.Any(), txMatcher, resource.ID,
			"current-index", newMark, resource.UpdateTime).Return(true, nil)
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
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(&interfaces.ResourceDataQueryResult{}, nil)
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		bts.EXPECT().InternalMarkCompleted(gomock.Any(), nil, task.ID).Return(true, nil)

		err := bbw.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)

		require.NoError(t, err)
		assert.Equal(t, resource.SyncMark, task.SyncedMark)
	})
}

func TestBatchBuildWorkerReconcileKeys(t *testing.T) {
	const checkpoint = `{"mode":"batch","cursor":[{"key":"id","value":1},{"key":"tenant","value":"north"}]}`
	const largeID int64 = 9007199254740993
	found := map[string]any{"tenant": "north", "id": largeID, "title": "new title"}
	foundID, err := generateDocumentID([]interfaces.KeyValue{
		{Key: "tenant", Value: "north"}, {Key: "id", Value: largeID},
	})
	require.NoError(t, err)
	missingID, err := generateDocumentID([]interfaces.KeyValue{
		{Key: "tenant", Value: "south"}, {Key: "id", Value: int64(7)},
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		page     *interfaces.QueryPage
		openErr  error
		readErr  error
		closeErr error
		recheck  bool
		newIndex bool
		indexErr bool
		wantErr  string
	}{
		{name: "refreshes found key and deletes absent key from current index", recheck: true,
			page: &interfaces.QueryPage{Entries: []map[string]any{found}}},
		{name: "incomplete page cannot cause deletions",
			page: &interfaces.QueryPage{HasMore: true}, wantErr: "could not complete the source key read"},
		{name: "query permission failure cannot cause deletions",
			openErr: errors.New("permission denied"), wantErr: "could not open a source key read session"},
		{name: "read failure cannot cause deletions",
			readErr: errors.New("source unavailable"), wantErr: "could not complete the source key read"},
		{name: "session close failure cannot cause deletions",
			page: &interfaces.QueryPage{}, closeErr: errors.New("connection failed"), wantErr: "could not complete the source key read"},
		{name: "collation-equivalent row cannot cause deletion of exact key",
			page:    &interfaces.QueryPage{Entries: []map[string]any{{"tenant": "NORTH", "id": largeID}}},
			wantErr: "primary key does not exactly match"},
		{name: "fails if published index changes during read", recheck: true, newIndex: true,
			page: &interfaces.QueryPage{Entries: []map[string]any{found}}, wantErr: "published index or resource configuration changed"},
		{name: "does not report completion after a partial write failure", recheck: true, indexErr: true,
			page: &interfaces.QueryPage{Entries: []map[string]any{found}}, wantErr: "could not write all found keys"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bts := vmock.NewMockBuildTaskService(ctrl)
			indexes := vmock.NewMockLocalIndexManager(ctrl)
			resources := vmock.NewMockResourceService(ctrl)
			factory := vmock.NewMockConnectorFactory(ctrl)
			session := vmock.NewMockQueryPageSession(ctrl)
			resource := workerTestResource()
			resource.Enabled = true
			resource.CatalogID = "catalog-1"
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
			resource.LocalIndexName = "current-index"
			resource.SyncMark = checkpoint
			resource.SchemaDefinition = append(resource.SchemaDefinition,
				&interfaces.Property{Name: "tenant", Type: interfaces.DataType_String},
				&interfaces.Property{Name: "title", Type: interfaces.DataType_String},
			)
			resource.IndexConfig.PrimaryKeyFields = []string{"tenant", "id"}
			task := workerTestFullTask(t, resource)
			task.IndexConfig.PrimaryKeyFields = resource.IndexConfig.PrimaryKeyFields
			task.ExecuteType = interfaces.BuildTaskExecuteTypeKeys
			task.IndexName = resource.LocalIndexName
			task.Keys = []map[string]any{
				{"id": json.Number("9007199254740993"), "tenant": "north"},
				{"tenant": "south", "id": json.Number("7")},
				{"tenant": "north", "id": json.Number("9007199254740993")},
			}
			catalog := &interfaces.Catalog{ConnectorType: "postgresql", Enabled: true}
			connector := &streamTableConnector{vmock.NewMockTableConnector(ctrl), vmock.NewMockTableReadSessionConnector(ctrl)}
			open := func(_ context.Context, gotResource *interfaces.Resource,
				params *interfaces.ResourceDataQueryParams) (interfaces.QueryPageSession, error) {
				assert.Same(t, resource, gotResource)
				assert.ElementsMatch(t, []string{"id", "tenant", "title"}, params.OutputFields)
				assert.False(t, params.NeedTotal)
				assert.Zero(t, params.Paging.Offset)
				require.NotNil(t, params.ActualFilterCond)
				filter := params.FilterCondCfg
				require.NotNil(t, filter)
				assert.Equal(t, "or", filter.Operation)
				require.Len(t, filter.SubConds, 2)
				for i, tuple := range []struct {
					tenant string
					id     int64
				}{{"north", largeID}, {"south", 7}} {
					branch := filter.SubConds[i]
					assert.Equal(t, "and", branch.Operation)
					require.Len(t, branch.SubConds, 2)
					assert.Equal(t, "tenant", branch.SubConds[0].Field)
					assert.Equal(t, tuple.tenant, branch.SubConds[0].Value)
					assert.Equal(t, "id", branch.SubConds[1].Field)
					assert.Equal(t, tuple.id, branch.SubConds[1].Value)
				}
				if tc.openErr != nil {
					return nil, tc.openErr
				}
				return session, nil
			}
			worker := &batchBuildWorker{bts: bts, rs: resources, cf: factory, lim: indexes}

			total := int64(2)
			zero := int64(0)
			initial := bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID,
				interfaces.BuildTaskProgress{TotalCount: &total, SyncedCount: &zero},
				runningOrStoppingStatuses).Return(true, nil)
			calls := []any{
				indexes.EXPECT().CheckIndexExist(gomock.Any(), resource.LocalIndexName).Return(true, nil),
				initial,
				factory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).Return(connector, nil),
				connector.MockTableConnector.EXPECT().Connect(gomock.Any()).Return(nil),
				connector.MockTableReadSessionConnector.EXPECT().OpenTableRead(gomock.Any(), resource, gomock.Any()).DoAndReturn(open),
			}
			if tc.openErr == nil {
				calls = append(calls, session.EXPECT().ReadPage(gomock.Any(), 2).Return(tc.page, tc.readErr),
					session.EXPECT().Close().Return(tc.closeErr))
			}
			calls = append(calls, connector.MockTableConnector.EXPECT().Close(gomock.Any()).Return(nil))
			if tc.recheck {
				currentResource := *resource
				if tc.newIndex {
					currentResource.LocalIndexName = "replacement-index"
				}
				calls = append(calls, resources.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(&currentResource, nil))
				if !tc.newIndex {
					documents := []interfaces.DocumentWrite{{ID: foundID, Document: found}}
					status := 200
					if tc.indexErr {
						status = 500
					}
					calls = append(calls,
						bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil),
						indexes.EXPECT().IndexDocuments(gomock.Any(), resource.LocalIndexName, documents).
							Return([]interfaces.DocumentWriteResult{{ID: foundID, Status: status}}, nil),
					)
				}
			}
			if tc.wantErr == "" {
				calls = append(calls,
					indexes.EXPECT().DeleteDocuments(gomock.Any(), resource.LocalIndexName, []string{missingID}).Return(nil),
					bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, interfaces.BuildTaskProgress{SyncedCount: &total},
						runningOrStoppingStatuses).Return(true, nil),
					bts.EXPECT().InternalMarkCompleted(gomock.Any(), nil, task.ID).Return(true, nil),
				)
			}
			if tc.wantErr == "" {
				task.Status = interfaces.BuildTaskStatusPending
				task.IndexName = "previous-attempt"
				catalogs := vmock.NewMockCatalogService(ctrl)
				worker.cs = catalogs
				btsWorker := &BuildTaskWorker{bts: bts, bbw: worker}
				claimCalls := []any{
					bts.EXPECT().InternalGetByID(gomock.Any(), task.ID).Return(task, nil),
					resources.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil),
					catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(catalog, nil),
					bts.EXPECT().InternalMarkRunning(gomock.Any(), nil, task.ID, &resource.LocalIndexName).Return(true, nil),
				}
				gomock.InOrder(append(claimCalls, calls...)...)
				require.NoError(t, btsWorker.runBatchTask(t.Context(), task.ID))
				assert.Equal(t, resource.LocalIndexName, task.IndexName)
			} else {
				gomock.InOrder(calls...)
				err := worker.reconcileKeys(t.Context(), catalog, resource, task)
				require.ErrorContains(t, err, tc.wantErr)
			}
			assert.Equal(t, checkpoint, resource.SyncMark)
			assert.Empty(t, task.SyncedMark)
		})
	}
}

func TestBatchBuildWorkerKeysFailsWithoutTableReadSession(t *testing.T) {
	ctrl := gomock.NewController(t)
	tasks := vmock.NewMockBuildTaskService(ctrl)
	indexes := vmock.NewMockLocalIndexManager(ctrl)
	factory := vmock.NewMockConnectorFactory(ctrl)
	connector := vmock.NewMockTableConnector(ctrl)
	resource := workerTestResource()
	task := workerTestFullTask(t, resource)
	task.ExecuteType = interfaces.BuildTaskExecuteTypeKeys
	task.Keys = []map[string]any{{"id": int64(1)}}
	catalog := &interfaces.Catalog{ConnectorType: "custom-table"}
	worker := &batchBuildWorker{bts: tasks, cf: factory, lim: indexes}

	total, zero := int64(1), int64(0)
	gomock.InOrder(
		indexes.EXPECT().CheckIndexExist(gomock.Any(), task.IndexName).Return(true, nil),
		tasks.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID,
			interfaces.BuildTaskProgress{TotalCount: &total, SyncedCount: &zero},
			runningOrStoppingStatuses).Return(true, nil),
		factory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).Return(connector, nil),
		connector.EXPECT().Close(gomock.Any()).Return(nil),
		tasks.EXPECT().InternalMarkFailed(gomock.Any(), nil, task.ID, "source connector does not support table read sessions",
			[]string{interfaces.BuildTaskStatusRunning}).Return(true, nil),
	)

	require.NoError(t, worker.Run(t.Context(), task, resource, catalog))
}

func TestBatchBuildWorkerCommitIncrementalProgress(t *testing.T) {
	for _, tc := range []struct {
		name, indexName, status, detail string
		compareAndSet                   bool
	}{
		{"published index replaced", "replacement-index", interfaces.ResourceLocalIndexStatusAvailable,
			"resource local index changed during incremental build", false},
		{"published index read-only", "current-index", interfaces.ResourceLocalIndexStatusReadOnly,
			"published local index is read-only", false},
		{"index state changes after reading Resource", "current-index", interfaces.ResourceLocalIndexStatusAvailable,
			"resource local index or checkpoint changed during incremental build", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bts := vmock.NewMockBuildTaskService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			ra := vmock.NewMockResourceAccess(ctrl)
			resource := workerTestResource()
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
			resource.LocalIndexName = "current-index"
			resource.SyncMark = `{"mode":"batch","cursor":[]}`
			current := *resource
			current.LocalIndexName = tc.indexName
			current.LocalIndexStatus = tc.status
			task := workerTestFullTask(t, resource)
			task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
			newMark := `{"mode":"batch","cursor":[{"key":"id","value":1}]}`
			progress := interfaces.BuildTaskProgress{SyncedMark: &newMark}
			bbw := &batchBuildWorker{bts: bts, rs: rs}

			db, mockDB, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			oldDB, oldRA := logics.DB, logics.RA
			logics.DB, logics.RA = db, ra
			defer func() { logics.DB, logics.RA = oldDB, oldRA }()

			mockDB.ExpectBegin()
			txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
			rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(&current, nil)
			if tc.compareAndSet {
				ra.EXPECT().CompareAndSetSyncMark(gomock.Any(), txMatcher, resource.ID, resource.LocalIndexName,
					interfaces.ResourceLocalIndexStatusAvailable, resource.SyncMark, newMark).Return(false, nil)
			}
			mockDB.ExpectRollback()

			err = bbw.commitIncrementalProgress(t.Context(), resource, task,
				resource.LocalIndexName, resource.SyncMark, newMark, progress)

			require.ErrorContains(t, err, tc.detail)
			assert.Equal(t, `{"mode":"batch","cursor":[]}`, resource.SyncMark)
			require.NoError(t, mockDB.ExpectationsWereMet())
		})
	}
}

func TestBatchBuildWorkerReadsSameIncrementalValueAcrossPages(t *testing.T) {
	ctrl := gomock.NewController(t)
	lim := vmock.NewMockLocalIndexManager(ctrl)
	bts := vmock.NewMockBuildTaskService(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	cf := vmock.NewMockConnectorFactory(ctrl)
	connector := vmock.NewMockTableConnector(ctrl)
	resource := workerTestResource()
	filterable := false
	for _, property := range resource.SchemaDefinition {
		if property.Name == "id" {
			property.Filterable = &filterable
		}
	}
	resource.SchemaDefinition = append(resource.SchemaDefinition,
		&interfaces.Property{Name: "ingested_at", Type: interfaces.DataType_Timestamp, Filterable: &filterable})
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
		func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			queryCount++
			for _, property := range resource.SchemaDefinition {
				if property.Name == "id" || property.Name == "ingested_at" {
					require.NotNil(t, property.Filterable)
					assert.False(t, *property.Filterable, "internal pagination must not mutate the public switch")
				}
			}
			require.NotNil(t, params.ActualFilterCond)
			require.Len(t, params.Sort, 2)
			assert.Equal(t, "ingested_at", params.Sort[0].Field)
			assert.Equal(t, "id", params.Sort[1].Field)
			require.NotNil(t, params.FilterCondCfg)
			if queryCount == 2 {
				require.Len(t, params.FilterCondCfg.SubConds, 2)
				idCondition := params.FilterCondCfg.SubConds[1].SubConds[1]
				assert.Equal(t, "id", idCondition.Field)
				assert.Equal(t, "gt", idCondition.Operation)
				assert.Equal(t, int64(1020), idCondition.Value)
			}
			cursorTime := params.FilterCondCfg.SubConds[0].SubConds[0].Value.(string)
			cursorID := params.FilterCondCfg.SubConds[1].SubConds[1].Value.(int64)
			entries := make([]map[string]any, 0, params.Paging.Limit)
			for _, row := range sourceRows {
				rowTime := row["ingested_at"].(string)
				rowID := row["id"].(int64)
				if rowTime > cursorTime || rowTime == cursorTime && rowID > cursorID {
					entries = append(entries, row)
					if len(entries) == params.Paging.Limit {
						break
					}
				}
			}
			result := &interfaces.ResourceDataQueryResult{Entries: entries}
			if queryCount == 1 {
				result.TotalCount = int64(len(sourceRows))
			}
			return result, nil
		})
	connector.EXPECT().Close(gomock.Any()).Return(nil)
	bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil).Times(2)
	bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any(), runningOrStoppingStatuses).DoAndReturn(
		func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress, _ []string) (bool, error) {
			require.NotNil(t, progress.TotalCount)
			assert.EqualValues(t, 1500, *progress.TotalCount)
			return true, nil
		})
	batchSizes := make([]int, 0, 2)
	lim.EXPECT().IndexDocuments(gomock.Any(), "current-index", gomock.Any()).Times(2).DoAndReturn(
		func(ctx context.Context, indexName string, documents []interfaces.DocumentWrite) ([]interfaces.DocumentWriteResult, error) {
			batchSizes = append(batchSizes, len(documents))
			return successfulIndexDocuments(ctx, indexName, documents)
		})
	for range 2 {
		mockDB.ExpectBegin()
		mockDB.ExpectCommit()
	}
	txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
	rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Times(2).Return(resource, nil)
	var finalProgress interfaces.BuildTaskProgress
	bts.EXPECT().InternalSetProgress(gomock.Any(), txMatcher, task.ID, gomock.Any(), runningOrStoppingStatuses).Times(2).DoAndReturn(
		func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress, _ []string) (bool, error) {
			finalProgress = progress
			return true, nil
		})
	ra := vmock.NewMockResourceAccess(ctrl)
	previousRA := logics.RA
	logics.RA = ra
	t.Cleanup(func() { logics.RA = previousRA })
	ra.EXPECT().CompareAndSetSyncMark(gomock.Any(), txMatcher, resource.ID, "current-index",
		interfaces.ResourceLocalIndexStatusAvailable, gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
	bts.EXPECT().InternalMarkCompleted(gomock.Any(), nil, task.ID).Return(true, nil)

	err = bbw.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)

	require.NoError(t, err)
	assert.Equal(t, []int{1000, 500}, batchSizes)
	for _, property := range resource.SchemaDefinition {
		if property.Name == "id" || property.Name == "ingested_at" {
			require.NotNil(t, property.Filterable)
			assert.False(t, *property.Filterable)
		}
	}
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
	t.Run("large integer supports only keyword features", func(t *testing.T) {
		for _, kind := range []string{interfaces.PropertyFeatureType_Keyword, interfaces.PropertyFeatureType_Fulltext, interfaces.PropertyFeatureType_Vector} {
			schema := []*interfaces.Property{{Name: "wide", Type: interfaces.DataType_LargeInteger, OriginalType: "LARGEINT", Features: []interfaces.PropertyFeature{{FeatureName: kind, FeatureType: kind, IsDefault: true}}}}
			err := validateBuildTaskSchemaFeatures(interfaces.ResourceCategoryTable, schema)
			if kind == interfaces.PropertyFeatureType_Keyword {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "does not support feature type")
			}
		}
	})
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
