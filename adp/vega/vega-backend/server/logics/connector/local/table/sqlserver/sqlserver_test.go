// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.

package sqlserver

import (
	"context"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	sq "github.com/Masterminds/squirrel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func TestSQLServerConnectorNew(t *testing.T) {
	builder := &SQLServerConnector{}
	connector, err := builder.New(interfaces.ConnectorConfig{
		"host":     "sqlserver",
		"port":     1433,
		"username": "reader",
		"password": "secret",
		"database": "erp",
		"schemas":  []string{" dbo ", " reporting "},
	})
	require.NoError(t, err)
	got := connector.(*SQLServerConnector)
	assert.Equal(t, "sqlserver", got.config.Host)
	assert.Equal(t, []string{"dbo", "reporting"}, got.config.Schemas)
	connectionURL, err := url.Parse(got.connectionString())
	require.NoError(t, err)
	assert.Equal(t, "erp", connectionURL.Query().Get("database"))
	assert.False(t, connectionURL.Query().Has("encrypt"))
	assert.False(t, connectionURL.Query().Has("trustservercertificate"))

	connector, err = builder.New(interfaces.ConnectorConfig{
		"host":     "sqlserver",
		"port":     1433,
		"username": "reader",
		"password": "secret",
		"database": "erp",
		"options": map[string]any{
			"Encrypt":                true,
			"TrustServerCertificate": false,
			"HostNameInCertificate":  "db.internal",
			"Connection Timeout":     10,
			"App Name":               "vega",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"encrypt":                true,
		"trustservercertificate": false,
		"hostnameincertificate":  "db.internal",
		"connection timeout":     uint64(10),
		"app name":               "vega",
	}, connector.(*SQLServerConnector).config.Options)
	connectionURL, err = url.Parse(connector.(*SQLServerConnector).connectionString())
	require.NoError(t, err)
	assert.Equal(t, "true", connectionURL.Query().Get("encrypt"))
	assert.Equal(t, "false", connectionURL.Query().Get("trustservercertificate"))

	t.Run("builds IPv6 connection URL", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"host":     "2001:db8::1",
			"port":     1433,
			"username": "reader",
			"password": "secret",
			"database": "erp",
		})
		require.NoError(t, err)

		connectionURL, err := url.Parse(connector.(*SQLServerConnector).connectionString())
		require.NoError(t, err)
		assert.Equal(t, "2001:db8::1", connectionURL.Hostname())
		assert.Equal(t, "1433", connectionURL.Port())
	})

	invalidConfigs := []struct {
		name         string
		config       interfaces.ConnectorConfig
		errorContain string
	}{
		{name: "incomplete", config: interfaces.ConnectorConfig{"host": "sqlserver"}, errorContain: "config is incomplete"},
		{
			name: "invalid port",
			config: interfaces.ConnectorConfig{
				"host":     "sqlserver",
				"port":     -1,
				"username": "reader",
				"password": "secret",
				"database": "erp",
			},
			errorContain: "out of valid range",
		},
		{
			name: "duplicate schema",
			config: interfaces.ConnectorConfig{
				"host":     "sqlserver",
				"port":     1433,
				"username": "reader",
				"password": "secret",
				"database": "erp",
				"schemas":  []string{"dbo", "DBO"},
			},
			errorContain: "duplicate element found in schemas",
		},
		{
			name: "unknown option",
			config: interfaces.ConnectorConfig{
				"host":     "sqlserver",
				"port":     1433,
				"username": "reader",
				"password": "secret",
				"database": "erp",
				"options":  map[string]any{"packet size": 4096},
			},
			errorContain: "unsupported sqlserver option: packet size",
		},
		{
			name: "duplicate option ignores case",
			config: interfaces.ConnectorConfig{
				"host":     "sqlserver",
				"port":     1433,
				"username": "reader",
				"password": "secret",
				"database": "erp",
				"options":  map[string]any{"encrypt": true, "Encrypt": false},
			},
			errorContain: "duplicate sqlserver option: encrypt",
		},
		{
			name: "encrypt must be boolean",
			config: interfaces.ConnectorConfig{
				"host":     "sqlserver",
				"port":     1433,
				"username": "reader",
				"password": "secret",
				"database": "erp",
				"options":  map[string]any{"encrypt": "strict"},
			},
			errorContain: "sqlserver option encrypt must be a boolean",
		},
		{
			name: "trust server certificate must be boolean",
			config: interfaces.ConnectorConfig{
				"host":     "sqlserver",
				"port":     1433,
				"username": "reader",
				"password": "secret",
				"database": "erp",
				"options":  map[string]any{"trustservercertificate": "false"},
			},
			errorContain: "sqlserver option trustservercertificate must be a boolean",
		},
		{
			name: "host name in certificate must be string",
			config: interfaces.ConnectorConfig{
				"host":     "sqlserver",
				"port":     1433,
				"username": "reader",
				"password": "secret",
				"database": "erp",
				"options":  map[string]any{"hostnameincertificate": true},
			},
			errorContain: "sqlserver option hostnameincertificate must be a string",
		},
		{
			name: "app name must be string",
			config: interfaces.ConnectorConfig{
				"host":     "sqlserver",
				"port":     1433,
				"username": "reader",
				"password": "secret",
				"database": "erp",
				"options":  map[string]any{"app name": []string{"vega"}},
			},
			errorContain: "sqlserver option app name must be a string",
		},
		{
			name: "connection timeout must be integer",
			config: interfaces.ConnectorConfig{
				"host":     "sqlserver",
				"port":     1433,
				"username": "reader",
				"password": "secret",
				"database": "erp",
				"options":  map[string]any{"connection timeout": 1.5},
			},
			errorContain: "sqlserver option connection timeout must be a non-negative integer",
		},
		{
			name: "connection timeout must be non-negative",
			config: interfaces.ConnectorConfig{
				"host":     "sqlserver",
				"port":     1433,
				"username": "reader",
				"password": "secret",
				"database": "erp",
				"options":  map[string]any{"connection timeout": -1},
			},
			errorContain: "sqlserver option connection timeout must be a non-negative integer",
		},
	}
	for _, test := range invalidConfigs {
		t.Run(test.name, func(t *testing.T) {
			connector, err := builder.New(test.config)
			require.ErrorContains(t, err, test.errorContain)
			assert.Nil(t, connector)
		})
	}
}

