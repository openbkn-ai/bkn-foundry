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

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
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
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_catalog_health_check_schedule (f_catalog_id,f_mode,f_cron_expr,f_last_run,f_next_run,f_last_status,f_last_message,f_run_id,f_creator,f_creator_type,f_create_time,f_updater,f_updater_type,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)")).
			WithArgs("catalog-1", "inherit", "", int64(0), int64(0), "", "", "", "", "", int64(0), "", "", int64(0)).WillReturnResult(sqlmock.NewResult(1, 1))
		require.NoError(t, access.Create(context.Background(), nil, schedule))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates insert error", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		insertErr := sql.ErrConnDone
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_catalog_health_check_schedule (f_catalog_id,f_mode,f_cron_expr,f_last_run,f_next_run,f_last_status,f_last_message,f_run_id,f_creator,f_creator_type,f_create_time,f_updater,f_updater_type,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)")).
			WithArgs("catalog-1", "inherit", "", int64(0), int64(0), "", "", "", "", "", int64(0), "", "", int64(0)).WillReturnError(insertErr)

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
		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_catalog_id, f_mode, f_cron_expr, f_last_run, f_next_run, f_last_status, f_last_message, f_run_id, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_catalog_health_check_schedule WHERE f_catalog_id = ?")).WithArgs("catalog-1").WillReturnRows(sqlmock.NewRows(columns).AddRow("catalog-1", "enabled", "*/5 * * * *", 1, 2, interfaces.ScheduleStatusSuccess, "", "run-previous", "creator", "user", 3, "updater", "user", 4))
		schedule, err := access.GetByCatalogID(context.Background(), "catalog-1")
		require.NoError(t, err)
		assert.Equal(t, "enabled", schedule.Mode)
		assert.Equal(t, interfaces.ScheduleStatusSuccess, schedule.LastStatus)
		assert.Empty(t, schedule.LastMessage)
		assert.Equal(t, "run-previous", schedule.RunID)
		assert.Equal(t, "*/5 * * * *", schedule.CronExpr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates not found", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_catalog_id, f_mode, f_cron_expr, f_last_run, f_next_run, f_last_status, f_last_message, f_run_id, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_catalog_health_check_schedule WHERE f_catalog_id = ?")).WithArgs("missing").WillReturnError(sql.ErrNoRows)
		_, err := access.GetByCatalogID(context.Background(), "missing")
		require.ErrorIs(t, err, sql.ErrNoRows)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogHealthCheckScheduleAccessListDue(t *testing.T) {
	t.Run("returns due schedules for enabled physical catalogs", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT s.f_catalog_id, s.f_mode, s.f_cron_expr, s.f_last_run, s.f_next_run, s.f_last_status, s.f_last_message, s.f_run_id, s.f_creator, s.f_creator_type, s.f_create_time, s.f_updater, s.f_updater_type, s.f_update_time FROM t_catalog_health_check_schedule s JOIN t_catalog c ON c.f_id = s.f_catalog_id WHERE s.f_mode IN (?,?) AND s.f_next_run <= ? AND c.f_enabled = ? AND c.f_type = ? ORDER BY s.f_next_run ASC, s.f_catalog_id ASC")).
			WithArgs("inherit", "enabled", int64(100), true, "physical").
			WillReturnRows(sqlmock.NewRows(scheduleColumns()).AddRow("catalog-1", "inherit", "", 0, 0, interfaces.ScheduleStatusSuccess, "", "run-previous", "", "", 0, "", "", 0))

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
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_mode = ?, f_cron_expr = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_catalog_id = ? AND f_update_time = ?")).
			WithArgs("enabled", "*/10 * * * *", int64(200), "user-1", "user", int64(300), "catalog-1", int64(300)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		rowsAffected, err := access.Update(context.Background(), schedule, schedule.UpdateTime)
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates update error", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		updateErr := sql.ErrConnDone
		schedule := &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1", Mode: interfaces.CatalogHealthCheckScheduleModeInherit}
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_mode = ?, f_cron_expr = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_catalog_id = ? AND f_update_time = ?")).
			WithArgs("inherit", "", int64(0), "", "", int64(0), "catalog-1", int64(0)).WillReturnError(updateErr)

		_, err := access.Update(context.Background(), schedule, schedule.UpdateTime)

		require.ErrorIs(t, err, updateErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns zero affected rows when no schedule is updated", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		schedule := &interfaces.CatalogHealthCheckSchedule{
			CatalogID: "missing",
			Mode:      interfaces.CatalogHealthCheckScheduleModeInherit,
		}
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_mode = ?, f_cron_expr = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_catalog_id = ? AND f_update_time = ?")).
			WithArgs("inherit", "", int64(0), "", "", int64(0), "missing", int64(0)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		rowsAffected, err := access.Update(context.Background(), schedule, schedule.UpdateTime)

		require.NoError(t, err)
		assert.Zero(t, rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns zero affected rows for a stale schedule", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		schedule := &interfaces.CatalogHealthCheckSchedule{
			CatalogID:  "catalog-1",
			Mode:       interfaces.CatalogHealthCheckScheduleModeInherit,
			UpdateTime: 300,
		}
		expectedUpdateTime := int64(299)
		mock.ExpectExec("UPDATE t_catalog_health_check_schedule SET .* WHERE f_catalog_id = \\? AND f_update_time = \\?").
			WillReturnResult(sqlmock.NewResult(0, 0))

		rowsAffected, err := access.Update(context.Background(), schedule, expectedUpdateTime)

		require.NoError(t, err)
		assert.Zero(t, rowsAffected)
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
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_mode = ?, f_cron_expr = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_catalog_id = ? AND f_update_time = ?")).
			WithArgs("inherit", "", int64(0), "", "", int64(0), "catalog-1", int64(0)).
			WillReturnResult(sqlmock.NewErrorResult(affectedRowsErr))

		_, err := access.Update(context.Background(), schedule, schedule.UpdateTime)

		require.ErrorIs(t, err, affectedRowsErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogHealthCheckScheduleAccessDeleteByCatalogID(t *testing.T) {
	t.Run("deletes schedule", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_catalog_health_check_schedule WHERE f_catalog_id = ?")).
			WithArgs("catalog-1").WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.DeleteByCatalogID(context.Background(), nil, "catalog-1"))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates delete error", func(t *testing.T) {
		access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
		defer cleanup()
		deleteErr := sql.ErrConnDone
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_catalog_health_check_schedule WHERE f_catalog_id = ?")).
			WithArgs("catalog-1").WillReturnError(deleteErr)

		err := access.DeleteByCatalogID(context.Background(), nil, "catalog-1")

		require.ErrorIs(t, err, deleteErr)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogHealthCheckScheduleAccessInitializeNextRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		affected int64
		dbErr    error
	}{
		{name: "updates matching occurrence", affected: 1},
		{name: "stale or deleted occurrence does not update", affected: 0},
		{name: "database failure propagates", dbErr: sql.ErrConnDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
			defer cleanup()
			expected := mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_next_run = ? WHERE f_catalog_id = ? AND f_next_run = ? AND f_update_time = ? AND f_mode IN (?,?)")).WithArgs(int64(200), "schedule", int64(0), int64(50), "inherit", "enabled")
			if tc.dbErr != nil {
				expected.WillReturnError(tc.dbErr)
			} else {
				expected.WillReturnResult(sqlmock.NewResult(0, tc.affected))
			}
			affected, err := access.InitializeNextRun(context.Background(), "schedule", 50, 0, 200)
			if tc.dbErr != nil {
				require.ErrorIs(t, err, tc.dbErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.affected, affected)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCatalogHealthCheckScheduleAccessClaimRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		affected int64
		dbErr    error
	}{
		{name: "updates matching occurrence", affected: 1},
		{name: "stale or deleted occurrence does not update", affected: 0},
		{name: "database failure propagates", dbErr: sql.ErrConnDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
			defer cleanup()
			expected := mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_last_run = ?, f_next_run = ?, f_last_status = ?, f_last_message = ?, f_run_id = ? WHERE f_catalog_id = ? AND f_next_run = ? AND f_update_time = ? AND f_mode IN (?,?)")).WithArgs(int64(100), int64(200), "triggering", "", "run-new", "schedule", int64(75), int64(50), "inherit", "enabled")
			if tc.dbErr != nil {
				expected.WillReturnError(tc.dbErr)
			} else {
				expected.WillReturnResult(sqlmock.NewResult(0, tc.affected))
			}
			affected, err := access.ClaimRun(context.Background(), "schedule", 50, 75, 100, 200, "run-new")
			if tc.dbErr != nil {
				require.ErrorIs(t, err, tc.dbErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.affected, affected)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCatalogHealthCheckScheduleAccessCompleteRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		affected int64
		dbErr    error
	}{
		{name: "updates matching occurrence", affected: 1},
		{name: "stale or deleted occurrence does not update", affected: 0},
		{name: "database failure propagates", dbErr: sql.ErrConnDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
			defer cleanup()
			expected := mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_last_status = ?, f_last_message = ? WHERE f_catalog_id = ? AND f_last_status = ? AND f_run_id = ?")).WithArgs("failed", "safe explanation", "schedule", "triggering", "run-old")
			if tc.dbErr != nil {
				expected.WillReturnError(tc.dbErr)
			} else {
				expected.WillReturnResult(sqlmock.NewResult(0, tc.affected))
			}
			affected, err := access.CompleteRun(context.Background(), "schedule", "run-old", interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusFailed, Message: "safe explanation"})
			if tc.dbErr != nil {
				require.ErrorIs(t, err, tc.dbErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.affected, affected)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCatalogHealthCheckScheduleAccessDisableInvalid(t *testing.T) {
	for _, tc := range []struct {
		name     string
		affected int64
		dbErr    error
	}{
		{name: "updates matching occurrence", affected: 1},
		{name: "stale or deleted occurrence does not update", affected: 0},
		{name: "database failure propagates", dbErr: sql.ErrConnDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access, mock, cleanup := newCatalogHealthCheckScheduleAccessMock(t)
			defer cleanup()
			expected := mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog_health_check_schedule SET f_mode = ?, f_last_status = ?, f_last_message = ?, f_run_id = ? WHERE f_catalog_id = ? AND f_next_run = ? AND f_update_time = ? AND f_mode IN (?,?)")).WithArgs("disabled", "failed", "invalid cron", "", "schedule", int64(75), int64(50), "inherit", "enabled")
			if tc.dbErr != nil {
				expected.WillReturnError(tc.dbErr)
			} else {
				expected.WillReturnResult(sqlmock.NewResult(0, tc.affected))
			}
			affected, err := access.DisableInvalid(context.Background(), "schedule", 50, 75, "invalid cron")
			if tc.dbErr != nil {
				require.ErrorIs(t, err, tc.dbErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.affected, affected)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
