// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
)

func TestBuildTaskWorkerFillBatchQueueRefillsEmptyQueue(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	bts := vmock.NewMockBuildTaskService(ctrl)
	worker := &BuildTaskWorker{
		bts:        bts,
		batchQueue: make(chan string, 4),
		inFlight:   make(map[string]struct{}),
	}
	first := bts.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
			assert.Equal(t, 4, params.Limit)
			assert.Equal(t, []string{interfaces.BuildTaskStatusPending}, params.Statuses)
			assert.Equal(t, interfaces.BuildTaskModeBatch, params.Mode)
			assert.Equal(t, interfaces.BuildTaskSortCreateTime, params.Sort)
			assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
			assert.Empty(t, params.ExcludeResourceIDs)
			return []*interfaces.BuildTaskSummary{
				{ID: "full-1", ResourceID: "resource-1", ExecuteType: interfaces.BuildTaskExecuteTypeFull},
				{ID: "keys-1", ResourceID: "resource-1", ExecuteType: interfaces.BuildTaskExecuteTypeKeys},
				{ID: "incremental-2", ResourceID: "resource-2", ExecuteType: interfaces.BuildTaskExecuteTypeIncremental},
			}, nil
		})
	waiting := bts.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
			assert.Equal(t, []string{"resource-1", "resource-2"}, params.ExcludeResourceIDs)
			return nil, nil
		}).After(first)
	bts.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
			assert.Equal(t, []string{"resource-2"}, params.ExcludeResourceIDs)
			return []*interfaces.BuildTaskSummary{{ID: "keys-1", ResourceID: "resource-1"}}, nil
		}).After(waiting)

	worker.stopCh = make(chan struct{})
	worker.fillBatchQueue(context.Background())

	require.Len(t, worker.batchQueue, 2)
	assert.False(t, worker.addInFlight("full-1"))
	assert.Equal(t, "full-1", <-worker.batchQueue)
	assert.Equal(t, "incremental-2", <-worker.batchQueue)

	worker.fillBatchQueue(t.Context())
	assert.Empty(t, worker.batchQueue)

	worker.removeInFlight("full-1")
	worker.fillBatchQueue(t.Context())
	require.Len(t, worker.batchQueue, 1)
	assert.Equal(t, "keys-1", <-worker.batchQueue)
}

func TestBuildTaskWorkerFillStreamingQueueRefillsEmptyQueue(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	bts := vmock.NewMockBuildTaskService(ctrl)
	worker := &BuildTaskWorker{
		bts:            bts,
		streamingQueue: make(chan string, 4),
		inFlight:       make(map[string]struct{}),
	}
	bts.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
			assert.Equal(t, 4, params.Limit)
			assert.Equal(t, []string{interfaces.BuildTaskStatusPending}, params.Statuses)
			assert.Equal(t, interfaces.BuildTaskModeStreaming, params.Mode)
			assert.Equal(t, interfaces.BuildTaskSortCreateTime, params.Sort)
			assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
			return []*interfaces.BuildTaskSummary{{ID: "task-1"}}, nil
		})

	worker.stopCh = make(chan struct{})
	worker.fillStreamingQueue(context.Background())

	assert.Len(t, worker.streamingQueue, 1)
	assert.False(t, worker.addInFlight("task-1"))
}

func TestBuildTaskWorkerFillBatchQueueSkipsDatabaseWhenQueueIsNotEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	bts := vmock.NewMockBuildTaskService(ctrl)
	worker := &BuildTaskWorker{
		bts:        bts,
		batchQueue: make(chan string, 4),
		inFlight:   make(map[string]struct{}),
	}
	worker.batchQueue <- "already-queued"

	worker.stopCh = make(chan struct{})
	worker.fillBatchQueue(context.Background())
}