func TestSQLServerConnectorBuildPagedSQL(t *testing.T) {
	connector := &SQLServerConnector{}
	t.Run("adds neutral order when query has no top-level order", func(t *testing.T) {
		query := connector.BuildPagedSQL("SELECT id FROM dbo.orders", 20, 10)
		assert.Equal(t,
			"SELECT id FROM dbo.orders\nORDER BY (SELECT 1) OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY",
			query,
		)
		assert.NotContains(t, strings.ToUpper(query), " LIMIT ")
	})
	t.Run("preserves top-level order", func(t *testing.T) {
		query := connector.BuildPagedSQL("SELECT id FROM dbo.orders ORDER BY id DESC", 20, 10)
		assert.Equal(t,
			"SELECT id FROM dbo.orders ORDER BY id DESC\nOFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY",
			query,
		)
		assert.NotContains(t, query, "FROM (SELECT")
	})
	t.Run("ignores nested and commented order tokens", func(t *testing.T) {
		query := connector.BuildPagedSQL(
			"SELECT id, (SELECT TOP 1 note FROM audit ORDER BY created_at) AS note FROM dbo.orders /* ORDER BY ignored */ ORDER BY id",
			0, 5,
		)
		assert.Equal(t,
			"SELECT id, (SELECT TOP 1 note FROM audit ORDER BY created_at) AS note FROM dbo.orders /* ORDER BY ignored */ ORDER BY id\nOFFSET 0 ROWS FETCH NEXT 5 ROWS ONLY",
			query,
		)
	})
	t.Run("rewrites literal top and keeps source order in the same scope", func(t *testing.T) {
		query := connector.BuildPagedSQL("SELECT TOP (20) t.id FROM dbo.orders t ORDER BY t.created_at", 5, 5)
		assert.Equal(t,
			"SELECT t.id FROM dbo.orders t ORDER BY t.created_at\nOFFSET 5 ROWS FETCH NEXT 5 ROWS ONLY",
			query,
		)
	})
	t.Run("caps page at literal top limit", func(t *testing.T) {
		query := connector.BuildPagedSQL("SELECT TOP 20 id FROM dbo.orders ORDER BY id", 15, 10)
		assert.Equal(t,
			"SELECT id FROM dbo.orders ORDER BY id\nOFFSET 15 ROWS FETCH NEXT 5 ROWS ONLY",
			query,
		)
	})
	t.Run("returns an empty top query after its limit", func(t *testing.T) {
		query := connector.BuildPagedSQL("SELECT TOP (20) id FROM dbo.orders ORDER BY id", 20, 5)
		assert.Equal(t, "SELECT TOP (0) id FROM dbo.orders ORDER BY id", query)
	})
	t.Run("places query option after paging", func(t *testing.T) {
		query := connector.BuildPagedSQL("SELECT id FROM dbo.orders ORDER BY id OPTION (RECOMPILE)", 0, 5)
		assert.Equal(t,
			"SELECT id FROM dbo.orders ORDER BY id\nOFFSET 0 ROWS FETCH NEXT 5 ROWS ONLY\nOPTION (RECOMPILE)",
			query,
		)
	})
}

