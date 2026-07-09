// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package entityextension

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	sq "github.com/Masterminds/squirrel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreReplace(t *testing.T) {
	store, mock, cleanup := newStoreMock(t)
	defer cleanup()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_entity_extension WHERE f_entity_id = ? AND f_entity_kind = ?")).
		WithArgs("catalog-1", KindCatalog).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_entity_extension (f_entity_kind,f_entity_id,f_key,f_value,f_create_time,f_update_time) VALUES (?,?,?,?,?,?)")).
		WithArgs(KindCatalog, "catalog-1", "owner", "team-a", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_entity_extension (f_entity_kind,f_entity_id,f_key,f_value,f_create_time,f_update_time) VALUES (?,?,?,?,?,?)")).
		WithArgs(KindCatalog, "catalog-1", "env", "prod", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	err := store.Replace(context.Background(), KindCatalog, "catalog-1", map[string]string{
		"owner": "team-a",
		"env":   "prod",
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreReplaceDeletesOnlyWhenMapIsEmpty(t *testing.T) {
	store, mock, cleanup := newStoreMock(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_entity_extension WHERE f_entity_id = ? AND f_entity_kind = ?")).
		WithArgs("resource-1", KindResource).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Replace(context.Background(), KindResource, "resource-1", map[string]string{}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreDeleteByEntityIDs(t *testing.T) {
	t.Run("skips empty ids", func(t *testing.T) {
		store, mock, cleanup := newStoreMock(t)
		defer cleanup()

		require.NoError(t, store.DeleteByEntityIDs(context.Background(), KindCatalog, nil))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("deletes by kind and ids", func(t *testing.T) {
		store, mock, cleanup := newStoreMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_entity_extension WHERE f_entity_id IN (?,?) AND f_entity_kind = ?")).
			WithArgs("catalog-1", "catalog-2", KindCatalog).
			WillReturnResult(sqlmock.NewResult(0, 2))

		require.NoError(t, store.DeleteByEntityIDs(context.Background(), KindCatalog, []string{"catalog-1", "catalog-2"}))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestStoreGetByEntityID(t *testing.T) {
	store, mock, cleanup := newStoreMock(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT f_key, f_value FROM t_entity_extension WHERE f_entity_id = ? AND f_entity_kind = ? ORDER BY f_key")).
		WithArgs("catalog-1", KindCatalog).
		WillReturnRows(sqlmock.NewRows([]string{"f_key", "f_value"}).
			AddRow("env", "prod").
			AddRow("owner", "team-a"))

	got, err := store.GetByEntityID(context.Background(), KindCatalog, "catalog-1")

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"env": "prod", "owner": "team-a"}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreGetByEntityIDs(t *testing.T) {
	t.Run("skips empty ids", func(t *testing.T) {
		store, mock, cleanup := newStoreMock(t)
		defer cleanup()

		got, err := store.GetByEntityIDs(context.Background(), KindResource, nil)

		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("groups rows by entity id", func(t *testing.T) {
		store, mock, cleanup := newStoreMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_entity_id, f_key, f_value FROM t_entity_extension WHERE f_entity_id IN (?,?) AND f_entity_kind = ? ORDER BY f_entity_id, f_key")).
			WithArgs("res-1", "res-2", KindResource).
			WillReturnRows(sqlmock.NewRows([]string{"f_entity_id", "f_key", "f_value"}).
				AddRow("res-1", "env", "prod").
				AddRow("res-1", "owner", "team-a").
				AddRow("res-2", "env", "dev"))

		got, err := store.GetByEntityIDs(context.Background(), KindResource, []string{"res-1", "res-2"})

		require.NoError(t, err)
		assert.Equal(t, map[string]map[string]string{
			"res-1": {"env": "prod", "owner": "team-a"},
			"res-2": {"env": "dev"},
		}, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestApplyJoins(t *testing.T) {
	t.Run("catalog joins extension filters", func(t *testing.T) {
		sql, args, err := ApplyJoinsForCatalog(
			sq.Select("t_catalog.f_id").From("t_catalog"),
			[]string{"env", "owner"},
			[]string{"prod", "team-a"},
		).ToSql()

		require.NoError(t, err)
		assert.Contains(t, sql, "JOIN t_entity_extension vex0 ON vex0.f_entity_kind = ? AND vex0.f_entity_id = t_catalog.f_id AND vex0.f_key = ? AND vex0.f_value = ?")
		assert.Contains(t, sql, "JOIN t_entity_extension vex1 ON vex1.f_entity_kind = ? AND vex1.f_entity_id = t_catalog.f_id AND vex1.f_key = ? AND vex1.f_value = ?")
		assert.Equal(t, []interface{}{KindCatalog, "env", "prod", KindCatalog, "owner", "team-a"}, args)
	})

	t.Run("resource joins extension filters", func(t *testing.T) {
		sql, args, err := ApplyJoinsForResource(
			sq.Select("t_resource.f_id").From("t_resource"),
			[]string{"env"},
			[]string{"prod"},
		).ToSql()

		require.NoError(t, err)
		assert.Contains(t, sql, "JOIN t_entity_extension vex0 ON vex0.f_entity_kind = ? AND vex0.f_entity_id = t_resource.f_id AND vex0.f_key = ? AND vex0.f_value = ?")
		assert.Equal(t, []interface{}{KindResource, "env", "prod"}, args)
	})
}

func TestFilterKeys(t *testing.T) {
	in := map[string]string{"env": "prod", "owner": "team-a"}

	assert.Equal(t, in, FilterKeys(in, ""))
	assert.Equal(t, in, FilterKeys(in, " , "))
	assert.Equal(t, map[string]string{"env": "prod"}, FilterKeys(in, " env ,missing "))
	assert.Empty(t, FilterKeys(in, "missing"))
}

func newStoreMock(t *testing.T) (*Store, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	return &Store{db: db}, mock, func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}
}
