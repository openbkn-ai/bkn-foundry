// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestDiscoverTaskWorkerSkipsCancelledTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts}
	dts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(&interfaces.DiscoverTask{
		ID: "task-1", Status: interfaces.DiscoverTaskStatusCancelled,
	}, nil)
	require.NoError(t, worker.Run(context.Background(), "task-1"))
}

func TestDiscoverTaskWorkerUpdateProgress(t *testing.T) {
	t.Run("updates running task progress", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		worker := &DiscoverTaskWorker{dts: dts}
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", 70, "resources reconciled").Return(true, nil)

		require.NoError(t, worker.updateProgress(context.Background(), "task-1", 70, "resources reconciled"))
	})

	t.Run("rejects an unchanged task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		worker := &DiscoverTaskWorker{dts: dts}
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", 70, "resources reconciled").Return(false, nil)

		require.ErrorContains(t, worker.updateProgress(context.Background(), "task-1", 70, "resources reconciled"), "was not updated")
	})
}

func TestDiscoverTaskWorkerCancelsTaskWhenCatalogWasDeleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts, cs: cs}
	dts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(&interfaces.DiscoverTask{
		ID: "task-1", CatalogID: "catalog-1", Status: interfaces.DiscoverTaskStatusPending,
	}, nil)
	dts.EXPECT().InternalMarkRunning(gomock.Any(), "task-1").Return(true, nil)
	cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
		Return(nil, &rest.HTTPError{HTTPCode: http.StatusNotFound})
	dts.EXPECT().InternalMarkCancelled(gomock.Any(), "task-1", "catalog deleted").Return(true, nil)

	require.NoError(t, worker.Run(context.Background(), "task-1"))
}

func TestDiscoverTaskWorkerFailsTaskWhenCatalogIsDisabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts, cs: cs}
	dts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(&interfaces.DiscoverTask{
		ID: "task-1", CatalogID: "catalog-1", Status: interfaces.DiscoverTaskStatusPending,
	}, nil)
	dts.EXPECT().InternalMarkRunning(gomock.Any(), "task-1").Return(true, nil)
	cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)
	dts.EXPECT().InternalMarkFailed(gomock.Any(), "task-1", "catalog is disabled").Return(true, nil)

	require.NoError(t, worker.Run(context.Background(), "task-1"))
}

func TestDiscoverTaskWorkerMarksTaskFailedWhenCatalogLookupFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts, cs: cs}
	gomock.InOrder(
		dts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(&interfaces.DiscoverTask{
			ID: "task-1", CatalogID: "catalog-1", Status: interfaces.DiscoverTaskStatusPending,
		}, nil),
		dts.EXPECT().InternalMarkRunning(gomock.Any(), "task-1").Return(true, nil),
		cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(nil, errors.New("temporary database error")),
		dts.EXPECT().InternalMarkFailed(gomock.Any(), "task-1", "temporary database error").
			Return(true, nil),
	)

	err := worker.Run(context.Background(), "task-1")

	require.ErrorContains(t, err, "temporary database error")
}

func TestDiscoverTaskWorkerStopsWhenRunningTransitionMisses(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts}
	dts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(&interfaces.DiscoverTask{
		ID: "task-1", CatalogID: "catalog-1", Status: interfaces.DiscoverTaskStatusPending,
	}, nil)
	dts.EXPECT().InternalMarkRunning(gomock.Any(), "task-1").
		Return(false, nil)

	require.NoError(t, worker.Run(context.Background(), "task-1"))
}

func TestDiscoverTaskWorkerKeepsPendingTaskWhenClaimFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts}
	dts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(&interfaces.DiscoverTask{
		ID: "task-1", CatalogID: "catalog-1", Status: interfaces.DiscoverTaskStatusPending,
	}, nil)
	dts.EXPECT().InternalMarkRunning(gomock.Any(), "task-1").
		Return(false, errors.New("temporary database error"))

	err := worker.Run(context.Background(), "task-1")

	require.ErrorContains(t, err, "temporary database error")
}

func TestDiscoverTaskWorkerRecoversInterruptedTasks(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts, queueSize: 2}

	firstList := dts.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, error) {
			assert.Equal(t, []string{interfaces.DiscoverTaskStatusRunning}, params.Statuses)
			assert.Equal(t, 2, params.Limit)
			assert.Equal(t, interfaces.DiscoverTaskSortCreateTime, params.Sort)
			assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
			return []*interfaces.DiscoverTaskSummary{{ID: "task-1"}}, nil
		})
	markFailed := dts.EXPECT().InternalMarkFailed(gomock.Any(), "task-1",
		"discover task interrupted by service restart").Return(true, nil).After(firstList)
	dts.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(
		[]*interfaces.DiscoverTaskSummary{}, nil).After(markFailed)

	require.NoError(t, worker.recoverInterruptedTasks(context.Background()))
}

