// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package sqlserver

import (
	"context"
	"net/url"
	"testing"

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
		"schemas":  []string{"dbo"},
	})
	require.NoError(t, err)
	got := connector.(*SQLServerConnector)
	assert.Equal(t, "sqlserver", got.config.Host)
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
		"connection timeout":     10,
		"app name":               "vega",
	}, connector.(*SQLServerConnector).config.Options)
	connectionURL, err = url.Parse(connector.(*SQLServerConnector).connectionString())
	require.NoError(t, err)
	assert.Equal(t, "true", connectionURL.Query().Get("encrypt"))
	assert.Equal(t, "false", connectionURL.Query().Get("trustservercertificate"))

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
	assert.Equal(t,
		"SELECT * FROM (SELECT id FROM dbo.orders) AS _raw_query_page ORDER BY (SELECT 1) OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY",
		connector.BuildPagedSQL("SELECT id FROM dbo.orders", 20, 10),
	)
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
	property := &interfaces.Property{Name: "status", OriginalName: "order_status"}
	condition := &filter_condition.EqualCond{
		Cfg:    &interfaces.FilterCondCfg{ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const}},
		Lfield: property,
		Value:  "paid",
	}

	converted, err := (&SQLServerConnector{}).convertFilterCondition(
		context.Background(), condition, map[string]*interfaces.Property{"status": property},
	)
	require.NoError(t, err)
	query, args, err := sq.StatementBuilder.PlaceholderFormat(sq.AtP).
		Select("*").From("[dbo].[orders]").Where(converted).ToSql()
	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM [dbo].[orders] WHERE [order_status] = @p1", query)
	assert.Equal(t, []any{"paid"}, args)
	assert.NotContains(t, query, "paid")
}
