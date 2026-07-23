// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package semantic_understanding_task

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestSemanticUnderstandingTaskAccessCreate(t *testing.T) {
	db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
	defer func() { _ = db.Close() }()
	task := sampleSemanticUnderstandingTask()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_semantic_understanding_task")).
		WithArgs(semanticUnderstandingTaskInsertArgs(task)...).
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, access.Create(context.Background(), task))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSemanticUnderstandingTaskAccessGetByID(t *testing.T) {
	t.Run("returns task", func(t *testing.T) {
		db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleSemanticUnderstandingTask()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinSemanticUnderstandingTaskColumns() + " FROM t_semantic_understanding_task WHERE f_id = ?")).
			WithArgs(task.ID).
			WillReturnRows(sqlmock.NewRows(semanticUnderstandingTaskColumns()).AddRow(semanticUnderstandingTaskRowValues(task)...))

		got, err := access.GetByID(context.Background(), task.ID)

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, task.ID, got.ID)
		assert.Equal(t, task.Scope, got.Scope)
		assert.Equal(t, task.AgentTaskID, got.AgentTaskID)
		assert.Equal(t, task.Creator, got.Creator)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns nil when not found", func(t *testing.T) {
		db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT " + joinSemanticUnderstandingTaskColumns() + " FROM t_semantic_understanding_task WHERE f_id = ?")).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := access.GetByID(context.Background(), "missing")

		require.NoError(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestSemanticUnderstandingTaskAccessGetByIDs(t *testing.T) {
	t.Run("returns tasks", func(t *testing.T) {
		db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task1 := sampleSemanticUnderstandingTask()
		task2 := sampleSemanticUnderstandingTask()
		task2.ID = "semantic-task-2"

		mock.ExpectQuery(regexp.QuoteMeta("SELECT "+joinSemanticUnderstandingTaskColumns()+" FROM t_semantic_understanding_task WHERE f_id IN (?,?)")).
			WithArgs(task1.ID, task2.ID).
			WillReturnRows(sqlmock.NewRows(semanticUnderstandingTaskColumns()).
				AddRow(semanticUnderstandingTaskRowValues(task1)...).
				AddRow(semanticUnderstandingTaskRowValues(task2)...))

		got, err := access.GetByIDs(context.Background(), []string{task1.ID, task2.ID})

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, task1.ID, got[0].ID)
		assert.Equal(t, task2.ID, got[1].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns empty without querying when ids are empty", func(t *testing.T) {
		db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		got, err := access.GetByIDs(context.Background(), nil)

		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestSemanticUnderstandingTaskAccessFindActiveByInputHash(t *testing.T) {
	db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
	defer func() { _ = db.Close() }()
	task := sampleSemanticUnderstandingTask()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+joinSemanticUnderstandingTaskColumns()+" FROM t_semantic_understanding_task WHERE f_scope = ? AND f_input_hash = ? AND f_status IN (?,?) ORDER BY f_create_time DESC LIMIT 1")).
		WithArgs(task.Scope, task.InputHash, interfaces.SemanticUnderstandingTaskStatusPending, interfaces.SemanticUnderstandingTaskStatusRunning).
		WillReturnRows(sqlmock.NewRows(semanticUnderstandingTaskColumns()).AddRow(semanticUnderstandingTaskRowValues(task)...))

	got, err := access.FindActiveByInputHash(context.Background(), task.Scope, task.InputHash)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, task.ID, got.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSemanticUnderstandingTaskAccessList(t *testing.T) {
	t.Run("returns tasks with filters", func(t *testing.T) {
		db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
		defer func() { _ = db.Close() }()
		task := sampleSemanticUnderstandingTask()

		params := interfaces.SemanticUnderstandingTaskQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 5, Limit: 10, Sort: "create_time", Direction: interfaces.ASC_DIRECTION},
			Scope:                 interfaces.SemanticUnderstandingTaskScopeResource,
			CatalogID:             "catalog-1",
			ResourceID:            "resource-1",
			Statuses: []string{
				interfaces.SemanticUnderstandingTaskStatusPending,
				interfaces.SemanticUnderstandingTaskStatusRunning,
			},
			ApplyMode: interfaces.SemanticUnderstandingApplyModeFillEmpty,
			Applied:   boolPtr(true),
		}

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_semantic_understanding_task WHERE f_scope = ? AND f_catalog_id = ? AND f_resource_id = ? AND f_status IN (?,?) AND f_apply_mode = ? AND f_applied = ?")).
			WithArgs(interfaces.SemanticUnderstandingTaskScopeResource, "catalog-1", "resource-1", interfaces.SemanticUnderstandingTaskStatusPending, interfaces.SemanticUnderstandingTaskStatusRunning, interfaces.SemanticUnderstandingApplyModeFillEmpty, true).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT "+joinSemanticUnderstandingTaskColumns()+" FROM t_semantic_understanding_task WHERE f_scope = ? AND f_catalog_id = ? AND f_resource_id = ? AND f_status IN (?,?) AND f_apply_mode = ? AND f_applied = ? ORDER BY f_create_time ASC LIMIT 10 OFFSET 5")).
			WithArgs(interfaces.SemanticUnderstandingTaskScopeResource, "catalog-1", "resource-1", interfaces.SemanticUnderstandingTaskStatusPending, interfaces.SemanticUnderstandingTaskStatusRunning, interfaces.SemanticUnderstandingApplyModeFillEmpty, true).
			WillReturnRows(sqlmock.NewRows(semanticUnderstandingTaskColumns()).AddRow(semanticUnderstandingTaskRowValues(task)...))

		got, total, err := access.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, got, 1)
		assert.Equal(t, task.ID, got[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func boolPtr(value bool) *bool {
	return &value
}

func TestSemanticUnderstandingTaskAccessMarkRunning(t *testing.T) {
	t.Run("claims pending task", func(t *testing.T) {
		db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_semantic_understanding_task SET f_agent_task_id = ?, f_failure_detail = ?, f_status = ?, f_update_time = ? WHERE f_id = ? AND f_status IN (?)")).
			WithArgs("agent-task-1", "", interfaces.SemanticUnderstandingTaskStatusRunning, sqlmock.AnyArg(), "semantic-task-1", interfaces.SemanticUnderstandingTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 1))

		claimed, err := access.MarkRunning(context.Background(), "semantic-task-1", "agent-task-1")

		require.NoError(t, err)
		assert.True(t, claimed)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns false when status does not match", func(t *testing.T) {
		db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_semantic_understanding_task SET f_agent_task_id = ?, f_failure_detail = ?, f_status = ?, f_update_time = ? WHERE f_id = ? AND f_status IN (?)")).
			WithArgs("agent-task-1", "", interfaces.SemanticUnderstandingTaskStatusRunning, sqlmock.AnyArg(), "semantic-task-1", interfaces.SemanticUnderstandingTaskStatusPending).
			WillReturnResult(sqlmock.NewResult(0, 0))

		claimed, err := access.MarkRunning(context.Background(), "semantic-task-1", "agent-task-1")

		require.NoError(t, err)
		assert.False(t, claimed)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestSemanticUnderstandingTaskAccessDelete(t *testing.T) {
	t.Run("deletes task", func(t *testing.T) {
		db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_semantic_understanding_task WHERE f_id = ?")).
			WithArgs("semantic-task-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.Delete(context.Background(), "semantic-task-1"))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns sql err no rows when not found", func(t *testing.T) {
		db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_semantic_understanding_task WHERE f_id = ?")).
			WithArgs("missing").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := access.Delete(context.Background(), "missing")

		require.ErrorIs(t, err, sql.ErrNoRows)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestSemanticUnderstandingTaskAccessDeleteByIDs(t *testing.T) {
	t.Run("deletes tasks", func(t *testing.T) {
		db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_semantic_understanding_task WHERE f_id IN (?,?)")).
			WithArgs("semantic-task-1", "semantic-task-2").
			WillReturnResult(sqlmock.NewResult(0, 2))

		affected, err := access.DeleteByIDs(context.Background(), []string{"semantic-task-1", "semantic-task-2"})

		require.NoError(t, err)
		assert.Equal(t, int64(2), affected)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns zero without querying when ids are empty", func(t *testing.T) {
		db, mock, access := newSemanticUnderstandingTaskAccessMock(t)
		defer func() { _ = db.Close() }()

		affected, err := access.DeleteByIDs(context.Background(), nil)

		require.NoError(t, err)
		assert.Equal(t, int64(0), affected)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func newSemanticUnderstandingTaskAccessMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock, *semanticUnderstandingTaskAccess) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return db, mock, &semanticUnderstandingTaskAccess{db: db}
}

func sampleSemanticUnderstandingTask() *interfaces.SemanticUnderstandingTask {
	return &interfaces.SemanticUnderstandingTask{
		ID:                   "semantic-task-1",
		Scope:                interfaces.SemanticUnderstandingTaskScopeResource,
		CatalogID:            "catalog-1",
		ResourceID:           "resource-1",
		AgentTaskID:          "agent-task-1",
		AgentID:              interfaces.SemanticUnderstandingResourceAgentID,
		Input:                `{"resource":{"id":"resource-1"}}`,
		InputHash:            "hash-1",
		Status:               interfaces.SemanticUnderstandingTaskStatusPending,
		ApplyMode:            interfaces.SemanticUnderstandingApplyModeFillEmpty,
		ResultJSON:           `{"confidence":0.8}`,
		ConfidenceThreshold:  0.75,
		Confidence:           0.8,
		ConfidenceDetailJSON: `{"fields":[]}`,
		ApplyDetailJSON:      "",
		Applied:              false,
		AppliedTime:          0,
		FailureDetail:        "",
		Creator:              interfaces.AccountInfo{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
		CreateTime:           100,
		UpdateTime:           200,
	}
}

func semanticUnderstandingTaskRowValues(task *interfaces.SemanticUnderstandingTask) []driver.Value {
	return []driver.Value{
		task.ID,
		task.Scope,
		task.CatalogID,
		task.ResourceID,
		task.AgentTaskID,
		task.AgentID,
		task.Input,
		task.InputHash,
		task.Status,
		task.ApplyMode,
		task.ResultJSON,
		task.ConfidenceThreshold,
		task.Confidence,
		task.ConfidenceDetailJSON,
		task.ApplyDetailJSON,
		task.Applied,
		task.AppliedTime,
		task.FailureDetail,
		task.Creator.ID,
		task.Creator.Type,
		task.CreateTime,
		task.UpdateTime,
	}
}

func semanticUnderstandingTaskInsertArgs(task *interfaces.SemanticUnderstandingTask) []driver.Value {
	return semanticUnderstandingTaskRowValues(task)
}

func joinSemanticUnderstandingTaskColumns() string {
	return strings.Join(semanticUnderstandingTaskColumns(), ", ")
}
