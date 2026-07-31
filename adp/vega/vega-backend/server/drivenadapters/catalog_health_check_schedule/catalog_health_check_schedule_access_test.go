// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog_health_check_schedule

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func newCatalogHealthCheckScheduleAccessMock(t *testing.T) (*catalogHealthCheckScheduleAccess, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return &catalogHealthCheckScheduleAccess{db: db}, mock, func() { _ = db.Close() }
}

func TestCatalogHealthCheckScheduleAccessCreate(t *testing.T) {
	t.Run("creates schedule", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		schedule := &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1", Mode: interfaces.CatalogHealthCheckScheduleModeInherit}
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_catalog_health_check_schedule (f_catalog_id,f_mode,f_cron_expr,f_last_run,f_next_run,f_creator,f_creator_type,f_create_time,f_updater,f_updater_type,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?)")).
			WithArgs("catalog-1", "inherit", "", int64(0), int64(0), "", "", int64(0), "", "", int64(0)).WillReturnResult(sqlmock.NewResult(1, 1))
		require.NoError(t, access.Create(context.Background(), nil, schedule))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates insert error", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		insertErr := sql.ErrConnDone
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_catalog_health_check_schedule (f_catalog_id,f_mode,f_cron_expr,f_last_run,f_next_run,f_creator,f_creator_type,f_create_time,f_updater,f_updater_type,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?)")).
			WithArgs("catalog-1", "inherit", "", int64(0), int64(0), "", "", int64(0), "", "", int64(0)).WillReturnError(insertErr)

		err := access.Create(context.Background(), nil, &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1", Mode: interfaces.CatalogHealthCheckScheduleModeInherit})

		require.ErrorIs(t, err, insertErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("creates schedule with transaction", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		mock.ExpectBegin()
		tx, err := access.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)

		mock.ExpectExec("INSERT INTO t_catalog_health_check_schedule").WillReturnResult(sqlmock.NewResult(1, 1))
		require.NoError(t, access.Create(context.Background(), tx,
			&interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1", Mode: interfaces.CatalogHealthCheckScheduleModeInherit}))
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogHealthCheckScheduleAccessGetByCatalogID(t *testing.T) {
	t.Run("returns schedule", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		columns := scheduleColumns()
		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_catalog_id, f_mode, f_cron_expr, f_last_run, f_next_run, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_catalog_health_check_schedule WHERE f_catalog_id = ?")).WithArgs("catalog-1").WillReturnRows(sqlmock.NewRows(columns).AddRow("catalog-1", "enabled", "*/5 * * * *", 1, 2, "creator", "user", 3, "updater", "user", 4))
		schedule, err := access.GetByCatalogID(context.Background(), "catalog-1")
		require.NoError(t, err)
		assert.Equal(t, "enabled", schedule.Mode)
		assert.Equal(t, "*/5 * * * *", schedule.CronExpr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates not found", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_catalog_id, f_mode, f_cron_expr, f_last_run, f_next_run, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_catalog_health_check_schedule WHERE f_catalog_id = ?")).WithArgs("missing").WillReturnError(sql.ErrNoRows)
		_, err := access.GetByCatalogID(context.Background(), "missing")
		require.ErrorIs(t, err, sql.ErrNoRows)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogHealthCheckScheduleAccessListDue(t *testing.T) {
	t.Run("returns due schedules for enabled physical catalogs", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT s.f_catalog_id, s.f_mode, s.f_cron_expr, s.f_last_run, s.f_next_run, s.f_creator, s.f_creator_type, s.f_create_time, s.f_updater, s.f_updater_type, s.f_update_time FROM t_catalog_health_check_schedule s JOIN t_catalog c ON c.f_id = s.f_catalog_id WHERE s.f_mode IN (?,?) AND s.f_next_run <= ? AND c.f_enabled = ? AND c.f_type = ? ORDER BY s.f_next_run ASC")).
			WithArgs("inherit", "enabled", int64(100), true, "physical").
			WillReturnRows(sqlmock.NewRows(scheduleColumns()).AddRow("catalog-1", "inherit", "", 0, 0, "", "", 0, "", "", 0))

		schedules, err := access.ListDue(context.Background(), 100)

		require.NoError(t, err)
		require.Len(t, schedules, 1)
		assert.Equal(t, "catalog-1", schedules[0].CatalogID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates query error", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()

		queryErr := sql.ErrConnDone
		mock.ExpectQuery("SELECT s.f_catalog_id").WillReturnError(queryErr)

		_, err := access.ListDue(context.Background(), 100)

		require.ErrorIs(t, err, queryErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogHealthCheckScheduleAccessUpdateInheritedNextRun(t *testing.T) {
	t.Run("reschedules only inherit schedules that are not due", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_next_run = ? WHERE f_mode = ? AND f_next_run > ?")).
			WithArgs(int64(200), "inherit", int64(100)).
			WillReturnResult(sqlmock.NewResult(0, 2))

		require.NoError(t, access.UpdateInheritedNextRun(context.Background(), 100, 200))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates update error", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()

		updateErr := sql.ErrConnDone
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_next_run = ? WHERE f_mode = ? AND f_next_run > ?")).
			WithArgs(int64(200), "inherit", int64(100)).
			WillReturnError(updateErr)

		err := access.UpdateInheritedNextRun(context.Background(), 100, 200)

		require.ErrorIs(t, err, updateErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogHealthCheckScheduleAccessUpdate(t *testing.T) {
	t.Run("updates configuration without modifying last run", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		schedule := &interfaces.CatalogHealthCheckSchedule{
			CatalogID:  "catalog-1",
			Mode:       interfaces.CatalogHealthCheckScheduleModeEnabled,
			CronExpr:   "*/10 * * * *",
			LastRun:    100,
			NextRun:    200,
			Updater:    interfaces.AccountInfo{ID: "user-1", Type: "user"},
			UpdateTime: 300,
		}
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_mode = ?, f_cron_expr = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_catalog_id = ?")).
			WithArgs("enabled", "*/10 * * * *", int64(200), "user-1", "user", int64(300), "catalog-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.Update(context.Background(), schedule))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates update error", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		updateErr := sql.ErrConnDone
		schedule := &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1", Mode: interfaces.CatalogHealthCheckScheduleModeInherit}
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_mode = ?, f_cron_expr = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_catalog_id = ?")).
			WithArgs("inherit", "", int64(0), "", "", int64(0), "catalog-1").WillReturnError(updateErr)

		err := access.Update(context.Background(), schedule)

		require.ErrorIs(t, err, updateErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns not found when no schedule is updated", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		schedule := &interfaces.CatalogHealthCheckSchedule{
			CatalogID: "missing",
			Mode:      interfaces.CatalogHealthCheckScheduleModeInherit,
		}
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_mode = ?, f_cron_expr = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_catalog_id = ?")).
			WithArgs("inherit", "", int64(0), "", "", int64(0), "missing").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := access.Update(context.Background(), schedule)

		require.ErrorIs(t, err, sql.ErrNoRows)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates affected rows error", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		affectedRowsErr := sql.ErrConnDone
		schedule := &interfaces.CatalogHealthCheckSchedule{
			CatalogID: "catalog-1",
			Mode:      interfaces.CatalogHealthCheckScheduleModeInherit,
		}
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_mode = ?, f_cron_expr = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_catalog_id = ?")).
			WithArgs("inherit", "", int64(0), "", "", int64(0), "catalog-1").
			WillReturnResult(sqlmock.NewErrorResult(affectedRowsErr))

		err := access.Update(context.Background(), schedule)

		require.ErrorIs(t, err, affectedRowsErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogHealthCheckScheduleAccessUpdateRunMetadata(t *testing.T) {
	t.Run("always updates last run and advances next run only when schedule update time matches", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_last_run = ?, f_next_run = CASE WHEN f_update_time = ? THEN ? ELSE f_next_run END WHERE f_catalog_id = ?")).
			WithArgs(int64(100), int64(50), int64(200), "catalog-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.UpdateRunMetadata(context.Background(), "catalog-1", 50, 100, 200))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates update error", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()

		updateErr := sql.ErrConnDone
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_last_run = ?, f_next_run = CASE WHEN f_update_time = ? THEN ? ELSE f_next_run END WHERE f_catalog_id = ?")).
			WithArgs(int64(100), int64(50), int64(200), "catalog-1").
			WillReturnError(updateErr)

		err := access.UpdateRunMetadata(context.Background(), "catalog-1", 50, 100, 200)

		require.ErrorIs(t, err, updateErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogHealthCheckScheduleAccessDeleteByCatalogIDs(t *testing.T) {
	t.Run("deletes schedules", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_catalog_health_check_schedule WHERE f_catalog_id IN (?,?)")).
			WithArgs("catalog-1", "catalog-2").WillReturnResult(sqlmock.NewResult(0, 2))

		require.NoError(t, access.DeleteByCatalogIDs(context.Background(), nil, []string{"catalog-1", "catalog-2"}))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("skips empty catalog IDs", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()

		require.NoError(t, access.DeleteByCatalogIDs(context.Background(), nil, nil))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates delete error", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		deleteErr := sql.ErrConnDone
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_catalog_health_check_schedule WHERE f_catalog_id IN (?)")).
			WithArgs("catalog-1").WillReturnError(deleteErr)

		err := access.DeleteByCatalogIDs(context.Background(), nil, []string{"catalog-1"})

		require.ErrorIs(t, err, deleteErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
