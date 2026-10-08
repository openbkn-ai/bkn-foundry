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
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
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

func TestDiscoverTaskWorkerCreateAndConnectConnector(t *testing.T) {
	t.Run("closes the connector when connecting fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockConnector(ctrl)
		factory := vmock.NewMockConnectorFactory(ctrl)
		worker := &DiscoverTaskWorker{cf: factory}
		catalog := &interfaces.Catalog{ConnectorType: "mariadb"}

		factory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).
			Return(connector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(errors.New("connection refused"))
		connector.EXPECT().Close(gomock.Any()).Return(nil)

		result, err := worker.createAndConnectConnector(context.Background(), catalog)

		assert.Nil(t, result)
		assert.ErrorContains(t, err, "failed to connect: connection refused")
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

type testTableCounter struct {
	interfaces.TableConnector
	count func(context.Context, *interfaces.TableMeta) (int64, error)
}

func (c *testTableCounter) CountRows(ctx context.Context, table *interfaces.TableMeta) (int64, error) {
	return c.count(ctx, table)
}
func TestDiscoverTaskWorkerCountResources(t *testing.T) {
	for _, scenario := range []string{"mixed", "failed", "empty", "single unsupported", "all skipped", "earlier deadline", "single view", "cancelled", "connector unsupported", "single connector unsupported"} {
		t.Run(scenario, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rs := vmock.NewMockResourceService(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			base := vmock.NewMockTableConnector(ctrl)
			dts := vmock.NewMockDiscoverTaskService(ctrl)
			dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task", gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
			worker := &DiscoverTaskWorker{rs: rs, cf: cf, dts: dts}
			task := &interfaces.DiscoverTask{ID: "task", CatalogID: "cat", Creator: interfaces.AccountInfo{ID: "user"}, Strategy: interfaces.DiscoverStrategyCountOnly, DiscoverActions: &interfaces.DiscoverActions{Count: true}}
			catalog := &interfaces.Catalog{ID: "cat", ConnectorType: "test", Type: interfaces.CatalogTypePhysical}
			resources := []*interfaces.Resource{{ID: "ok", CatalogID: "cat", Category: interfaces.ResourceCategoryTable, SourceIdentifier: "app.orders"}, {ID: "bad", CatalogID: "cat", Category: interfaces.ResourceCategoryTable}, {ID: "skip", Category: interfaces.ResourceCategoryLogicView}}
			ctx := context.Background()
			if scenario == "earlier deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				resources = resources[:1]
			}
			if scenario == "connector unsupported" || scenario == "single connector unsupported" {
				resources = resources[:1]
			}
			if scenario == "empty" {
				resources = nil
			}
			if scenario == "all skipped" {
				resources = resources[2:]
			}
			if scenario == "failed" {
				resources = resources[1:]
			}
			switch scenario {
			case "single unsupported":
				task.ResourceID = "skip"
				resources[2].CatalogID = "cat"
				rs.EXPECT().InternalGetByID(gomock.Any(), nil, "skip").Return(resources[2], nil)
			case "single connector unsupported":
				task.ResourceID = "ok"
				rs.EXPECT().InternalGetByID(gomock.Any(), nil, "ok").Return(resources[0], nil)
			case "single view":
				task.ResourceID = "ok"
				resources = resources[:1]
				resources[0].SourceMetadata = map[string]any{"table_type": interfaces.TableTypeView}
				rs.EXPECT().InternalGetByID(gomock.Any(), nil, "ok").Return(resources[0], nil)
			default:
				rs.EXPECT().InternalGetByCatalogID(gomock.Any(), "cat").Return(resources, nil)
			}
			{
				counter := &testTableCounter{TableConnector: base, count: func(queryCtx context.Context, table *interfaces.TableMeta) (int64, error) {
					id := (&DiscoverTaskWorker{}).buildSourceIdentifier(table)
					if id == "app.orders" {
						require.Equal(t, "app", table.Schema)
						require.Equal(t, "app", table.Database)
						require.Equal(t, "orders", table.Name)
						if scenario == "single view" {
							require.Equal(t, interfaces.TableTypeView, table.TableType)
						}
					}
					deadline, ok := queryCtx.Deadline()
					require.True(t, ok)
					if scenario == "earlier deadline" {
						parentDeadline, _ := ctx.Deadline()
						require.Equal(t, parentDeadline, deadline)
					} else {
						require.InDelta(t, resourceCountTimeout.Seconds(), time.Until(deadline).Seconds(), 1)
					}
					if scenario == "connector unsupported" || scenario == "single connector unsupported" {
						return 0, interfaces.ErrRowCountUnavailable
					}
					if id == "app.orders" {
						return 0, nil
					}
					return 0, context.DeadlineExceeded
				}}
				cf.EXPECT().CreateConnectorInstance(gomock.Any(), "test", gomock.Any()).Return(counter, nil)
				base.EXPECT().Connect(gomock.Any()).Return(nil)
				base.EXPECT().Close(gomock.Any()).Return(nil)
				if task.ResourceID == "" {
					base.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)
				}
				if scenario == "mixed" || scenario == "earlier deadline" || scenario == "single view" {
					rs.EXPECT().InternalUpdateRowCount(gomock.Any(), resources[0], int64(0), gomock.Any()).DoAndReturn(func(saveCtx context.Context, _ *interfaces.Resource, _ int64, _ int64) error {
						require.NoError(t, saveCtx.Err())
						return nil
					})
				}
				if scenario == "cancelled" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
			}
			var result *interfaces.DiscoverResult
			var err error
			result, err = worker.countResources(ctx, catalog, task)
			if scenario == "cancelled" {
				require.ErrorIs(t, err, context.Canceled)
				return
			}
			require.NoError(t, err)
			switch scenario {
			case "mixed":
				require.Equal(t, 1, result.UpdatedCount)
				require.Equal(t, 1, result.FailedCount)
				require.Equal(t, 1, result.SkippedCount)
				require.False(t, result.Failed)
			case "failed":
				require.True(t, result.Failed)
				require.Equal(t, 1, result.FailedCount)
				require.Equal(t, 1, result.SkippedCount)
			case "earlier deadline", "single view":
				require.Equal(t, 1, result.UpdatedCount)
				require.False(t, result.Failed)
			case "all skipped", "connector unsupported":
				require.True(t, result.Failed)
				require.Zero(t, result.FailedCount)
				require.Equal(t, 1, result.SkippedCount)
			case "single unsupported", "single connector unsupported":
				require.True(t, result.Failed)
				require.Equal(t, 1, result.FailedCount)
			case "empty":
				require.Zero(t, result.UpdatedCount)
				require.False(t, result.Failed)
			}
		})
	}
}

