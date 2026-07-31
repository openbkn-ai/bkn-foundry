// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package sqlserver

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
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
