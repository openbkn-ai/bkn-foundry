// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
	"vega-backend/logics"
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
	bts.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
			assert.Equal(t, 4, params.Limit)
			assert.Equal(t, []string{interfaces.BuildTaskStatusPending}, params.Statuses)
			assert.Equal(t, interfaces.BuildTaskModeBatch, params.Mode)
			assert.Equal(t, interfaces.BuildTaskSortCreateTime, params.Sort)
			assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
			return []*interfaces.BuildTaskSummary{{ID: "task-1"}}, nil
		})

	worker.stopCh = make(chan struct{})
	worker.fillBatchQueue(context.Background())

	assert.Len(t, worker.batchQueue, 1)
	assert.False(t, worker.addInFlight("task-1"))
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
		"build task interrupted by service restart").Return(true, nil).After(firstList)
	stoppingUpdate := bts.EXPECT().InternalMarkStopped(gomock.Any(), nil, "stopping-task").Return(true, nil).After(runningUpdate)
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
		"build task interrupted by service restart").Return(false, errors.New("database unavailable"))

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
			bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "task-1", tt.errorMsg).
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
			name: "batch stops after state change",
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
				task = workerTestFullTask(t, resource)
				task.ID = "task-1"
				task.Status = interfaces.BuildTaskStatusPending
				rs := vmock.NewMockResourceService(ctrl)
				cs := vmock.NewMockCatalogService(ctrl)
				worker.bbw = &batchBuildWorker{rs: rs, cs: cs}
				rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
				cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
					Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
			}
			bts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(task, nil)
			bts.EXPECT().InternalMarkRunning(gomock.Any(), nil, "task-1").
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
			bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "task-1", "resource index config has changed").Return(true, nil)

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
		"batch build task snapshot requires primary_key_fields and incremental_fields").Return(true, nil)

	err := worker.runBatchTask(context.Background(), "task-1")
	require.EqualError(t, err, "batch build task snapshot requires primary_key_fields and incremental_fields")
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
	bts.EXPECT().InternalMarkRunning(gomock.Any(), txMatcher, task.ID).Return(true, nil)
	bts.EXPECT().InternalSetProgress(gomock.Any(), txMatcher, task.ID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress) (bool, error) {
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

func TestBuildTaskWorkerRejectsIncrementalWithoutCommittedCheckpoint(t *testing.T) {
	ctrl := gomock.NewController(t)
	bts := vmock.NewMockBuildTaskService(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	resource := workerTestResource()
	resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
	resource.LocalIndexName = "current-index"
	task := workerTestFullTask(t, resource)
	task.ID = "task-1"
	task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
	task.Status = interfaces.BuildTaskStatusPending
	worker := &BuildTaskWorker{bts: bts, bbw: &batchBuildWorker{rs: rs}}

	bts.EXPECT().InternalGetByID(gomock.Any(), task.ID).Return(task, nil)
	rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
	bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, task.ID,
		"incremental build requires an available local index and committed checkpoint").Return(true, nil)

	err := worker.runBatchTask(context.Background(), task.ID)

	require.EqualError(t, err, "incremental build requires an available local index and committed checkpoint")
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
			"get catalog before claiming batch build task: forbidden").Return(true, nil)

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
			Return(nil, &rest.HTTPError{HTTPCode: http.StatusNotFound})
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
		bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "task-1", "catalog is disabled").Return(true, nil)

		err := worker.runBatchTask(context.Background(), "task-1")

		require.EqualError(t, err, "catalog is disabled")
	})
}