func TestDiscoverTaskWorkerCountResourcesIndexes(t *testing.T) {
	for _, scenario := range []string{"resource", "catalog", "query failure", "save failure", "cancelled during query"} {
		t.Run(scenario, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rs := vmock.NewMockResourceService(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			connector := vmock.NewMockIndexConnector(ctrl)
			dts := vmock.NewMockDiscoverTaskService(ctrl)
			dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task", gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
			worker := &DiscoverTaskWorker{rs: rs, cf: cf, dts: dts}
			task := &interfaces.DiscoverTask{ID: "task", CatalogID: "cat", Creator: interfaces.AccountInfo{ID: "user"}, DiscoverActions: &interfaces.DiscoverActions{Count: true}}
			catalog := &interfaces.Catalog{ID: "cat", Type: interfaces.CatalogTypePhysical, ConnectorType: "opensearch"}
			resource := &interfaces.Resource{ID: "index", CatalogID: "cat", Category: interfaces.ResourceCategoryIndex, SourceIdentifier: "products"}
			if scenario == "catalog" {
				rs.EXPECT().InternalGetByCatalogID(gomock.Any(), "cat").Return([]*interfaces.Resource{resource, {Category: interfaces.ResourceCategoryLogicView}}, nil)
				connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryIndex)
			} else {
				task.ResourceID = resource.ID
				rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
			}
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "opensearch", gomock.Any()).Return(connector, nil)
			connector.EXPECT().Connect(gomock.Any()).Return(nil)
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			connector.EXPECT().CountRows(gomock.Any(), gomock.Any()).DoAndReturn(func(queryCtx context.Context, index *interfaces.IndexMeta) (int64, error) {
				require.Equal(t, "products", index.Name)
				deadline, ok := queryCtx.Deadline()
				require.True(t, ok)
				require.InDelta(t, resourceCountTimeout.Seconds(), time.Until(deadline).Seconds(), 1)
				if scenario == "cancelled during query" {
					cancel()
					return 0, context.Canceled
				}
				if scenario == "query failure" {
					return 0, context.DeadlineExceeded
				}
				return 0, nil
			})
			if scenario != "query failure" && scenario != "cancelled during query" {
				rs.EXPECT().InternalUpdateRowCount(gomock.Any(), resource, int64(0), gomock.Any()).DoAndReturn(func(saveCtx context.Context, saved *interfaces.Resource, _ int64, timestamp int64) error {
					require.NoError(t, saveCtx.Err())
					require.Equal(t, task.Creator, saved.Updater)
					require.Positive(t, timestamp)
					if scenario == "save failure" {
						return errors.New("write failed")
					}
					return nil
				})
			}
			var result *interfaces.DiscoverResult
			var err error
			result, err = worker.countResources(ctx, catalog, task)
			require.NoError(t, err)
			if scenario == "query failure" || scenario == "save failure" || scenario == "cancelled during query" {
				require.True(t, result.Failed)
				require.Equal(t, 1, result.FailedCount)
				require.Zero(t, result.UpdatedCount)
				require.Zero(t, result.SkippedCount)
			} else {
				require.False(t, result.Failed)
				require.Equal(t, 1, result.UpdatedCount)
				if scenario == "catalog" {
					require.Equal(t, 1, result.SkippedCount)
				}
			}
		})
	}
}

