package build_task

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestBuildTaskAccessGetByID(t *testing.T) {
	t.Run("returns build task", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleBuildTask()

		rows := sqlmock.NewRows(buildTaskColumns()).AddRow(buildTaskRowValues(task)...)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinBuildTaskColumns() + " FROM t_build_task WHERE f_id = ?")).
			WithArgs(task.ID).
			WillReturnRows(rows)

		got, err := access.GetByID(context.Background(), task.ID)

		require.NoError(t, err)
		assert.Equal(t, task.ID, got.ID)
		assert.Equal(t, task.ResourceID, got.ResourceID)
		assert.Equal(t, task.CatalogID, got.CatalogID)
		assert.Equal(t, task.Status, got.Status)
		assert.Equal(t, task.Mode, got.Mode)
		assert.Equal(t, task.Creator, got.Creator)
		assert.Equal(t, task.IndexConfig, got.IndexConfig)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns nil when not found", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinBuildTaskColumns() + " FROM t_build_task WHERE f_id = ?")).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := access.GetByID(context.Background(), "missing")

		require.NoError(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns query error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinBuildTaskColumns() + " FROM t_build_task WHERE f_id = ?")).
			WithArgs("task-1").
			WillReturnError(errors.New("query failed"))

		got, err := access.GetByID(context.Background(), "task-1")

		require.ErrorContains(t, err, "query failed")
		assert.Nil(t, got)
	})
}

