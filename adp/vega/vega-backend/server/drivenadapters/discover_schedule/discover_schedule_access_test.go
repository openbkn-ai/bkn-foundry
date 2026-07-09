// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package discover_schedule

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestCalculateNextRun(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	next, err := calculateNextRun("0 13 * * *", base)

	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 7, 9, 13, 0, 0, 0, time.UTC), next)

	_, err = calculateNextRun("bad cron", base)
	require.Error(t, err)
}

func TestDiscoverScheduleAccessGetAndList(t *testing.T) {
	t.Run("get by id", func(t *testing.T) {
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

	t.Run("list with filters", func(t *testing.T) {
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
}

func TestDiscoverScheduleAccessExecs(t *testing.T) {
	t.Run("disable", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectExec("UPDATE t_discover_schedule SET f_enabled = ? WHERE f_id = ?").
			WithArgs(0, "schedule-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.Disable(context.Background(), "schedule-1"))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update last run gets schedule and updates next run", func(t *testing.T) {
		access, mock, cleanup := newDiscoverScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectQuery("SELECT f_id, f_name, f_catalog_id, f_cron_expr, f_start_time, f_end_time, f_enabled, f_strategy, f_last_run, f_next_run, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_discover_schedule WHERE f_id = ?").
			WithArgs("schedule-1").
			WillReturnRows(discoverScheduleRows().AddRow("schedule-1", "Nightly", "catalog-1", "0 13 * * *", int64(0), int64(0), true, "full_sync", int64(0), int64(0), "u1", interfaces.ACCESSOR_TYPE_USER, int64(1), "u2", interfaces.ACCESSOR_TYPE_USER, int64(2)))
		lastRun := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC).UnixMilli()
		mock.ExpectExec("UPDATE t_discover_schedule SET f_last_run = ?, f_next_run = ? WHERE f_id = ?").
			WithArgs(lastRun, sqlmock.AnyArg(), "schedule-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.UpdateLastRun(context.Background(), "schedule-1", lastRun))
		require.NoError(t, mock.ExpectationsWereMet())
	})
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