func TestSQLServerConnectorBuildCountSQL(t *testing.T) {
	connector := &SQLServerConnector{}
	t.Run("removes presentation-only order", func(t *testing.T) {
		assert.Equal(t,
			"SELECT COUNT(*) AS _raw_query_total_count FROM (SELECT id FROM dbo.orders\n) AS _raw_query_total",
			connector.BuildCountSQL("SELECT id FROM dbo.orders ORDER BY id"),
		)
	})
	t.Run("retains top order because it selects the row set", func(t *testing.T) {
		assert.Equal(t,
			"SELECT COUNT(*) AS _raw_query_total_count FROM (SELECT TOP (20) id FROM dbo.orders ORDER BY id\n) AS _raw_query_total",
			connector.BuildCountSQL("SELECT TOP (20) id FROM dbo.orders ORDER BY id"),
		)
	})
	t.Run("moves query option to the outer count", func(t *testing.T) {
		assert.Equal(t,
			"SELECT COUNT(*) AS _raw_query_total_count FROM (SELECT id FROM dbo.orders\n) AS _raw_query_total\nOPTION (RECOMPILE)",
			connector.BuildCountSQL("SELECT id FROM dbo.orders ORDER BY id OPTION (RECOMPILE)"),
		)
	})
}

func TestSQLServerConnectorExecuteRawSQL(t *testing.T) {
	t.Run("preserves binary values", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
		connector := &SQLServerConnector{connected: true, db: db}
		binaryValue := []byte{0xff, 0xfe, 0x01}
		mock.ExpectQuery("SELECT payload FROM dbo.documents").WillReturnRows(
			sqlmock.NewRows([]string{"payload"}).AddRow(binaryValue),
		)

		result, err := connector.ExecuteRawSQL(context.Background(), "SELECT payload FROM dbo.documents")

		require.NoError(t, err)
		require.Len(t, result.Entries, 1)
		assert.Equal(t, binaryValue, result.Entries[0]["payload"])
	})
	t.Run("normalizes decimal and time values", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
		connector := &SQLServerConnector{connected: true, db: db}
		workTime := time.Date(1, time.January, 1, 9, 30, 0, 123000000, time.UTC)
		mock.ExpectQuery("SELECT salary, work_time FROM dbo.employees").WillReturnRows(
			sqlmock.NewRowsWithColumnDefinition(
				sqlmock.NewColumn("salary").OfType("DECIMAL", []byte{}),
				sqlmock.NewColumn("work_time").OfType("TIME", time.Time{}),
			).AddRow([]byte("18000.50"), workTime),
		)

		result, err := connector.ExecuteRawSQL(context.Background(), "SELECT salary, work_time FROM dbo.employees")

		require.NoError(t, err)
		require.Len(t, result.Entries, 1)
		assert.Equal(t, "18000.50", result.Entries[0]["salary"])
		assert.Equal(t, "09:30:00.123", result.Entries[0]["work_time"])
	})
}

