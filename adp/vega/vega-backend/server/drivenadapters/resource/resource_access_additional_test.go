package resource

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestResourceAccessGetByName(t *testing.T) {
	access, mock, cleanup := newResourceAccessMock(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_catalog_id, f_name, f_tags, f_description, f_category, f_status, f_status_message, f_last_discover_status, f_database, f_source_identifier, f_source_metadata, f_schema_definition, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_resource WHERE f_catalog_id = ? AND f_name = ?")).
		WithArgs("catalog-1", "orders").
		WillReturnRows(resourceNameRows().AddRow(resourceNameRowValues(sampleResource())...))

	got, err := access.GetByName(context.Background(), "catalog-1", "orders")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "resource-1", got.ID)
	assert.Equal(t, []string{"pii", "core"}, got.Tags)
	assert.Equal(t, "db1", got.Database)
	assert.Equal(t, "public.orders", got.SourceIdentifier)
	require.Len(t, got.SchemaDefinition, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResourceAccessGetByNameNotFound(t *testing.T) {
	access, mock, cleanup := newResourceAccessMock(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_catalog_id, f_name, f_tags, f_description, f_category, f_status, f_status_message, f_last_discover_status, f_database, f_source_identifier, f_source_metadata, f_schema_definition, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_resource WHERE f_catalog_id = ? AND f_name = ?")).
		WithArgs("catalog-1", "missing").
		WillReturnError(sql.ErrNoRows)

	got, err := access.GetByName(context.Background(), "catalog-1", "missing")

	require.NoError(t, err)
	assert.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResourceAccessGetByCatalogID(t *testing.T) {
	access, mock, cleanup := newResourceAccessMock(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_catalog_id, f_name, f_tags, f_description, f_category, f_status, f_status_message, f_last_discover_status, f_database, f_source_identifier, f_source_metadata, f_schema_definition, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_resource WHERE f_catalog_id = ?")).
		WithArgs("catalog-1").
		WillReturnRows(resourceNameRows().
			AddRow(resourceNameRowValues(sampleResource())...).
			AddRow(resourceNameRowValues(withResourceID(sampleResource(), "resource-2"))...))

	got, err := access.GetByCatalogID(context.Background(), "catalog-1")

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "resource-1", got[0].ID)
	assert.Equal(t, "resource-2", got[1].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResourceAccessList(t *testing.T) {
	access, mock, cleanup := newResourceAccessMock(t)
	defer cleanup()
	params := interfaces.ResourcesQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Sort: "f_name", Direction: "ASC"},
		Name:                  "order",
		CatalogID:             "catalog-1",
		Category:              interfaces.ResourceCategoryTable,
		Status:                interfaces.ResourceStatusActive,
		Database:              "db1",
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM t_resource WHERE f_name LIKE ? AND f_catalog_id = ? AND f_category = ? AND f_status = ? AND f_database = ?")).
		WithArgs("%order%", "catalog-1", interfaces.ResourceCategoryTable, interfaces.ResourceStatusActive, "db1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_catalog_id, f_name, f_tags, f_description, f_category, f_status, f_status_message, f_last_discover_status, f_database, f_source_identifier, f_source_metadata, f_schema_definition, f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time FROM t_resource WHERE f_name LIKE ? AND f_catalog_id = ? AND f_category = ? AND f_status = ? AND f_database = ? ORDER BY f_name ASC")).
		WithArgs("%order%", "catalog-1", interfaces.ResourceCategoryTable, interfaces.ResourceStatusActive, "db1").
		WillReturnRows(resourceNameRows().AddRow(resourceNameRowValues(sampleResource())...))

	got, total, err := access.List(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, "orders", got[0].Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResourceAccessUpdate(t *testing.T) {
	access, mock, cleanup := newResourceAccessMock(t)
	defer cleanup()
	res := sampleResource()
	res.LocalIndexName = "vega-build-resource-1-task-1"

	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_resource SET f_catalog_id = ?, f_name = ?, f_tags = ?, f_description = ?, f_source_metadata = ?, f_schema_definition = ?, f_logic_type = ?, f_logic_definition = ?, f_updater = ?, f_updater_type = ?, f_update_time = ?, f_local_index_name = ?, f_last_discover_status = ? WHERE f_id = ?")).
		WithArgs(
			res.CatalogID,
			res.Name,
			`"pii","core"`,
			res.Description,
			`{"properties":{"row_count":42}}`,
			`[{"name":"id","display_name":"","type":"integer","description":"","original_name":"","original_type":"","original_description":"","features":null,"attributes":null}]`,
			"",
			"[]",
			res.Updater.ID,
			res.Updater.Type,
			res.UpdateTime,
			res.LocalIndexName,
			res.LastDiscoverStatus,
			res.ID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, access.Update(context.Background(), res))
	require.NoError(t, mock.ExpectationsWereMet())
}

func withResourceID(resource *interfaces.Resource, id string) *interfaces.Resource {
	cp := *resource
	cp.ID = id
	return &cp
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
		"f_database",
		"f_source_identifier",
		"f_source_metadata",
		"f_schema_definition",
		"f_creator",
		"f_creator_type",
		"f_create_time",
		"f_updater",
		"f_updater_type",
		"f_update_time",
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
		resource.Database,
		resource.SourceIdentifier,
		`{"properties":{"row_count":42}}`,
		`[{"name":"id","type":"integer"}]`,
		resource.Creator.ID,
		resource.Creator.Type,
		resource.CreateTime,
		resource.Updater.ID,
		resource.Updater.Type,
		resource.UpdateTime,
	}
}
