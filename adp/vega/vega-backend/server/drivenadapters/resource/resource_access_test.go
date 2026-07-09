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
	"reflect"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	sq "github.com/Masterminds/squirrel"
	"github.com/agiledragon/gomonkey/v2"
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

func TestResourceAccessGetByID(t *testing.T) {
	t.Run("returns resource with extensions", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		restore := replaceResourceExtensionStore(&fakeResourceExtensionStore{
			byID: map[string]string{"env": "prod"},
		})
		defer restore()

		mock.ExpectQuery(regexp.QuoteMeta(resourceSelectSQL("f_id = ?"))).
			WithArgs("resource-1").
			WillReturnRows(resourceRows().AddRow(resourceRowValues(sampleResource())...))

		got, err := access.GetByID(context.Background(), "resource-1")

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "resource-1", got.ID)
		assert.Equal(t, map[string]string{"env": "prod"}, got.Extensions)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns nil when resource is not found", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(resourceSelectSQL("f_id = ?"))).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := access.GetByID(context.Background(), "missing")

		require.NoError(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns scan error", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		values := resourceRowValues(sampleResource())
		values[17] = "not-int64"

		mock.ExpectQuery(regexp.QuoteMeta(resourceSelectSQL("f_id = ?"))).
			WithArgs("resource-1").
			WillReturnRows(resourceRows().AddRow(values...))

		got, err := access.GetByID(context.Background(), "resource-1")

		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceAccessGetByIDs(t *testing.T) {
	t.Run("returns resources and clears extensions", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		second := sampleResourceWithID("resource-2")
		second.Extensions = map[string]string{"env": "prod"}

		mock.ExpectQuery(regexp.QuoteMeta(resourceSelectSQL("f_id IN (?,?)"))).
			WithArgs("resource-1", "resource-2").
			WillReturnRows(resourceRows().
				AddRow(resourceRowValues(sampleResource())...).
				AddRow(resourceRowValues(second)...))

		got, err := access.GetByIDs(context.Background(), []string{"resource-1", "resource-2"})

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, []string{"resource-1", "resource-2"}, []string{got[0].ID, got[1].ID})
		assert.Nil(t, got[0].Extensions)
		assert.Nil(t, got[1].Extensions)
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
		values[17] = "not-int64"

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

func TestResourceAccessAttachListExtensions(t *testing.T) {
	t.Run("skips empty resources", func(t *testing.T) {
		access, _, cleanup := newResourceAccessMock(t)
		defer cleanup()

		err := access.AttachListExtensions(context.Background(), interfaces.ResourcesQueryParams{IncludeExtensions: true}, nil)

		require.NoError(t, err)
	})

	t.Run("clears extensions when include extensions is false", func(t *testing.T) {
		access, _, cleanup := newResourceAccessMock(t)
		defer cleanup()
		resources := []*interfaces.Resource{{ID: "resource-1", Extensions: map[string]string{"env": "prod"}}}

		err := access.AttachListExtensions(context.Background(), interfaces.ResourcesQueryParams{}, resources)

		require.NoError(t, err)
		assert.Nil(t, resources[0].Extensions)
	})

	t.Run("attaches filtered extensions", func(t *testing.T) {
		access, _, cleanup := newResourceAccessMock(t)
		defer cleanup()
		restore := replaceResourceExtensionStore(&fakeResourceExtensionStore{
			byIDs: map[string]map[string]string{
				"resource-1": {"env": "prod", "owner": "data"},
			},
		})
		defer restore()
		resources := []*interfaces.Resource{{ID: "resource-1"}}

		err := access.AttachListExtensions(context.Background(), interfaces.ResourcesQueryParams{
			IncludeExtensions:    true,
			IncludeExtensionKeys: "env",
		}, resources)

		require.NoError(t, err)
		assert.Equal(t, map[string]string{"env": "prod"}, resources[0].Extensions)
	})

	t.Run("returns store error", func(t *testing.T) {
		access, _, cleanup := newResourceAccessMock(t)
		defer cleanup()
		restore := replaceResourceExtensionStore(&fakeResourceExtensionStore{err: errors.New("store down")})
		defer restore()

		err := access.AttachListExtensions(context.Background(), interfaces.ResourcesQueryParams{IncludeExtensions: true}, []*interfaces.Resource{{ID: "resource-1"}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "store down")
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

func TestResourceAccessDeleteByIDs(t *testing.T) {
	t.Run("skips empty ids", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		require.NoError(t, access.DeleteByIDs(context.Background(), nil))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("deletes extensions and resources", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		store := &fakeResourceExtensionStore{}
		restore := replaceResourceExtensionStore(store)
		defer restore()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_resource WHERE f_id IN (?,?)")).
			WithArgs("resource-1", "resource-2").
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := access.DeleteByIDs(context.Background(), []string{"resource-1", "resource-2"})

		require.NoError(t, err)
		assert.Equal(t, []string{"resource-1", "resource-2"}, store.deletedIDs)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns extension delete error", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		restore := replaceResourceExtensionStore(&fakeResourceExtensionStore{err: errors.New("store down")})
		defer restore()

		err := access.DeleteByIDs(context.Background(), []string{"resource-1"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "store down")
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

func TestResourceAccessDeleteByCatalogIDs(t *testing.T) {
	t.Run("skips empty catalog ids", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		require.NoError(t, access.DeleteByCatalogIDs(context.Background(), nil))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns query error", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id FROM t_resource WHERE f_catalog_id IN (?)")).
			WithArgs("catalog-1").
			WillReturnError(errors.New("db down"))

		err := access.DeleteByCatalogIDs(context.Background(), []string{"catalog-1"})

		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns scan error", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id FROM t_resource WHERE f_catalog_id IN (?)")).
			WithArgs("catalog-1").
			WillReturnRows(sqlmock.NewRows([]string{"f_id", "extra"}).AddRow("resource-1", "unexpected"))

		err := access.DeleteByCatalogIDs(context.Background(), []string{"catalog-1"})

		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("deletes extensions and resources by catalog ids", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		store := &fakeResourceExtensionStore{}
		restore := replaceResourceExtensionStore(store)
		defer restore()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id FROM t_resource WHERE f_catalog_id IN (?)")).
			WithArgs("catalog-1").
			WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("resource-1").AddRow("resource-2"))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_resource WHERE f_catalog_id IN (?)")).
			WithArgs("catalog-1").
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := access.DeleteByCatalogIDs(context.Background(), []string{"catalog-1"})

		require.NoError(t, err)
		assert.Equal(t, []string{"resource-1", "resource-2"}, store.deletedIDs)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns extension delete error", func(t *testing.T) {
		access, mock, cleanup := newResourceAccessMock(t)
		defer cleanup()
		restore := replaceResourceExtensionStore(&fakeResourceExtensionStore{err: errors.New("store down")})
		defer restore()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id FROM t_resource WHERE f_catalog_id IN (?)")).
			WithArgs("catalog-1").
			WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("resource-1"))

		err := access.DeleteByCatalogIDs(context.Background(), []string{"catalog-1"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "store down")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResourceExtCol(t *testing.T) {
	t.Run("qualifies column only when extension joins are present", func(t *testing.T) {
		assert.Equal(t, "f_id", resourceExtCol(interfaces.ResourcesQueryParams{}, "f_id"))
		assert.Equal(t, "t_resource.f_id", resourceExtCol(interfaces.ResourcesQueryParams{ExtensionKeys: []string{"env"}}, "f_id"))
	})
}

func TestApplyResourceExtensionJoins(t *testing.T) {
	t.Run("skips join when extension keys are empty", func(t *testing.T) {
		sql, args, err := applyResourceExtensionJoins(
			sq.Select("f_id").From("t_resource"),
			interfaces.ResourcesQueryParams{},
		).ToSql()

		require.NoError(t, err)
		assert.Equal(t, "SELECT f_id FROM t_resource", sql)
		assert.Empty(t, args)
	})

	t.Run("applies extension joins", func(t *testing.T) {
		sql, args, err := applyResourceExtensionJoins(
			sq.Select("t_resource.f_id").From("t_resource"),
			interfaces.ResourcesQueryParams{
				ExtensionKeys:   []string{"env"},
				ExtensionValues: []string{"prod"},
			},
		).ToSql()

		require.NoError(t, err)
		assert.Contains(t, sql, "JOIN t_entity_extension vex0")
		assert.Equal(t, []interface{}{entityextension.KindResource, "env", "prod"}, args)
	})
}

func TestResourceListOrderExpr(t *testing.T) {
	t.Run("builds default and extension-safe order expression", func(t *testing.T) {
		assert.Equal(t, "f_update_time DESC", resourceListOrderExpr(interfaces.ResourcesQueryParams{PaginationQueryParams: interfaces.PaginationQueryParams{Direction: "DESC"}}))
		assert.Equal(t, "t_resource.f_name ASC", resourceListOrderExpr(interfaces.ResourcesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Sort: "f_name", Direction: "ASC"},
			ExtensionKeys:         []string{"env"},
		}))
	})
}

func TestAttachResourceExtensions(t *testing.T) {
	t.Run("skips empty resources", func(t *testing.T) {
		err := attachResourceExtensions(context.Background(), &common.AppSetting{}, interfaces.ResourcesQueryParams{IncludeExtensions: true}, nil)

		require.NoError(t, err)
	})

	t.Run("clears extensions when include extensions is false", func(t *testing.T) {
		resources := []*interfaces.Resource{{ID: "resource-1", Extensions: map[string]string{"env": "prod"}}}

		err := attachResourceExtensions(context.Background(), &common.AppSetting{}, interfaces.ResourcesQueryParams{}, resources)

		require.NoError(t, err)
		assert.Nil(t, resources[0].Extensions)
	})
}

func TestAttachSingleResourceExtensions(t *testing.T) {
	t.Run("skips nil resource", func(t *testing.T) {
		err := attachSingleResourceExtensions(context.Background(), &common.AppSetting{}, nil)

		require.NoError(t, err)
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

type fakeResourceExtensionStore struct {
	byID       map[string]string
	byIDs      map[string]map[string]string
	deletedIDs []string
	err        error
}

func (s *fakeResourceExtensionStore) DeleteByEntityIDs(ctx context.Context, kind string, entityIDs []string) error {
	s.deletedIDs = append([]string(nil), entityIDs...)
	return s.err
}

func (s *fakeResourceExtensionStore) GetByEntityID(ctx context.Context, kind string, entityID string) (map[string]string, error) {
	return s.byID, s.err
}

func (s *fakeResourceExtensionStore) GetByEntityIDs(ctx context.Context, kind string, entityIDs []string) (map[string]map[string]string, error) {
	return s.byIDs, s.err
}

func replaceResourceExtensionStore(store *fakeResourceExtensionStore) func() {
	patches := gomonkey.NewPatches()
	patches.ApplyFunc(entityextension.NewStore, func(app *common.AppSetting) *entityextension.Store {
		return &entityextension.Store{}
	})
	patches.ApplyMethod(reflect.TypeOf(&entityextension.Store{}), "DeleteByEntityIDs",
		func(_ *entityextension.Store, ctx context.Context, kind string, entityIDs []string) error {
			return store.DeleteByEntityIDs(ctx, kind, entityIDs)
		})
	patches.ApplyMethod(reflect.TypeOf(&entityextension.Store{}), "GetByEntityID",
		func(_ *entityextension.Store, ctx context.Context, kind string, entityID string) (map[string]string, error) {
			return store.GetByEntityID(ctx, kind, entityID)
		})
	patches.ApplyMethod(reflect.TypeOf(&entityextension.Store{}), "GetByEntityIDs",
		func(_ *entityextension.Store, ctx context.Context, kind string, entityIDs []string) (map[string]map[string]string, error) {
			return store.GetByEntityIDs(ctx, kind, entityIDs)
		})
	return patches.Reset
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

func sampleResourceWithID(id string) *interfaces.Resource {
	resource := sampleResource()
	resource.ID = id
	return resource
}

func resourceSelectSQL(where string) string {
	return "SELECT f_id, f_catalog_id, f_name, f_tags, f_description, f_category, f_status, f_status_message, f_last_discover_status, f_database, f_source_identifier, f_source_metadata, f_schema_definition, f_logic_type, f_logic_definition, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time, f_local_index_name FROM t_resource WHERE " + where
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
		"f_database",
		"f_source_identifier",
		"f_source_metadata",
		"f_schema_definition",
		"f_logic_type",
		"f_logic_definition",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
		"f_local_index_name",
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
		resource.Database,
		resource.SourceIdentifier,
		`{"properties":{"row_count":42}}`,
		`[{"name":"id","type":"integer"}]`,
		resource.LogicType,
		"[]",
		resource.Creator.ID,
		resource.Creator.Type,
		resource.CreateTime,
		resource.Updater.ID,
		resource.Updater.Type,
		resource.UpdateTime,
		resource.LocalIndexName,
	}
}