func TestScanQueryRows(t *testing.T) {
	t.Run("preserves binary values", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
		binaryValue := []byte{0x00, 0xff, 0x7f}
		mock.ExpectQuery("SELECT payload FROM dbo.documents").WillReturnRows(
			sqlmock.NewRows([]string{"payload"}).AddRow(binaryValue),
		)
		rows, err := db.QueryContext(context.Background(), "SELECT payload FROM dbo.documents")
		require.NoError(t, err)
		defer func() { require.NoError(t, rows.Close()) }()

		result, err := scanQueryRows(rows)

		require.NoError(t, err)
		require.Len(t, result.Entries, 1)
		assert.Equal(t, binaryValue, result.Entries[0]["payload"])
	})
	t.Run("normalizes decimal and time values", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
		workTime := time.Date(1, time.January, 1, 8, 45, 0, 0, time.UTC)
		mock.ExpectQuery("SELECT salary, work_time FROM dbo.employees").WillReturnRows(
			sqlmock.NewRowsWithColumnDefinition(
				sqlmock.NewColumn("salary").OfType("numeric", []byte{}),
				sqlmock.NewColumn("work_time").OfType("time", time.Time{}),
			).AddRow([]byte("99.50"), workTime),
		)
		rows, err := db.QueryContext(context.Background(), "SELECT salary, work_time FROM dbo.employees")
		require.NoError(t, err)
		defer func() { require.NoError(t, rows.Close()) }()

		result, err := scanQueryRows(rows)

		require.NoError(t, err)
		require.Len(t, result.Entries, 1)
		assert.Equal(t, "99.50", result.Entries[0]["salary"])
		assert.Equal(t, "08:45:00", result.Entries[0]["work_time"])
	})
}

func TestQualifiedResourceTable(t *testing.T) {
	t.Run("quotes schema and preserves dots in table name", func(t *testing.T) {
		resource := &interfaces.Resource{
			Schema:           "sales data",
			SourceIdentifier: "sales data.Order.Archive]",
		}

		assert.Equal(t, "[sales data].[Order.Archive]]]", qualifiedResourceTable(resource))
	})
	t.Run("preserves dots in schema name", func(t *testing.T) {
		resource := &interfaces.Resource{
			Schema:           "sales.archive",
			SourceIdentifier: "sales.archive.orders",
		}

		assert.Equal(t, "[sales.archive].[orders]", qualifiedResourceTable(resource))
	})
}

func TestSQLServerConnectorListTables(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
	connector := &SQLServerConnector{config: &config{Database: "erp", Schemas: []string{"dbo"}}, connected: true, db: db}
	mock.ExpectQuery("SELECT s.name, o.name, o.type, COALESCE").WithArgs("dbo").WillReturnRows(
		sqlmock.NewRows([]string{"schema", "name", "type", "description"}).
			AddRow("dbo", "orders", "U", "Sales orders").
			AddRow("dbo", "order_view", "V", "Order summary"),
	)

	tables, err := connector.ListTables(context.Background())
	require.NoError(t, err)
	require.Len(t, tables, 2)
	assert.Equal(t, "table", tables[0].TableType)
	assert.Equal(t, "Sales orders", tables[0].Description)
	assert.Equal(t, "view", tables[1].TableType)
	assert.Equal(t, "Order summary", tables[1].Description)
	mock.ExpectClose()
	require.NoError(t, connector.Close(context.Background()))
}