func TestDiscoverTaskWorkerCombinedDiscoveryAndCount(t *testing.T) {
	for _, category := range []string{interfaces.ResourceCategoryTable, interfaces.ResourceCategoryIndex} {
		for _, entry := range []string{"resource", "catalog"} {
			for _, scenario := range []string{"success", "count failure", "metadata failure", "cancelled", "create and count", "cleanup and count"} {
				if entry == "resource" && (scenario == "create and count" || scenario == "cleanup and count") {
					continue
				}
				t.Run(category+"/"+entry+"/"+scenario, func(t *testing.T) {
					ctrl := gomock.NewController(t)
					rs := vmock.NewMockResourceService(ctrl)
					cf := vmock.NewMockConnectorFactory(ctrl)
					cs := vmock.NewMockCatalogService(ctrl)
					dts := vmock.NewMockDiscoverTaskService(ctrl)
					dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task", gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
					worker := &DiscoverTaskWorker{rs: rs, cf: cf, cs: cs, dts: dts}
					actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)
					actions.Count = true
					countWithoutRefresh := scenario == "create and count" || scenario == "cleanup and count"
					if countWithoutRefresh {
						actions.Refresh = false
						actions.Create = scenario == "create and count"
						actions.MarkStale = scenario == "cleanup and count"
					}
					task := &interfaces.DiscoverTask{ID: "task", CatalogID: "cat", DiscoverActions: &actions}
					catalog := &interfaces.Catalog{ID: "cat", Type: interfaces.CatalogTypePhysical, ConnectorType: "test"}
					oldProperties := map[string]any{"row_count": int64(7), "row_count_time": int64(10)}
					resource := &interfaces.Resource{ID: "res", CatalogID: "cat", Category: category, SourceIdentifier: "orders", Status: interfaces.ResourceStatusActive, UpdateTime: 20, LastDiscoverTime: 10, SourceMetadata: map[string]any{"properties": oldProperties}}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					if entry == "resource" {
						task.ResourceID = resource.ID
						rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
					} else {
						rs.EXPECT().InternalGetByCatalogID(gomock.Any(), catalog.ID).Return([]*interfaces.Resource{resource}, nil)
						cs.EXPECT().UpdateMetadata(gomock.Any(), catalog.ID, gomock.Any()).Return(nil)
					}
					var connector interfaces.Connector
					var metadataCall, countCall *gomock.Call
					metadataErr := error(nil)
					if scenario == "metadata failure" {
						metadataErr = errors.New("metadata failed")
					}
					count := func(queryCtx context.Context) (int64, error) {
						_, ok := queryCtx.Deadline()
						require.True(t, ok)
						if scenario == "cancelled" {
							cancel()
							return 0, context.Canceled
						}
						if scenario == "count failure" {
							return 0, context.DeadlineExceeded
						}
						return 0, nil
					}
					if category == interfaces.ResourceCategoryTable {
						c := vmock.NewMockTableConnector(ctrl)
						connector = c
						table := &interfaces.TableMeta{Name: "orders", Properties: map[string]any{"estimated_row_count": int64(99)}}
						if entry == "resource" {
							c.EXPECT().GetTableMetaByIdentifier(gomock.Any(), "orders").Return(table, nil)
						} else {
							c.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)
							c.EXPECT().ListTables(gomock.Any()).Return([]*interfaces.TableMeta{table}, nil)
						}
						if !countWithoutRefresh {
							metadataCall = c.EXPECT().GetTableMeta(gomock.Any(), table).Return(metadataErr)
						}
						if scenario != "metadata failure" {
							countCall = c.EXPECT().CountRows(gomock.Any(), table).DoAndReturn(func(ctx context.Context, _ *interfaces.TableMeta) (int64, error) { return count(ctx) })
						}
						c.EXPECT().Connect(ctx).Return(nil)
						c.EXPECT().Close(gomock.Any()).Return(nil)
						if entry == "catalog" {
							c.EXPECT().GetMetadata(gomock.Any()).Return(map[string]any{}, nil)
						}
					} else {
						c := vmock.NewMockIndexConnector(ctrl)
						connector = c
						index := &interfaces.IndexMeta{Name: "orders", Properties: map[string]any{"row_count": int64(99)}}
						if entry == "resource" {
							c.EXPECT().GetIndexMetaByIdentifier(gomock.Any(), "orders").Return(index, nil)
						} else {
							c.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryIndex)
							c.EXPECT().ListIndexes(gomock.Any()).Return([]*interfaces.IndexMeta{index}, nil)
						}
						if !countWithoutRefresh {
							metadataCall = c.EXPECT().GetIndexMeta(gomock.Any(), index).Return(metadataErr)
						}
						if scenario != "metadata failure" {
							countCall = c.EXPECT().CountRows(gomock.Any(), index).DoAndReturn(func(ctx context.Context, _ *interfaces.IndexMeta) (int64, error) { return count(ctx) })
						}
						c.EXPECT().Connect(ctx).Return(nil)
						c.EXPECT().Close(gomock.Any()).Return(nil)
						if entry == "catalog" {
							c.EXPECT().GetMetadata(gomock.Any()).Return(map[string]any{}, nil)
						}
					}
					cf.EXPECT().CreateConnectorInstance(gomock.Any(), "test", gomock.Any()).Return(connector, nil)
					if countCall != nil && metadataCall != nil {
						countCall.After(metadataCall)
					}
					if countWithoutRefresh {
						rs.EXPECT().InternalUpdateRowCount(gomock.Any(), resource, int64(0), gomock.Any()).DoAndReturn(func(ctx context.Context, saved *interfaces.Resource, _ int64, _ int64) error {
							require.NoError(t, ctx.Err())
							require.Equal(t, int64(10), saved.LastDiscoverTime)
							return nil
						}).After(countCall)
					}
					if scenario == "success" || scenario == "metadata failure" {
						save := rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, resource, int64(20)).DoAndReturn(func(saveCtx context.Context, _ *sql.Tx, saved *interfaces.Resource, _ int64) error {
							require.NoError(t, saveCtx.Err())
							properties := saved.SourceMetadata["properties"].(map[string]any)
							if scenario == "success" {
								count, ok := common.NumberAsInt64(properties["row_count"])
								require.True(t, ok)
								require.Equal(t, int64(0), count)
								countTime, ok := common.NumberAsInt64(properties["row_count_time"])
								require.True(t, ok)
								require.Equal(t, saved.LastDiscoverTime, countTime)
								require.Greater(t, saved.LastDiscoverTime, int64(10))
							} else {
								require.Equal(t, oldProperties, properties)
								require.Equal(t, int64(10), saved.LastDiscoverTime)
							}
							return nil
						})
						if countCall != nil {
							save.After(countCall)
						} else {
							save.After(metadataCall)
						}
					}
					progress := &discoverTaskReconcileProgress{}
					var result *interfaces.DiscoverResult
					var err error
					if entry == "resource" {
						result, err = worker.discoverResource(ctx, catalog, task, progress)
					} else {
						result, err = worker.discoverCatalog(ctx, catalog, task, progress)
					}
					if scenario == "cancelled" {
						require.ErrorIs(t, err, context.Canceled)
						return
					}
					require.NoError(t, err)
					if scenario == "success" || countWithoutRefresh {
						require.Equal(t, 1, result.UpdatedCount)
						require.Zero(t, result.FailedCount)
					} else {
						require.Equal(t, 1, result.FailedCount)
						require.Zero(t, result.UpdatedCount)
						require.Equal(t, int64(10), resource.LastDiscoverTime)
						require.Equal(t, oldProperties, resource.SourceMetadata["properties"])
					}
				})
			}
		}
	}
}