func TestBuildTaskWorkerRecoversInterruptedTasks(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	bts := vmock.NewMockBuildTaskService(ctrl)
	worker := &BuildTaskWorker{
		bts:            bts,
		batchQueue:     make(chan string, 2),
		streamingQueue: make(chan string, 2),
	}
	firstList := bts.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
			assert.Equal(t, 4, params.Limit)
			assert.Equal(t, []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping}, params.Statuses)
			return []*interfaces.BuildTaskSummary{
				{ID: "running-task", Status: interfaces.BuildTaskStatusRunning},
				{ID: "stopping-task", Status: interfaces.BuildTaskStatusStopping},
			}, nil
		})
	runningUpdate := bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "running-task",
		"build task interrupted by service restart", nil).Return(true, nil).After(firstList)
	stoppingUpdate := bts.EXPECT().InternalMarkStopped(gomock.Any(), nil, "stopping-task", nil).Return(true, nil).After(runningUpdate)
	bts.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(
		[]*interfaces.BuildTaskSummary{}, nil).After(stoppingUpdate)

	require.NoError(t, worker.recoverInterruptedTasks(context.Background()))
}

func TestBuildTaskWorkerRecoveryReturnsUpdateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	bts := vmock.NewMockBuildTaskService(ctrl)
	worker := &BuildTaskWorker{
		bts:            bts,
		batchQueue:     make(chan string, 1),
		streamingQueue: make(chan string, 1),
	}
	bts.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTaskSummary{{
		ID: "task-1", Status: interfaces.BuildTaskStatusRunning,
	}}, nil)
	bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "task-1",
		"build task interrupted by service restart", nil).Return(false, errors.New("database unavailable"))

	err := worker.recoverInterruptedTasks(context.Background())

	require.ErrorContains(t, err, "database unavailable")
}

func TestNewBuildTaskWorkerUsesModeSpecificConcurrency(t *testing.T) {
	batchWorkerCount, streamingWorkerCount := calculateTaskWorkerCounts(
		&common.AppSetting{
			TaskWorker: common.TaskWorkerConfig{
				BatchWorkerCount:     2,
				StreamingWorkerCount: 3,
			},
		},
	)

	assert.Equal(t, 2, batchWorkerCount)
	assert.Equal(t, 3, streamingWorkerCount)
}

func TestBuildTaskWorkerRejectsModeMismatch(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		errorMsg string
		runTask  func(*BuildTaskWorker) error
	}{
		{
			name:     "batch queue",
			mode:     interfaces.BuildTaskModeStreaming,
			errorMsg: `batch build task has mode "streaming"`,
			runTask: func(worker *BuildTaskWorker) error {
				return worker.runBatchTask(context.Background(), "task-1")
			},
		},
		{
			name:     "streaming queue",
			mode:     interfaces.BuildTaskModeBatch,
			errorMsg: `streaming build task has mode "batch"`,
			runTask: func(worker *BuildTaskWorker) error {
				return worker.runStreamingTask(context.Background(), "task-1")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			bts := vmock.NewMockBuildTaskService(ctrl)
			worker := &BuildTaskWorker{bts: bts}
			task := &interfaces.BuildTask{
				ID:     "task-1",
				Status: interfaces.BuildTaskStatusPending,
				Mode:   tt.mode,
			}
			bts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(task, nil)
			bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "task-1", tt.errorMsg, nil).
				Return(true, nil)

			err := tt.runTask(worker)

			require.EqualError(t, err, tt.errorMsg)
		})
	}
}