func TestSQLServerConnectorGetTableMeta(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
	connector := &SQLServerConnector{connected: true, db: db}
	table := &interfaces.TableMeta{
		Name: "orders", Schema: "sales", Database: "erp", Properties: map[string]any{"existing": true},
	}

	mock.ExpectQuery("SELECT o.type, COALESCE").WithArgs("sales", "orders").WillReturnRows(
		sqlmock.NewRows([]string{"type", "description"}).AddRow("U", "Sales orders"),
	)
	mock.ExpectQuery("SELECT c.name, t.name, c.is_nullable").WithArgs("sales", "orders").WillReturnRows(
		sqlmock.NewRows([]string{
			"name", "type", "nullable", "char_max_len", "num_precision", "num_scale",
			"datetime_precision", "default", "description", "collation", "ordinal",
		}).
			AddRow("order_id", "int", false, 0, 10, 0, 0, "", "Order identifier", "", 1).
			AddRow("customer_id", "int", false, 0, 10, 0, 0, "", "", "", 2).
			AddRow("customer_name", "nvarchar", true, 100, 0, 0, 0, "(N'unknown')", "Customer name", "Chinese_PRC_CI_AS", 3).
			AddRow("amount", "decimal", false, 0, 18, 2, 0, "((0))", "Order amount", "", 4).
			AddRow("occurred_at", "datetime2", false, 0, 0, 0, 7, "(sysdatetime())", "", "", 5),
	)
	mock.ExpectQuery("SELECT i.name, i.is_unique, i.is_primary_key, col.name").WithArgs("sales", "orders").WillReturnRows(
		sqlmock.NewRows([]string{"name", "unique", "primary", "column"}).
			AddRow("PK_orders", true, true, "order_id").
			AddRow("UX_orders_customer_time", true, false, "customer_id").
			AddRow("UX_orders_customer_time", true, false, "occurred_at"),
	)
	mock.ExpectQuery("SELECT fk.name, parent_col.name, ref_schema.name").WithArgs("sales", "orders").WillReturnRows(
		sqlmock.NewRows([]string{"name", "column", "ref_schema", "ref_table", "ref_column", "on_delete", "on_update"}).
			AddRow("FK_orders_customer", "customer_id", "crm", "customers", "id", "NO_ACTION", "CASCADE"),
	)

	require.NoError(t, connector.GetTableMeta(context.Background(), table))
	assert.Equal(t, "table", table.TableType)
	assert.Equal(t, "Sales orders", table.Description)
	assert.Equal(t, map[string]any{"existing": true}, table.Properties)
	require.Len(t, table.Columns, 5)
	assert.Equal(t, "int", table.Columns[0].Type)
	assert.Equal(t, "Order identifier", table.Columns[0].Description)
	assert.Equal(t, "PRI", table.Columns[0].ColumnKey)
	assert.Equal(t, 100, table.Columns[2].CharMaxLen)
	assert.Equal(t, "(N'unknown')", table.Columns[2].DefaultValue)
	assert.Equal(t, "Chinese_PRC_CI_AS", table.Columns[2].Collation)
	assert.Equal(t, 18, table.Columns[3].NumPrecision)
	assert.Equal(t, 2, table.Columns[3].NumScale)
	assert.Equal(t, 7, table.Columns[4].DatetimePrecision)
	assert.Equal(t, []string{"order_id"}, table.PKs)
	require.Len(t, table.Indices, 2)
	assert.Equal(t, []string{"customer_id", "occurred_at"}, table.Indices[1].Columns)
	require.Len(t, table.ForeignKeys, 1)
	assert.Equal(t, "crm.customers", table.ForeignKeys[0].RefTable)
	assert.Equal(t, []string{"customer_id"}, table.ForeignKeys[0].Columns)
	assert.Equal(t, []string{"id"}, table.ForeignKeys[0].RefColumns)
	assert.Equal(t, "NO_ACTION", table.ForeignKeys[0].OnDelete)
	assert.Equal(t, "CASCADE", table.ForeignKeys[0].OnUpdate)
}

func TestSQLServerConnectorExecuteQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
	connector := &SQLServerConnector{connected: true, db: db}
	resource := &interfaces.Resource{
		SourceIdentifier: "dbo.orders",
		SchemaDefinition: []*interfaces.Property{{Name: "id", OriginalName: "order_id"}},
	}
	mock.ExpectQuery(`SELECT \[order_id\] FROM \[dbo\]\.\[orders\] ORDER BY \[order_id\] DESC OFFSET @p1 ROWS FETCH NEXT @p2 ROWS ONLY`).
		WithArgs(0, 10).
		WillReturnRows(sqlmock.NewRows([]string{"order_id"}).AddRow(1))

	result, err := connector.ExecuteQuery(context.Background(), resource, &interfaces.ResourceDataQueryParams{
		OutputFields: []string{"id"}, Sort: []*interfaces.SortField{{Field: "id", Direction: interfaces.DESC_DIRECTION}}, Limit: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"order_id": int64(1)}}, result.Entries)
	assert.Equal(t, int64(1), result.Total)
}

func TestSQLServerConnectorExecuteAggregateQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
	connector := &SQLServerConnector{connected: true, db: db}
	resource := &interfaces.Resource{
		SourceIdentifier: "sales.orders",
		SchemaDefinition: []*interfaces.Property{
			{Name: "customer", OriginalName: "customer_id"},
			{Name: "amount", OriginalName: "order_amount"},
		},
	}
	mock.ExpectQuery(
		`SELECT \[customer_id\], SUM\(\[order_amount\]\) AS \[total_amount\] FROM \[sales\]\.\[orders\] `+
			`GROUP BY \[customer_id\] HAVING SUM\(\[order_amount\]\) >= @p1 `+
			`ORDER BY \[total_amount\] DESC OFFSET @p2 ROWS FETCH NEXT @p3 ROWS ONLY`,
	).WithArgs(100, 0, 10).WillReturnRows(
		sqlmock.NewRows([]string{"customer_id", "total_amount"}).AddRow("c-1", 128),
	)

	result, err := connector.ExecuteQuery(context.Background(), resource, &interfaces.ResourceDataQueryParams{
		GroupBy:     []*interfaces.GroupByItem{{Property: "customer"}},
		Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "sum", Alias: "total_amount"},
		Having:      &interfaces.HavingClause{Field: "__value", Operation: ">=", Value: 100},
		Sort:        []*interfaces.SortField{{Field: "total_amount", Direction: interfaces.DESC_DIRECTION}},
		Limit:       10,
		NeedTotal:   true,
	})
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"customer_id": "c-1", "total_amount": int64(128)}}, result.Entries)
	assert.Equal(t, int64(1), result.Total)
}

func TestSQLServerConnectorConvertFilterCondition(t *testing.T) {
	constCfg := &interfaces.FilterCondCfg{ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const}}
	status := &interfaces.Property{Name: "status", OriginalName: "order_status", Type: interfaces.DataType_String}
	tags := &interfaces.Property{Name: "tags", OriginalName: "order_tags", Type: interfaces.DataType_String}
	createdAt := &interfaces.Property{Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime}
	workTime := &interfaces.Property{Name: "work_time", OriginalName: "work_time", Type: interfaces.DataType_Time}
	fields := map[string]*interfaces.Property{"status": status, "tags": tags, "created_at": createdAt, "work_time": workTime}
	tests := []struct {
		name        string
		condition   interfaces.FilterCondition
		containsSQL []string
		args        []any
	}{
		{
			name: "equal",
			condition: &filter_condition.EqualCond{
				Cfg: constCfg, Lfield: status, Value: "paid",
			},
			containsSQL: []string{"[order_status] = @p1"},
			args:        []any{"paid"},
		},
		{
			name: "contain",
			condition: &filter_condition.ContainCond{
				Cfg: constCfg, Lfield: tags, Value: []any{"priority", "paid"},
			},
			containsSQL: []string{"STRING_SPLIT(CAST([order_tags] AS nvarchar(max)), ',')", "_split.value = @p1", "_split.value = @p2", " AND "},
			args:        []any{"priority", "paid"},
		},
		{
			name: "not contain",
			condition: &filter_condition.NotContainCond{
				Cfg: constCfg, Lfield: tags, Value: []any{"priority", "paid"},
			},
			containsSQL: []string{"NOT EXISTS (SELECT 1 FROM STRING_SPLIT", ") OR NOT EXISTS (SELECT 1 FROM STRING_SPLIT"},
			args:        []any{"priority", "paid"},
		},
		{
			name: "prefix escapes wildcard",
			condition: &filter_condition.PrefixCond{
				Cfg: constCfg, Lfield: status, Value: "paid%",
			},
			containsSQL: []string{`[order_status] LIKE @p1 ESCAPE '\'`},
			args:        []any{`paid\%%`},
		},
		{
			name: "between datetime converts unix milliseconds",
			condition: &filter_condition.BetweenCond{
				Cfg: constCfg, Lfield: createdAt, Value: []any{float64(1000), float64(2000)},
			},
			containsSQL: []string{"[created_at] >= @p1", "[created_at] <= @p2"},
			args:        []any{time.UnixMilli(1000).UTC(), time.UnixMilli(2000).UTC()},
		},
		{
			name: "time value does not use unix milliseconds",
			condition: &filter_condition.EqualCond{
				Cfg: constCfg, Lfield: workTime, Value: "09:00:00",
			},
			containsSQL: []string{"[work_time] = @p1"},
			args:        []any{"09:00:00"},
		},
		{
			name:        "exist",
			condition:   &filter_condition.ExistCond{Cfg: constCfg, Lfield: status},
			containsSQL: []string{"[order_status] IS NOT NULL"},
		},
		{
			name: "before",
			condition: &filter_condition.BeforeCond{
				Cfg: constCfg, Lfield: createdAt, Value: []any{float64(2), "day"},
			},
			containsSQL: []string{"[created_at] < DATEADD(day, -@p1, SYSDATETIME())"},
			args:        []any{int64(2)},
		},
		{
			name: "current month",
			condition: &filter_condition.CurrentCond{
				Cfg: constCfg, Lfield: createdAt, Value: "month",
			},
			containsSQL: []string{"DATETRUNC(month, [created_at]) = DATETRUNC(month, SYSDATETIME())"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converted, err := (&SQLServerConnector{}).convertFilterCondition(context.Background(), test.condition, fields)
			require.NoError(t, err)
			query, args, err := sq.StatementBuilder.PlaceholderFormat(sq.AtP).
				Select("*").From("[dbo].[orders]").Where(converted).ToSql()
			require.NoError(t, err)
			for _, fragment := range test.containsSQL {
				assert.Contains(t, query, fragment)
			}
			assert.Equal(t, test.args, args)
		})
	}
	t.Run("rejects invalid unix milliseconds", func(t *testing.T) {
		_, err := (&SQLServerConnector{}).convertFilterCondition(context.Background(), &filter_condition.EqualCond{
			Cfg: constCfg, Lfield: createdAt, Value: "2026-08-03",
		}, fields)

		require.ErrorContains(t, err, "date condition value must be unix milliseconds")
	})
	t.Run("rejects before interval outside DATEADD range", func(t *testing.T) {
		_, err := (&SQLServerConnector{}).convertFilterCondition(context.Background(), &filter_condition.BeforeCond{
			Cfg: constCfg, Lfield: createdAt, Value: []any{float64(math.MaxInt32) + 1, "day"},
		}, fields)

		require.ErrorContains(t, err, "interval exceeds SQL Server DATEADD range")
	})
}

