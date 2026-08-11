package build_task

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
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

func TestBuildTaskAccessGetByResourceID(t *testing.T) {
	t.Run("returns build task", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleBuildTask()

		rows := sqlmock.NewRows(buildTaskColumns()).AddRow(buildTaskRowValues(task)...)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinBuildTaskColumns() + " FROM t_build_task WHERE f_resource_id = ? ORDER BY f_create_time DESC LIMIT 1")).
			WithArgs(task.ResourceID).
			WillReturnRows(rows)

		got, err := access.GetByResourceID(context.Background(), task.ResourceID)

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, task.ID, got.ID)
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

func TestBuildTaskAccessUpdateStatus(t *testing.T) {
	t.Run("updates status", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_status = ?, f_update_time = ? WHERE f_id = ? AND f_status <> ?")).
			WithArgs(interfaces.BuildTaskStatusRunning, sqlmock.AnyArg(), "task-1", interfaces.BuildTaskStatusCancelled).
			WillReturnResult(sqlmock.NewResult(0, 1))

		update := interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusRunning)
		updated, err := access.UpdateStatus(context.Background(), nil, "task-1", update)

		require.NoError(t, err)
		assert.True(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("does not overwrite cancelled task", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_status = ?, f_update_time = ? WHERE f_id = ? AND f_status <> ?")).
			WithArgs(interfaces.BuildTaskStatusFailed, sqlmock.AnyArg(), "task-1", interfaces.BuildTaskStatusCancelled).
			WillReturnResult(sqlmock.NewResult(0, 0))

		updated, err := access.UpdateStatus(context.Background(), nil, "task-1",
			interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusFailed))

		require.NoError(t, err)
		assert.False(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildTaskAccessUpdateStatusWithAllowedStatuses(t *testing.T) {
	t.Run("returns true when a row is claimed", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_error_msg = ?, f_status = ?, f_update_time = ? WHERE f_id = ? AND f_status IN (?)")).
			WithArgs("", interfaces.BuildTaskStatusRunning, sqlmock.AnyArg(), "task-1", interfaces.BuildTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 1))

		claimed, err := access.UpdateStatus(context.Background(), nil, "task-1",
			interfaces.NewBuildTaskUpdate().
				WithStatus(interfaces.BuildTaskStatusRunning).
				WithErrorMsg(""),
			interfaces.BuildTaskStatusPending,
		)

		require.NoError(t, err)
		assert.True(t, claimed)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns false when status does not match", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_build_task SET f_status = ?, f_update_time = ? WHERE f_id = ? AND f_status IN (?)")).
			WithArgs(interfaces.BuildTaskStatusRunning, sqlmock.AnyArg(), "task-1", interfaces.BuildTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 0))

		claimed, err := access.UpdateStatus(context.Background(), nil, "task-1",
			interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusRunning),
			interfaces.BuildTaskStatusPending,
		)

		require.NoError(t, err)
		assert.False(t, claimed)
		require.NoError(t, mock.ExpectationsWereMet())
	})
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
				Direction: interfaces.ASC_DIRECTION,
			},
			ResourceID: task.ResourceID,
			CatalogID:  task.CatalogID,
			Statuses:   []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusPending},
			Mode:       interfaces.BuildTaskModeBatch,
			OrderBy:    interfaces.BuildTaskOrderByCreatedAt,
			Order:      interfaces.ASC_DIRECTION,
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

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_build_task")).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	rows := sqlmock.NewRows(buildTaskColumns()).AddRow(buildTaskRowValues(task)...)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinBuildTaskColumns() + " FROM t_build_task ORDER BY f_create_time DESC")).
		WillReturnRows(rows)

	got, total, err := access.InternalList(context.Background(), interfaces.BuildTasksQueryParams{})

	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, task.IndexConfig, got[0].IndexConfig)
	assert.Equal(t, task.FailureDetail, got[0].FailureDetail)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildTaskAccessDelete(t *testing.T) {
	t.Run("deletes task", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_build_task WHERE f_id = ?")).
			WithArgs("task-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.Delete(context.Background(), "task-1"))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns not found error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_build_task WHERE f_id = ?")).
			WithArgs("missing").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := access.Delete(context.Background(), "missing")

		require.Error(t, err)
		assert.ErrorContains(t, err, "build task not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns rows affected error", func(t *testing.T) {
		db, mock, access := newBuildTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_build_task WHERE f_id = ?")).
			WithArgs("task-1").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))

		err := access.Delete(context.Background(), "task-1")

		require.Error(t, err)
		assert.ErrorContains(t, err, "rows affected failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildOrderByClause(t *testing.T) {
	t.Run("empty order_by defaults to created_at desc", func(t *testing.T) {
		assert.Equal(t, "f_create_time DESC", buildOrderByClause("", "asc"))
	})

	t.Run("unknown order_by falls back to created_at desc", func(t *testing.T) {
		assert.Equal(t, "f_create_time DESC", buildOrderByClause("bogus", "asc"))
	})

	t.Run("created_at follows order direction without tie breaker", func(t *testing.T) {
		assert.Equal(t, "f_create_time ASC", buildOrderByClause(interfaces.BuildTaskOrderByCreatedAt, "asc"))
		assert.Equal(t, "f_create_time DESC", buildOrderByClause(interfaces.BuildTaskOrderByCreatedAt, "desc"))
	})

	t.Run("updated_at follows order direction without tie breaker", func(t *testing.T) {
		assert.Equal(t, "f_update_time ASC", buildOrderByClause(interfaces.BuildTaskOrderByUpdatedAt, "asc"))
		assert.Equal(t, "f_update_time DESC", buildOrderByClause(interfaces.BuildTaskOrderByUpdatedAt, "desc"))
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
		ID:              "task-1",
		ResourceID:      "resource-1",
		CatalogID:       "catalog-1",
		Status:          interfaces.BuildTaskStatusPending,
		Mode:            interfaces.BuildTaskModeBatch,
		ExecuteType:     interfaces.BuildTaskExecuteTypeIncremental,
		TotalCount:      100,
		SyncedCount:     80,
		VectorizedCount: 70,
		SyncedMark:      "cursor-1",
		ErrorMsg:        "soft error",
		Creator:         interfaces.AccountInfo{ID: "creator-1", Type: interfaces.ACCESSOR_TYPE_USER},
		CreateTime:      1000,
		UpdateTime:      2000,
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
		task.UpdateTime,
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
