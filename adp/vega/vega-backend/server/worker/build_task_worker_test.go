// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
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
	runningUpdate := bts.EXPECT().InternalMarkFailed(gomock.Any(), "running-task",
		"build task interrupted by service restart").Return(true, nil).After(firstList)
	stoppingUpdate := bts.EXPECT().InternalMarkStopped(gomock.Any(), "stopping-task").Return(true, nil).After(runningUpdate)
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
	bts.EXPECT().InternalMarkFailed(gomock.Any(), "task-1",
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
			bts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
				ID:     "task-1",
				Status: interfaces.BuildTaskStatusPending,
				Mode:   tt.mode,
			}, nil)
			bts.EXPECT().InternalMarkFailed(gomock.Any(), "task-1", tt.errorMsg).
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
			bts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
				ID:     "task-1",
				Status: interfaces.BuildTaskStatusPending,
				Mode:   tt.mode,
			}, nil)
			bts.EXPECT().InternalMarkRunning(gomock.Any(), "task-1").
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
