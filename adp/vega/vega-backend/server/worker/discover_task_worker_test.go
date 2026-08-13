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

	"github.com/openbkn-ai/bkn-comm-go/rest"
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

func TestDiscoverTaskWorkerCancelsTaskWhenCatalogWasDeleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts, cs: cs}
	dts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(&interfaces.DiscoverTask{
		ID: "task-1", CatalogID: "catalog-1", Status: interfaces.DiscoverTaskStatusPending,
	}, nil)
	cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
		Return(nil, &rest.HTTPError{HTTPCode: http.StatusNotFound})
	dts.EXPECT().InternalMarkCancelled(gomock.Any(), "task-1", "catalog deleted", gomock.Any()).Return(true, nil)

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
	cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)
	dts.EXPECT().InternalMarkFailed(gomock.Any(), "task-1", "catalog is disabled", gomock.Any()).Return(true, nil)

	require.NoError(t, worker.Run(context.Background(), "task-1"))
}

func TestDiscoverTaskWorkerMarksTaskFailedWhenCatalogLookupFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts, cs: cs}
	dts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(&interfaces.DiscoverTask{
		ID: "task-1", CatalogID: "catalog-1", Status: interfaces.DiscoverTaskStatusPending,
	}, nil)
	cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
		Return(nil, errors.New("temporary database error"))
	dts.EXPECT().InternalMarkFailed(gomock.Any(), "task-1", "temporary database error", gomock.Any()).
		Return(true, nil)

	err := worker.Run(context.Background(), "task-1")

	require.ErrorContains(t, err, "temporary database error")
}

func TestDiscoverTaskWorkerStopsWhenRunningTransitionMisses(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts, cs: cs}
	dts.EXPECT().InternalGetByID(gomock.Any(), "task-1").Return(&interfaces.DiscoverTask{
		ID: "task-1", CatalogID: "catalog-1", Status: interfaces.DiscoverTaskStatusPending,
	}, nil)
	cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	dts.EXPECT().InternalMarkRunning(gomock.Any(), "task-1").
		Return(false, nil)

	require.NoError(t, worker.Run(context.Background(), "task-1"))
}

func TestDiscoverTaskWorkerRecoversInterruptedTasks(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts, queueSize: 2}

	firstList := dts.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, int64, error) {
			assert.Equal(t, []string{interfaces.DiscoverTaskStatusRunning}, params.Statuses)
			assert.Equal(t, 2, params.Limit)
			assert.Equal(t, interfaces.DiscoverTaskSortCreateTime, params.Sort)
			assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
			return []*interfaces.DiscoverTaskSummary{{ID: "task-1"}}, 1, nil
		})
	markFailed := dts.EXPECT().InternalMarkFailed(gomock.Any(), "task-1",
		"discover task interrupted by service restart", gomock.Any()).Return(true, nil).After(firstList)
	dts.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(
		[]*interfaces.DiscoverTaskSummary{}, int64(0), nil).After(markFailed)

	require.NoError(t, worker.recoverInterruptedTasks(context.Background()))
}

func TestDiscoverTaskWorkerRecoveryReturnsUpdateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{dts: dts, queueSize: 1}
	dts.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(
		[]*interfaces.DiscoverTaskSummary{{ID: "task-1"}}, int64(1), nil)
	dts.EXPECT().InternalMarkFailed(gomock.Any(), "task-1",
		"discover task interrupted by service restart", gomock.Any()).Return(false, errors.New("database unavailable"))

	err := worker.recoverInterruptedTasks(context.Background())

	require.ErrorContains(t, err, "database unavailable")
}

func TestDiscoverTaskWorkerFillQueueRefillsEmptyQueue(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{
		dts:      dts,
		queue:    make(chan string, 2),
		inFlight: make(map[string]struct{}),
	}
	dts.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, int64, error) {
			assert.Equal(t, 2, params.Limit)
			assert.Equal(t, []string{interfaces.DiscoverTaskStatusPending}, params.Statuses)
			assert.Equal(t, interfaces.DiscoverTaskSortCreateTime, params.Sort)
			assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
			return []*interfaces.DiscoverTaskSummary{{ID: "task-1"}}, 1, nil
		})

	worker.fillQueue(context.Background())

	assert.Len(t, worker.queue, 1)
	assert.False(t, worker.addInFlight("task-1"))
}

func TestDiscoverTaskWorkerFillQueueSkipsDatabaseWhenQueueIsNotEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dts := vmock.NewMockDiscoverTaskService(ctrl)
	worker := &DiscoverTaskWorker{
		dts:      dts,
		queue:    make(chan string, 2),
		inFlight: make(map[string]struct{}),
	}
	worker.queue <- "already-queued"

	worker.fillQueue(context.Background())
}

