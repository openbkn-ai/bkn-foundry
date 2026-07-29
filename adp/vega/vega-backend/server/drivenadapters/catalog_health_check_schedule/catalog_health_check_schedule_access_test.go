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

func newCatalogHealthCheckScheduleAccessMock(t *testing.T) (*scheduleAccess, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return &scheduleAccess{db: db}, mock, func() { _ = db.Close() }
}

func TestCatalogHealthCheckScheduleAccessCreate(t *testing.T) {
	t.Run("creates schedule", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		schedule := &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1", Mode: interfaces.CatalogHealthCheckScheduleModeInherit}
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_catalog_health_check_schedule (f_catalog_id,f_mode,f_cron_expr,f_last_run,f_next_run,f_creator,f_creator_type,f_create_time,f_updater,f_updater_type,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?)")).
			WithArgs("catalog-1", "inherit", "", int64(0), int64(0), "", "", int64(0), "", "", int64(0)).WillReturnResult(sqlmock.NewResult(1, 1))
		require.NoError(t, access.Create(context.Background(), schedule))
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
}

func TestCatalogHealthCheckScheduleAccessDeleteByCatalogIDs(t *testing.T) {
	t.Run("deletes schedules", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_catalog_health_check_schedule WHERE f_catalog_id IN (?,?)")).
			WithArgs("catalog-1", "catalog-2").WillReturnResult(sqlmock.NewResult(0, 2))

		require.NoError(t, access.DeleteByCatalogIDs(context.Background(), []string{"catalog-1", "catalog-2"}))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("skips empty catalog IDs", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()

		require.NoError(t, access.DeleteByCatalogIDs(context.Background(), nil))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
