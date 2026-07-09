// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog

import (
	"context"
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
			Enabled:               &enabled,
			HealthCheckStatus:     interfaces.CatalogHealthStatusHealthy,
		}

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_catalog WHERE f_name LIKE ? AND f_tags LIKE ? AND f_type = ? AND f_enabled = ? AND f_health_check_status = ?")).
			WithArgs("%Catalog%", "%tag-a%", interfaces.CatalogTypePhysical, true, interfaces.CatalogHealthStatusHealthy).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_name, f_tags, f_description, f_type, f_enabled, f_internal, f_connector_type, f_connector_config, f_metadata, f_health_check_enabled, f_health_check_status, f_last_check_time, f_health_check_result, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_catalog WHERE f_name LIKE ? AND f_tags LIKE ? AND f_type = ? AND f_enabled = ? AND f_health_check_status = ? ORDER BY f_name ASC")).
			WithArgs("%Catalog%", "%tag-a%", interfaces.CatalogTypePhysical, true, interfaces.CatalogHealthStatusHealthy).
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

func TestCatalogExtensionHelpers(t *testing.T) {
	t.Run("maps columns and order expressions", func(t *testing.T) {
		assert.Equal(t, "f_id", catalogExtCol(interfaces.CatalogsQueryParams{}, "f_id"))
		assert.Equal(t, "t_catalog.f_id", catalogExtCol(interfaces.CatalogsQueryParams{ExtensionKeys: []string{"env"}}, "f_id"))
		assert.Equal(t, "f_update_time DESC", catalogListOrderExpr(interfaces.CatalogsQueryParams{PaginationQueryParams: interfaces.PaginationQueryParams{Direction: "DESC"}}))
		assert.Equal(t, "t_catalog.f_name ASC", catalogListOrderExpr(interfaces.CatalogsQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Sort: "f_name", Direction: "ASC"},
			ExtensionKeys:         []string{"env"},
		}))
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