func TestBuildTaskWorkerClaim(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		claimed   bool
		claimErr  error
		wantError string
		runTask   func(*BuildTaskWorker) error
	}{
		{
			name:      "batch keeps pending after claim error",
			mode:      interfaces.BuildTaskModeBatch,
			claimErr:  errors.New("temporary database error"),
			wantError: "temporary database error",
			runTask: func(worker *BuildTaskWorker) error {
				return worker.runBatchTask(context.Background(), "task-1")
			},
		},
		{
			name: "primary key only full batch stops after state change",
			mode: interfaces.BuildTaskModeBatch,
			runTask: func(worker *BuildTaskWorker) error {
				return worker.runBatchTask(context.Background(), "task-1")
			},
		},
		{
			name:      "streaming keeps pending after claim error",
			mode:      interfaces.BuildTaskModeStreaming,
			claimErr:  errors.New("temporary database error"),
			wantError: "temporary database error",
			runTask: func(worker *BuildTaskWorker) error {
				return worker.runStreamingTask(context.Background(), "task-1")
			},
		},
		{
			name: "streaming stops after state change",
			mode: interfaces.BuildTaskModeStreaming,
			runTask: func(worker *BuildTaskWorker) error {
				return worker.runStreamingTask(context.Background(), "task-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			bts := vmock.NewMockBuildTaskService(ctrl)
			worker := &BuildTaskWorker{bts: bts}
			task := &interfaces.BuildTask{
				ID:     "task-1",
				Status: interfaces.BuildTaskStatusPending,
				Mode:   tt.mode,
			}
			if tt.mode == interfaces.BuildTaskModeBatch {
				resource := workerTestResource()
				resource.CatalogID = "catalog-1"
				resource.IndexConfig.IncrementalFields = nil
				task = workerTestFullTask(t, resource)
				task.ID = "task-1"
				task.Status = interfaces.BuildTaskStatusPending
				task.IndexConfig.IncrementalFields = nil
				rs := vmock.NewMockResourceService(ctrl)
				cs := vmock.NewMockCatalogService(ctrl)
				worker.bbw = &batchBuildWorker{rs: rs, cs: cs}
				rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
				cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
					Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
			}
			bts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(task, nil)
			bts.EXPECT().InternalMarkRunning(gomock.Any(), nil, "task-1", nil).
				Return(tt.claimed, tt.claimErr)

			err := tt.runTask(worker)

			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
		})
	}
}

func TestBuildTaskWorkerDoesNotReplaceKeysTargetWhenClaimLoses(t *testing.T) {
	ctrl := gomock.NewController(t)
	bts := vmock.NewMockBuildTaskService(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	worker := &BuildTaskWorker{bts: bts, bbw: &batchBuildWorker{rs: rs, cs: cs}}
	resource := workerTestResource()
	resource.Enabled = true
	resource.CatalogID = "catalog-1"
	resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
	resource.LocalIndexName = "latest-published"
	task := workerTestFullTask(t, resource)
	task.Status = interfaces.BuildTaskStatusPending
	task.ExecuteType = interfaces.BuildTaskExecuteTypeKeys
	task.IndexName = "previous-attempt"

	bts.EXPECT().InternalGetByID(gomock.Any(), task.ID).Return(task, nil)
	rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
	cs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(&interfaces.Catalog{Enabled: true}, nil)
	bts.EXPECT().InternalMarkRunning(gomock.Any(), nil, task.ID, &resource.LocalIndexName).Return(false, nil)

	require.NoError(t, worker.runBatchTask(t.Context(), task.ID))
	assert.Equal(t, "previous-attempt", task.IndexName)
}

func TestBuildTaskWorkerRejectsChangedBatchTaskBeforeClaim(t *testing.T) {
	for _, executeType := range []string{
		interfaces.BuildTaskExecuteTypeFull,
		interfaces.BuildTaskExecuteTypeIncremental,
	} {
		t.Run(executeType, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bts := vmock.NewMockBuildTaskService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			resource := workerTestResource()
			task := workerTestFullTask(t, resource)
			task.ID = "task-1"
			task.Status = interfaces.BuildTaskStatusPending
			task.ExecuteType = executeType
			resource.IndexConfig.IncrementalFields = []string{"updated_at"}
			resource.SchemaDefinition = []*interfaces.Property{{Name: "updated_at", Type: interfaces.DataType_Timestamp}}
			worker := &BuildTaskWorker{
				bts: bts,
				bbw: &batchBuildWorker{rs: rs},
			}

			bts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(task, nil)
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, "r1").Return(resource, nil)
			bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "task-1", "resource index config has changed", nil).Return(true, nil)

			err := worker.runBatchTask(context.Background(), "task-1")

			require.EqualError(t, err, "resource index config has changed")
		})
	}
}

