// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package build_schedule

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"strconv"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	sq "github.com/Masterminds/squirrel"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAccessFixture(t *testing.T) (*buildScheduleAccess, sqlmock.Sqlmock) {
	t.Helper()
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, m.ExpectationsWereMet()); _ = db.Close() })
	return &buildScheduleAccess{db: db}, m
}

func TestBuildScheduleAccessClaimRun(t *testing.T) {
	for _, rows := range []int64{0, 1} {
		t.Run(strconv.FormatInt(rows, 10), func(t *testing.T) {
			a, m := newAccessFixture(t)
			query := "UPDATE t_build_schedule SET f_last_run = ?, f_next_run = ?, f_run_id = ?, f_last_status = ?, f_last_message = ? WHERE f_enabled = ? AND f_id = ? AND f_next_run = ? AND f_update_time = ?"
			m.ExpectExec(regexp.QuoteMeta(query)).WithArgs(int64(100), int64(200), "run", interfaces.ScheduleStatusTriggering, "", true, "schedule", int64(50), int64(25)).WillReturnResult(sqlmock.NewResult(0, rows))
			affected, err := a.ClaimRun(context.Background(), "schedule", 25, 50, 100, 200, "run")
			require.NoError(t, err)
			assert.Equal(t, rows, affected)
		})
	}
}

func TestBuildScheduleAccessCompleteRun(t *testing.T) {
	a, m := newAccessFixture(t)
	query := "UPDATE t_build_schedule SET f_last_status = ?, f_last_message = ? WHERE f_id = ? AND f_last_status = ? AND f_run_id = ?"
	m.ExpectExec(regexp.QuoteMeta(query)).WithArgs(interfaces.ScheduleStatusFailed, "safe explanation", "schedule", interfaces.ScheduleStatusTriggering, "old-run").WillReturnResult(sqlmock.NewResult(0, 0))
	affected, err := a.CompleteRun(context.Background(), "schedule", "old-run", interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusFailed, Message: "safe explanation"})
	require.NoError(t, err)
	assert.Zero(t, affected)
	// 不依赖 update_time 或 enabled，配置修改、禁用不会取消已经取得的触发。
}

func TestBuildScheduleAccessInitializeNextRun(t *testing.T) {
	a, m := newAccessFixture(t)
	query := "UPDATE t_build_schedule SET f_next_run = ? WHERE f_enabled = ? AND f_id = ? AND f_next_run = ? AND f_update_time = ?"
	m.ExpectExec(regexp.QuoteMeta(query)).WithArgs(int64(200), true, "schedule", int64(0), int64(25)).WillReturnResult(sqlmock.NewResult(0, 1))
	affected, err := a.InitializeNextRun(context.Background(), "schedule", 25, 0, 200)
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)
}

func TestBuildScheduleAccessDisableInvalid(t *testing.T) {
	a, m := newAccessFixture(t)
	query := "UPDATE t_build_schedule SET f_enabled = ?, f_last_status = ?, f_last_message = ? WHERE f_enabled = ? AND f_id = ? AND f_next_run = ? AND f_update_time = ?"
	m.ExpectExec(regexp.QuoteMeta(query)).WithArgs(false, interfaces.ScheduleStatusFailed, "invalid cron", true, "schedule", int64(50), int64(25)).WillReturnResult(sqlmock.NewResult(0, 0))
	affected, err := a.DisableInvalid(context.Background(), "schedule", 25, 50, "invalid cron")
	require.NoError(t, err)
	assert.Zero(t, affected)
}

func TestBuildScheduleAccessList(t *testing.T) {
	a, m := newAccessFixture(t)
	enabled := true
	p := interfaces.BuildScheduleQueryParams{PaginationQueryParams: interfaces.PaginationQueryParams{Limit: 5, Offset: 10, Sort: interfaces.BuildScheduleSortNextRun, Direction: interfaces.ASC_DIRECTION}, CatalogIDs: []string{"visible"}, ExcludeCatalogIDs: []string{"internal"}, ResourceID: "resource", ExecuteType: interfaces.BuildTaskExecuteTypeFull, Enabled: &enabled}
	condition := " WHERE f_resource_id = ? AND f_catalog_id IN (?) AND f_catalog_id NOT IN (?) AND f_execute_type = ? AND f_enabled = ?"
	args := []driver.Value{"resource", "visible", "internal", interfaces.BuildTaskExecuteTypeFull, true}
	m.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_build_schedule" + condition)).WithArgs(args...).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	query, _, err := sq.Select(columns()...).From(tableName).ToSql()
	require.NoError(t, err)
	m.ExpectQuery(regexp.QuoteMeta(query + condition + " ORDER BY f_next_run ASC, f_id ASC LIMIT 5 OFFSET 10")).WithArgs(args...).WillReturnRows(sqlmock.NewRows(columns()))
	rows, total, err := a.List(context.Background(), p)
	require.NoError(t, err)
	assert.NotNil(t, rows)
	assert.Empty(t, rows)
	assert.Equal(t, int64(2), total)
}