func TestDiscoverTaskWorkerRecoveryReturnsUpdateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts, queueSize: 1}
	dts.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(
		[]*interfaces.DiscoverTaskSummary{{ID: "task-1"}}, nil)
	dts.EXPECT().InternalMarkFailed(gomock.Any(), "task-1",
		"discover task interrupted by service restart").Return(false, errors.New("database unavailable"))

	err := worker.recoverInterruptedTasks(context.Background())

	require.ErrorContains(t, err, "database unavailable")
}

func TestDiscoverTaskWorkerFillQueueRefillsEmptyQueue(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{
		dts:              dts,
		queue:            make(chan discoverTaskQueueItem, 2),
		activeTaskIDs:    make(map[string]struct{}),
		activeCatalogIDs: make(map[string]struct{}),
	}
	dts.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, error) {
			assert.Equal(t, 2, params.Limit)
			assert.Equal(t, []string{interfaces.DiscoverTaskStatusPending}, params.Statuses)
			assert.Equal(t, interfaces.DiscoverTaskSortQueuePriority, params.Sort)
			assert.Equal(t, interfaces.DESC_DIRECTION, params.Direction)
			return []*interfaces.DiscoverTaskSummary{{ID: "task-1"}}, nil
		})

	worker.stopCh = make(chan struct{})
	worker.fillQueue(context.Background())

	assert.Len(t, worker.queue, 1)
	assert.Contains(t, worker.activeTaskIDs, "task-1")
}

func TestDiscoverTaskWorkerFillQueueSkipsDatabaseWhenQueueIsNotEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{
		dts:              dts,
		queue:            make(chan discoverTaskQueueItem, 2),
		activeTaskIDs:    make(map[string]struct{}),
		activeCatalogIDs: make(map[string]struct{}),
	}
	worker.queue <- discoverTaskQueueItem{taskID: "already-queued"}

	worker.stopCh = make(chan struct{})
	worker.fillQueue(context.Background())
}

func TestDiscoverTaskWorkerReservesOneTaskPerCatalog(t *testing.T) {
	worker := &DiscoverTaskWorker{
		activeTaskIDs:    make(map[string]struct{}),
		activeCatalogIDs: make(map[string]struct{}),
	}

	assert.True(t, worker.reserveTask(discoverTaskQueueItem{taskID: "task-1", catalogID: "catalog-1"}))
	assert.False(t, worker.reserveTask(discoverTaskQueueItem{taskID: "task-2", catalogID: "catalog-1"}))
	assert.True(t, worker.reserveTask(discoverTaskQueueItem{taskID: "task-3", catalogID: "catalog-2"}))

	worker.releaseTask(discoverTaskQueueItem{taskID: "task-1", catalogID: "catalog-1"})
	assert.True(t, worker.reserveTask(discoverTaskQueueItem{taskID: "task-2", catalogID: "catalog-1"}))
}

func TestDiscoverTaskWorkerFillQueueSkipsActiveCatalogAndContinuesPaging(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{
		dts:              dts,
		queue:            make(chan discoverTaskQueueItem, 2),
		activeTaskIDs:    make(map[string]struct{}),
		activeCatalogIDs: make(map[string]struct{}),
		stopCh:           make(chan struct{}),
	}

	firstPage := dts.EXPECT().InternalList(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, error) {
			assert.Equal(t, 0, params.Offset)
			return []*interfaces.DiscoverTaskSummary{
				{ID: "task-1", CatalogID: "catalog-1"},
				{ID: "task-2", CatalogID: "catalog-1"},
			}, nil
		})
	dts.EXPECT().InternalList(gomock.Any(), gomock.Any()).After(firstPage).DoAndReturn(
		func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, error) {
			assert.Equal(t, 2, params.Offset)
			return []*interfaces.DiscoverTaskSummary{{ID: "task-3", CatalogID: "catalog-2"}}, nil
		})

	worker.fillQueue(context.Background())
	assert.Equal(t, "task-1", (<-worker.queue).taskID)
	assert.Equal(t, "task-3", (<-worker.queue).taskID)
}