func TestBuildTaskWorkerRejectsBatchTaskWithoutNewKeyFields(t *testing.T) {
	ctrl := gomock.NewController(t)
	bts := vmock.NewMockBuildTaskService(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	resource := workerTestResource()
	task := workerTestFullTask(t, resource)
	task.ID = "task-1"
	task.Status = interfaces.BuildTaskStatusPending
	task.IndexConfig.PrimaryKeyFields = nil
	worker := &BuildTaskWorker{bts: bts, bbw: &batchBuildWorker{rs: rs}}

	bts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(task, nil)
	rs.EXPECT().InternalGetByID(gomock.Any(), nil, "r1").Return(resource, nil)
	bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "task-1",
		"batch build task snapshot requires primary_key_fields", nil).Return(true, nil)

	err := worker.runBatchTask(context.Background(), "task-1")
	require.ErrorContains(t, err, "requires primary_key_fields")
}

func TestBuildTaskWorkerCancelsBatchTaskWhenResourceWasDeleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	bts := vmock.NewMockBuildTaskService(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	task := &interfaces.BuildTask{
		ID:         "task-1",
		ResourceID: "r1",
		Status:     interfaces.BuildTaskStatusPending,
		Mode:       interfaces.BuildTaskModeBatch,
	}
	worker := &BuildTaskWorker{
		bts: bts,
		bbw: &batchBuildWorker{rs: rs},
	}

	bts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(task, nil)
	rs.EXPECT().InternalGetByID(gomock.Any(), nil, "r1").Return(nil, nil)
	bts.EXPECT().InternalMarkCancelled(gomock.Any(), nil, "task-1", "resource deleted").Return(true, nil)

	require.NoError(t, worker.runBatchTask(context.Background(), "task-1"))
}

func TestBuildTaskWorkerClaimsIncrementalWithResourceCheckpoint(t *testing.T) {
	ctrl := gomock.NewController(t)
	bts := vmock.NewMockBuildTaskService(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	resource := workerTestResource()
	resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
	resource.LocalIndexName = "current-index"
	resource.SyncMark = `{"mode":"batch","cursor":[]}`
	task := workerTestFullTask(t, resource)
	task.ID = "task-1"
	task.IndexName = resource.LocalIndexName
	task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
	task.Status = interfaces.BuildTaskStatusPending
	worker := &BuildTaskWorker{bts: bts, bbw: &batchBuildWorker{rs: rs}}

	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDB := logics.DB
	logics.DB = db
	defer func() { logics.DB = oldDB }()

	mockDB.ExpectBegin()
	txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
	rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(resource, nil)
	bts.EXPECT().InternalMarkRunning(gomock.Any(), txMatcher, task.ID, nil).Return(true, nil)
	bts.EXPECT().InternalSetProgress(gomock.Any(), txMatcher, task.ID, gomock.Any(), runningOrStoppingStatuses).DoAndReturn(
		func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress, _ []string) (bool, error) {
			require.NotNil(t, progress.SyncedMark)
			assert.Equal(t, resource.SyncMark, *progress.SyncedMark)
			return true, nil
		})
	mockDB.ExpectCommit()

	claimed, current, err := worker.claimIncrementalBatchTask(context.Background(), task, resource)

	require.NoError(t, err)
	require.True(t, claimed)
	assert.Same(t, resource, current)
	assert.Equal(t, interfaces.BuildTaskStatusRunning, task.Status)
	assert.Equal(t, resource.SyncMark, task.SyncedMark)
	require.NoError(t, mockDB.ExpectationsWereMet())
}

