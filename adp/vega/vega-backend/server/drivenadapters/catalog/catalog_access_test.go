// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
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

func TestCatalogAccessCreate(t *testing.T) {
	t.Run("creates catalog", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_catalog (f_id,f_name,f_tags,f_description,f_type,f_enabled,f_internal,f_connector_type,f_connector_config,f_metadata,f_health_check_enabled,f_health_check_status,f_last_check_time,f_health_check_result,f_creator,f_creator_type,f_create_time,f_updater,f_updater_type,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")).
			WithArgs(
				"catalog-1",
				"Catalog One",
				`"tag-a","tag-b"`,
				"desc",
				interfaces.CatalogTypePhysical,
				true,
				false,
				interfaces.ConnectorTypePostgreSQL,
				`{"host":"127.0.0.1"}`,
				`{"region":"cn"}`,
				true,
				interfaces.CatalogHealthStatusHealthy,
				int64(100),
				"ok",
				"u1",
				interfaces.ACCESSOR_TYPE_USER,
				int64(1),
				"u2",
				interfaces.ACCESSOR_TYPE_USER,
				int64(2),
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, access.Create(context.Background(), sampleCatalog()))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessListIDs(t *testing.T) {
	t.Run("returns catalog ids", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		enabled := true
		params := interfaces.CatalogsQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Sort: "f_name", Direction: "ASC"},
			Name:                  "cat",
			Tag:                   "tag",
			Type:                  interfaces.CatalogTypePhysical,
			Enabled:               &enabled,
			HealthCheckStatus:     interfaces.CatalogHealthStatusHealthy,
		}
		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id FROM t_catalog WHERE f_name LIKE ? AND f_tags LIKE ? AND f_type = ? AND f_enabled = ? AND f_health_check_status = ? ORDER BY f_name ASC")).
			WithArgs("%cat%", "%tag%", interfaces.CatalogTypePhysical, true, interfaces.CatalogHealthStatusHealthy).
			WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("catalog-1").AddRow("catalog-2"))

		got, err := access.ListIDs(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, []string{"catalog-1", "catalog-2"}, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessListInternalIDs(t *testing.T) {
	t.Run("returns internal catalog ids", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id FROM t_catalog WHERE f_internal = ?")).
			WithArgs(true).
			WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("internal-1").AddRow("internal-2"))

		got, err := access.ListInternalIDs(context.Background())

		require.NoError(t, err)
		assert.Equal(t, []string{"internal-1", "internal-2"}, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessList(t *testing.T) {
	t.Run("returns catalogs with filters", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		enabled := true
		params := interfaces.CatalogsQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Sort: "f_name", Direction: "ASC"},
			Name:                  "Catalog",
			Tag:                   "tag-a",
			Type:                  interfaces.CatalogTypePhysical,
			ConnectorType:         "postgresql",
			Enabled:               &enabled,
			HealthCheckStatus:     interfaces.CatalogHealthStatusHealthy,
		}

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_catalog WHERE f_name LIKE ? AND f_tags LIKE ? AND f_type = ? AND f_connector_type = ? AND f_enabled = ? AND f_health_check_status = ?")).
			WithArgs("%Catalog%", "%tag-a%", interfaces.CatalogTypePhysical, "postgresql", true, interfaces.CatalogHealthStatusHealthy).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_name, f_tags, f_description, f_type, f_enabled, f_internal, f_connector_type, f_connector_config, f_metadata, f_health_check_enabled, f_health_check_status, f_last_check_time, f_health_check_result, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_catalog WHERE f_name LIKE ? AND f_tags LIKE ? AND f_type = ? AND f_connector_type = ? AND f_enabled = ? AND f_health_check_status = ? ORDER BY f_name ASC")).
			WithArgs("%Catalog%", "%tag-a%", interfaces.CatalogTypePhysical, "postgresql", true, interfaces.CatalogHealthStatusHealthy).
			WillReturnRows(catalogRows().AddRow(catalogRowValues(sampleCatalog())...))

		got, total, err := access.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, got, 1)
		assert.Equal(t, "catalog-1", got[0].ID)
		assert.Nil(t, got[0].Extensions)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessGetByID(t *testing.T) {
	t.Run("returns catalog with extensions", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		restore := replaceCatalogExtensionStore(&fakeCatalogExtensionStore{
			byID: map[string]string{"env": "prod"},
		})
		defer restore()

		mock.ExpectQuery(regexp.QuoteMeta(catalogSelectSQL("f_id = ?"))).
			WithArgs("catalog-1").
			WillReturnRows(catalogRows().AddRow(catalogRowValues(sampleCatalog())...))

		got, err := access.GetByID(context.Background(), "catalog-1")

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "catalog-1", got.ID)
		assert.Equal(t, map[string]string{"env": "prod"}, got.Extensions)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns nil when catalog is not found", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(catalogSelectSQL("f_id = ?"))).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := access.GetByID(context.Background(), "missing")

		require.NoError(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns scan error", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		values := catalogRowValues(sampleCatalog())
		values[5] = "not-bool"

		mock.ExpectQuery(regexp.QuoteMeta(catalogSelectSQL("f_id = ?"))).
			WithArgs("catalog-1").
			WillReturnRows(catalogRows().AddRow(values...))

		got, err := access.GetByID(context.Background(), "catalog-1")

		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessGetByIDs(t *testing.T) {
	t.Run("returns catalogs and clears extensions", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		second := sampleCatalogWithID("catalog-2")
		second.Extensions = map[string]string{"env": "prod"}

		mock.ExpectQuery(regexp.QuoteMeta(catalogSelectSQL("f_id IN (?,?)"))).
			WithArgs("catalog-1", "catalog-2").
			WillReturnRows(catalogRows().
				AddRow(catalogRowValues(sampleCatalog())...).
				AddRow(catalogRowValues(second)...))

		got, err := access.GetByIDs(context.Background(), []string{"catalog-1", "catalog-2"})

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, []string{"catalog-1", "catalog-2"}, []string{got[0].ID, got[1].ID})
		assert.Nil(t, got[0].Extensions)
		assert.Nil(t, got[1].Extensions)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns query error", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(catalogSelectSQL("f_id IN (?)"))).
			WithArgs("catalog-1").
			WillReturnError(errors.New("db down"))

		got, err := access.GetByIDs(context.Background(), []string{"catalog-1"})

		require.Error(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns scan error", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		values := catalogRowValues(sampleCatalog())
		values[5] = "not-bool"

		mock.ExpectQuery(regexp.QuoteMeta(catalogSelectSQL("f_id IN (?)"))).
			WithArgs("catalog-1").
			WillReturnRows(catalogRows().AddRow(values...))

		got, err := access.GetByIDs(context.Background(), []string{"catalog-1"})

		require.Error(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessListAuthResources(t *testing.T) {
	t.Run("returns auth resources", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_name FROM t_catalog WHERE f_internal = ? AND f_id = ? AND f_name LIKE ? ORDER BY f_name ASC")).
			WithArgs(false, "catalog-1", "%Catalog%").
			WillReturnRows(sqlmock.NewRows([]string{"f_id", "f_name"}).AddRow("catalog-1", "Catalog One"))

		got, err := access.ListAuthResources(context.Background(), interfaces.AuthResourceQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Sort: "f_name", Direction: "ASC"},
			ID:                    "catalog-1",
			Keyword:               "Catalog",
		})

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "catalog-1", got[0].ID)
		assert.Equal(t, interfaces.AUTH_RESOURCE_TYPE_CATALOG, got[0].Type)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessAttachListExtensions(t *testing.T) {
	t.Run("skips empty catalogs", func(t *testing.T) {
		access, _, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		err := access.AttachListExtensions(context.Background(), interfaces.CatalogsQueryParams{IncludeExtensions: true}, nil)

		require.NoError(t, err)
	})

	t.Run("clears extensions when include extensions is false", func(t *testing.T) {
		access, _, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		catalogs := []*interfaces.Catalog{{ID: "catalog-1", Extensions: map[string]string{"env": "prod"}}}

		err := access.AttachListExtensions(context.Background(), interfaces.CatalogsQueryParams{}, catalogs)

		require.NoError(t, err)
		assert.Nil(t, catalogs[0].Extensions)
	})

	t.Run("attaches filtered extensions", func(t *testing.T) {
		access, _, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		restore := replaceCatalogExtensionStore(&fakeCatalogExtensionStore{
			byIDs: map[string]map[string]string{
				"catalog-1": {"env": "prod", "owner": "data"},
			},
		})
		defer restore()
		catalogs := []*interfaces.Catalog{{ID: "catalog-1"}}

		err := access.AttachListExtensions(context.Background(), interfaces.CatalogsQueryParams{
			IncludeExtensions:    true,
			IncludeExtensionKeys: "env",
		}, catalogs)

		require.NoError(t, err)
		assert.Equal(t, map[string]string{"env": "prod"}, catalogs[0].Extensions)
	})

	t.Run("returns store error", func(t *testing.T) {
		access, _, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		restore := replaceCatalogExtensionStore(&fakeCatalogExtensionStore{err: errors.New("store down")})
		defer restore()

		err := access.AttachListExtensions(context.Background(), interfaces.CatalogsQueryParams{IncludeExtensions: true}, []*interfaces.Catalog{{ID: "catalog-1"}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "store down")
	})
}

func TestCatalogAccessGetByName(t *testing.T) {
	t.Run("returns catalog with extensions", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		restore := replaceCatalogExtensionStore(&fakeCatalogExtensionStore{
			byID: map[string]string{"env": "prod"},
		})
		defer restore()

		mock.ExpectQuery(regexp.QuoteMeta(catalogSelectSQL("f_name = ?"))).
			WithArgs("Catalog One").
			WillReturnRows(catalogRows().AddRow(catalogRowValues(sampleCatalog())...))

		got, err := access.GetByName(context.Background(), "Catalog One")

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "catalog-1", got.ID)
		assert.Equal(t, map[string]string{"env": "prod"}, got.Extensions)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns nil when catalog is not found", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(catalogSelectSQL("f_name = ?"))).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := access.GetByName(context.Background(), "missing")

		require.NoError(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessUpdate(t *testing.T) {
	t.Run("updates catalog", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		catalog := sampleCatalog()
		catalog.Name = "Updated Catalog"

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog SET f_name = ?, f_tags = ?, f_description = ?, f_enabled = ?, f_connector_type = ?, f_connector_config = ?, f_metadata = ?, f_health_check_enabled = ?, f_health_check_status = ?, f_last_check_time = ?, f_health_check_result = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_id = ?")).
			WithArgs(catalog.Name, `"tag-a","tag-b"`, catalog.Description, catalog.Enabled, catalog.ConnectorType, `{"host":"127.0.0.1"}`, `{"region":"cn"}`, catalog.HealthCheckEnabled, catalog.HealthCheckStatus, catalog.LastCheckTime, catalog.HealthCheckResult, catalog.Updater.ID, catalog.Updater.Type, catalog.UpdateTime, catalog.ID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.Update(context.Background(), catalog))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessUpdateEnabled(t *testing.T) {
	t.Run("updates enabled and health status", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog SET f_enabled = ?, f_health_check_status = ?, f_last_check_time = ?, f_health_check_result = ?, f_updater = ?, f_updater_type = ?, f_update_time = ? WHERE f_id = ?")).
			WithArgs(false, interfaces.CatalogHealthStatusOffline, int64(200), "disabled", "u2", interfaces.ACCESSOR_TYPE_USER, int64(300), "catalog-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := access.UpdateEnabled(context.Background(), "catalog-1", false, interfaces.CatalogHealthCheckStatus{
			HealthCheckStatus: interfaces.CatalogHealthStatusOffline,
			LastCheckTime:     200,
			HealthCheckResult: "disabled",
		}, 300, interfaces.AccountInfo{ID: "u2", Type: interfaces.ACCESSOR_TYPE_USER})

		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessUpdateMetadata(t *testing.T) {
	t.Run("updates metadata", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog SET f_metadata = ? WHERE f_id = ?")).
			WithArgs(`{"region":"cn"}`, "catalog-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, access.UpdateMetadata(context.Background(), "catalog-1", map[string]any{"region": "cn"}))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessUpdateHealthCheckStatus(t *testing.T) {
	t.Run("returns db error", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("UPDATE t_catalog SET f_health_check_status = ?, f_last_check_time = ?, f_health_check_result = ? WHERE f_id = ?")).
			WithArgs(interfaces.CatalogHealthStatusUnhealthy, int64(400), "failed", "catalog-1").
			WillReturnError(errors.New("db down"))

		err := access.UpdateHealthCheckStatus(context.Background(), "catalog-1", interfaces.CatalogHealthCheckStatus{
			HealthCheckStatus: interfaces.CatalogHealthStatusUnhealthy,
			LastCheckTime:     400,
			HealthCheckResult: "failed",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "db down")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogAccessDeleteByIDs(t *testing.T) {
	t.Run("skips empty ids", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()

		require.NoError(t, access.DeleteByIDs(context.Background(), nil))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("deletes extensions and catalogs", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		store := &fakeCatalogExtensionStore{}
		restore := replaceCatalogExtensionStore(store)
		defer restore()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_catalog WHERE f_id IN (?,?)")).
			WithArgs("catalog-1", "catalog-2").
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := access.DeleteByIDs(context.Background(), []string{"catalog-1", "catalog-2"})

		require.NoError(t, err)
		assert.Equal(t, []string{"catalog-1", "catalog-2"}, store.deletedIDs)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns extension delete error", func(t *testing.T) {
		access, mock, cleanup := newCatalogAccessMock(t)
		defer cleanup()
		restore := replaceCatalogExtensionStore(&fakeCatalogExtensionStore{err: errors.New("store down")})
		defer restore()

		err := access.DeleteByIDs(context.Background(), []string{"catalog-1"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "store down")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCatalogExtCol(t *testing.T) {
	t.Run("qualifies column only when extension joins are present", func(t *testing.T) {
		assert.Equal(t, "f_id", catalogExtCol(interfaces.CatalogsQueryParams{}, "f_id"))
		assert.Equal(t, "t_catalog.f_id", catalogExtCol(interfaces.CatalogsQueryParams{ExtensionKeys: []string{"env"}}, "f_id"))
	})
}

func TestApplyCatalogExtensionJoins(t *testing.T) {
	t.Run("skips join when extension keys are empty", func(t *testing.T) {
		sql, args, err := applyCatalogExtensionJoins(
			sq.Select("f_id").From("t_catalog"),
			interfaces.CatalogsQueryParams{},
		).ToSql()

		require.NoError(t, err)
		assert.Equal(t, "SELECT f_id FROM t_catalog", sql)
		assert.Empty(t, args)
	})

	t.Run("applies extension joins", func(t *testing.T) {
		sql, args, err := applyCatalogExtensionJoins(
			sq.Select("t_catalog.f_id").From("t_catalog"),
			interfaces.CatalogsQueryParams{
				ExtensionKeys:   []string{"env"},
				ExtensionValues: []string{"prod"},
			},
		).ToSql()

		require.NoError(t, err)
		assert.Contains(t, sql, "JOIN t_entity_extension vex0")
		assert.Equal(t, []interface{}{entityextension.KindCatalog, "env", "prod"}, args)
	})
}

func TestCatalogListOrderExpr(t *testing.T) {
	t.Run("builds default and extension-safe order expression", func(t *testing.T) {
		assert.Equal(t, "f_update_time DESC", catalogListOrderExpr(interfaces.CatalogsQueryParams{PaginationQueryParams: interfaces.PaginationQueryParams{Direction: "DESC"}}))
		assert.Equal(t, "t_catalog.f_name ASC", catalogListOrderExpr(interfaces.CatalogsQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Sort: "f_name", Direction: "ASC"},
			ExtensionKeys:         []string{"env"},
		}))
	})
}

func TestAttachCatalogExtensions(t *testing.T) {
	t.Run("skips empty catalogs", func(t *testing.T) {
		err := attachCatalogExtensions(context.Background(), &common.AppSetting{}, interfaces.CatalogsQueryParams{IncludeExtensions: true}, nil)

		require.NoError(t, err)
	})

	t.Run("clears extensions when include extensions is false", func(t *testing.T) {
		catalogs := []*interfaces.Catalog{{ID: "catalog-1", Extensions: map[string]string{"env": "prod"}}}

		err := attachCatalogExtensions(context.Background(), &common.AppSetting{}, interfaces.CatalogsQueryParams{}, catalogs)

		require.NoError(t, err)
		assert.Nil(t, catalogs[0].Extensions)
	})
}

func TestAttachSingleCatalogExtensions(t *testing.T) {
	t.Run("skips nil catalog", func(t *testing.T) {
		err := attachSingleCatalogExtensions(context.Background(), &common.AppSetting{}, nil)

		require.NoError(t, err)
	})
}

func newCatalogAccessMock(t *testing.T) (*catalogAccess, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	return &catalogAccess{db: db, appSetting: &common.AppSetting{}}, mock, func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}
}

type fakeCatalogExtensionStore struct {
	byID       map[string]string
	byIDs      map[string]map[string]string
	deletedIDs []string
	err        error
}

func (s *fakeCatalogExtensionStore) DeleteByEntityIDs(ctx context.Context, kind string, entityIDs []string) error {
	s.deletedIDs = append([]string(nil), entityIDs...)
	return s.err
}

func (s *fakeCatalogExtensionStore) GetByEntityID(ctx context.Context, kind string, entityID string) (map[string]string, error) {
	return s.byID, s.err
}

func (s *fakeCatalogExtensionStore) GetByEntityIDs(ctx context.Context, kind string, entityIDs []string) (map[string]map[string]string, error) {
	return s.byIDs, s.err
}

func replaceCatalogExtensionStore(store *fakeCatalogExtensionStore) func() {
	patches := gomonkey.NewPatches()
	patches.ApplyFunc(entityextension.NewStore, func(app *common.AppSetting) *entityextension.Store {
		return &entityextension.Store{}
	})
	patches.ApplyMethod(&entityextension.Store{}, "DeleteByEntityIDs",
		func(_ *entityextension.Store, ctx context.Context, kind string, entityIDs []string) error {
			return store.DeleteByEntityIDs(ctx, kind, entityIDs)
		})
	patches.ApplyMethod(&entityextension.Store{}, "GetByEntityID",
		func(_ *entityextension.Store, ctx context.Context, kind string, entityID string) (map[string]string, error) {
			return store.GetByEntityID(ctx, kind, entityID)
		})
	patches.ApplyMethod(&entityextension.Store{}, "GetByEntityIDs",
		func(_ *entityextension.Store, ctx context.Context, kind string, entityIDs []string) (map[string]map[string]string, error) {
			return store.GetByEntityIDs(ctx, kind, entityIDs)
		})
	return patches.Reset
}

func sampleCatalog() *interfaces.Catalog {
	return &interfaces.Catalog{
		ID:                       "catalog-1",
		Name:                     "Catalog One",
		Tags:                     []string{"tag-a", "tag-b"},
		Description:              "desc",
		Type:                     interfaces.CatalogTypePhysical,
		Enabled:                  true,
		Internal:                 false,
		ConnectorType:            interfaces.ConnectorTypePostgreSQL,
		ConnectorCfg:             interfaces.ConnectorConfig{"host": "127.0.0.1"},
		Metadata:                 map[string]any{"region": "cn"},
		HealthCheckEnabled:       true,
		CatalogHealthCheckStatus: interfaces.CatalogHealthCheckStatus{HealthCheckStatus: interfaces.CatalogHealthStatusHealthy, LastCheckTime: 100, HealthCheckResult: "ok"},
		Creator:                  interfaces.AccountInfo{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
		CreateTime:               1,
		Updater:                  interfaces.AccountInfo{ID: "u2", Type: interfaces.ACCESSOR_TYPE_USER},
		UpdateTime:               2,
	}
}

func sampleCatalogWithID(id string) *interfaces.Catalog {
	catalog := sampleCatalog()
	catalog.ID = id
	return catalog
}

func catalogSelectSQL(where string) string {
	return "SELECT f_id, f_name, f_tags, f_description, f_type, f_enabled, f_internal, f_connector_type, f_connector_config, f_metadata, f_health_check_enabled, f_health_check_status, f_last_check_time, f_health_check_result, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_catalog WHERE " + where
}

func catalogRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"f_id",
		"f_name",
		"f_tags",
		"f_description",
		"f_type",
		"f_enabled",
		"f_internal",
		"f_connector_type",
		"f_connector_config",
		"f_metadata",
		"f_health_check_enabled",
		"f_health_check_status",
		"f_last_check_time",
		"f_health_check_result",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
	})
}

func catalogRowValues(catalog *interfaces.Catalog) []driver.Value {
	return []driver.Value{
		catalog.ID,
		catalog.Name,
		"tag-a,tag-b",
		catalog.Description,
		catalog.Type,
		catalog.Enabled,
		catalog.Internal,
		catalog.ConnectorType,
		`{"host":"127.0.0.1"}`,
		`{"region":"cn"}`,
		catalog.HealthCheckEnabled,
		catalog.HealthCheckStatus,
		catalog.LastCheckTime,
		catalog.HealthCheckResult,
		catalog.Creator.ID,
		catalog.Creator.Type,
		catalog.CreateTime,
		catalog.Updater.ID,
		catalog.Updater.Type,
		catalog.UpdateTime,
	}
}