func TestDiscoverTaskWorkerRecoversTaskPanic(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	taskService := vmock.NewMockDiscoverTaskService(ctrl)
	stopCh := make(chan struct{})
	worker := &DiscoverTaskWorker{
		dts: taskService,
		queue: func() chan discoverTaskQueueItem {
			queue := make(chan discoverTaskQueueItem, 2)
			queue <- discoverTaskQueueItem{taskID: "task-1", catalogID: "catalog-1"}
			queue <- discoverTaskQueueItem{taskID: "task-2", catalogID: "catalog-2"}
			return queue
		}(),
		activeTaskIDs:    map[string]struct{}{"task-1": {}, "task-2": {}},
		activeCatalogIDs: map[string]struct{}{"catalog-1": {}, "catalog-2": {}},
		stopCh:           stopCh,
	}
	taskService.EXPECT().InternalGetByID(gomock.Any(), "task-1").DoAndReturn(
		func(context.Context, string) (*interfaces.DiscoverTask, error) {
			panic("unexpected connector panic")
		},
	)
	taskService.EXPECT().
		InternalMarkFailed(gomock.Any(), "task-1", "discover task panicked: unexpected connector panic").
		Return(true, nil)
	taskService.EXPECT().InternalGetByID(gomock.Any(), "task-2").Return(&interfaces.DiscoverTask{
		ID: "task-2", Status: interfaces.DiscoverTaskStatusFailed,
	}, nil)
	dispatchCount := 0
	taskService.EXPECT().RequestDispatch().Times(2).Do(func() {
		dispatchCount++
		if dispatchCount == 2 {
			close(stopCh)
		}
	})
	done := make(chan struct{})

	go func() {
		defer close(done)
		worker.runQueuedTasks(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("discover task worker did not continue after panic")
	}
	assert.NotContains(t, worker.activeTaskIDs, "task-1", "panic must not leak active task state")
	assert.NotContains(t, worker.activeTaskIDs, "task-2", "worker must continue and release active task state")
}

func TestDiscoverTaskWorkerDoesNotStartQueuedTaskAfterCancellation(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	worker := &DiscoverTaskWorker{
		dts:   vmock.NewMockDiscoverTaskService(ctrl),
		queue: make(chan discoverTaskQueueItem, 1),
	}
	worker.queue <- discoverTaskQueueItem{taskID: "pending-task"}
	stopCh := make(chan struct{})
	worker.stopped.Store(true)
	close(stopCh)
	worker.stopCh = stopCh

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.runQueuedTasks(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled worker did not exit")
	}
}

func TestUpdateDiscoverResultForEnrichStatus(t *testing.T) {
	t.Run("increments status counters", func(t *testing.T) {
		result := &interfaces.DiscoverResult{}

		updateDiscoverResultForEnrichStatus(result, interfaces.DiscoverStatusUnchanged)
		updateDiscoverResultForEnrichStatus(result, interfaces.DiscoverStatusUpdated)
		updateDiscoverResultForEnrichStatus(result, interfaces.DiscoverStatusError)

		assert.Equal(t, 1, result.UnchangedCount)
		assert.Equal(t, 1, result.UpdatedCount)
		assert.Equal(t, 1, result.FailedCount)
	})
}

func TestSourceSnapshotHashIgnoresDerivedAndUserEditableFields(t *testing.T) {
	t.Run("ignores derived and user editable fields", func(t *testing.T) {
		resource := &interfaces.Resource{
			Description:      "user text",
			Tags:             []string{"a"},
			Name:             "users",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: "int", Description: "derived"}},
			SourceMetadata:   map[string]any{"original_name": "users"},
		}
		before := sourceSnapshotHash(resource)

		resource.Description = "edited by user"
		resource.Tags = []string{"b"}
		resource.Name = "display name"
		resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{Name: "name", Type: "string"})

		assert.Equal(t, before, sourceSnapshotHash(resource))
	})
}

func TestSourceSnapshotHashChangesForSourceMetadata(t *testing.T) {
	t.Run("changes for source metadata", func(t *testing.T) {
		resource := &interfaces.Resource{
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: "int"}},
			SourceMetadata:   map[string]any{"original_name": "users", "columns": []interfaces.TableColumnMeta{{Name: "id", Type: "int"}}},
		}
		before := sourceSnapshotHash(resource)

		resource.SourceMetadata["columns"] = []interfaces.TableColumnMeta{{Name: "id", Type: "int"}, {Name: "name", Type: "varchar"}}

		assert.NotEqual(t, before, sourceSnapshotHash(resource))
	})
}