func TestDiscoverTaskWorkerRecoversTaskPanic(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	taskService := vmock.NewMockDiscoverTaskService(ctrl)
	ctx, cancel := context.WithCancel(context.Background())
	worker := &DiscoverTaskWorker{
		dts: taskService,
		queue: func() chan string {
			queue := make(chan string, 2)
			queue <- "task-1"
			queue <- "task-2"
			return queue
		}(),
		inFlight: map[string]struct{}{"task-1": {}, "task-2": {}},
	}
	taskService.EXPECT().InternalGetByID(gomock.Any(), "task-1").DoAndReturn(
		func(context.Context, string) (*interfaces.DiscoverTask, error) {
			panic("unexpected connector panic")
		},
	)
	taskService.EXPECT().
		InternalMarkFailed(gomock.Any(), "task-1", "discover task panicked: unexpected connector panic", gomock.Any()).
		Return(true, nil)
	taskService.EXPECT().InternalGetByID(gomock.Any(), "task-2").Return(&interfaces.DiscoverTask{
		ID: "task-2", Status: interfaces.DiscoverTaskStatusFailed,
	}, nil)
	dispatchCount := 0
	taskService.EXPECT().RequestDispatch().Times(2).Do(func() {
		dispatchCount++
		if dispatchCount == 2 {
			cancel()
		}
	})
	done := make(chan struct{})

	go func() {
		defer close(done)
		worker.runQueuedTasks(ctx)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("discover task worker did not continue after panic")
	}
	assert.True(t, worker.addInFlight("task-1"), "panic must not leak the in-flight task ID")
	assert.True(t, worker.addInFlight("task-2"), "worker must continue and release the next task ID")
}

func TestReconcileTableResources(t *testing.T) {
	t.Run("marks new table resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		table := &interfaces.TableMeta{Name: "users"}
		created := &interfaces.Resource{ID: "r1", SourceIdentifier: "users", Status: interfaces.ResourceStatusActive}
		rs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(created, nil)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusNew).Return(nil)
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, items, err := dh.reconcileTableResources(context.Background(), &interfaces.Catalog{ID: "cat1"},
			[]*interfaces.TableMeta{table}, nil, &actions)

		require.NoError(t, err)
		assert.Equal(t, 1, result.NewCount)
		require.Len(t, items, 1)
		assert.False(t, items[0].markAfterEnrich)
	})

	t.Run("refreshes missing status when already stale", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusMissing).Return(nil)
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, _, err := dh.reconcileTableResources(context.Background(), &interfaces.Catalog{ID: "cat1"}, nil,
			[]*interfaces.Resource{{
				ID:               "r1",
				SourceIdentifier: "users",
				Category:         interfaces.ResourceCategoryTable,
				Status:           interfaces.ResourceStatusStale,
			}}, &actions)

		require.NoError(t, err)
		assert.Zero(t, result.StaleCount)
	})

	t.Run("does not disable user-disabled resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusMissing).Return(nil)
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, _, err := dh.reconcileTableResources(context.Background(), &interfaces.Catalog{ID: "cat1"}, nil,
			[]*interfaces.Resource{{
				ID:               "r1",
				SourceIdentifier: "users",
				Category:         interfaces.ResourceCategoryTable,
				Status:           interfaces.ResourceStatusDisabled,
			}}, &actions)

		require.NoError(t, err)
		assert.Zero(t, result.StaleCount)
	})

	t.Run("marks restored stale table", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		rs.EXPECT().UpdateStatus(gomock.Any(), "r1", interfaces.ResourceStatusActive, "").Return(nil)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusRestored).Return(nil)
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, items, err := dh.reconcileTableResources(context.Background(), &interfaces.Catalog{ID: "cat1"},
			[]*interfaces.TableMeta{{Name: "users"}},
			[]*interfaces.Resource{{
				ID:               "r1",
				SourceIdentifier: "users",
				Category:         interfaces.ResourceCategoryTable,
				Status:           interfaces.ResourceStatusStale,
			}}, &actions)

		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.False(t, items[0].markAfterEnrich)
		assert.Equal(t, 1, result.RestoredCount)
		assert.Zero(t, result.UnchangedCount)
	})
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

