// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestMariaDBConnectorListTables(t *testing.T) {
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
		assert.Equal(t, "", tables[0].Properties["engine"])
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
	connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
	defer cleanup()
	mock.ExpectQuery("SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE").
		WithArgs("app", "orders").
		WillReturnRows(sqlmock.NewRows([]string{
			"COLUMN_NAME", "DATA_TYPE", "COLUMN_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT",
			"CHARACTER_MAXIMUM_LENGTH", "NUMERIC_PRECISION", "NUMERIC_SCALE", "DATETIME_PRECISION",
			"CHARACTER_SET_NAME", "COLLATION_NAME", "ORDINAL_POSITION", "COLUMN_KEY",
		}).AddRow("id", "int", "int(11)", "NO", nil, nil, nil, nil, nil, nil, nil, nil, 1, "PRI"))

	table := &interfaces.TableMeta{Database: "app", Name: "orders"}
	require.NoError(t, connector.fetchColumns(context.Background(), table))
	require.Len(t, table.Columns, 1)
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

func mariaDBTableRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"TABLE_SCHEMA", "TABLE_NAME", "TABLE_TYPE", "ENGINE", "TABLE_COLLATION",
		"TABLE_ROWS", "TABLE_COMMENT", "CREATE_TIME", "UPDATE_TIME", "DATA_LENGTH", "INDEX_LENGTH",
	})
}