func TestBuildTaskWorkerRunBatchTaskRecoversPanic(t *testing.T) {
	const detail = "batch build task panicked: source read panicked"
	for _, status := range []string{
		interfaces.BuildTaskStatusPending,
		interfaces.BuildTaskStatusRunning,
		interfaces.BuildTaskStatusStopping,
	} {
		t.Run(status, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bts := vmock.NewMockBuildTaskService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			cs := vmock.NewMockCatalogService(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			lim := vmock.NewMockLocalIndexManager(ctrl)
			worker := &BuildTaskWorker{
				bts: bts, bbw: &batchBuildWorker{bts: bts, rs: rs, cs: cs, cf: cf, lim: lim},
				stopCh: make(chan struct{}), batchQueue: make(chan string, 1),
			}
			resource := workerTestResource()
			resource.CatalogID = "catalog"
			task := workerTestFullTask(t, resource)
			task.Status = interfaces.BuildTaskStatusPending
			task.SyncedMark = `{"mode":"batch","cursor":[{"key":"id","value":20}]}`
			catalog := &interfaces.Catalog{ConnectorType: "mysql", Enabled: true}
			loadResource := rs.EXPECT().InternalGetByID(gomock.Any(), nil, task.ResourceID)
			calls := []any{bts.EXPECT().InternalGetByID(gomock.Any(), task.ID).Return(task, nil), loadResource}

			if status == interfaces.BuildTaskStatusPending {
				loadResource.Do(func(context.Context, *sql.Tx, string) { panic("source read panicked") })
				calls = append(calls, bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, task.ID, detail,
					[]string{interfaces.BuildTaskStatusPending}).Return(true, nil))
			} else {
				connector := &streamTableConnector{vmock.NewMockTableConnector(ctrl), vmock.NewMockTableReadSessionConnector(ctrl)}
				stream := vmock.NewMockQueryPageSession(ctrl)
				loadResource.Return(resource, nil)
				calls = append(calls,
					cs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).Return(catalog, nil),
					bts.EXPECT().InternalMarkRunning(gomock.Any(), nil, task.ID, nil).Return(true, nil),
					lim.EXPECT().CheckIndexExist(gomock.Any(), task.IndexName).Return(true, nil),
					cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil),
					connector.MockTableConnector.EXPECT().Connect(gomock.Any()).Return(nil),
					bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil),
					connector.MockTableReadSessionConnector.EXPECT().OpenTableRead(gomock.Any(), resource, gomock.Any()).Return(stream, nil),
					stream.EXPECT().ReadTotal(gomock.Any()).Return(nil, nil),
					stream.EXPECT().ReadPage(gomock.Any(), 1000).Do(func(context.Context, int) { panic("source read panicked") }),
					stream.EXPECT().Close().Return(nil),
					connector.MockTableConnector.EXPECT().Close(gomock.Any()).Return(nil),
					bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, task.ID, detail,
						[]string{interfaces.BuildTaskStatusRunning}).Return(status == interfaces.BuildTaskStatusRunning, nil),
				)
				if status == interfaces.BuildTaskStatusStopping {
					// Stop committed during the read, so the attempt to fail the running task matched no rows.
					calls = append(calls, bts.EXPECT().InternalMarkStopped(gomock.Any(), nil, task.ID,
						[]string{interfaces.BuildTaskStatusStopping}).Return(true, nil))
				}
			}
			calls = append(calls, bts.EXPECT().RequestDispatch().Do(func() { close(worker.stopCh) }))
			gomock.InOrder(calls...)

			worker.batchQueue <- task.ID
			worker.runBatchTasks(t.Context())
		})
	}
}

