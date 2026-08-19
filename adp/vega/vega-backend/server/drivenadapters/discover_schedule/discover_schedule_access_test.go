// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package discover_schedule

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestDiscoverScheduleAccessGetByID(t *testing.T) {
	t.Run("returns schedule", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectQuery("SELECT f_id, f_name, f_catalog_id, f_cron_expr, f_start_time, f_end_time, f_enabled, f_strategy, f_last_run, f_next_run, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_discover_schedule WHERE f_id = ?").
			WithArgs("schedule-1").
			WillReturnRows(discoverScheduleRows().AddRow("schedule-1", "Nightly", "catalog-1", "0 0 * * *", int64(0), int64(0), true, "full_sync", int64(10), int64(20), "u1", interfaces.ACCESSOR_TYPE_USER, int64(1), "u2", interfaces.ACCESSOR_TYPE_USER, int64(2)))

		got, err := access.GetByID(context.Background(), "schedule-1")

		require.NoError(t, err)
		assert.Equal(t, "schedule-1", got.ID)
		assert.True(t, got.Enabled)
		assert.Equal(t, "u2", got.Updater.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns nil when not found", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectQuery("SELECT f_id, f_name, f_catalog_id, f_cron_expr, f_start_time, f_end_time, f_enabled, f_strategy, f_last_run, f_next_run, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_discover_schedule WHERE f_id = ?").
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := access.GetByID(context.Background(), "missing")

		require.NoError(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverScheduleAccessList(t *testing.T) {
	t.Run("returns schedules with filters", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		enabled := true
		params := interfaces.DiscoverScheduleQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 0, Limit: 10},
			Name:                  "Night",
			CatalogID:             "catalog-1",
			Enabled:               &enabled,
		}

		mock.ExpectQuery("SELECT COUNT(*) FROM t_discover_schedule WHERE f_name LIKE ? AND f_catalog_id = ? AND f_enabled = ?").
			WithArgs("%Night%", "catalog-1", true).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("SELECT f_id, f_name, f_catalog_id, f_cron_expr, f_start_time, f_end_time, f_enabled, f_strategy, f_last_run, f_next_run, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_discover_schedule WHERE f_name LIKE ? AND f_catalog_id = ? AND f_enabled = ? ORDER BY f_update_time DESC LIMIT 10 OFFSET 0").
			WithArgs("%Night%", "catalog-1", true).
			WillReturnRows(discoverScheduleRows().AddRow("schedule-1", "Nightly", "catalog-1", "0 0 * * *", int64(0), int64(0), true, "full_sync", int64(10), int64(20), "u1", interfaces.ACCESSOR_TYPE_USER, int64(1), "u2", interfaces.ACCESSOR_TYPE_USER, int64(2)))

		got, total, err := access.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, got, 1)
		assert.Equal(t, "Nightly", got[0].Name)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns count error", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectQuery("SELECT COUNT(*) FROM t_discover_schedule").
			WillReturnError(sql.ErrConnDone)

		got, total, err := access.List(context.Background(), interfaces.DiscoverScheduleQueryParams{})

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Zero(t, total)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverScheduleAccessCreate(t *testing.T) {
	t.Run("persists next run", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()
		schedule := sampleDiscoverSchedule()
		schedule.NextRun = 123

		mock.ExpectExec("INSERT INTO t_discover_schedule (f_id,f_name,f_catalog_id,f_cron_expr,f_start_time,f_end_time,f_enabled,f_strategy,f_last_run,f_next_run,f_creator,f_creator_type,f_create_time,f_updater,f_updater_type,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)").
			WithArgs(schedule.ID, schedule.Name, schedule.CatalogID, schedule.CronExpr, schedule.StartTime, schedule.EndTime, schedule.Enabled, schedule.Strategy, schedule.LastRun, schedule.NextRun, schedule.Creator.ID, schedule.Creator.Type, schedule.CreateTime, schedule.Updater.ID, schedule.Updater.Type, schedule.UpdateTime).
			WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, access.Create(context.Background(), schedule))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverScheduleAccessUpdateEnabled(t *testing.T) {
	t.Run("disables schedule without changing next run", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		updater := interfaces.AccountInfo{ID: "u2", Type: interfaces.ACCESSOR_TYPE_USER}
		mock.ExpectExec("UPDATE t_discover_schedule SET f_enabled = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_id = ? AND f_update_time = ?").
			WithArgs(false, updater.ID, updater.Type, int64(456), "schedule-1", int64(123)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		nextRun := int64(123)
		rowsAffected, err := access.UpdateEnabled(context.Background(), "schedule-1", false, &nextRun, 123, 456, updater)
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("sets caller supplied next run", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		updater := interfaces.AccountInfo{ID: "u2", Type: interfaces.ACCESSOR_TYPE_USER}
		mock.ExpectExec("UPDATE t_discover_schedule SET f_enabled = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_id = ? AND f_update_time = ?").
			WithArgs(true, int64(123), updater.ID, updater.Type, int64(456), "schedule-1", int64(234)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		nextRun := int64(123)
		rowsAffected, err := access.UpdateEnabled(context.Background(), "schedule-1", true, &nextRun, 234, 456, updater)
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("returns zero affected rows for a stale schedule", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		updater := interfaces.AccountInfo{ID: "u2", Type: interfaces.ACCESSOR_TYPE_USER}
		mock.ExpectExec("UPDATE t_discover_schedule SET f_enabled = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_id = ? AND f_update_time = ?").
			WithArgs(false, updater.ID, updater.Type, int64(456), "schedule-1", int64(123)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		rowsAffected, err := access.UpdateEnabled(context.Background(), "schedule-1", false, nil, 123, 456, updater)
		require.NoError(t, err)
		assert.Zero(t, rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverScheduleAccessUpdate(t *testing.T) {
	t.Run("updates schedule", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()
		schedule := sampleDiscoverSchedule()
		schedule.Name = "Updated"
		schedule.NextRun = 123
		schedule.Updater = interfaces.AccountInfo{ID: "u2", Type: interfaces.ACCESSOR_TYPE_USER}
		schedule.UpdateTime = 9

		mock.ExpectExec("UPDATE t_discover_schedule SET f_name = ?, f_cron_expr = ?, f_start_time = ?, f_end_time = ?, f_strategy = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_id = ? AND f_update_time = ?").
			WithArgs(schedule.Name, schedule.CronExpr, schedule.StartTime, schedule.EndTime, schedule.Strategy, schedule.NextRun, schedule.Updater.ID, schedule.Updater.Type, schedule.UpdateTime, schedule.ID, schedule.UpdateTime).
			WillReturnResult(sqlmock.NewResult(0, 1))

		rowsAffected, err := access.Update(context.Background(), schedule, schedule.UpdateTime)
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns zero affected rows for a stale schedule", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()
		schedule := sampleDiscoverSchedule()
		expectedUpdateTime := int64(1)

		mock.ExpectExec("UPDATE t_discover_schedule SET f_name = ?, f_cron_expr = ?, f_start_time = ?, f_end_time = ?, f_strategy = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_id = ? AND f_update_time = ?").
			WithArgs(schedule.Name, schedule.CronExpr, schedule.StartTime, schedule.EndTime, schedule.Strategy, schedule.NextRun, schedule.Updater.ID, schedule.Updater.Type, schedule.UpdateTime, schedule.ID, expectedUpdateTime).
			WillReturnResult(sqlmock.NewResult(0, 0))

		rowsAffected, err := access.Update(context.Background(), schedule, expectedUpdateTime)

		require.NoError(t, err)
		assert.Zero(t, rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverScheduleAccessDelete(t *testing.T) {
	t.Run("deletes schedule", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectExec("DELETE FROM t_discover_schedule WHERE f_id = ?").
			WithArgs("schedule-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.Delete(context.Background(), "schedule-1"))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDiscoverScheduleAccessDeleteByCatalogID(t *testing.T) {
	access, mock, cleanup := newDiscoverScheduleAccessMock(t)
	defer cleanup()

	mock.ExpectExec("DELETE FROM t_discover_schedule WHERE f_catalog_id = ?").
		WithArgs("catalog-1").
		WillReturnResult(sqlmock.NewResult(0, 2))

	require.NoError(t, access.DeleteByCatalogID(context.Background(), nil, "catalog-1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDiscoverScheduleAccessListDue(t *testing.T) {
	access, mock, cleanup := newDiscoverScheduleAccessMock(t)
	defer cleanup()

	mock.ExpectQuery("SELECT f_id, f_name, f_catalog_id, f_cron_expr, f_start_time, f_end_time, f_enabled, f_strategy, f_last_run, f_next_run, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_discover_schedule WHERE f_enabled = ? AND f_next_run <= ? ORDER BY f_next_run ASC").
		WithArgs(true, int64(100)).
		WillReturnRows(discoverScheduleRows().
			AddRow("schedule-1", "Nightly", "catalog-1", "0 * * * *", int64(0), int64(0), true, "full_sync", int64(10), int64(20), "u1", interfaces.ACCESSOR_TYPE_USER, int64(1), "u2", interfaces.ACCESSOR_TYPE_USER, int64(2)))

	got, err := access.ListDue(context.Background(), 100)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "schedule-1", got[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDiscoverScheduleAccessUpdateRunMetadata(t *testing.T) {
	t.Run("updates metadata when schedule update time matches", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectExec("UPDATE t_discover_schedule SET f_last_run = ?, f_next_run = ? WHERE f_id = ? AND f_update_time = ? AND f_next_run = ? AND f_enabled = ?").
			WithArgs(int64(100), int64(200), "schedule-1", int64(50), int64(75), true).
			WillReturnResult(sqlmock.NewResult(0, 1))

		rowsAffected, err := access.UpdateRunMetadata(context.Background(), "schedule-1", 50, 75, 100, 200)
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns zero rows when schedule changed, was disabled, or was deleted", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectExec("UPDATE t_discover_schedule SET f_last_run = ?, f_next_run = ? WHERE f_id = ? AND f_update_time = ? AND f_next_run = ? AND f_enabled = ?").
			WithArgs(int64(100), int64(200), "schedule-1", int64(50), int64(75), true).
			WillReturnResult(sqlmock.NewResult(0, 0))

		rowsAffected, err := access.UpdateRunMetadata(context.Background(), "schedule-1", 50, 75, 100, 200)

		require.NoError(t, err)
		assert.Zero(t, rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func sampleDiscoverSchedule() *interfaces.DiscoverSchedule {
	return &interfaces.DiscoverSchedule{
		ID:         "schedule-1",
		Name:       "Nightly",
		CatalogID:  "catalog-1",
		CronExpr:   "0 13 * * *",
		StartTime:  0,
		EndTime:    0,
		Enabled:    true,
		Strategy:   "full_sync",
		LastRun:    0,
		NextRun:    0,
		Creator:    interfaces.AccountInfo{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
		CreateTime: 1,
	}
}

func newDiscoverScheduleAccessMock(t *testing.T) (*discoverScheduleAccess, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)

	return &discoverScheduleAccess{db: db}, mock, func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}
}

func discoverScheduleRows() *sqlmock.Rows {
	return sqlmock.NewRows(discoverScheduleColumns())
}
