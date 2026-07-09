// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common"
	"vega-backend/drivenadapters/entityextension"
	"vega-backend/interfaces"
)

func TestResourceAccessCreate(t *testing.T) {
	t.Run("creates resource", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_resource (f_id,f_catalog_id,f_name,f_tags,f_description,f_category,f_status,f_status_message,f_last_discover_status,f_database,f_source_identifier,f_source_metadata,f_schema_definition,f_logic_type,f_logic_definition,f_local_enabled,f_local_storage_engine,f_local_storage_config,f_local_index_name,f_sync_strategy,f_sync_config,f_sync_status,f_last_sync_time,f_sync_error_message,f_creator,f_creator_type,f_create_time,f_updater,f_updater_type,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")).
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
				"",
				"[]",
				false,
				"",
				"",
				"",
				"",
				"",
				"",
				0,
				"",
				"u1",
				interfaces.ACCESSOR_TYPE_USER,
				int64(1),
				"u2",
				interfaces.ACCESSOR_TYPE_USER,
				int64(2),
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, access.Create(context.Background(), sampleResource()))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessGetByIDsBasic(t *testing.T) {
	t.Run("returns basic resources", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_catalog_id, f_name, f_tags, f_description, f_category, f_status, f_status_message, f_last_discover_status, f_database, f_source_identifier, f_source_metadata, f_schema_definition, f_logic_type, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_resource WHERE f_id IN (?,?)")).
			WithArgs("resource-1", "resource-2").
			WillReturnRows(sqlmock.NewRows([]string{
				"f_id", "f_catalog_id", "f_name", "f_tags", "f_description", "f_category", "f_status", "f_status_message", "f_last_discover_status",
				"f_database", "f_source_identifier", "f_source_metadata", "f_schema_definition", "f_logic_type",
				"f_creator", "f_creator_type", "f_create_time", "f_updater", "f_updater_type", "f_update_time",
			}).AddRow(
				"resource-1", "catalog-1", "orders", "pii,core", "desc", interfaces.ResourceCategoryTable, interfaces.ResourceStatusActive, "ready", interfaces.DiscoverStatusNew,
				"db1", "public.orders", `{"properties":{"row_count":42}}`, `[{"name":"id"},{"name":"name"}]`, "",
				"u1", interfaces.ACCESSOR_TYPE_USER, int64(1), "u2", interfaces.ACCESSOR_TYPE_USER, int64(2),
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

func TestResourceAccessListIDs(t *testing.T) {
	t.Run("returns resource ids", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		params := interfaces.ResourcesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Direction: "DESC"},
			CatalogID:             "catalog-1",
			Category:              interfaces.ResourceCategoryTable,
			ExtensionKeys:         []string{"env"},
			ExtensionValues:       []string{"prod"},
		}
		mock.ExpectQuery(regexp.QuoteMeta("SELECT t_resource.f_id FROM t_resource JOIN t_entity_extension vex0 ON vex0.f_entity_kind = ? AND vex0.f_entity_id = t_resource.f_id AND vex0.f_key = ? AND vex0.f_value = ? WHERE t_resource.f_catalog_id = ? AND t_resource.f_category = ? ORDER BY t_resource.f_update_time DESC")).
			WithArgs(entityextension.KindResource, "env", "prod", "catalog-1", interfaces.ResourceCategoryTable).
			WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("resource-1"))

		got, err := access.ListIDs(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, []string{"resource-1"}, got)
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

		require.NoError(t, access.UpdateStatus(context.Background(), "resource-1", interfaces.ResourceStatusDisabled, "manual"))
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

func TestResourceExtensionHelpers(t *testing.T) {
	t.Run("maps columns and order expressions", func(t *testing.T) {
		assert.Equal(t, "f_id", resourceExtCol(interfaces.ResourcesQueryParams{}, "f_id"))
		assert.Equal(t, "t_resource.f_id", resourceExtCol(interfaces.ResourcesQueryParams{ExtensionKeys: []string{"env"}}, "f_id"))
		assert.Equal(t, "f_update_time DESC", resourceListOrderExpr(interfaces.ResourcesQueryParams{PaginationQueryParams: interfaces.PaginationQueryParams{Direction: "DESC"}}))
		assert.Equal(t, "t_resource.f_name ASC", resourceListOrderExpr(interfaces.ResourcesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Sort: "f_name", Direction: "ASC"},
			ExtensionKeys:         []string{"env"},
		}))
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
		Database:           "db1",
		SourceIdentifier:   "public.orders",
		SourceMetadata:     map[string]any{"properties": map[string]any{"row_count": 42}},
		SchemaDefinition:   []*interfaces.Property{{Name: "id", Type: "integer"}},
		Creator:            interfaces.AccountInfo{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
		CreateTime:         1,
		Updater:            interfaces.AccountInfo{ID: "u2", Type: interfaces.ACCESSOR_TYPE_USER},
		UpdateTime:         2,
	}
}