func TestBuildTaskAccessGetByIDs(t *testing.T) {
	t.Run("returns tasks keyed by id", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleBuildTask()
		second := sampleBuildTask()
		second.ID = "task-2"

		mock.ExpectQuery(regexp.QuoteMeta("SELECT "+joinBuildTaskColumns()+" FROM t_build_task WHERE f_id IN (?,?,?)")).
			WithArgs(task.ID, second.ID, "missing").
			WillReturnRows(sqlmock.NewRows(buildTaskColumns()).
				AddRow(buildTaskRowValues(task)...).
				AddRow(buildTaskRowValues(second)...))

		got, err := access.GetByIDs(context.Background(), []string{task.ID, second.ID, "missing"})

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, task.ID, got[task.ID].ID)
		assert.Equal(t, second.ID, got[second.ID].ID)
		assert.NotContains(t, got, "missing")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns query error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinBuildTaskColumns() + " FROM t_build_task WHERE f_id IN (?)")).
			WithArgs("task-1").WillReturnError(errors.New("query failed"))

		got, err := access.GetByIDs(context.Background(), []string{"task-1"})

		require.ErrorContains(t, err, "query failed")
		assert.Nil(t, got)
	})

	t.Run("returns scan error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleBuildTask()
		values := buildTaskRowValues(task)
		values[5] = "{"
		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinBuildTaskColumns() + " FROM t_build_task WHERE f_id IN (?)")).
			WithArgs(task.ID).WillReturnRows(sqlmock.NewRows(buildTaskColumns()).AddRow(values...))

		got, err := access.GetByIDs(context.Background(), []string{task.ID})

		require.Error(t, err)
		assert.Nil(t, got)
	})

	t.Run("returns rows iteration error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleBuildTask()
		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinBuildTaskColumns() + " FROM t_build_task WHERE f_id IN (?)")).
			WithArgs("task-1").WillReturnRows(sqlmock.NewRows(buildTaskColumns()).
			AddRow(buildTaskRowValues(task)...).
			RowError(0, errors.New("rows failed")))

		got, err := access.GetByIDs(context.Background(), []string{"task-1"})

		require.ErrorContains(t, err, "rows failed")
		assert.Nil(t, got)
	})

	t.Run("skips database query for empty ids", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		got, err := access.GetByIDs(context.Background(), nil)

		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessCreate(t *testing.T) {
	t.Run("creates build task", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleBuildTask()

		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_build_task")).
			WithArgs(buildTaskInsertArgs(task)...).
			WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, access.Create(context.Background(), task))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns insert error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleBuildTask()

		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_build_task")).
			WithArgs(buildTaskInsertArgs(task)...).
			WillReturnError(errors.New("insert failed"))

		err := access.Create(context.Background(), task)

		require.Error(t, err)
		assert.ErrorContains(t, err, "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessGetByCatalogID(t *testing.T) {
	t.Run("returns tasks", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleBuildTask()

		rows := sqlmock.NewRows(buildTaskColumns()).
			AddRow(buildTaskRowValues(task)...).
			AddRow(buildTaskRowValues(withBuildTaskID(task, "task-2"))...)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinBuildTaskColumns() + " FROM t_build_task WHERE f_catalog_id = ?")).
			WithArgs(task.CatalogID).
			WillReturnRows(rows)

		got, err := access.GetByCatalogID(context.Background(), task.CatalogID)

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "task-1", got[0].ID)
		assert.Equal(t, "task-2", got[1].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns rows error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleBuildTask()

		rows := sqlmock.NewRows(buildTaskColumns()).
			AddRow(buildTaskRowValues(task)...).
			RowError(0, errors.New("row failed"))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinBuildTaskColumns() + " FROM t_build_task WHERE f_catalog_id = ?")).
			WithArgs(task.CatalogID).
			WillReturnRows(rows)

		got, err := access.GetByCatalogID(context.Background(), task.CatalogID)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "row failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessSetProgress(t *testing.T) {
	t.Run("updates progress for running task", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_last_progress_time = ?, f_synced_count = ? WHERE f_id = ? AND f_status IN (?,?)")).
			WithArgs(int64(123), int64(10), "task-1", interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping).
			WillReturnResult(sqlmock.NewResult(0, 1))

		syncedCount := int64(10)
		progress := interfaces.BuildTaskProgress{SyncedCount: &syncedCount}
		updated, err := access.SetProgress(context.Background(), nil, "task-1", progress, 123)

		require.NoError(t, err)
		assert.True(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("does not update terminal task", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_last_progress_time = ?, f_synced_count = ? WHERE f_id = ? AND f_status IN (?,?)")).
			WithArgs(int64(123), int64(10), "task-1", interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping).
			WillReturnResult(sqlmock.NewResult(0, 0))

		syncedCount := int64(10)
		updated, err := access.SetProgress(context.Background(), nil, "task-1",
			interfaces.BuildTaskProgress{SyncedCount: &syncedCount}, 123)

		require.NoError(t, err)
		assert.False(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns execution error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_last_progress_time = ? WHERE f_id = ? AND f_status IN (?,?)")).
			WithArgs(int64(123), "task-1", interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping).
			WillReturnError(errors.New("update failed"))

		updated, err := access.SetProgress(context.Background(), nil, "task-1", interfaces.BuildTaskProgress{}, 123)

		require.ErrorContains(t, err, "update failed")
		assert.False(t, updated)
	})

	t.Run("uses caller transaction", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectBegin()
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_last_progress_time = ? WHERE f_id = ? AND f_status IN (?,?)")).
			WithArgs(int64(123), "task-1", interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.SetProgress(context.Background(), tx, "task-1", interfaces.BuildTaskProgress{}, 123)

		require.NoError(t, err)
		assert.True(t, updated)
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessMarkRunning(t *testing.T) {
	t.Run("returns true when a row is claimed", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_start_time = ?, f_status = ? WHERE f_id = ? AND f_status = ?")).
			WithArgs("", int64(123), interfaces.BuildTaskStatusRunning, "task-1", interfaces.BuildTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 1))

		claimed, err := access.MarkRunning(context.Background(), nil, "task-1", 123)

		require.NoError(t, err)
		assert.True(t, claimed)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns false when status does not match", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_start_time = ?, f_status = ? WHERE f_id = ? AND f_status = ?")).
			WithArgs("", int64(123), interfaces.BuildTaskStatusRunning, "task-1", interfaces.BuildTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 0))

		claimed, err := access.MarkRunning(context.Background(), nil, "task-1", 123)

		require.NoError(t, err)
		assert.False(t, claimed)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("uses caller transaction", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectBegin()
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_start_time = ?, f_status = ? WHERE f_id = ? AND f_status = ?")).
			WithArgs("", int64(123), interfaces.BuildTaskStatusRunning, "task-1", interfaces.BuildTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 1))

		claimed, err := access.MarkRunning(context.Background(), tx, "task-1", 123)

		require.NoError(t, err)
		assert.True(t, claimed)
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessMarkPending(t *testing.T) {
	t.Run("transitions stopped task without reset", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_finish_time = ?, f_last_progress_time = ?, f_start_time = ?, f_status = ? WHERE f_id = ? AND f_status IN (?,?)")).
			WithArgs(int64(0), int64(0), int64(0), interfaces.BuildTaskStatusPending, "task-1",
				interfaces.BuildTaskStatusStopped, interfaces.BuildTaskStatusFailed).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.MarkPending(context.Background(), nil, "task-1", false)

		require.NoError(t, err)
		assert.True(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("resets progress using caller transaction", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectBegin()
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_failure_detail = ?, f_finish_time = ?, f_last_progress_time = ?, f_start_time = ?, f_status = ?, f_synced_count = ?, f_synced_mark = ?, f_total_count = ? WHERE f_id = ? AND f_status IN (?,?)")).
			WithArgs("", "", int64(0), int64(0), int64(0), interfaces.BuildTaskStatusPending,
				int64(0), "", int64(0), "task-1", interfaces.BuildTaskStatusStopped, interfaces.BuildTaskStatusFailed).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.MarkPending(context.Background(), tx, "task-1", true)

		require.NoError(t, err)
		assert.True(t, updated)
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessMarkFailed(t *testing.T) {
	t.Run("accepts stopping task", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_finish_time = ?, f_status = ? WHERE f_id = ? AND f_status IN (?,?,?)")).
			WithArgs("execution failed", int64(123), interfaces.BuildTaskStatusFailed, "task-1", interfaces.BuildTaskStatusPending, interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.MarkFailed(context.Background(), nil, "task-1", "execution failed", 123)

		require.NoError(t, err)
		assert.True(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("uses caller transaction", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectBegin()
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_finish_time = ?, f_status = ? WHERE f_id = ? AND f_status IN (?,?,?)")).
			WithArgs("execution failed", int64(123), interfaces.BuildTaskStatusFailed, "task-1",
				interfaces.BuildTaskStatusPending, interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.MarkFailed(context.Background(), tx, "task-1", "execution failed", 123)

		require.NoError(t, err)
		assert.True(t, updated)
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessMarkCancelled(t *testing.T) {
	t.Run("accepts active task", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_finish_time = ?, f_status = ? WHERE f_id = ? AND f_status IN (?,?,?)")).
			WithArgs("execution failed", int64(123), interfaces.BuildTaskStatusCancelled, "task-1", interfaces.BuildTaskStatusPending, interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.MarkCancelled(context.Background(), nil, "task-1", "execution failed", 123)

		require.NoError(t, err)
		assert.True(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("uses caller transaction", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectBegin()
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_finish_time = ?, f_status = ? WHERE f_id = ? AND f_status IN (?,?,?)")).
			WithArgs("cancelled", int64(123), interfaces.BuildTaskStatusCancelled, "task-1",
				interfaces.BuildTaskStatusPending, interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.MarkCancelled(context.Background(), tx, "task-1", "cancelled", 123)

		require.NoError(t, err)
		assert.True(t, updated)
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessMarkStopping(t *testing.T) {
	t.Run("uses caller transaction", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectBegin()
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_status = ? WHERE f_id = ? AND f_status = ?")).
			WithArgs(interfaces.BuildTaskStatusStopping, "task-1", interfaces.BuildTaskStatusRunning).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.MarkStopping(context.Background(), tx, "task-1")

		require.NoError(t, err)
		assert.True(t, updated)
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessMarkStopped(t *testing.T) {
	t.Run("uses caller transaction", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectBegin()
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_finish_time = ?, f_status = ? WHERE f_id = ? AND f_status IN (?,?)")).
			WithArgs(int64(123), interfaces.BuildTaskStatusStopped, "task-1", interfaces.BuildTaskStatusPending, interfaces.BuildTaskStatusStopping).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.MarkStopped(context.Background(), tx, "task-1", 123)

		require.NoError(t, err)
		assert.True(t, updated)
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessMarkCompleted(t *testing.T) {
	t.Run("uses caller transaction", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectBegin()
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_finish_time = ?, f_status = ? WHERE f_id = ? AND f_status = ?")).
			WithArgs(int64(123), interfaces.BuildTaskStatusCompleted, "task-1", interfaces.BuildTaskStatusRunning).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.MarkCompleted(context.Background(), tx, "task-1", 123)

		require.NoError(t, err)
		assert.True(t, updated)
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessMarkCancelledByCatalogID(t *testing.T) {
	t.Run("cancels pending tasks", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_finish_time = ?, f_status = ? WHERE f_catalog_id = ? AND f_status = ?")).
			WithArgs("catalog deleted", int64(123), interfaces.BuildTaskStatusCancelled, "catalog-1", interfaces.BuildTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := access.MarkCancelledByCatalogID(context.Background(), nil, "catalog-1", "catalog deleted", 123)

		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("uses caller transaction", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectBegin()
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_finish_time = ?, f_status = ? WHERE f_catalog_id = ? AND f_status = ?")).
			WithArgs("catalog deleted", int64(123), interfaces.BuildTaskStatusCancelled, "catalog-1", interfaces.BuildTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 2))

		err = access.MarkCancelledByCatalogID(context.Background(), tx, "catalog-1", "catalog deleted", 123)

		require.NoError(t, err)
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessGetStatusByID(t *testing.T) {
	t.Run("returns status", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_status FROM t_build_task WHERE f_id = ?")).
			WithArgs("task-1").
			WillReturnRows(sqlmock.NewRows([]string{"f_status"}).AddRow(interfaces.BuildTaskStatusCompleted))

		got, err := access.GetStatusByID(context.Background(), "task-1")

		require.NoError(t, err)
		assert.Equal(t, interfaces.BuildTaskStatusCompleted, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns not found error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_status FROM t_build_task WHERE f_id = ?")).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := access.GetStatusByID(context.Background(), "missing")

		require.Error(t, err)
		assert.Empty(t, got)
		assert.ErrorContains(t, err, "build task not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessList(t *testing.T) {
	t.Run("returns tasks with filters", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleBuildTask()
		params := interfaces.BuildTasksQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{
				Offset:    5,
				Limit:     10,
				Sort:      interfaces.BuildTaskSortCreateTime,
				Direction: interfaces.ASC_DIRECTION,
			},
			ResourceID:  task.ResourceID,
			CatalogID:   task.CatalogID,
			Statuses:    []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusPending},
			Mode:        interfaces.BuildTaskModeBatch,
			ExecuteType: interfaces.BuildTaskExecuteTypeIncremental,
		}

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_build_task WHERE f_status IN (?,?) AND f_mode = ? AND f_execute_type = ? AND f_resource_id = ? AND f_catalog_id = ?")).
			WithArgs(interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusPending, interfaces.BuildTaskModeBatch, interfaces.BuildTaskExecuteTypeIncremental, task.ResourceID, task.CatalogID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(2)))
		rows := sqlmock.NewRows(buildTaskSummaryColumns()).AddRow(buildTaskSummaryRowValues(task)...)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT "+joinBuildTaskSummaryColumns()+" FROM t_build_task WHERE f_status IN (?,?) AND f_mode = ? AND f_execute_type = ? AND f_resource_id = ? AND f_catalog_id = ? ORDER BY f_create_time ASC LIMIT 10 OFFSET 5")).
			WithArgs(interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusPending, interfaces.BuildTaskModeBatch, interfaces.BuildTaskExecuteTypeIncremental, task.ResourceID, task.CatalogID).
			WillReturnRows(rows)

		got, total, err := access.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(2), total)
		require.Len(t, got, 1)
		assert.Equal(t, task.ID, got[0].ID)
		payload, err := sonic.MarshalString(got[0])
		require.NoError(t, err)
		assert.NotContains(t, payload, "failure_detail")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns count error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_build_task")).
			WillReturnError(errors.New("count failed"))

		got, total, err := access.List(context.Background(), interfaces.BuildTasksQueryParams{})

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Zero(t, total)
		assert.ErrorContains(t, err, "count failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns data query error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_build_task")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinBuildTaskSummaryColumns() + " FROM t_build_task ORDER BY f_create_time DESC")).
			WillReturnError(errors.New("list failed"))

		got, total, err := access.List(context.Background(), interfaces.BuildTasksQueryParams{})

		require.ErrorContains(t, err, "list failed")
		assert.Nil(t, got)
		assert.Zero(t, total)
	})
}

func TestBuildTaskAccessInternalList(t *testing.T) {
	t.Run("returns tasks", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleBuildTask()
		rows := sqlmock.NewRows(buildTaskSummaryColumns()).AddRow(buildTaskSummaryRowValues(task)...)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + strings.Join(buildTaskSummaryColumns(), ", ") + " FROM t_build_task ORDER BY f_create_time DESC")).
			WillReturnRows(rows)

		got, err := access.InternalList(context.Background(), interfaces.BuildTasksQueryParams{})

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, task.ID, got[0].ID)
	})
	t.Run("returns query error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + strings.Join(buildTaskSummaryColumns(), ", ") + " FROM t_build_task ORDER BY f_create_time DESC")).
			WillReturnError(errors.New("list failed"))

		got, err := access.InternalList(context.Background(), interfaces.BuildTasksQueryParams{})

		require.ErrorContains(t, err, "list failed")
		assert.Nil(t, got)
	})
}

func TestBuildTaskAccessDeleteByIDs(t *testing.T) {
	t.Run("deletes tasks", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_build_task WHERE f_id IN (?,?)")).
			WithArgs("task-1", "task-2").
			WillReturnResult(sqlmock.NewResult(0, 2))

		deleted, err := access.DeleteByIDs(context.Background(), []string{"task-1", "task-2"})

		require.NoError(t, err)
		assert.Equal(t, int64(2), deleted)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty ids do not query database", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		deleted, err := access.DeleteByIDs(context.Background(), nil)

		require.NoError(t, err)
		assert.Zero(t, deleted)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns rows affected error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_build_task WHERE f_id IN (?)")).
			WithArgs("task-1").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))

		_, err := access.DeleteByIDs(context.Background(), []string{"task-1"})

		require.Error(t, err)
		assert.ErrorContains(t, err, "rows affected failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns execution error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_build_task WHERE f_id IN (?)")).
			WithArgs("task-1").
			WillReturnError(errors.New("delete failed"))

		deleted, err := access.DeleteByIDs(context.Background(), []string{"task-1"})

		require.ErrorContains(t, err, "delete failed")
		assert.Zero(t, deleted)
	})
}

func TestBuildOrderByClause(t *testing.T) {
	t.Run("empty sort defaults to create_time desc", func(t *testing.T) {
		assert.Equal(t, "f_create_time DESC", buildOrderByClause("", "asc"))
	})

	t.Run("unknown sort falls back to create_time desc", func(t *testing.T) {
		assert.Equal(t, "f_create_time DESC", buildOrderByClause("bogus", "asc"))
	})

	t.Run("create_time follows direction", func(t *testing.T) {
		assert.Equal(t, "f_create_time ASC", buildOrderByClause(interfaces.BuildTaskSortCreateTime, "asc"))
		assert.Equal(t, "f_create_time DESC", buildOrderByClause(interfaces.BuildTaskSortCreateTime, "desc"))
	})

	t.Run("start_time follows direction", func(t *testing.T) {
		assert.Equal(t, "f_start_time ASC", buildOrderByClause(interfaces.BuildTaskSortStartTime, "asc"))
		assert.Equal(t, "f_start_time DESC", buildOrderByClause(interfaces.BuildTaskSortStartTime, "desc"))
	})

	t.Run("finish_time follows direction", func(t *testing.T) {
		assert.Equal(t, "f_finish_time ASC", buildOrderByClause(interfaces.BuildTaskSortFinishTime, "asc"))
		assert.Equal(t, "f_finish_time DESC", buildOrderByClause(interfaces.BuildTaskSortFinishTime, "desc"))
	})

	t.Run("last_progress_time follows direction", func(t *testing.T) {
		assert.Equal(t, "f_last_progress_time ASC", buildOrderByClause(interfaces.BuildTaskSortLastProgressTime, "asc"))
		assert.Equal(t, "f_last_progress_time DESC", buildOrderByClause(interfaces.BuildTaskSortLastProgressTime, "desc"))
	})
}

func newBuildTaskAccessMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock, *buildTaskAccess) {
	t.Helper()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
	})
	return db, mock, &buildTaskAccess{db: db}
}

func sampleBuildTask() *interfaces.BuildTask {
	return &interfaces.BuildTask{
		ID:               "task-1",
		ResourceID:       "resource-1",
		CatalogID:        "catalog-1",
		Status:           interfaces.BuildTaskStatusPending,
		Mode:             interfaces.BuildTaskModeBatch,
		ExecuteType:      interfaces.BuildTaskExecuteTypeIncremental,
		TotalCount:       100,
		SyncedCount:      80,
		SyncedMark:       "cursor-1",
		ErrorMsg:         "soft error",
		Creator:          interfaces.AccountInfo{ID: "creator-1", Type: interfaces.ACCESSOR_TYPE_USER},
		CreateTime:       1000,
		StartTime:        1500,
		FinishTime:       2000,
		LastProgressTime: 1800,
		IndexConfig: &interfaces.BuildTaskIndexConfig{
			IndexConfigContract: interfaces.IndexConfigContract{BuildKeyFields: []string{"id"}},
			Features: map[string]interfaces.BuildTaskFieldIndexFeature{
				"title": {
					Vector:   &interfaces.SmallModel{ModelID: "embedding", EmbeddingDim: 1024},
					Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"},
				},
				"body": {
					Vector: &interfaces.SmallModel{ModelID: "embedding-v2", EmbeddingDim: 2048},
				},
			},
		},
		FailureDetail: "partial failed",
	}
}

func withBuildTaskID(task *interfaces.BuildTask, id string) *interfaces.BuildTask {
	cp := *task
	cp.ID = id
	return &cp
}

func buildTaskRowValues(task *interfaces.BuildTask) []driver.Value {
	return []driver.Value{
		task.ID,
		task.ResourceID,
		task.CatalogID,
		task.Mode,
		task.ExecuteType,
		mustMarshalJSON(task.IndexConfig),
		task.Status,
		task.TotalCount,
		task.SyncedCount,
		task.SyncedMark,
		task.ErrorMsg,
		task.FailureDetail,
		task.Creator.ID,
		task.Creator.Type,
		task.CreateTime,
		task.StartTime,
		task.FinishTime,
		task.LastProgressTime,
	}
}

func buildTaskInsertArgs(task *interfaces.BuildTask) []driver.Value {
	args := buildTaskRowValues(task)
	args[5] = sqlmock.AnyArg()
	return args
}

func mustMarshalJSON(v any) string {
	bs, err := sonic.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(bs)
}

func joinBuildTaskColumns() string {
	cols := buildTaskColumns()
	out := cols[0]
	for _, col := range cols[1:] {
		out += ", " + col
	}
	return out
}

func joinBuildTaskSummaryColumns() string {
	cols := buildTaskSummaryColumns()
	out := cols[0]
	for _, col := range cols[1:] {
		out += ", " + col
	}
	return out
}

func buildTaskSummaryRowValues(task *interfaces.BuildTask) []driver.Value {
	values := buildTaskRowValues(task)
	result := append([]driver.Value{}, values[:5]...)
	result = append(result, values[6:11]...)
	return append(result, values[12:]...)
}
