// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
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
	bts.EXPECT().InternalMarkRunning(gomock.Any(), txMatcher, task.ID).Return(true, nil)
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
					bts.EXPECT().InternalMarkRunning(gomock.Any(), nil, task.ID).Return(true, nil),
					lim.EXPECT().CheckIndexExist(gomock.Any(), task.IndexName).Return(true, nil),
					cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil),
					connector.MockTableConnector.EXPECT().Connect(gomock.Any()).Return(nil),
					bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil),
					connector.MockTableReadSessionConnector.EXPECT().OpenTableRead(gomock.Any(), resource, gomock.Any()).Return(stream, nil),
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

func TestBuildTaskWorkerRunBatchTaskRecoversIncrementalBaseline(t *testing.T) {
	const recovered = `{"mode":"batch","cursor":[{"key":"id","value":42}]}`
	const old = `{"mode":"batch","cursor":[{"key":"id","value":13}]}`
	const scanFailed = "cannot recover incremental checkpoint: published index query failed"
	const unsupportedField = "cannot recover incremental checkpoint: unsupported cursor field"
	const invalidCursor = "cannot recover incremental checkpoint: invalid indexed cursor"
	const saveFailed = "cannot save the recovered incremental checkpoint"
	const conflict = "resource, catalog, or task changed during incremental baseline recovery"
	runningOnly := []string{interfaces.BuildTaskStatusRunning}
	stoppingOnly := []string{interfaces.BuildTaskStatusStopping}

	tests := []struct {
		name        string
		wantFailure string
		wantError   string
	}{
		{name: "commits both checkpoints before reading the source"},
		{name: "restart with a committed checkpoint skips the index lookup"},
		{name: "index unavailable", wantFailure: scanFailed, wantError: "index search failed"},
		{name: "failure status write fails after index error", wantFailure: scanFailed, wantError: "failure status write failed"},
		{name: "stop wins before recovery failure write", wantFailure: scanFailed},
		{name: "unsupported cursor field", wantFailure: unsupportedField, wantError: "cannot be recovered losslessly"},
		{name: "invalid indexed cursor", wantFailure: invalidCursor, wantError: "cursor value must not be null"},
		{name: "catalog changed", wantFailure: conflict, wantError: conflict},
		{name: "resource index changed", wantFailure: conflict, wantError: conflict},
		{name: "resource checkpoint was already initialized", wantFailure: conflict, wantError: conflict},
		{name: "metadata write failed", wantFailure: saveFailed, wantError: "write failed"},
		{name: "metadata commit failed", wantFailure: saveFailed, wantError: "commit failed"},
		{name: "stop wins during checkpoint write"},
		{name: "requeued before checkpoint write"},
		{name: "stop before the index lookup"},
		{name: "shutdown lets the index lookup finish without saving a checkpoint"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bts := vmock.NewMockBuildTaskService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			cs := vmock.NewMockCatalogService(ctrl)
			lim := vmock.NewMockLocalIndexManager(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			ra := vmock.NewMockResourceAccess(ctrl)
			worker := &BuildTaskWorker{
				bts: bts,
				bbw: &batchBuildWorker{bts: bts, rs: rs, cs: cs, lim: lim, cf: cf, stopped: new(atomic.Bool)},
			}
			db, dbMock, err := sqlmock.New()
			require.NoError(t, err)
			oldDB, oldRA := logics.DB, logics.RA
			logics.DB, logics.RA = db, ra
			t.Cleanup(func() {
				logics.DB, logics.RA = oldDB, oldRA
				_ = db.Close()
			})
			resource := workerTestResource()
			resource.CatalogID = "catalog"
			resource.SchemaDefinition[0].OriginalName = "id"
			resource.SchemaDefinition[0].OriginalType = "bigint"
			if tc.name == "unsupported cursor field" {
				resource.SchemaDefinition[0].Type = interfaces.DataType_String
				resource.SchemaDefinition[0].OriginalType = "varchar"
			}
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
			resource.LocalIndexName = "published-index"
			catalog := &interfaces.Catalog{ID: resource.CatalogID, Enabled: true, UpdateTime: 100, ConnectorType: "mysql"}
			task := workerTestFullTask(t, resource)
			task.ID = "incremental"
			task.CatalogID = catalog.ID
			task.IndexName = resource.LocalIndexName
			task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
			task.Status = interfaces.BuildTaskStatusPending
			task.SyncedMark = old

			isRestart := tc.name == "restart with a committed checkpoint skips the index lookup"
			if isRestart {
				resource.SyncMark = recovered
			}
			txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
			claim := []any{
				bts.EXPECT().InternalGetByID(gomock.Any(), task.ID).Return(task, nil),
				rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil),
				cs.EXPECT().InternalGetByID(gomock.Any(), catalog.ID, true).Return(catalog, nil),
				rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(resource, nil),
				bts.EXPECT().InternalMarkRunning(gomock.Any(), txMatcher, task.ID).Return(true, nil),
			}
			if isRestart {
				claim = append(claim, bts.EXPECT().InternalSetProgress(gomock.Any(), txMatcher, task.ID,
					interfaces.BuildTaskProgress{SyncedMark: &resource.SyncMark}, runningOrStoppingStatuses).Return(true, nil))
			}
			gomock.InOrder(claim...)
			dbMock.ExpectBegin()
			dbMock.ExpectCommit()

			if !isRestart {
				var query *gomock.Call
				if tc.name == "stop before the index lookup" {
					bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusStopping, nil)
					bts.EXPECT().InternalMarkStopped(gomock.Any(), nil, task.ID, stoppingOnly).Return(true, nil)
				} else if tc.name != "unsupported cursor field" {
					status := bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
					query = lim.EXPECT().ListDocuments(gomock.Any(), resource.LocalIndexName, resource, gomock.Any()).
						After(status).DoAndReturn(func(ctx context.Context, _ string, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
						assert.Zero(t, db.Stats().InUse, "the claim transaction must finish before querying the index")
						assert.Equal(t, interfaces.BuildTaskStatusRunning, task.Status)
						assert.Equal(t, old, task.SyncedMark, "claim must preserve the previous task mark while recovery is pending")
						assert.Equal(t, 1, params.Paging.Limit)
						assert.True(t, params.NeedTotal)
						assert.Equal(t, []*interfaces.SortField{{Field: "id", Direction: "desc"}}, params.Sort)
						switch tc.name {
						case "index unavailable", "failure status write fails after index error", "stop wins before recovery failure write":
							return nil, errors.New("index search failed")
						case "invalid indexed cursor":
							return baselineResult(9, map[string]any{"id": nil}), nil
						case "shutdown lets the index lookup finish without saving a checkpoint":
							worker.bbw.stopped.Store(true)
							require.NoError(t, ctx.Err())
						}
						return baselineResult(9, map[string]any{"id": json.Number("42")}), nil
					})
				}
				switch tc.name {
				case "index unavailable", "failure status write fails after index error", "stop wins before recovery failure write", "stop before the index lookup",
					"shutdown lets the index lookup finish without saving a checkpoint", "unsupported cursor field", "invalid indexed cursor":
					// None of these paths may start a metadata transaction.
				case "catalog changed":
					changed := *catalog
					changed.UpdateTime++
					cs.EXPECT().InternalGetByID(gomock.Any(), catalog.ID, false).After(query).Return(&changed, nil)
				default:
					dbMock.ExpectBegin()
					switch tc.name {
					case "commits both checkpoints before reading the source":
						dbMock.ExpectCommit()
					case "metadata commit failed":
						dbMock.ExpectCommit().WillReturnError(errors.New("commit failed"))
					default:
						dbMock.ExpectRollback()
					}
					current := *resource
					current.SyncMark = recovered
					if tc.name == "resource index changed" {
						current.LocalIndexName = "replacement-index"
					}
					wonResourceCAS := tc.name != "resource checkpoint was already initialized"
					recoveredTx := []any{
						cs.EXPECT().InternalGetByID(gomock.Any(), catalog.ID, false).After(query).Return(catalog, nil),
						ra.EXPECT().CompareAndSetSyncMark(gomock.Any(), txMatcher, resource.ID, resource.LocalIndexName,
							interfaces.ResourceLocalIndexStatusAvailable, "", recovered).Return(wonResourceCAS, nil),
					}
					if wonResourceCAS {
						recoveredTx = append(recoveredTx, rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(&current, nil))
						if tc.name != "resource index changed" {
							var updateErr error
							if tc.name == "metadata write failed" {
								updateErr = errors.New("write failed")
							}
							wonTaskUpdate := tc.name == "commits both checkpoints before reading the source" || tc.name == "metadata commit failed"
							recoveredTx = append(recoveredTx, bts.EXPECT().InternalSetProgress(gomock.Any(), txMatcher, task.ID,
								interfaces.BuildTaskProgress{SyncedMark: &current.SyncMark}, []string{interfaces.BuildTaskStatusRunning}).
								Return(wonTaskUpdate, updateErr))
						}
					}
					gomock.InOrder(recoveredTx...)
				}
			}

			wantSource := isRestart || tc.name == "commits both checkpoints before reading the source"
			switch {
			case wantSource:
				connector := vmock.NewMockTableConnector(ctrl)
				gomock.InOrder(
					lim.EXPECT().CheckIndexExist(gomock.Any(), resource.LocalIndexName).DoAndReturn(func(_ context.Context, _ string) (bool, error) {
						assert.Zero(t, db.Stats().InUse)
						assert.Equal(t, recovered, task.SyncedMark)
						return true, nil
					}),
					cf.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, gomock.Any()).Return(connector, nil),
					connector.EXPECT().Connect(gomock.Any()).Return(nil),
					bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil),
					connector.EXPECT().ExecuteQuery(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
						func(_ context.Context, source *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
							assert.Equal(t, recovered, source.SyncMark)
							require.NotNil(t, params.FilterCondCfg)
							require.Len(t, params.FilterCondCfg.SubConds, 1)
							require.Len(t, params.FilterCondCfg.SubConds[0].SubConds, 1)
							assert.Equal(t, int64(42), params.FilterCondCfg.SubConds[0].SubConds[0].Value)
							return &interfaces.ResourceDataQueryResult{}, nil
						}),
					bts.EXPECT().InternalMarkCompleted(gomock.Any(), nil, task.ID).Return(true, nil),
					connector.EXPECT().Close(gomock.Any()).Return(nil),
				)
			case tc.name == "stop wins during checkpoint write", tc.name == "requeued before checkpoint write":
				gomock.InOrder(
					bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, task.ID, conflict, runningOnly).Return(false, nil),
					bts.EXPECT().InternalMarkStopped(gomock.Any(), nil, task.ID, stoppingOnly).
						Return(tc.name == "stop wins during checkpoint write", nil),
				)
			case tc.name == "stop wins before recovery failure write":
				gomock.InOrder(
					bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, task.ID, scanFailed, runningOnly).Return(false, nil),
					bts.EXPECT().InternalMarkStopped(gomock.Any(), nil, task.ID, stoppingOnly).Return(true, nil),
				)
			case tc.wantFailure != "":
				markFailed := bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, task.ID, tc.wantFailure, runningOnly)
				if tc.name == "failure status write fails after index error" {
					markFailed.Return(false, errors.New("failure status write failed"))
				} else {
					markFailed.Return(true, nil)
				}
			}

			err = worker.runBatchTask(t.Context(), task.ID)
			if tc.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantError)
			}
			if !wantSource {
				assert.Equal(t, old, task.SyncedMark)
				assert.Empty(t, resource.SyncMark)
			}
			require.NoError(t, dbMock.ExpectationsWereMet())
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