func TestBuildTaskWorkerRejectsIncrementalWithoutCheckpoint(t *testing.T) {
	const oldMark = `{"mode":"batch","cursor":[{"key":"id","value":20}]}`
	for _, clearedDuringClaim := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleared during claim=%t", clearedDuringClaim), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bts := vmock.NewMockBuildTaskService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			catalogs := vmock.NewMockCatalogService(ctrl)
			factory := vmock.NewMockConnectorFactory(ctrl)
			indexes := vmock.NewMockLocalIndexManager(ctrl)
			resource := workerTestResource()
			resource.CatalogID = "catalog-1"
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
			resource.LocalIndexName = "current-index"
			task := workerTestFullTask(t, resource)
			task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
			task.IndexName = resource.LocalIndexName
			task.Status = interfaces.BuildTaskStatusPending
			task.SyncedMark = oldMark
			worker := &BuildTaskWorker{
				bts: bts, bbw: &batchBuildWorker{rs: rs, cs: catalogs, cf: factory, lim: indexes},
			}

			bts.EXPECT().InternalGetByID(gomock.Any(), task.ID).Return(task, nil)
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
			if clearedDuringClaim {
				resource.SyncMark = oldMark
				clearedResource := *resource
				clearedResource.SyncMark = ""
				db, mockDB, err := sqlmock.New()
				require.NoError(t, err)
				originalDB := logics.DB
				logics.DB = db
				t.Cleanup(func() {
					logics.DB = originalDB
					_ = db.Close()
				})
				mockDB.ExpectBegin()
				mockDB.ExpectRollback()
				t.Cleanup(func() { assert.NoError(t, mockDB.ExpectationsWereMet()) })
				catalogs.EXPECT().InternalGetByID(gomock.Any(), resource.CatalogID, true).
					Return(&interfaces.Catalog{ID: resource.CatalogID, Enabled: true}, nil)
				rs.EXPECT().InternalGetByID(gomock.Any(), gomock.AssignableToTypeOf(&sql.Tx{}), resource.ID).
					Return(&clearedResource, nil)
			}
			bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, task.ID, gomock.Regex("incremental baseline is missing; run a full build"), nil).
				Return(true, nil)

			err := worker.runBatchTask(t.Context(), task.ID)

			require.ErrorContains(t, err, "run a full build")
			assert.Equal(t, interfaces.BuildTaskStatusPending, task.Status)
			assert.Equal(t, oldMark, task.SyncedMark, "a task mark is not a substitute for a committed resource baseline")
		})
	}
}

func TestBuildTaskWorkerRejectsIncrementalWithoutPublishedIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	bts := vmock.NewMockBuildTaskService(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	resource := workerTestResource()
	task := workerTestFullTask(t, resource)
	task.ID = "task-1"
	task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
	task.Status = interfaces.BuildTaskStatusPending
	worker := &BuildTaskWorker{bts: bts, bbw: &batchBuildWorker{rs: rs}}

	bts.EXPECT().InternalGetByID(gomock.Any(), task.ID).Return(task, nil)
	rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
	bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, task.ID,
		"incremental build requires an available local index", nil).Return(true, nil)

	err := worker.runBatchTask(context.Background(), task.ID)

	require.EqualError(t, err, "incremental build requires an available local index")
}

