// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package sqlserver

import (
	"context"
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
		"host": "sqlserver", "port": 1433, "username": "reader", "password": "secret", "database": "erp",
		"schemas": []string{"dbo"}, "options": map[string]any{"encrypt": true},
	})
	require.NoError(t, err)
	got := connector.(*SQLServerConnector)
	assert.Equal(t, "sqlserver", got.config.Host)
	assert.Contains(t, got.connectionString(), "database=erp")

	for _, cfg := range []interfaces.ConnectorConfig{
		{"host": "sqlserver"},
		{"host": "sqlserver", "port": 0, "username": "reader", "password": "secret", "database": "erp"},
		{"host": "sqlserver", "port": 1433, "username": "reader", "password": "secret", "database": "erp", "schemas": []string{"dbo", "DBO"}},
	} {
		connector, err := builder.New(cfg)
		require.Error(t, err)
		assert.Nil(t, connector)
	}
}

func TestSQLServerConnectorListTables(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
	connector := &SQLServerConnector{config: &config{Database: "erp", Schemas: []string{"dbo"}}, connected: true, db: db}
	mock.ExpectQuery("SELECT s.name, o.name, o.type").WithArgs("dbo").WillReturnRows(
		sqlmock.NewRows([]string{"schema", "name", "type"}).AddRow("dbo", "orders", "U").AddRow("dbo", "order_view", "V"),
	)

	tables, err := connector.ListTables(context.Background())
	require.NoError(t, err)
	require.Len(t, tables, 2)
	assert.Equal(t, "table", tables[0].TableType)
	assert.Equal(t, "view", tables[1].TableType)
	mock.ExpectClose()
	require.NoError(t, connector.Close(context.Background()))
}

func TestSQLServerConnectorMapType(t *testing.T) {
	connector := &SQLServerConnector{}
	assert.Equal(t, interfaces.DataType_Decimal, connector.MapType("DECIMAL"))
	assert.Equal(t, interfaces.DataType_Timestamp, connector.MapType("datetimeoffset"))
	assert.Equal(t, interfaces.DataType_Other, connector.MapType("geography"))
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