func TestBuildSourceIdentifierUsesSchemaAsQueryableNamespace(t *testing.T) {
	dh := &DiscoverTaskWorker{}

	cases := []struct {
		name  string
		table *interfaces.TableMeta
		want  string
	}{
		{
			name:  "postgresql schema table",
			table: &interfaces.TableMeta{Database: "ecommerce_db", Schema: "public", Name: "supplier_catalog"},
			want:  "public.supplier_catalog",
		},
		{
			name:  "mariadb schema equals database",
			table: &interfaces.TableMeta{Database: "ecommerce_db", Schema: "ecommerce_db", Name: "supplier_catalog"},
			want:  "ecommerce_db.supplier_catalog",
		},
		{
			name:  "database fallback",
			table: &interfaces.TableMeta{Database: "ecommerce_db", Name: "supplier_catalog"},
			want:  "ecommerce_db.supplier_catalog",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := dh.buildSourceIdentifier(tt.table); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestEnrichTableMetadataContinuesWhenOneTableFails(t *testing.T) {
	t.Run("continues when one table fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		inaccessible := &interfaces.Resource{ID: "r1", SourceIdentifier: "public.no_access", LastDiscoverStatus: interfaces.DiscoverStatusNew}
		accessible := &interfaces.Resource{
			ID:                 "r2",
			SourceIdentifier:   "public.erp_material",
			LastDiscoverStatus: interfaces.DiscoverStatusNew,
			StatusMessage:      "discover metadata failed: table metadata not found or inaccessible: public.erp_material",
			SourceMetadata:     map[string]any{"original_name": "public.erp_material"},
		}
		connector := vmock.NewMockTableConnector(ctrl)
		connector.EXPECT().
			GetTableMeta(gomock.Any(), &interfaces.TableMeta{Name: "no_access", Schema: "public"}).
			Return(errors.New("permission denied"))
		connector.EXPECT().
			GetTableMeta(gomock.Any(), &interfaces.TableMeta{Name: "erp_material", Schema: "public"}).
			DoAndReturn(func(_ context.Context, table *interfaces.TableMeta) error {
				table.Columns = []interfaces.TableColumnMeta{{Name: "id", Type: "int4"}}
				return nil
			})
		connector.EXPECT().MapType("int4").Return("int4")
		rs.EXPECT().UpdateResource(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.Resource{})).
			DoAndReturn(func(_ context.Context, resource *interfaces.Resource) error {
				assert.Equal(t, "r1", resource.ID)
				assert.Equal(t, interfaces.DiscoverStatusError, resource.LastDiscoverStatus)
				assert.NotEmpty(t, resource.StatusMessage)
				return nil
			})
		rs.EXPECT().UpdateResource(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.Resource{})).
			DoAndReturn(func(_ context.Context, resource *interfaces.Resource) error {
				assert.Equal(t, "r2", resource.ID)
				require.Len(t, resource.SchemaDefinition, 1)
				assert.Equal(t, "id", resource.SchemaDefinition[0].Name)
				assert.Empty(t, resource.StatusMessage)
				return nil
			})

		result := &interfaces.DiscoverResult{}
		err := dh.enrichTableMetadata(context.Background(), connector, []tableDiscoverItem{
			{resource: inaccessible, tableMeta: &interfaces.TableMeta{Name: "no_access", Schema: "public"}},
			{resource: accessible, tableMeta: &interfaces.TableMeta{Name: "erp_material", Schema: "public"}},
		}, result)

		require.NoError(t, err)
		assert.Equal(t, 1, result.FailedCount)
	})
}

func TestEnrichTableMetadataPreservesBusinessMetadata(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverTaskWorker{rs: rs}
	resource := &interfaces.Resource{
		ID:               "r1",
		SourceIdentifier: "public.departments",
		SchemaDefinition: []*interfaces.Property{
			{
				Name:                "department_id",
				DisplayName:         "部门ID",
				Description:         "部门的唯一标识",
				Type:                "integer",
				OriginalDescription: "旧源端注释",
				Features: []interfaces.PropertyFeature{{
					FeatureName: "fulltext",
					FeatureType: interfaces.PropertyFeatureType_Fulltext,
				}},
			},
			{Name: "obsolete_column", DisplayName: "已删除字段", Type: interfaces.DataType_String},
		},
	}
	connector := vmock.NewMockTableConnector(ctrl)
	connector.EXPECT().
		GetTableMeta(gomock.Any(), &interfaces.TableMeta{Name: "departments", Schema: "public"}).
		DoAndReturn(func(_ context.Context, table *interfaces.TableMeta) error {
			table.Columns = []interfaces.TableColumnMeta{
				{Name: "department_id", Type: "varchar", Description: "源端最新部门编号注释"},
				{Name: "department_name", Type: "varchar", Description: "部门名称"},
			}
			return nil
		})
	connector.EXPECT().MapType("varchar").Return(interfaces.DataType_String).Times(2)
	rs.EXPECT().UpdateResource(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.Resource{})).
		DoAndReturn(func(_ context.Context, updated *interfaces.Resource) error {
			require.Len(t, updated.SchemaDefinition, 2)

			existing := updated.SchemaDefinition[0]
			assert.Equal(t, "department_id", existing.Name)
			assert.Equal(t, "部门ID", existing.DisplayName)
			assert.Equal(t, "部门的唯一标识", existing.Description)
			assert.Equal(t, interfaces.DataType_String, existing.Type)
			assert.Equal(t, "varchar", existing.OriginalType)
			assert.Equal(t, "源端最新部门编号注释", existing.OriginalDescription)
			assert.Equal(t, []interfaces.PropertyFeature{{
				FeatureName: "fulltext",
				FeatureType: interfaces.PropertyFeatureType_Fulltext,
			}}, existing.Features)

			added := updated.SchemaDefinition[1]
			assert.Equal(t, "department_name", added.Name)
			assert.Equal(t, "department_name", added.DisplayName)
			assert.Equal(t, "部门名称", added.Description)
			assert.Equal(t, "部门名称", added.OriginalDescription)
			assert.Equal(t, []string{"department_id", "department_name"}, []string{existing.Name, added.Name})
			return nil
		})

	err := dh.enrichTableMetadata(context.Background(), connector, []tableDiscoverItem{{
		resource: resource,
		tableMeta: &interfaces.TableMeta{
			Name: "departments", Schema: "public",
		},
	}}, &interfaces.DiscoverResult{})

	require.NoError(t, err)
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
