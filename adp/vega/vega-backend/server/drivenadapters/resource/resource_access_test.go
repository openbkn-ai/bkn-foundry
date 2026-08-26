// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func TestResourceAccessCreate(t *testing.T) {
	t.Run("creates resource", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_resource (f_id,f_catalog_id,f_name,f_tags,f_description,f_category,f_status,f_status_message,f_last_discover_status,f_schema,f_source_identifier,f_source_metadata,f_schema_definition,f_index_config,f_logic_type,f_logic_definition,f_local_status,f_local_index_name,f_sync_mark,f_creator,f_creator_type,f_create_time,f_updater,f_updater_type,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")).
			WithArgs(
				"resource-1",
				"catalog-1",
				"orders",
				`"pii","core"`,
				"desc",
				interfaces.ResourceCategoryTable,
				interfaces.ResourceStatusActive,
				"ready",
				interfaces.DiscoverStatusNew,
				"db1",
				"public.orders",
				`{"properties":{"row_count":42}}`,
				`[{"name":"id","display_name":"","type":"integer","description":"","original_name":"","original_type":"","original_description":"","features":null,"attributes":null}]`,
				`{"build_key_fields":["updated_at","id"],"default_fulltext_analyzer":"ik_max_word","default_embedding_model":"embedding"}`,
				"",
				"[]",
				interfaces.ResourceLocalIndexStatusAvailable,
				"vega-build-resource-1-task-1",
				`{"mode":"batch","cursor":[10,"a"]}`,
				"u1",
				interfaces.ACCESSOR_TYPE_USER,
				int64(1),
				"u2",
				interfaces.ACCESSOR_TYPE_USER,
				int64(2),
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, access.Create(context.Background(), nil, sampleResource()))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("creates resource with transaction", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectBegin()
		tx, err := access.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_resource")).
			WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, access.Create(context.Background(), tx, sampleResource()))
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("defaults local index status to unavailable", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		resource := sampleResource()
		resource.LocalIndexStatus = ""
		resource.LocalIndexName = ""
		resource.SyncMark = ""
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_resource")).
			WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, access.Create(context.Background(), nil, resource))
		assert.Equal(t, interfaces.ResourceLocalIndexStatusUnavailable, resource.LocalIndexStatus)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessGetByID(t *testing.T) {
	t.Run("returns resource", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(resourceSelectSQL("f_id = ?"))).
			WithArgs("resource-1").
			WillReturnRows(resourceRows().AddRow(resourceRowValues(sampleResource())...))

		got, err := access.GetByID(context.Background(), nil, "resource-1")

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "resource-1", got.ID)
		require.NotNil(t, got.IndexConfig)
		assert.Equal(t, []string{"updated_at", "id"}, got.IndexConfig.BuildKeyFields)
		assert.Equal(t, interfaces.ResourceLocalIndexStatusAvailable, got.LocalIndexStatus)
		assert.Equal(t, "vega-build-resource-1-task-1", got.LocalIndexName)
		assert.Equal(t, `{"mode":"batch","cursor":[10,"a"]}`, got.SyncMark)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns nil when resource is not found", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(resourceSelectSQL("f_id = ?"))).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := access.GetByID(context.Background(), nil, "missing")

		require.NoError(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns scan error", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		values := resourceRowValues(sampleResource())
		values[18] = "not-int64"

		mock.ExpectQuery(regexp.QuoteMeta(resourceSelectSQL("f_id = ?"))).
			WithArgs("resource-1").
			WillReturnRows(resourceRows().AddRow(values...))

		got, err := access.GetByID(context.Background(), nil, "resource-1")

		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessGetByIDWithTransaction(t *testing.T) {
	t.Run("uses transaction and returns resource", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		mock.ExpectBegin()
		tx, err := access.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectQuery(regexp.QuoteMeta(resourceSelectSQL("f_id = ?"))).
			WithArgs("resource-1").
			WillReturnRows(resourceRows().AddRow(resourceRowValues(sampleResource())...))

		got, err := access.GetByID(context.Background(), tx, "resource-1")

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "resource-1", got.ID)
		mock.ExpectRollback()
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessGetByIDs(t *testing.T) {
	t.Run("returns resources", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		second := sampleResourceWithID("resource-2")

		mock.ExpectQuery(regexp.QuoteMeta(resourceSelectSQL("f_id IN (?,?)"))).
			WithArgs("resource-1", "resource-2").
			WillReturnRows(resourceRows().
				AddRow(resourceRowValues(sampleResource())...).
				AddRow(resourceRowValues(second)...))

		got, err := access.GetByIDs(context.Background(), []string{"resource-1", "resource-2"})

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, []string{"resource-1", "resource-2"}, []string{got[0].ID, got[1].ID})
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns query error", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(resourceSelectSQL("f_id IN (?)"))).
			WithArgs("resource-1").
			WillReturnError(errors.New("db down"))

		got, err := access.GetByIDs(context.Background(), []string{"resource-1"})

		require.Error(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns scan error", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		values := resourceRowValues(sampleResource())
		values[18] = "not-int64"

		mock.ExpectQuery(regexp.QuoteMeta(resourceSelectSQL("f_id IN (?)"))).
			WithArgs("resource-1").
			WillReturnRows(resourceRows().AddRow(values...))

		got, err := access.GetByIDs(context.Background(), []string{"resource-1"})

		require.Error(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessGetByIDsBasic(t *testing.T) {
	t.Run("returns basic resources", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_catalog_id, f_name, f_tags, f_description, f_category, f_status, f_status_message, f_last_discover_status, f_schema, f_source_identifier, f_source_metadata, f_schema_definition, f_logic_type, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time, f_local_status, f_local_index_name, f_sync_mark FROM t_resource WHERE f_id IN (?,?)")).
			WithArgs("resource-1", "resource-2").
			WillReturnRows(sqlmock.NewRows([]string{
				"f_id", "f_catalog_id", "f_name", "f_tags", "f_description", "f_category", "f_status", "f_status_message", "f_last_discover_status",
				"f_schema", "f_source_identifier", "f_source_metadata", "f_schema_definition", "f_logic_type",
				"f_creator", "f_creator_type", "f_create_time", "f_updater", "f_updater_type", "f_update_time",
				"f_local_status", "f_local_index_name", "f_sync_mark",
			}).AddRow(
				"resource-1", "catalog-1", "orders", "pii,core", "desc", interfaces.ResourceCategoryTable, interfaces.ResourceStatusActive, "ready", interfaces.DiscoverStatusNew,
				"db1", "public.orders", `{"properties":{"row_count":42}}`, `[{"name":"id"},{"name":"name"}]`, "",
				"u1", interfaces.ACCESSOR_TYPE_USER, int64(1), "u2", interfaces.ACCESSOR_TYPE_USER, int64(2),
				interfaces.ResourceLocalIndexStatusAvailable, "vega-build-resource-1-task-1", `{"mode":"batch","cursor":[10,"a"]}`,
			))

		got, err := access.GetByIDsBasic(context.Background(), []string{"resource-1", "resource-2"})

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, []string{"pii", "core"}, got[0].Tags)
		require.NotNil(t, got[0].ColumnCount)
		assert.Equal(t, 2, *got[0].ColumnCount)
		require.NotNil(t, got[0].RowCount)
		assert.Equal(t, int64(42), *got[0].RowCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessGetByName(t *testing.T) {
	t.Run("returns resource", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(resourceNameSelectSQL("f_catalog_id = ? AND f_name = ?"))).
			WithArgs("catalog-1", "orders").
			WillReturnRows(resourceNameRows().AddRow(resourceNameRowValues(sampleResource())...))

		got, err := access.GetByName(context.Background(), "catalog-1", "orders")

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "resource-1", got.ID)
		assert.Equal(t, []string{"pii", "core"}, got.Tags)
		assert.Equal(t, "db1", got.Schema)
		assert.Equal(t, "public.orders", got.SourceIdentifier)
		require.Len(t, got.SchemaDefinition, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns nil when not found", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(resourceNameSelectSQL("f_catalog_id = ? AND f_name = ?"))).
			WithArgs("catalog-1", "missing").
			WillReturnError(sql.ErrNoRows)

		got, err := access.GetByName(context.Background(), "catalog-1", "missing")

		require.NoError(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessGetByCatalogID(t *testing.T) {
	t.Run("returns resources", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(resourceNameSelectSQL("f_catalog_id = ?"))).
			WithArgs("catalog-1").
			WillReturnRows(resourceNameRows().
				AddRow(resourceNameRowValues(sampleResource())...).
				AddRow(resourceNameRowValues(sampleResourceWithID("resource-2"))...))

		got, err := access.GetByCatalogID(context.Background(), "catalog-1")

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "resource-1", got[0].ID)
		assert.Equal(t, "resource-2", got[1].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessList(t *testing.T) {
	t.Run("returns resources with filters", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		params := interfaces.ResourcesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Sort: "name", Direction: "ASC"},
			Name:                  "order",
			CatalogID:             "catalog-1",
			Category:              interfaces.ResourceCategoryTable,
			Status:                interfaces.ResourceStatusActive,
			Schema:                "db1",
		}

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_resource WHERE f_name LIKE ? AND f_catalog_id = ? AND f_category = ? AND f_status = ? AND f_schema = ?")).
			WithArgs("%order%", "catalog-1", interfaces.ResourceCategoryTable, interfaces.ResourceStatusActive, "db1").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
		mock.ExpectQuery(regexp.QuoteMeta(resourceNameSelectSQL("f_name LIKE ? AND f_catalog_id = ? AND f_category = ? AND f_status = ? AND f_schema = ? ORDER BY f_name ASC"))).
			WithArgs("%order%", "catalog-1", interfaces.ResourceCategoryTable, interfaces.ResourceStatusActive, "db1").
			WillReturnRows(resourceNameRows().AddRow(resourceNameRowValues(sampleResource())...))

		got, total, err := access.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, got, 1)
		assert.Equal(t, "orders", got[0].Name)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessUpdate(t *testing.T) {
	t.Run("updates resource", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		res := sampleResource()
		res.LocalIndexName = "vega-build-resource-1-task-1"
		res.StatusMessage = "discover metadata failed: table metadata not found or inaccessible: public.orders"

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_resource SET f_name = ?, f_tags = ?, f_description = ?, f_schema_definition = ?, f_index_config = ?, f_logic_type = ?, f_logic_definition = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_id = ? AND f_update_time = ?")).
			WithArgs(
				res.Name,
				`"pii","core"`,
				res.Description,
				`[{"name":"id","display_name":"","type":"integer","description":"","original_name":"","original_type":"","original_description":"","features":null,"attributes":null}]`,
				`{"build_key_fields":["updated_at","id"],"default_fulltext_analyzer":"ik_max_word","default_embedding_model":"embedding"}`,
				"",
				"[]",
				res.Updater.ID,
				res.Updater.Type,
				res.UpdateTime,
				res.ID,
				res.UpdateTime,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		rowsAffected, err := access.Update(context.Background(), nil, res, res.UpdateTime)
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns zero affected rows for a stale resource", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		res := sampleResource()
		expectedUpdateTime := res.UpdateTime - 1

		mock.ExpectExec("UPDATE t_resource SET .* WHERE f_id = \\? AND f_update_time = \\?").
			WillReturnResult(sqlmock.NewResult(0, 0))

		rowsAffected, err := access.Update(context.Background(), nil, res, expectedUpdateTime)

		require.NoError(t, err)
		assert.Zero(t, rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessUpdateLocalIndexName(t *testing.T) {
	t.Run("updates only local index name", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_resource SET f_local_index_name = ? WHERE f_id = ?")).
			WithArgs("vega-build-resource-1-task-1", "resource-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.UpdateLocalIndexName(context.Background(), nil, "resource-1", "vega-build-resource-1-task-1"))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("updates local index name in transaction", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectBegin()
		tx, err := access.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_resource SET f_local_index_name = ? WHERE f_id = ?")).
			WithArgs("vega-build-resource-1-task-1", "resource-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.UpdateLocalIndexName(context.Background(), tx, "resource-1", "vega-build-resource-1-task-1"))
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessUpdateLocalIndexState(t *testing.T) {
	t.Run("updates all state fields in transaction", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		mock.ExpectBegin()
		tx, err := access.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_resource SET f_local_status = ?, f_local_index_name = ?, f_sync_mark = ? WHERE f_id = ?")).
			WithArgs(
				interfaces.ResourceLocalIndexStatusAvailable,
				"index-v2",
				`{"mode":"batch","cursor":[20]}`,
				"resource-1",
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		updated, err := access.UpdateLocalIndexState(
			context.Background(),
			tx,
			"resource-1",
			interfaces.ResourceLocalIndexStatusAvailable,
			"index-v2",
			`{"mode":"batch","cursor":[20]}`,
		)

		require.NoError(t, err)
		assert.True(t, updated)
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns false when resource does not exist", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		mock.ExpectExec("UPDATE t_resource SET .* WHERE f_id = \\?").
			WillReturnResult(sqlmock.NewResult(0, 0))

		updated, err := access.UpdateLocalIndexState(
			context.Background(),
			nil,
			"missing",
			interfaces.ResourceLocalIndexStatusUnavailable,
			"",
			"",
		)

		require.NoError(t, err)
		assert.False(t, updated)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessUpdateSemanticMetadata(t *testing.T) {
	t.Run("updates only semantic metadata", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		resource := sampleResource()
		resource.LocalIndexName = "vega-build-resource-1-task-1"
		expectedUpdateTime := resource.UpdateTime - 1

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_resource SET f_name = ?, f_description = ?, f_schema_definition = ?, f_logic_definition = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_id = ? AND f_update_time = ?")).
			WithArgs(
				resource.Name,
				resource.Description,
				`[{"name":"id","display_name":"","type":"integer","description":"","original_name":"","original_type":"","original_description":"","features":null,"attributes":null}]`,
				"[]",
				resource.Updater.ID,
				resource.Updater.Type,
				resource.UpdateTime,
				resource.ID,
				expectedUpdateTime,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		rowsAffected, err := access.UpdateSemanticMetadata(context.Background(), nil, resource, expectedUpdateTime)
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("updates semantic metadata in transaction", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		resource := sampleResource()

		mock.ExpectBegin()
		tx, err := access.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec("UPDATE t_resource SET .* WHERE f_id = \\?").
			WillReturnResult(sqlmock.NewResult(0, 1))

		rowsAffected, err := access.UpdateSemanticMetadata(context.Background(), tx, resource, resource.UpdateTime)
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessUpdateDiscoveryMetadata(t *testing.T) {
	access, mock, cleanup := newResourceAccessMock(t)
	defer cleanup()
	resource := sampleResource()
	resource.StatusMessage = ""
	resource.LastDiscoverStatus = interfaces.DiscoverStatusUpdated
	expectedUpdateTime := resource.UpdateTime - 1

	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_resource SET f_description = ?, f_status_message = ?, f_source_metadata = ?, f_schema_definition = ?, f_last_discover_status = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_id = ? AND f_update_time = ?")).
		WithArgs(
			resource.Description,
			resource.StatusMessage,
			`{"properties":{"row_count":42}}`,
			`[{"name":"id","display_name":"","type":"integer","description":"","original_name":"","original_type":"","original_description":"","features":null,"attributes":null}]`,
			resource.LastDiscoverStatus,
			resource.Updater.ID,
			resource.Updater.Type,
			resource.UpdateTime,
			resource.ID,
			expectedUpdateTime,
		).
		WillReturnResult(sqlmock.NewResult(0, 0))

	rowsAffected, err := access.UpdateDiscoveryMetadata(context.Background(), nil, resource, expectedUpdateTime)
	require.NoError(t, err)
	assert.Zero(t, rowsAffected)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResourceAccessListIDs(t *testing.T) {
	t.Run("returns resource ids", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		params := interfaces.ResourcesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Direction: "DESC"},
			CatalogID:             "catalog-1",
			Category:              interfaces.ResourceCategoryTable,
		}
		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id FROM t_resource WHERE f_catalog_id = ? AND f_category = ? ORDER BY f_update_time DESC")).
			WithArgs("catalog-1", interfaces.ResourceCategoryTable).
			WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("resource-1"))

		got, err := access.ListIDs(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, []string{"resource-1"}, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessListIDsReturnsIterationError(t *testing.T) {
	access, mock, cleanup := newResourceAccessMock(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"f_id"}).
		AddRow("resource-1").
		AddRow("resource-2").
		RowError(1, errors.New("rows interrupted"))
	mock.ExpectQuery("SELECT f_id FROM t_resource").WillReturnRows(rows)

	got, err := access.ListIDs(context.Background(), interfaces.ResourcesQueryParams{})

	require.ErrorContains(t, err, "rows interrupted")
	assert.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResourceAccessDeleteByIDs(t *testing.T) {
	t.Run("skips empty ids", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		require.NoError(t, access.DeleteByIDs(context.Background(), nil))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("deletes resources", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_resource WHERE f_id IN (?,?)")).
			WithArgs("resource-1", "resource-2").
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := access.DeleteByIDs(context.Background(), []string{"resource-1", "resource-2"})

		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessListAuthResources(t *testing.T) {
	t.Run("returns auth resources", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_name FROM t_resource WHERE f_id = ? AND f_name LIKE ? ORDER BY f_name ASC")).
			WithArgs("resource-1", "%order\\%%").
			WillReturnRows(sqlmock.NewRows([]string{"f_id", "f_name"}).AddRow("resource-1", "order%"))

		got, err := access.ListAuthResources(context.Background(), interfaces.AuthResourceQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Sort: "f_name", Direction: "ASC"},
			ID:                    "resource-1",
			Keyword:               "order%",
		})

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, interfaces.AUTH_RESOURCE_TYPE_RESOURCE, got[0].Type)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessUpdateStatus(t *testing.T) {
	t.Run("updates status", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_resource SET f_status = ?, f_status_message = ? WHERE f_id = ?")).
			WithArgs(interfaces.ResourceStatusDisabled, "manual", "resource-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.UpdateStatus(context.Background(), nil, "resource-1", interfaces.ResourceStatusDisabled, "manual"))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("updates status in transaction", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectBegin()
		tx, err := access.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_resource SET f_status = ?, f_status_message = ? WHERE f_id = ?")).
			WithArgs(interfaces.ResourceStatusDisabled, "manual", "resource-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.UpdateStatus(context.Background(), tx, "resource-1", interfaces.ResourceStatusDisabled, "manual"))
		mock.ExpectCommit()
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessUpdateDiscoverStatus(t *testing.T) {
	t.Run("updates discover status", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_resource SET f_last_discover_status = ? WHERE f_id = ?")).
			WithArgs(interfaces.DiscoverStatusUpdated, "resource-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.UpdateDiscoverStatus(context.Background(), "resource-1", interfaces.DiscoverStatusUpdated))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessCheckExistByCategories(t *testing.T) {
	t.Run("returns true when matching resource exists", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_resource WHERE f_catalog_id = ? AND f_category IN (?,?)")).
			WithArgs("catalog-1", interfaces.ResourceCategoryTable, interfaces.ResourceCategoryIndex).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		got, err := access.CheckExistByCategories(context.Background(), "catalog-1", []string{interfaces.ResourceCategoryTable, interfaces.ResourceCategoryIndex})

		require.NoError(t, err)
		assert.True(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessDeleteByCatalogID(t *testing.T) {
	t.Run("deletes resources by catalog ID", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_resource WHERE f_catalog_id = ?")).
			WithArgs("catalog-1").
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := access.DeleteByCatalogID(context.Background(), nil, "catalog-1")

		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceListOrderByClause(t *testing.T) {
	t.Run("maps supported API fields", func(t *testing.T) {
		assert.Equal(t, "f_name ASC", resourceListOrderByClause(interfaces.ResourceSortName, "ASC"))
		assert.Equal(t, "f_create_time DESC", resourceListOrderByClause(interfaces.ResourceSortCreateTime, "desc"))
		assert.Equal(t, "f_update_time ASC", resourceListOrderByClause(interfaces.ResourceSortUpdateTime, "asc"))
	})

	t.Run("falls back for empty or invalid values", func(t *testing.T) {
		assert.Equal(t, "f_update_time DESC", resourceListOrderByClause("", "ASC"))
		assert.Equal(t, "f_update_time DESC", resourceListOrderByClause("f_name", "ASC"))
		assert.Equal(t, "f_name DESC", resourceListOrderByClause(interfaces.ResourceSortName, "invalid"))
	})
}

func newResourceAccessMock(t *testing.T) (*resourceAccess, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	return &resourceAccess{db: db, appSetting: &common.AppSetting{}}, mock, func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}
}

func sampleResource() *interfaces.Resource {
	return &interfaces.Resource{
		ID:                 "resource-1",
		CatalogID:          "catalog-1",
		Name:               "orders",
		Tags:               []string{"pii", "core"},
		Description:        "desc",
		Category:           interfaces.ResourceCategoryTable,
		Status:             interfaces.ResourceStatusActive,
		StatusMessage:      "ready",
		LastDiscoverStatus: interfaces.DiscoverStatusNew,
		Schema:             "db1",
		SourceIdentifier:   "public.orders",
		SourceMetadata:     map[string]any{"properties": map[string]any{"row_count": 42}},
		SchemaDefinition:   []*interfaces.Property{{Name: "id", Type: "integer"}},
		IndexConfig: &interfaces.ResourceIndexConfig{
			BuildKeyFields:          []string{"updated_at", "id"},
			DefaultFulltextAnalyzer: "ik_max_word",
			DefaultEmbeddingModel:   "embedding",
		},
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
		LocalIndexName:   "vega-build-resource-1-task-1",
		SyncMark:         `{"mode":"batch","cursor":[10,"a"]}`,
		Creator:          interfaces.AccountInfo{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
		CreateTime:       1,
		Updater:          interfaces.AccountInfo{ID: "u2", Type: interfaces.ACCESSOR_TYPE_USER},
		UpdateTime:       2,
	}
}

func sampleResourceWithID(id string) *interfaces.Resource {
	resource := sampleResource()
	resource.ID = id
	return resource
}

func resourceNameRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"f_id",
		"f_catalog_id",
		"f_name",
		"f_tags",
		"f_description",
		"f_category",
		"f_status",
		"f_status_message",
		"f_last_discover_status",
		"f_schema",
		"f_source_identifier",
		"f_source_metadata",
		"f_schema_definition",
		"f_index_config",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
		"f_local_status",
		"f_local_index_name",
		"f_sync_mark",
	})
}

func resourceNameRowValues(resource *interfaces.Resource) []driver.Value {
	return []driver.Value{
		resource.ID,
		resource.CatalogID,
		resource.Name,
		"pii,core",
		resource.Description,
		resource.Category,
		resource.Status,
		resource.StatusMessage,
		resource.LastDiscoverStatus,
		resource.Schema,
		resource.SourceIdentifier,
		`{"properties":{"row_count":42}}`,
		`[{"name":"id","type":"integer"}]`,
		`{"build_key_fields":["updated_at","id"],"default_fulltext_analyzer":"ik_max_word","default_embedding_model":"embedding"}`,
		resource.Creator.ID,
		resource.Creator.Type,
		resource.CreateTime,
		resource.Updater.ID,
		resource.Updater.Type,
		resource.UpdateTime,
		resource.LocalIndexStatus,
		resource.LocalIndexName,
		resource.SyncMark,
	}
}

func resourceNameSelectSQL(where string) string {
	return "SELECT f_id, f_catalog_id, f_name, f_tags, f_description, f_category, f_status, f_status_message, f_last_discover_status, f_schema, f_source_identifier, f_source_metadata, f_schema_definition, f_index_config, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time, f_local_status, f_local_index_name, f_sync_mark FROM t_resource WHERE " + where
}

func resourceSelectSQL(where string) string {
	return "SELECT f_id, f_catalog_id, f_name, f_tags, f_description, f_category, f_status, f_status_message, f_last_discover_status, f_schema, f_source_identifier, f_source_metadata, f_schema_definition, f_index_config, f_logic_type, f_logic_definition, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time, f_local_status, f_local_index_name, f_sync_mark FROM t_resource WHERE " + where
}

func resourceRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"f_id",
		"f_catalog_id",
		"f_name",
		"f_tags",
		"f_description",
		"f_category",
		"f_status",
		"f_status_message",
		"f_last_discover_status",
		"f_schema",
		"f_source_identifier",
		"f_source_metadata",
		"f_schema_definition",
		"f_index_config",
		"f_logic_type",
		"f_logic_definition",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
		"f_local_status",
		"f_local_index_name",
		"f_sync_mark",
	})
}

func resourceRowValues(resource *interfaces.Resource) []driver.Value {
	return []driver.Value{
		resource.ID,
		resource.CatalogID,
		resource.Name,
		"pii,core",
		resource.Description,
		resource.Category,
		resource.Status,
		resource.StatusMessage,
		resource.LastDiscoverStatus,
		resource.Schema,
		resource.SourceIdentifier,
		`{"properties":{"row_count":42}}`,
		`[{"name":"id","type":"integer"}]`,
		`{"build_key_fields":["updated_at","id"],"default_fulltext_analyzer":"ik_max_word","default_embedding_model":"embedding"}`,
		resource.LogicType,
		"[]",
		resource.Creator.ID,
		resource.Creator.Type,
		resource.CreateTime,
		resource.Updater.ID,
		resource.Updater.Type,
		resource.UpdateTime,
		resource.LocalIndexStatus,
		resource.LocalIndexName,
		resource.SyncMark,
	}
}