func TestBuildScheduleAccessUpdate(t *testing.T) {
	a, m := newAccessFixture(t)
	query := "UPDATE t_build_schedule SET f_name = ?, f_cron_expr = ?, f_execute_type = ?, f_start_time = ?, f_end_time = ?, f_next_run = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_id = ? AND f_update_time = ?"
	s := &interfaces.BuildSchedule{ID: "schedule", Name: "nightly", CronExpr: "0 * * * *", ExecuteType: "full", NextRun: 200, UpdateTime: 30, LastStatus: interfaces.ScheduleStatusFailed, LastMessage: "previous", Creator: interfaces.AccountInfo{ID: "creator"}, Updater: interfaces.AccountInfo{ID: "updater", Type: "user"}}
	m.ExpectExec(regexp.QuoteMeta(query)).WithArgs("nightly", "0 * * * *", "full", int64(0), int64(0), int64(200), "updater", "user", int64(30), "schedule", int64(25)).WillReturnResult(sqlmock.NewResult(0, 1))
	n, err := a.Update(context.Background(), s, 25)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

func TestBuildScheduleAccessGetByID(t *testing.T) {
	t.Run("reads recent result and internal trigger identity", func(t *testing.T) {
		a, m := newAccessFixture(t)
		row := []driver.Value{"schedule", "nightly", "resource", "catalog", "0 * * * *", "full", int64(0), int64(0), true, int64(100), int64(200), interfaces.ScheduleStatusFailed, "safe explanation", "run", "creator", "user", int64(1), "updater", "user", int64(25)}
		m.ExpectQuery("SELECT .* FROM t_build_schedule WHERE f_id = \\?").WithArgs("schedule").WillReturnRows(sqlmock.NewRows(columns()).AddRow(row...))
		s, err := a.GetByID(context.Background(), "schedule")
		require.NoError(t, err)
		assert.Equal(t, interfaces.ScheduleStatusFailed, s.LastStatus)
		assert.Equal(t, "safe explanation", s.LastMessage)
		assert.Equal(t, "run", s.RunID)
	})
	t.Run("database errors propagate", func(t *testing.T) {
		a, m := newAccessFixture(t)
		outage := errors.New("db unavailable")
		m.ExpectQuery("SELECT .* FROM t_build_schedule WHERE f_id = \\?").WithArgs("schedule").WillReturnError(outage)
		s, err := a.GetByID(context.Background(), "schedule")
		assert.Nil(t, s)
		assert.ErrorIs(t, err, outage)
	})
}

func TestBuildScheduleAccessCreate(t *testing.T) {
	a, m := newAccessFixture(t)
	s := &interfaces.BuildSchedule{ID: "schedule", Name: "nightly", ResourceID: "resource", CatalogID: "catalog", CronExpr: "0 * * * *", ExecuteType: "full", NextRun: 200, LastStatus: interfaces.ScheduleStatusNever, Creator: interfaces.AccountInfo{ID: "creator", Type: "user"}, CreateTime: 100, Updater: interfaces.AccountInfo{ID: "creator", Type: "user"}, UpdateTime: 100}
	m.ExpectExec("INSERT INTO t_build_schedule").WithArgs("schedule", "nightly", "resource", "catalog", "0 * * * *", "full", int64(0), int64(0), false, int64(0), int64(200), interfaces.ScheduleStatusNever, "", "", "creator", "user", int64(100), "creator", "user", int64(100)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, a.Create(context.Background(), s))
}

func TestBuildScheduleAccessDeleteByCatalogID(t *testing.T) {
	a, m := newAccessFixture(t)
	m.ExpectBegin()
	tx, err := a.db.Begin()
	require.NoError(t, err)
	m.ExpectExec(regexp.QuoteMeta("DELETE FROM t_build_schedule WHERE f_catalog_id = ?")).WithArgs("catalog").WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, a.DeleteByCatalogID(context.Background(), tx, "catalog"))
	// Access 不自行提交调用方事务。
	m.ExpectRollback()
	require.NoError(t, tx.Rollback())
}

func TestBuildScheduleAccessDeleteByResourceIDs(t *testing.T) {
	t.Run("skips empty resource ids", func(t *testing.T) {
		a, _ := newAccessFixture(t)
		require.NoError(t, a.DeleteByResourceIDs(context.Background(), nil, nil))
	})
	t.Run("deletes bound schedules without an existing transaction", func(t *testing.T) {
		a, m := newAccessFixture(t)
		m.ExpectExec(regexp.QuoteMeta("DELETE FROM t_build_schedule WHERE f_resource_id IN (?,?)")).
			WithArgs("resource-1", "resource-2").WillReturnResult(sqlmock.NewResult(0, 3))
		require.NoError(t, a.DeleteByResourceIDs(context.Background(), nil, []string{"resource-1", "resource-2"}))
	})
	t.Run("uses caller transaction without committing it", func(t *testing.T) {
		a, m := newAccessFixture(t)
		m.ExpectBegin()
		tx, err := a.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		m.ExpectExec(regexp.QuoteMeta("DELETE FROM t_build_schedule WHERE f_resource_id IN (?)")).
			WithArgs("resource-1").WillReturnResult(sqlmock.NewResult(0, 0))
		require.NoError(t, a.DeleteByResourceIDs(context.Background(), tx, []string{"resource-1"}))
		m.ExpectCommit()
		require.NoError(t, tx.Commit())
	})
	t.Run("propagates deletion error without rolling back caller transaction", func(t *testing.T) {
		a, m := newAccessFixture(t)
		m.ExpectBegin()
		tx, err := a.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		deleteErr := errors.New("delete schedule failed")
		m.ExpectExec(regexp.QuoteMeta("DELETE FROM t_build_schedule WHERE f_resource_id IN (?)")).
			WithArgs("resource-1").WillReturnError(deleteErr)
		require.ErrorIs(t, a.DeleteByResourceIDs(context.Background(), tx, []string{"resource-1"}), deleteErr)
		m.ExpectRollback()
		require.NoError(t, tx.Rollback())
	})
}
