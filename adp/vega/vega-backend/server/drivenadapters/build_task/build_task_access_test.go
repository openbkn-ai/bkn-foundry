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
}

func TestBuildTaskAccessMarkRunning(t *testing.T) {
	t.Run("returns true when a row is claimed", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_start_time = ?, f_status = ? WHERE f_id = ? AND f_status = ?")).
			WithArgs("", int64(123), interfaces.BuildTaskStatusRunning, "task-1", interfaces.BuildTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 1))

		claimed, err := access.MarkRunning(context.Background(), "task-1", 123)

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

		claimed, err := access.MarkRunning(context.Background(), "task-1", 123)

		require.NoError(t, err)
		assert.False(t, claimed)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessMarkPending(t *testing.T) {
	db, mock, access := newBuildTaskAccessMock(t)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_finish_time = ?, f_last_progress_time = ?, f_start_time = ?, f_status = ? WHERE f_id = ? AND f_status IN (?,?)")).
		WithArgs(int64(0), int64(0), int64(0), interfaces.BuildTaskStatusPending, "task-1",
			interfaces.BuildTaskStatusStopped, interfaces.BuildTaskStatusFailed).
		WillReturnResult(sqlmock.NewResult(0, 1))

	updated, err := access.MarkPending(context.Background(), "task-1", false)

	require.NoError(t, err)
	assert.True(t, updated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildTaskAccessMarkTerminal(t *testing.T) {
	tests := []struct {
		name   string
		status string
		mark   func(*buildTaskAccess) (bool, error)
	}{
		{
			name:   "failed",
			status: interfaces.BuildTaskStatusFailed,
			mark: func(access *buildTaskAccess) (bool, error) {
				return access.MarkFailed(context.Background(), "task-1", "execution failed", 123)
			},
		},
		{
			name:   "cancelled",
			status: interfaces.BuildTaskStatusCancelled,
			mark: func(access *buildTaskAccess) (bool, error) {
				return access.MarkCancelled(context.Background(), "task-1", "execution failed", 123)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" accepts stopping task", func(t *testing.T) {
			db, mock, access := newBuildTaskAccessMock(t)
			defer func() { _ = db.Close() }()

			mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_finish_time = ?, f_status = ? WHERE f_id = ? AND f_status IN (?,?,?)")).
				WithArgs("execution failed", int64(123), tt.status, "task-1",
					interfaces.BuildTaskStatusPending,
					interfaces.BuildTaskStatusRunning,
					interfaces.BuildTaskStatusStopping).
				WillReturnResult(sqlmock.NewResult(0, 1))

			updated, err := tt.mark(access)

			require.NoError(t, err)
			assert.True(t, updated)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestBuildTaskAccessMarkCancelledByCatalogID(t *testing.T) {
	db, mock, access := newBuildTaskAccessMock(t)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_finish_time = ?, f_status = ? WHERE f_catalog_id = ? AND f_status = ?")).
		WithArgs("catalog deleted", int64(123), interfaces.BuildTaskStatusCancelled, "catalog-1", interfaces.BuildTaskStatusPending).
		WillReturnResult(sqlmock.NewResult(0, 2))

	err := access.MarkCancelledByCatalogID(context.Background(), nil, "catalog-1", "catalog deleted", 123)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildTaskAccessGetStatus(t *testing.T) {
	t.Run("returns status", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_status FROM t_build_task WHERE f_id = ?")).
			WithArgs("task-1").
			WillReturnRows(sqlmock.NewRows([]string{"f_status"}).AddRow(interfaces.BuildTaskStatusCompleted))

		got, err := access.GetStatus(context.Background(), "task-1")

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

		got, err := access.GetStatus(context.Background(), "missing")

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
			ResourceID: task.ResourceID,
			CatalogID:  task.CatalogID,
			Statuses:   []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusPending},
			Mode:       interfaces.BuildTaskModeBatch,
		}

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_build_task WHERE f_resource_id = ? AND f_catalog_id = ? AND f_status IN (?,?) AND f_mode = ?")).
			WithArgs(task.ResourceID, task.CatalogID, interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusPending, interfaces.BuildTaskModeBatch).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(2)))
		rows := sqlmock.NewRows(buildTaskSummaryColumns()).AddRow(buildTaskSummaryRowValues(task)...)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT "+joinBuildTaskSummaryColumns()+" FROM t_build_task WHERE f_resource_id = ? AND f_catalog_id = ? AND f_status IN (?,?) AND f_mode = ? ORDER BY f_create_time ASC LIMIT 10 OFFSET 5")).
			WithArgs(task.ResourceID, task.CatalogID, interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusPending, interfaces.BuildTaskModeBatch).
			WillReturnRows(rows)

		got, total, err := access.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(2), total)
		require.Len(t, got, 1)
		assert.Equal(t, task.ID, got[0].ID)
		assert.Equal(t, task.IndexConfig, got[0].IndexConfig)
		payload, err := sonic.MarshalString(got[0])
		require.NoError(t, err)
		assert.NotContains(t, payload, "index_config")
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
}

func TestBuildTaskAccessInternalList(t *testing.T) {
	db, mock, access := newBuildTaskAccessMock(t)
	defer func() { _ = db.Close() }()
	task := sampleBuildTask()

	rows := sqlmock.NewRows(buildTaskSummaryColumns()).AddRow(buildTaskSummaryRowValues(task)...)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + strings.Join(buildTaskSummaryColumns(), ", ") + " FROM t_build_task ORDER BY f_create_time DESC")).
		WillReturnRows(rows)

	got, err := access.InternalList(context.Background(), interfaces.BuildTasksQueryParams{})

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, task.IndexConfig, got[0].IndexConfig)
	require.NoError(t, mock.ExpectationsWereMet())
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
		VectorizedCount:  70,
		SyncedMark:       "cursor-1",
		ErrorMsg:         "soft error",
		Creator:          interfaces.AccountInfo{ID: "creator-1", Type: interfaces.ACCESSOR_TYPE_USER},
		CreateTime:       1000,
		StartTime:        1500,
		FinishTime:       2000,
		LastProgressTime: 1800,
		IndexConfig: &interfaces.BuildTaskIndexConfig{
			BuildKeyFields: []string{"id"},
			Features: map[string]interfaces.BuildTaskFieldIndexFeature{
				"title": {
					Vector:   &interfaces.BuildTaskEmbeddingConfig{ModelID: "embedding", Dimensions: 1024},
					Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"},
				},
				"body": {
					Vector: &interfaces.BuildTaskEmbeddingConfig{ModelID: "embedding-v2", Dimensions: 2048},
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
		task.VectorizedCount,
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
	return append(values[:12], values[13:]...)
}