func TestDiscoverTaskWorkerRunCountOnly(t *testing.T) {
	for _, single := range []bool{false, true} {
		name := "catalog"
		if single {
			name = "resource"
		}
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			dts := vmock.NewMockDiscoverTaskService(ctrl)
			cs := vmock.NewMockCatalogService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			connector := vmock.NewMockTableConnector(ctrl)
			worker := &DiscoverTaskWorker{dts: dts, cs: cs, rs: rs, cf: cf}
			task := &interfaces.DiscoverTask{ID: "task", CatalogID: "cat", Strategy: interfaces.DiscoverStrategyCountOnly, Status: interfaces.DiscoverTaskStatusPending}
			resource := &interfaces.Resource{ID: "res", CatalogID: "cat", Category: interfaces.ResourceCategoryTable, SourceIdentifier: "app.orders"}
			catalog := &interfaces.Catalog{ID: "cat", Enabled: true, Type: interfaces.CatalogTypePhysical, ConnectorType: "test"}
			if single {
				task.ResourceID = resource.ID
				rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
			} else {
				connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)
				rs.EXPECT().InternalGetByCatalogID(gomock.Any(), catalog.ID).Return([]*interfaces.Resource{resource}, nil)
			}
			dts.EXPECT().InternalGetByID(gomock.Any(), task.ID).Return(task, nil)
			dts.EXPECT().InternalMarkRunning(gomock.Any(), task.ID).Return(true, nil)
			dts.EXPECT().InternalUpdateProgress(gomock.Any(), task.ID, gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
			cs.EXPECT().InternalGetByID(gomock.Any(), catalog.ID, true).Return(catalog, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "test", gomock.Any()).Return(connector, nil)
			connector.EXPECT().Connect(gomock.Any()).DoAndReturn(func(connectCtx context.Context) error {
				_, hasDeadline := connectCtx.Deadline()
				require.False(t, hasDeadline, "connecting must not add the count query timeout")
				require.NoError(t, connectCtx.Err())
				return nil
			})
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			connector.EXPECT().CountRows(gomock.Any(), gomock.Any()).Return(int64(42), nil)
			rs.EXPECT().InternalUpdateRowCount(gomock.Any(), resource, int64(42), gomock.Any()).Return(nil)
			dts.EXPECT().InternalMarkCompleted(gomock.Any(), task.ID, gomock.Any()).DoAndReturn(func(_ context.Context, _ string, result *interfaces.DiscoverResult) (bool, error) {
				require.Equal(t, 1, result.UpdatedCount)
				require.Zero(t, result.FailedCount)
				require.False(t, result.Failed)
				return true, nil
			})
			require.NoError(t, worker.Run(context.Background(), task.ID))
		})
	}
}