func TestBuildSQLServerProjection(t *testing.T) {
	createdAt := &interfaces.Property{Name: "created_at", OriginalName: "created_time", Type: interfaces.DataType_Datetime}
	fields := map[string]*interfaces.Property{"created_at": createdAt}
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{createdAt}}
	t.Run("builds calendar interval with datetrunc", func(t *testing.T) {
		projection, groupFields, _, _, aggregate, err := buildSQLServerProjection(resource, &interfaces.ResourceDataQueryParams{
			GroupBy: []*interfaces.GroupByItem{{Property: "created_at", CalendarInterval: "month"}},
		}, fields)

		require.NoError(t, err)
		assert.True(t, aggregate)
		assert.Equal(t, []string{"DATETRUNC(month, [created_time]) AS [created_at]"}, projection)
		assert.Equal(t, []string{"DATETRUNC(month, [created_time])"}, groupFields)
	})
	t.Run("adds count projection for count having", func(t *testing.T) {
		projection, _, alias, expression, aggregate, err := buildSQLServerProjection(resource, &interfaces.ResourceDataQueryParams{
			Having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 1},
		}, fields)

		require.NoError(t, err)
		assert.True(t, aggregate)
		assert.Equal(t, []string{"COUNT(*) AS [__value]"}, projection)
		assert.Equal(t, "__value", alias)
		assert.Equal(t, "COUNT(*)", expression)
	})
}

func TestBuildSQLServerHaving(t *testing.T) {
	t.Run("supports in", func(t *testing.T) {
		condition, err := buildSQLServerHaving(&interfaces.HavingClause{
			Field: "__value", Operation: "in", Value: []any{10, 20},
		}, "SUM([amount])")
		require.NoError(t, err)
		query, args, err := sq.StatementBuilder.PlaceholderFormat(sq.AtP).
			Select("customer_id").From("orders").GroupBy("customer_id").Having(condition).ToSql()

		require.NoError(t, err)
		assert.Contains(t, query, "HAVING SUM([amount]) IN (@p1,@p2)")
		assert.Equal(t, []any{10, 20}, args)
	})
}
