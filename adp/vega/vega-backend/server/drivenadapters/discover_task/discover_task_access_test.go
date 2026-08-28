// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package discover_task

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestDiscoverTaskAccessGetByID(t *testing.T) {
	t.Run("returns discover task", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectQuery("SELECT " + strings.Join(discoverTaskColumns(), ", ") + " FROM t_discover_task WHERE f_id = ?").
			WithArgs("task-1").
			WillReturnRows(discoverTaskRows().AddRow(
				"task-1",
				"catalog-1",
				"",
				"schedule-1",
				"full_sync",
				interfaces.DiscoverTaskTriggerManual,
				interfaces.DiscoverTaskQueuePriorityNormal,
				interfaces.DiscoverTaskStatusCompleted,
				100,
				"done",
				int64(10),
				int64(20),
				int64(15),
				`{"databases":[{"name":"db1"}]}`,
				"u1",
				interfaces.ACCESSOR_TYPE_USER,
				int64(1),
			))

		got, err := access.GetByID(context.Background(), "task-1")

		require.NoError(t, err)
		assert.Equal(t, "task-1", got.ID)
		assert.Equal(t, interfaces.DiscoverTaskStatusCompleted, got.Status)
		assert.Equal(t, int64(15), got.LastProgressTime)
		assert.Equal(t, "u1", got.Creator.ID)
		require.NotNil(t, got.Result)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverTaskAccessList(t *testing.T) {
	t.Run("returns tasks with filters", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		params := interfaces.DiscoverTaskQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 5, Limit: 10, Sort: interfaces.DiscoverTaskSortCreateTime, Direction: "ASC"},
			CatalogID:             "catalog-1",
			Statuses:              []string{interfaces.DiscoverTaskStatusRunning, interfaces.DiscoverTaskStatusPending},
			Strategy:              interfaces.DiscoverStrategyFullSync,
			TriggerType:           interfaces.DiscoverTaskTriggerScheduled,
		}

		mock.ExpectQuery("SELECT COUNT(*) FROM t_discover_task WHERE f_catalog_id = ? AND f_status IN (?,?) AND f_strategy = ? AND f_trigger_type = ?").
			WithArgs("catalog-1", interfaces.DiscoverTaskStatusRunning, interfaces.DiscoverTaskStatusPending, interfaces.DiscoverStrategyFullSync, interfaces.DiscoverTaskTriggerScheduled).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("SELECT "+strings.Join(discoverTaskSummaryColumns(), ", ")+" FROM t_discover_task WHERE f_catalog_id = ? AND f_status IN (?,?) AND f_strategy = ? AND f_trigger_type = ? ORDER BY f_create_time ASC LIMIT 10 OFFSET 5").
			WithArgs("catalog-1", interfaces.DiscoverTaskStatusRunning, interfaces.DiscoverTaskStatusPending, interfaces.DiscoverStrategyFullSync, interfaces.DiscoverTaskTriggerScheduled).
			WillReturnRows(discoverTaskSummaryRows().AddRow("task-1", "catalog-1", "", "schedule-1", "full_sync", interfaces.DiscoverTaskTriggerScheduled, interfaces.DiscoverTaskQueuePriorityLow, interfaces.DiscoverTaskStatusRunning, 10, int64(0), int64(0), int64(0), `{"catalog_id":"catalog-1","new_count":2,"message":"large detail"}`, "u1", interfaces.ACCESSOR_TYPE_USER, int64(1)))

		got, total, err := access.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, got, 1)
		assert.Equal(t, "task-1", got[0].ID)
		require.NotNil(t, got[0].Result)
		assert.Equal(t, 2, got[0].Result.NewCount)
		serialized, err := sonic.MarshalString(got[0])
		require.NoError(t, err)
		assert.NotContains(t, serialized, "large detail")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("internal list skips count query", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		params := interfaces.DiscoverTaskQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Limit: 1},
			Statuses:              []string{interfaces.DiscoverTaskStatusPending},
		}
		mock.ExpectQuery("SELECT " + strings.Join(discoverTaskSummaryColumns(), ", ") + " FROM t_discover_task WHERE f_status IN (?) ORDER BY f_create_time DESC LIMIT 1 OFFSET 0").
			WithArgs(interfaces.DiscoverTaskStatusPending).
			WillReturnRows(discoverTaskSummaryRows().AddRow("task-1", "catalog-1", "", "", "full_sync", "manual", interfaces.DiscoverTaskQueuePriorityNormal, interfaces.DiscoverTaskStatusPending, 0, int64(0), int64(0), int64(0), "", "u1", interfaces.ACCESSOR_TYPE_USER, int64(1)))

		got, err := access.InternalList(context.Background(), params)

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "task-1", got[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("internal list orders dispatch candidates by priority then creation time", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		params := interfaces.DiscoverTaskQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{
				Limit: 1, Sort: interfaces.DiscoverTaskSortQueuePriority, Direction: interfaces.DESC_DIRECTION,
			},
			Statuses: []string{interfaces.DiscoverTaskStatusPending},
		}
		mock.ExpectQuery("SELECT " + strings.Join(discoverTaskSummaryColumns(), ", ") + " FROM t_discover_task WHERE f_status IN (?) ORDER BY f_queue_priority DESC, f_create_time ASC LIMIT 1 OFFSET 0").
			WithArgs(interfaces.DiscoverTaskStatusPending).
			WillReturnRows(discoverTaskSummaryRows().AddRow("task-1", "catalog-1", "", "", "full_sync", "manual", interfaces.DiscoverTaskQueuePriorityHigh, interfaces.DiscoverTaskStatusPending, 0, int64(0), int64(0), int64(0), "", "u1", interfaces.ACCESSOR_TYPE_USER, int64(1)))

		_, err := access.InternalList(context.Background(), params)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("sorts by last progress time", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		params := interfaces.DiscoverTaskQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{
				Limit: 1, Sort: interfaces.DiscoverTaskSortLastProgressTime, Direction: interfaces.ASC_DIRECTION,
			},
		}
		mock.ExpectQuery("SELECT " + strings.Join(discoverTaskSummaryColumns(), ", ") + " FROM t_discover_task ORDER BY f_last_progress_time ASC LIMIT 1 OFFSET 0").
			WillReturnRows(discoverTaskSummaryRows().AddRow("task-1", "catalog-1", "", "", "full_sync", "manual", interfaces.DiscoverTaskQueuePriorityNormal, interfaces.DiscoverTaskStatusRunning, 25, int64(1), int64(0), int64(2), "", "u1", interfaces.ACCESSOR_TYPE_USER, int64(1)))

		got, err := access.InternalList(context.Background(), params)

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, int64(2), got[0].LastProgressTime)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverTaskAccessCreate(t *testing.T) {
	t.Run("creates discover task", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()
		task := sampleDiscoverTask()

		mock.ExpectExec("INSERT INTO t_discover_task (f_id,f_catalog_id,f_resource_id,f_schedule_id,f_strategy,f_trigger_type,f_queue_priority,f_status,f_progress,f_message,f_start_time,f_finish_time,f_last_progress_time,f_result,f_creator,f_creator_type,f_create_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)").
			WithArgs(task.ID, task.CatalogID, task.ResourceID, task.ScheduleID, task.Strategy, task.TriggerType, task.QueuePriority, task.Status, task.Progress, task.Message, task.StartTime, task.FinishTime, task.LastProgressTime, "", task.Creator.ID, task.Creator.Type, task.CreateTime).
			WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, access.Create(context.Background(), task))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverTaskAccessGetScheduledTaskStrategy(t *testing.T) {
	t.Run("returns strategy", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectQuery("SELECT f_strategy FROM t_discover_schedule WHERE f_id = ?").
			WithArgs("schedule-1").
			WillReturnRows(sqlmock.NewRows([]string{"f_strategy"}).AddRow("cleanup_only"))

		got, err := access.GetScheduledTaskStrategy(context.Background(), "schedule-1")

		require.NoError(t, err)
		assert.Equal(t, "cleanup_only", got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns empty when schedule not found", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectQuery("SELECT f_strategy FROM t_discover_schedule WHERE f_id = ?").
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := access.GetScheduledTaskStrategy(context.Background(), "missing")

		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverTaskAccessMarkRunning(t *testing.T) {
	t.Run("marks pending task running", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectExec("UPDATE t_discover_task SET f_message = ?, f_start_time = ?, f_status = ? WHERE f_id = ? AND f_status = ?").
			WithArgs("", int64(123), interfaces.DiscoverTaskStatusRunning, "task-1", interfaces.DiscoverTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.MarkRunning(context.Background(), "task-1", 123)

		require.NoError(t, err)
		assert.True(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("reports status mismatch", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectExec("UPDATE t_discover_task SET f_message = ?, f_start_time = ?, f_status = ? WHERE f_id = ? AND f_status = ?").
			WithArgs("", int64(123), interfaces.DiscoverTaskStatusRunning, "task-1", interfaces.DiscoverTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 0))

		updated, err := access.MarkRunning(context.Background(), "task-1", 123)

		require.NoError(t, err)
		assert.False(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverTaskAccessUpdateProgress(t *testing.T) {
	access, mock, cleanup := newDiscoverTaskAccessMock(t)
	defer cleanup()

	mock.ExpectExec("UPDATE t_discover_task SET f_last_progress_time = ?, f_message = ?, f_progress = ? WHERE f_id = ? AND f_status = ?").
		WithArgs(int64(456), "resources reconciled", 70, "task-1", interfaces.DiscoverTaskStatusRunning).
		WillReturnResult(sqlmock.NewResult(0, 1))

	updated, err := access.UpdateProgress(context.Background(), "task-1", 70, "resources reconciled", 456)

	require.NoError(t, err)
	assert.True(t, updated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDiscoverTaskAccessMarkFailed(t *testing.T) {
	access, mock, cleanup := newDiscoverTaskAccessMock(t)
	defer cleanup()

	mock.ExpectExec("UPDATE t_discover_task SET f_finish_time = ?, f_message = ?, f_status = ? WHERE f_id = ? AND f_status IN (?,?)").
		WithArgs(int64(123), "execution failed", interfaces.DiscoverTaskStatusFailed, "task-1",
			interfaces.DiscoverTaskStatusPending, interfaces.DiscoverTaskStatusRunning).
		WillReturnResult(sqlmock.NewResult(0, 1))

	updated, err := access.MarkFailed(context.Background(), "task-1", "execution failed", 123)

	require.NoError(t, err)
	assert.True(t, updated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDiscoverTaskAccessMarkCompleted(t *testing.T) {
	t.Run("completes task with result", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectExec("UPDATE t_discover_task SET f_finish_time = ?, f_progress = ?, f_result = ?, f_status = ? WHERE f_id = ? AND f_status = ?").
			WithArgs(int64(999), 100, `{"catalog_id":"catalog-1","new_count":1,"stale_count":0,"unchanged_count":0,"updated_count":0,"restored_count":0,"failed_count":0,"message":"done"}`, interfaces.DiscoverTaskStatusCompleted, "task-1", interfaces.DiscoverTaskStatusRunning).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.MarkCompleted(context.Background(), "task-1", &interfaces.DiscoverResult{
			CatalogID: "catalog-1",
			NewCount:  1,
			Message:   "done",
		}, 999)

		require.NoError(t, err)
		assert.True(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("reports status mismatch", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectExec("UPDATE t_discover_task SET f_finish_time = ?, f_progress = ?, f_result = ?, f_status = ? WHERE f_id = ? AND f_status = ?").
			WithArgs(int64(999), 100, `{"catalog_id":"catalog-1","new_count":0,"stale_count":0,"unchanged_count":0,"updated_count":0,"restored_count":0,"failed_count":0,"message":""}`, interfaces.DiscoverTaskStatusCompleted, "task-1", interfaces.DiscoverTaskStatusRunning).
			WillReturnResult(sqlmock.NewResult(0, 0))

		updated, err := access.MarkCompleted(context.Background(), "task-1", &interfaces.DiscoverResult{
			CatalogID: "catalog-1",
		}, 999)

		require.NoError(t, err)
		assert.False(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverTaskAccessDeleteByIDs(t *testing.T) {
	t.Run("deletes tasks", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectExec("DELETE FROM t_discover_task WHERE f_id IN (?,?)").
			WithArgs("task-1", "task-2").
			WillReturnResult(sqlmock.NewResult(0, 2))

		deleted, err := access.DeleteByIDs(context.Background(), []string{"task-1", "task-2"})

		require.NoError(t, err)
		assert.Equal(t, int64(2), deleted)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty ids do not query database", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		deleted, err := access.DeleteByIDs(context.Background(), nil)

		require.NoError(t, err)
		assert.Zero(t, deleted)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverTaskAccessMarkCancelledByCatalogID(t *testing.T) {
	access, mock, cleanup := newDiscoverTaskAccessMock(t)
	defer cleanup()

	mock.ExpectExec("UPDATE t_discover_task SET f_finish_time = ?, f_message = ?, f_status = ? WHERE f_catalog_id = ? AND f_status = ?").
		WithArgs(int64(100), "catalog deleted", interfaces.DiscoverTaskStatusCancelled, "catalog-1",
			interfaces.DiscoverTaskStatusPending).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, access.MarkCancelledByCatalogID(context.Background(), nil, "catalog-1", "catalog deleted", 100))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDiscoverTaskAccessMarkCancelled(t *testing.T) {
	access, mock, cleanup := newDiscoverTaskAccessMock(t)
	defer cleanup()

	mock.ExpectExec("UPDATE t_discover_task SET f_finish_time = ?, f_message = ?, f_status = ? WHERE f_id = ? AND f_status IN (?,?)").
		WithArgs(int64(100), "catalog deleted", interfaces.DiscoverTaskStatusCancelled, "task-1",
			interfaces.DiscoverTaskStatusPending, interfaces.DiscoverTaskStatusRunning).
		WillReturnResult(sqlmock.NewResult(0, 1))

	updated, err := access.MarkCancelled(context.Background(), "task-1", "catalog deleted", 100)

	require.NoError(t, err)
	assert.True(t, updated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func sampleDiscoverTask() *interfaces.DiscoverTask {
	return &interfaces.DiscoverTask{
		ID:            "task-1",
		CatalogID:     "catalog-1",
		ResourceID:    "",
		ScheduleID:    "schedule-1",
		Strategy:      "full_sync",
		TriggerType:   interfaces.DiscoverTaskTriggerManual,
		QueuePriority: interfaces.DiscoverTaskQueuePriorityNormal,
		Status:        interfaces.DiscoverTaskStatusPending,
		Progress:      0,
		Message:       "queued",
		StartTime:     0,
		FinishTime:    0,
		Creator:       interfaces.AccountInfo{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
		CreateTime:    1,
	}
}

func newDiscoverTaskAccessMock(t *testing.T) (*discoverTaskAccess, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)

	return &discoverTaskAccess{db: db}, mock, func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}
}

func discoverTaskRows() *sqlmock.Rows {
	return sqlmock.NewRows(discoverTaskColumns())
}

func discoverTaskSummaryRows() *sqlmock.Rows {
	return sqlmock.NewRows(discoverTaskSummaryColumns())
}
