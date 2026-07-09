// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package discover_task

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestDiscoverTaskAccessGetByID(t *testing.T) {
	access, mock, cleanup := newDiscoverTaskAccessMock(t)
	defer cleanup()

	mock.ExpectQuery("SELECT f_id, f_catalog_id, f_schedule_id, f_strategy, f_trigger_type, f_status, f_progress, f_message, f_start_time, f_finish_time, f_result, f_creator, f_creator_type, f_create_time FROM t_discover_task WHERE f_id = ?").
		WithArgs("task-1").
		WillReturnRows(discoverTaskRows().AddRow(
			"task-1",
			"catalog-1",
			"schedule-1",
			"full_sync",
			interfaces.DiscoverTaskTriggerManual,
			interfaces.DiscoverTaskStatusCompleted,
			100,
			"done",
			int64(10),
			int64(20),
			`{"databases":[{"name":"db1"}]}`,
			"u1",
			interfaces.ACCESSOR_TYPE_USER,
			int64(1),
		))

	got, err := access.GetByID(context.Background(), "task-1")

	require.NoError(t, err)
	assert.Equal(t, "task-1", got.ID)
	assert.Equal(t, interfaces.DiscoverTaskStatusCompleted, got.Status)
	assert.Equal(t, "u1", got.Creator.ID)
	require.NotNil(t, got.Result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDiscoverTaskAccessList(t *testing.T) {
	access, mock, cleanup := newDiscoverTaskAccessMock(t)
	defer cleanup()

	params := interfaces.DiscoverTaskQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 5, Limit: 10, Sort: "f_create_time", Direction: "ASC"},
		CatalogID:             "catalog-1",
		Status:                interfaces.DiscoverTaskStatusRunning,
		TriggerType:           interfaces.DiscoverTaskTriggerScheduled,
	}

	mock.ExpectQuery("SELECT COUNT(*) FROM t_discover_task WHERE f_catalog_id = ? AND f_status = ? AND f_trigger_type = ?").
		WithArgs("catalog-1", interfaces.DiscoverTaskStatusRunning, interfaces.DiscoverTaskTriggerScheduled).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT f_id, f_catalog_id, f_schedule_id, f_strategy, f_trigger_type, f_status, f_progress, f_message, f_start_time, f_finish_time, f_result, f_creator, f_creator_type, f_create_time FROM t_discover_task WHERE f_catalog_id = ? AND f_status = ? AND f_trigger_type = ? ORDER BY f_create_time ASC LIMIT 10 OFFSET 5").
		WithArgs("catalog-1", interfaces.DiscoverTaskStatusRunning, interfaces.DiscoverTaskTriggerScheduled).
		WillReturnRows(discoverTaskRows().AddRow("task-1", "catalog-1", "schedule-1", "full_sync", interfaces.DiscoverTaskTriggerScheduled, interfaces.DiscoverTaskStatusRunning, 10, "", int64(0), int64(0), "", "u1", interfaces.ACCESSOR_TYPE_USER, int64(1)))

	got, total, err := access.List(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, "task-1", got[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDiscoverTaskAccessUpdatesAndDelete(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()
		task := sampleDiscoverTask()

		mock.ExpectExec("INSERT INTO t_discover_task (f_id,f_catalog_id,f_schedule_id,f_strategy,f_trigger_type,f_status,f_progress,f_message,f_start_time,f_finish_time,f_result,f_creator,f_creator_type,f_create_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)").
			WithArgs(task.ID, task.CatalogID, task.ScheduleID, task.Strategy, task.TriggerType, task.Status, task.Progress, task.Message, task.StartTime, task.FinishTime, "", task.Creator.ID, task.Creator.Type, task.CreateTime).
			WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, access.Create(context.Background(), task))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get scheduled strategy", func(t *testing.T) {
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

	t.Run("update running status sets start time", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectExec("UPDATE t_discover_task SET f_message = ?, f_start_time = ?, f_status = ? WHERE f_id = ?").
			WithArgs("started", int64(123), interfaces.DiscoverTaskStatusRunning, "task-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.UpdateStatus(context.Background(), "task-1", interfaces.DiscoverTaskStatusRunning, "started", 123))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update progress", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectExec("UPDATE t_discover_task SET f_progress = ?, f_update_time = ? WHERE f_id = ?").
			WithArgs(30, sqlmock.AnyArg(), "task-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.UpdateProgress(context.Background(), "task-1", 30))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update result completes task", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectExec("UPDATE t_discover_task SET f_status = ?, f_result = ?, f_progress = ?, f_finish_time = ? WHERE f_id = ?").
			WithArgs(interfaces.DiscoverTaskStatusCompleted, `{"catalog_id":"catalog-1","new_count":1,"stale_count":0,"unchanged_count":0,"updated_count":0,"restored_count":0,"failed_count":0,"message":"done"}`, 100, int64(999), "task-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.UpdateResult(context.Background(), "task-1", &interfaces.DiscoverResult{
			CatalogID: "catalog-1",
			NewCount:  1,
			Message:   "done",
		}, 999))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("check exist by statuses", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectQuery("SELECT COUNT(*) FROM t_discover_task WHERE f_catalog_id = ? AND f_status IN (?,?)").
			WithArgs("catalog-1", interfaces.DiscoverTaskStatusPending, interfaces.DiscoverTaskStatusRunning).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

		got, err := access.CheckExistByStatuses(context.Background(), "catalog-1", []string{interfaces.DiscoverTaskStatusPending, interfaces.DiscoverTaskStatusRunning})

		require.NoError(t, err)
		assert.True(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete returns sql err no rows when nothing affected", func(t *testing.T) {
		access, mock, cleanup := newDiscoverTaskAccessMock(t)
		defer cleanup()

		mock.ExpectExec("DELETE FROM t_discover_task WHERE f_id = ?").
			WithArgs("missing").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := access.Delete(context.Background(), "missing")

		require.ErrorIs(t, err, sql.ErrNoRows)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func sampleDiscoverTask() *interfaces.DiscoverTask {
	return &interfaces.DiscoverTask{
		ID:          "task-1",
		CatalogID:   "catalog-1",
		ScheduleID:  "schedule-1",
		Strategy:    "full_sync",
		TriggerType: interfaces.DiscoverTaskTriggerManual,
		Status:      interfaces.DiscoverTaskStatusPending,
		Progress:    0,
		Message:     "queued",
		StartTime:   0,
		FinishTime:  0,
		Creator:     interfaces.AccountInfo{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
		CreateTime:  1,
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