func TestBuildTaskWorkerRejectsIncrementalWithoutConfiguredFields(t *testing.T) {
	for _, inTransaction := range []bool{false, true} {
		t.Run(fmt.Sprintf("in transaction=%t", inTransaction), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bts := vmock.NewMockBuildTaskService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			resource := workerTestResource()
			resource.IndexConfig.IncrementalFields = nil
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
			resource.LocalIndexName = "current-index"
			resource.SyncMark = `{"mode":"batch","cursor":[{"key":"id","value":20}]}`
			task := workerTestFullTask(t, resource)
			task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
			task.Status = interfaces.BuildTaskStatusPending
			task.IndexConfig.IncrementalFields = nil
			worker := &BuildTaskWorker{bts: bts, bbw: &batchBuildWorker{rs: rs}}

			if inTransaction {
				db, mockDB, err := sqlmock.New()
				require.NoError(t, err)
				oldDB := logics.DB
				logics.DB = db
				t.Cleanup(func() {
					logics.DB = oldDB
					_ = db.Close()
				})
				mockDB.ExpectBegin()
				mockDB.ExpectRollback()
				rs.EXPECT().InternalGetByID(gomock.Any(), gomock.AssignableToTypeOf(&sql.Tx{}), resource.ID).Return(resource, nil)

				claimed, current, err := worker.claimIncrementalBatchTask(t.Context(), task, resource)
				require.ErrorIs(t, err, errIncrementalBatchResourceInvalid)
				assert.ErrorContains(t, err, "requires incremental_fields")
				assert.False(t, claimed)
				assert.Same(t, resource, current)
				require.NoError(t, mockDB.ExpectationsWereMet())
			} else {
				bts.EXPECT().InternalGetByID(gomock.Any(), task.ID).Return(task, nil)
				rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
				bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, task.ID, gomock.Regex("requires incremental_fields"), nil).Return(true, nil)

				err := worker.runBatchTask(t.Context(), task.ID)
				require.ErrorContains(t, err, "requires incremental_fields")
			}
			assert.Equal(t, interfaces.BuildTaskStatusPending, task.Status)
			assert.Empty(t, task.SyncedMark)
		})
	}
}

func TestBuildTaskWorkerValidatesCatalogBeforeClaim(t *testing.T) {
	t.Run("uses task creator and fails lookup error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		resource := workerTestResource()
		resource.CatalogID = "catalog-1"
		task := workerTestFullTask(t, resource)
		task.ID = "task-1"
		task.Status = interfaces.BuildTaskStatusPending
		task.Creator = interfaces.AccountInfo{ID: "u1", Type: "user"}
		worker := &BuildTaskWorker{bts: bts, bbw: &batchBuildWorker{rs: rs, cs: cs}}

		bts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(task, nil)
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
		cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).DoAndReturn(
			func(ctx context.Context, _ string, _ bool) (*interfaces.Catalog, error) {
				account, ok := workerAccountFromCtx(ctx)
				require.True(t, ok)
				assert.Equal(t, task.Creator, account)
				return nil, errors.New("forbidden")
			})
		bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "task-1",
			"get catalog before claiming batch build task: forbidden", nil).Return(true, nil)

		err := worker.runBatchTask(context.Background(), "task-1")

		require.EqualError(t, err, "get catalog before claiming batch build task: forbidden")
	})

	t.Run("cancels task when catalog was deleted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		resource := workerTestResource()
		resource.CatalogID = "catalog-1"
		task := workerTestFullTask(t, resource)
		task.ID = "task-1"
		task.Status = interfaces.BuildTaskStatusPending
		worker := &BuildTaskWorker{bts: bts, bbw: &batchBuildWorker{rs: rs, cs: cs}}

		bts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(task, nil)
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
		cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(nil, &httperrors.HTTPError{HTTPCode: http.StatusNotFound})
		bts.EXPECT().InternalMarkCancelled(gomock.Any(), nil, "task-1", "catalog deleted").Return(true, nil)

		require.NoError(t, worker.runBatchTask(context.Background(), "task-1"))
	})

	t.Run("fails task when catalog is disabled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		resource := workerTestResource()
		resource.CatalogID = "catalog-1"
		task := workerTestFullTask(t, resource)
		task.ID = "task-1"
		task.Status = interfaces.BuildTaskStatusPending
		worker := &BuildTaskWorker{bts: bts, bbw: &batchBuildWorker{rs: rs, cs: cs}}

		bts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(task, nil)
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
		cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)
		bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "task-1", "catalog is disabled", nil).Return(true, nil)

		err := worker.runBatchTask(context.Background(), "task-1")

		require.EqualError(t, err, "catalog is disabled")
	})
}
