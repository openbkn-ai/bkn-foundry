// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestMariaDBConnectorListTables(t *testing.T) {
	for _, tc := range []struct {
		name      string
		statistic driver.Value
		timestamp driver.Value
	}{
		{"NULL", nil, nil}, {"negative and zero time", int64(-1), time.Time{}}, {"zero retained", int64(0), nil},
	} {
		t.Run("valid properties "+tc.name, func(t *testing.T) {
			c, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
			defer cleanup()
			c.connected = true
			mock.ExpectQuery("SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE").WithArgs("app").WillReturnRows(mariaDBTableRows().AddRow("app", "orders", "BASE TABLE", "", "", tc.statistic, nil, tc.timestamp, tc.timestamp, tc.statistic, tc.statistic))
			tables, err := c.ListTables(context.Background())
			require.NoError(t, err)
			require.Len(t, tables, 1)
			want := map[string]any{}
			if tc.name == "zero retained" {
				want = map[string]any{"estimated_row_count": int64(0), "data_length": int64(0), "index_length": int64(0)}
			}
			assert.Equal(t, want, tables[0].Properties)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	t.Run("rejects scope before connecting", func(t *testing.T) {
		connector := &MariaDBConnector{config: &mariadbConfig{Databases: []string{"app"}}}
		connected := false
		patches := gomonkey.ApplyMethod(connector, "Connect", func(_ *MariaDBConnector, _ context.Context) error {
			connected = true
			return errors.New("unexpected connection attempt")
		})
		defer patches.Reset()

		tables, err := connector.listTables(context.Background(), "outside", "")
		require.ErrorContains(t, err, "scope")
		assert.Nil(t, tables)
		assert.False(t, connected)
	})
	t.Run("rejects database outside configured scope before querying", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
		defer cleanup()
		connector.connected = true

		tables, err := connector.listTables(context.Background(), "audit", "")

		require.ErrorContains(t, err, "outside the connector scope")
		assert.Nil(t, tables)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("allows explicit database with unrestricted scope", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, nil)
		defer cleanup()
		connector.connected = true
		mock.ExpectQuery("SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE").
			WithArgs("audit").WillReturnRows(mariaDBTableRows())

		tables, err := connector.listTables(context.Background(), "audit", "")

		require.NoError(t, err)
		assert.Empty(t, tables)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("accepts null optional metadata", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
		defer cleanup()
		connector.connected = true

		mock.ExpectQuery("SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE").
			WillReturnRows(mariaDBTableRows().AddRow("app", "orders", "BASE TABLE", nil, nil, nil, nil, nil, nil, nil, nil))

		tables, err := connector.ListTables(context.Background())

		require.NoError(t, err)
		require.Len(t, tables, 1)
		assert.Equal(t, "", tables[0].Description)
		assert.Empty(t, tables[0].Properties)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("maps view metadata type", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
		defer cleanup()
		connector.connected = true
		mock.ExpectQuery("SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE").
			WithArgs("app").WillReturnRows(
			mariaDBTableRows().AddRow("app", "orders_view", "VIEW", nil, nil, nil, nil, nil, nil, nil, nil),
		)

		tables, err := connector.ListTables(context.Background())

		require.NoError(t, err)
		require.Len(t, tables, 1)
		assert.Equal(t, "view", tables[0].TableType)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rejects null required metadata without exposing scan conversion error", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
		defer cleanup()
		connector.connected = true

		mock.ExpectQuery("SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE").
			WillReturnRows(mariaDBTableRows().AddRow(nil, "orders", "BASE TABLE", nil, nil, nil, nil, nil, nil, nil, nil))

		tables, err := connector.ListTables(context.Background())

		require.Error(t, err)
		assert.Nil(t, tables)
		assert.ErrorContains(t, err, "required table metadata contains NULL")
		assert.NotContains(t, err.Error(), "converting NULL to string")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestMariaDBConnectorFetchColumns(t *testing.T) {
	t.Run("query failure preserves cause and context", func(t *testing.T) {
		c, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
		defer cleanup()
		cause := errors.New("source unavailable")
		mock.ExpectQuery("SELECT COLUMN_NAME, COLUMN_TYPE").WithArgs("app", "orders").WillReturnError(cause)
		table := &interfaces.TableMeta{Database: "app", Name: "orders", Columns: []interfaces.TableColumnMeta{{Name: "old"}}}
		before := *table
		err := c.fetchColumns(context.Background(), table)
		require.ErrorIs(t, err, cause)
		assert.ErrorContains(t, err, "failed to execute column metadata query")
		assert.Equal(t, before, *table)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty column metadata fails without replacing existing columns", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
		defer cleanup()
		mock.ExpectQuery("SELECT COLUMN_NAME, COLUMN_TYPE").WithArgs("app", "orders").WillReturnRows(sqlmock.NewRows([]string{"column"})).RowsWillBeClosed()
		table := &interfaces.TableMeta{Database: "app", Schema: "app", Name: "orders", Columns: []interfaces.TableColumnMeta{{Name: "old"}}}
		require.ErrorContains(t, connector.fetchColumns(context.Background(), table), "column metadata is empty")
		assert.Equal(t, []interfaces.TableColumnMeta{{Name: "old"}}, table.Columns)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
	defer cleanup()
	mock.ExpectQuery("SELECT COLUMN_NAME, COLUMN_TYPE").
		WithArgs("app", "orders").
		WillReturnRows(sqlmock.NewRows([]string{
			"COLUMN_NAME", "COLUMN_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT",
			"CHARACTER_MAXIMUM_LENGTH", "NUMERIC_PRECISION", "NUMERIC_SCALE", "DATETIME_PRECISION",
			"CHARACTER_SET_NAME", "COLLATION_NAME", "COLUMN_KEY", "ORDINAL_POSITION",
		}).AddRow("id", " INT(11) ", "NO", nil, nil, nil, nil, nil, nil, nil, nil, "PRI", 1).
			AddRow("status", " enum('Active','Inactive') ", "YES", nil, nil, nil, nil, nil, nil, nil, nil, "", 2))

	table := &interfaces.TableMeta{Database: "app", Name: "orders"}
	require.NoError(t, connector.fetchColumns(context.Background(), table))
	require.Len(t, table.Columns, 2)
	assert.Equal(t, "int(11)", table.Columns[0].Type)
	assert.Equal(t, "enum('Active','Inactive')", table.Columns[1].Type)
	assert.Equal(t, interfaces.DataType_String, connector.MapType(table.Columns[1].Type))
	assert.Equal(t, "", table.Columns[0].DefaultValue)
	assert.Equal(t, "", table.Columns[0].Description)
	assert.Equal(t, "", table.Columns[0].Collation)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMariaDBConnectorGetMetadata(t *testing.T) {
	t.Run("returns scan error", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, nil)
		defer cleanup()
		connector.connected = true
		mock.ExpectQuery("SHOW GLOBAL VARIABLES").
			WillReturnRows(sqlmock.NewRows([]string{"Variable_name"}).AddRow("version"))

		metadata, err := connector.GetMetadata(context.Background())

		require.Error(t, err)
		assert.Nil(t, metadata)
		assert.ErrorContains(t, err, "failed to scan database metadata")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rejects null required name", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, nil)
		defer cleanup()
		connector.connected = true
		mock.ExpectQuery("SHOW GLOBAL VARIABLES").
			WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow(nil, "11.4.5"))

		metadata, err := connector.GetMetadata(context.Background())

		require.Error(t, err)
		assert.Nil(t, metadata)
		assert.ErrorContains(t, err, "required database metadata contains NULL")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestMariaDBConnectorListSchemas(t *testing.T) {
	t.Run("default scope includes only databases with discovered tables", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, nil)
		defer cleanup()
		connector.connected = true
		mock.ExpectQuery("SELECT DISTINCT TABLE_SCHEMA FROM information_schema\\.TABLES WHERE TABLE_SCHEMA NOT IN.*ORDER BY TABLE_SCHEMA").
			WithArgs("information_schema", "mariadb", "mysql", "performance_schema", "sys").
			WillReturnRows(sqlmock.NewRows([]string{"TABLE_SCHEMA"}).AddRow("app").AddRow("reporting"))

		schemas, err := connector.listSchemas(context.Background())

		require.NoError(t, err)
		assert.Equal(t, []string{"app", "reporting"}, schemas)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("configured scope retains existing empty databases", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app", "empty"})
		defer cleanup()
		connector.connected = true
		mock.ExpectQuery("SELECT SCHEMA_NAME FROM information_schema\\.SCHEMATA WHERE SCHEMA_NAME IN.*ORDER BY SCHEMA_NAME").
			WithArgs("app", "empty").
			WillReturnRows(sqlmock.NewRows([]string{"SCHEMA_NAME"}).AddRow("app").AddRow("empty"))

		schemas, err := connector.listSchemas(context.Background())

		require.NoError(t, err)
		assert.Equal(t, []string{"app", "empty"}, schemas)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("configured scope omits missing databases", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app", "missing"})
		defer cleanup()
		connector.connected = true
		mock.ExpectQuery("SELECT SCHEMA_NAME FROM information_schema\\.SCHEMATA WHERE SCHEMA_NAME IN.*ORDER BY SCHEMA_NAME").
			WithArgs("app", "missing").
			WillReturnRows(sqlmock.NewRows([]string{"SCHEMA_NAME"}).AddRow("app"))

		schemas, err := connector.listSchemas(context.Background())

		require.NoError(t, err)
		assert.Equal(t, []string{"app"}, schemas)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// mariaDBTableRows defines the exact column order returned by the table discovery query.
func mariaDBTableRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"TABLE_SCHEMA", "TABLE_NAME", "TABLE_TYPE", "ENGINE", "TABLE_COLLATION",
		"TABLE_ROWS", "TABLE_COMMENT", "CREATE_TIME", "UPDATE_TIME", "DATA_LENGTH", "INDEX_LENGTH",
	})
}

func TestMariaDBConnectorCountRows(t *testing.T) {
	for _, tc := range []struct {
		name, identifier string
		count            int64
		sourceErr        error
		wantErr          string
	}{
		{name: "quoted reference preserves component dots", identifier: "`sales`.`orders.part`", count: 42},
		{name: "escaped quote", identifier: "`sales`.`ord``ers`", count: 3},
		{name: "empty result", identifier: "`sales`.`orders.part`"},
		{name: "empty identifier", wantErr: "empty source identifier"},
		{name: "source error", identifier: "`sales`.`orders.part`", sourceErr: errors.New("source failed"), wantErr: "source failed"},
		{name: "cancelled query", identifier: "`sales`.`orders.part`", sourceErr: context.Canceled, wantErr: "context canceled"},
		{name: "negative count", identifier: "`sales`.`orders.part`", count: -1, wantErr: "invalid negative row count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			connector := &MariaDBConnector{db: db, connected: true}
			if tc.identifier != "" {
				expect := mock.ExpectQuery("^" + regexp.QuoteMeta("SELECT COUNT(*) FROM "+tc.identifier) + "$")
				if tc.sourceErr != nil {
					expect.WillReturnError(tc.sourceErr)
				} else {
					expect.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tc.count))
				}
			}
			count, err := connector.CountRows(context.Background(), tc.identifier)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.Zero(t, count)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.count, count)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestMariaDBConnectorDecodeTableMeta(t *testing.T) {
	for _, tc := range []struct {
		name, original       string
		meta                 *interfaces.TableMeta
		wantName, wantSchema string
		invalid              bool
	}{
		{"qualified legacy name", "sales.orders", &interfaces.TableMeta{}, "orders", "sales", false},
		{"resource schema already known", "orders", &interfaces.TableMeta{Schema: "sales"}, "orders", "sales", false},
		{"current names preserved", "wrong.legacy.extra", &interfaces.TableMeta{Name: "order.items", Schema: "sales.eu", Database: "existing"}, "order.items", "sales.eu", false},
		{"missing name", "", &interfaces.TableMeta{Schema: "sales"}, "", "", true},
		{"invalid legacy name", "sales.orders.extra", &interfaces.TableMeta{}, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &MariaDBConnector{config: &mariadbConfig{Databases: []string{"app"}}}
			metadata := map[string]any{"name": tc.meta.Name, "schema": tc.meta.Schema, "database": tc.meta.Database, "original_name": tc.original}
			meta, err := connector.DecodeTableMeta(metadata)
			if tc.invalid {
				require.Error(t, err)
				assert.Nil(t, meta)
				assert.False(t, connector.connected)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantName, meta.Name)
			assert.Equal(t, tc.wantSchema, meta.Schema)
			if tc.name == "current names preserved" {
				assert.Equal(t, "existing", meta.Database)
			} else {
				assert.Equal(t, "sales", meta.Database)
			}
			assert.False(t, connector.connected)
		})
	}
	for _, tc := range []struct {
		name        string
		metadata    map[string]any
		description string
		invalid     bool
	}{
		{"legacy description", map[string]any{"name": "orders", "schema": "sales", "original_description": "source comment", "properties": map[string]any{"row_count": int64(9007199254740993)}}, "source comment", false},
		{"empty current description takes precedence", map[string]any{"name": "orders", "schema": "sales", "description": "", "original_description": "old"}, "", false},
		{"invalid name type", map[string]any{"name": 7, "schema": "sales"}, "", true},
		{"missing metadata", nil, "", true},
		{"unserializable metadata", map[string]any{"name": "orders", "schema": "sales", "properties": map[string]any{"invalid": make(chan int)}}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &MariaDBConnector{config: &mariadbConfig{Databases: []string{"app"}}}
			meta, err := connector.DecodeTableMeta(tc.metadata)
			assert.False(t, connector.connected)
			if tc.invalid {
				require.Error(t, err)
				assert.Nil(t, meta)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.description, meta.Description)
			assert.NotContains(t, tc.metadata, "database")
			if tc.name == "legacy description" {
				assert.Equal(t, json.Number("9007199254740993"), meta.Properties["row_count"])
			}
		})
	}

}

func TestMariaDBConnectorFetchTableStatus(t *testing.T) {
	for _, tc := range []struct {
		name      string
		statistic driver.Value
		timestamp driver.Value
	}{
		{"NULL", nil, nil}, {"negative and zero time", int64(-1), time.Time{}}, {"zero retained", int64(0), nil},
	} {
		t.Run("valid properties "+tc.name, func(t *testing.T) {
			c, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
			defer cleanup()
			mock.ExpectQuery("SELECT TABLE_TYPE, AUTO_INCREMENT, ENGINE").WithArgs("app", "orders").WillReturnRows(sqlmock.NewRows([]string{"TABLE_TYPE", "AUTO_INCREMENT", "ENGINE", "TABLE_COLLATION", "TABLE_ROWS", "TABLE_COMMENT", "CREATE_TIME", "UPDATE_TIME", "DATA_LENGTH", "INDEX_LENGTH"}).AddRow("BASE TABLE", tc.statistic, "", "", tc.statistic, nil, tc.timestamp, tc.timestamp, tc.statistic, tc.statistic))
			previous := map[string]any{"legacy": true}
			table := &interfaces.TableMeta{Name: "orders", Database: "app", Schema: "app", Properties: previous}
			require.NoError(t, c.fetchTableStatus(context.Background(), table))
			want := map[string]any{}
			if tc.name == "zero retained" {
				want = map[string]any{"auto_increment": int64(0), "estimated_row_count": int64(0), "data_length": int64(0), "index_length": int64(0)}
			}
			assert.Equal(t, want, table.Properties)
			assert.Equal(t, map[string]any{"legacy": true}, previous)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	for _, tc := range []struct {
		name                           string
		populated, valid, queryFailure bool
		collation                      any
	}{
		{name: "NULL optional values clear previous values", populated: true},
		{name: "empty collation clears previous charset", populated: true, collation: ""},
		{name: "new optional values replace previous values", populated: true, valid: true, collation: "utf8mb4_general_ci"},
		{name: "nil properties are initialized", valid: true, collation: "utf8mb4_general_ci"},
		{name: "query failure preserves previous values", populated: true, queryFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
			defer cleanup()
			metadata := map[string]any{"name": "orders", "schema": "app", "database": "app", "description": "old comment"}
			if tc.populated {
				metadata["properties"] = map[string]any{"auto_increment": int64(12), "create_time": int64(10), "update_time": int64(11), "charset": "utf8", "custom": "kept"}
			}
			meta, err := connector.DecodeTableMeta(metadata)
			require.NoError(t, err)
			previousProperties := meta.Properties
			query := mock.ExpectQuery("SELECT TABLE_TYPE, AUTO_INCREMENT, ENGINE").WithArgs("app", "orders")
			if tc.queryFailure {
				query.WillReturnError(errors.New("query failed"))
			} else {
				var autoIncrement, createTime, updateTime any
				if tc.valid {
					autoIncrement, createTime, updateTime = int64(24), time.UnixMilli(1000), time.UnixMilli(2000)
				}
				query.WillReturnRows(sqlmock.NewRows([]string{"TABLE_TYPE", "AUTO_INCREMENT", "ENGINE", "TABLE_COLLATION", "TABLE_ROWS", "TABLE_COMMENT", "CREATE_TIME", "UPDATE_TIME", "DATA_LENGTH", "INDEX_LENGTH"}).AddRow("BASE TABLE", autoIncrement, "InnoDB", tc.collation, 0, "new comment", createTime, updateTime, 0, 0))
			}
			err = connector.fetchTableStatus(context.Background(), meta)
			require.NoError(t, mock.ExpectationsWereMet())
			if tc.queryFailure {
				require.ErrorContains(t, err, "query failed")
				assert.Equal(t, map[string]any{"auto_increment": json.Number("12"), "create_time": json.Number("10"), "update_time": json.Number("11"), "charset": "utf8", "custom": "kept"}, meta.Properties)
				assert.Equal(t, "old comment", meta.Description)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "new comment", meta.Description)
			assert.NotContains(t, meta.Properties, "custom")
			if tc.populated {
				assert.Equal(t, json.Number("12"), previousProperties["auto_increment"])
				assert.Equal(t, "kept", previousProperties["custom"])
			}
			if tc.valid {
				assert.Equal(t, int64(24), meta.Properties["auto_increment"])
				assert.Equal(t, int64(1000), meta.Properties["create_time"])
				assert.Equal(t, int64(2000), meta.Properties["update_time"])
				assert.Equal(t, "utf8mb4", meta.Properties["charset"])
			} else {
				for _, key := range []string{"auto_increment", "create_time", "update_time", "charset"} {
					assert.NotContains(t, meta.Properties, key)
				}
			}
		})
	}
}

func TestMariaDBConnectorGetTableMetaRejectsOutsideScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	connector := &MariaDBConnector{config: &mariadbConfig{Databases: []string{"app"}}, connected: true, db: db}
	connector.connected = false
	connected := false
	patches := gomonkey.ApplyMethod(connector, "Connect", func(_ *MariaDBConnector, _ context.Context) error {
		connected = true
		return errors.New("unexpected connection attempt")
	})
	defer patches.Reset()
	table := &interfaces.TableMeta{Database: "other", Schema: "other", Name: "orders"}
	err = connector.GetTableMeta(context.Background(), table)
	require.ErrorContains(t, err, "outside")
	assert.False(t, connected)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMariaDBConnectorListTablesFiltersOutsideScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	connector := &MariaDBConnector{config: &mariadbConfig{Databases: []string{"Sales"}}, connected: true, db: db}
	mock.ExpectQuery("SELECT").WithArgs("Sales").WillReturnRows(
		sqlmock.NewRows([]string{"c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8", "c9", "c10"}).AddRow("sales", "orders", "BASE TABLE", nil, nil, nil, nil, nil, nil, nil, nil).AddRow("Sales", "orders", "BASE TABLE", nil, nil, nil, nil, nil, nil, nil, nil))
	tables, err := connector.listTables(context.Background(), "", "")
	require.NoError(t, err)
	require.Len(t, tables, 1)
	assert.Equal(t, "Sales", tables[0].Database)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestMariaDBConnectorListSchemasFiltersOutsideScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	connector := &MariaDBConnector{config: &mariadbConfig{Databases: []string{"Sales"}}, connected: true, db: db}
	mock.ExpectQuery("SELECT").WithArgs("Sales").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("sales").AddRow("Sales"))
	schemas, err := connector.listSchemas(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"Sales"}, schemas)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMariaDBConnectorFetchIndexes(t *testing.T) {
	c, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
	defer cleanup()
	cause := errors.New("source unavailable")
	mock.ExpectQuery("SELECT INDEX_NAME, COLUMN_NAME").WithArgs("app", "orders").WillReturnError(cause)
	table := &interfaces.TableMeta{Database: "app", Name: "orders", Indices: []interfaces.TableIndexMeta{{Name: "old"}}}
	before := *table
	err := c.fetchIndexes(context.Background(), table)
	require.ErrorIs(t, err, cause)
	assert.ErrorContains(t, err, "failed to execute index metadata query")
	assert.Equal(t, before, *table)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMariaDBConnectorFetchForeignKeys(t *testing.T) {
	c, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
	defer cleanup()
	cause := errors.New("source unavailable")
	mock.ExpectQuery("SELECT CONSTRAINT_NAME, COLUMN_NAME").WithArgs("app", "orders").WillReturnError(cause)
	table := &interfaces.TableMeta{Database: "app", Name: "orders", ForeignKeys: []interfaces.TableForeignKeyMeta{{Name: "old"}}}
	before := *table
	err := c.fetchForeignKeys(context.Background(), table)
	require.ErrorIs(t, err, cause)
	assert.ErrorContains(t, err, "failed to execute foreign key metadata query")
	assert.Equal(t, before, *table)
	require.NoError(t, mock.ExpectationsWereMet())
}
