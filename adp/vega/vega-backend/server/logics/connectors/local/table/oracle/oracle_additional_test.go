// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package oracle

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOracleConnectorValidateSchemas(t *testing.T) {
	t.Run("success case insensitive", func(t *testing.T) {
		connector, mock, cleanup := newOracleConnectorMock(t, []string{"app"})
		defer cleanup()

		mock.ExpectQuery("SELECT USERNAME FROM ALL_USERS").
			WillReturnRows(sqlmock.NewRows([]string{"USERNAME"}).AddRow("APP"))

		require.NoError(t, connector.validateSchemas(context.Background()))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		connector, mock, cleanup := newOracleConnectorMock(t, []string{"APP"})
		defer cleanup()

		mock.ExpectQuery("SELECT USERNAME FROM ALL_USERS").
			WillReturnError(errors.New("db down"))

		err := connector.validateSchemas(context.Background())

		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to list schemas")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing schema", func(t *testing.T) {
		connector, mock, cleanup := newOracleConnectorMock(t, []string{"MISSING"})
		defer cleanup()

		mock.ExpectQuery("SELECT USERNAME FROM ALL_USERS").
			WillReturnRows(sqlmock.NewRows([]string{"USERNAME"}).AddRow("APP"))

		err := connector.validateSchemas(context.Background())

		require.Error(t, err)
		assert.ErrorContains(t, err, "schemas not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestOracleConnectorListSchemas(t *testing.T) {
	connector, mock, cleanup := newOracleConnectorMock(t, nil)
	defer cleanup()
	connector.connected = true

	mock.ExpectQuery("SELECT USERNAME FROM ALL_USERS WHERE ORACLE_MAINTAINED = 'N' ORDER BY USERNAME").
		WillReturnRows(sqlmock.NewRows([]string{"USERNAME"}).AddRow("APP").AddRow("SYS").AddRow("AUDIT"))

	got, err := connector.ListSchemas(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{"APP", "AUDIT"}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOracleConnectorListTables(t *testing.T) {
	t.Run("filters configured schemas", func(t *testing.T) {
		connector, mock, cleanup := newOracleConnectorMock(t, []string{"app"})
		defer cleanup()
		connector.connected = true
		lastAnalyzed := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

		mock.ExpectQuery("SELECT OWNER,OBJECT_NAME AS TABLE_NAME,OBJECT_TYPE AS TABLE_TYPE,LAST_DDL_TIME AS LAST_ANALYZED FROM all_objects WHERE 1=1  AND OWNER IN \\('APP'\\)").
			WillReturnRows(sqlmock.NewRows([]string{"OWNER", "TABLE_NAME", "TABLE_TYPE", "LAST_ANALYZED"}).
				AddRow("APP", "ORDERS", "TABLE", sql.NullTime{Time: lastAnalyzed, Valid: true}))

		got, err := connector.ListTables(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "ORDERS", got[0].Name)
		assert.Equal(t, "APP", got[0].Database)
		assert.Equal(t, lastAnalyzed.UnixMilli(), got[0].Properties["last_analyzed"])
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		connector, mock, cleanup := newOracleConnectorMock(t, nil)
		defer cleanup()
		connector.connected = true

		mock.ExpectQuery("SELECT OWNER,OBJECT_NAME AS TABLE_NAME,OBJECT_TYPE AS TABLE_TYPE,LAST_DDL_TIME AS LAST_ANALYZED FROM all_objects WHERE 1=1 ").
			WillReturnError(errors.New("db down"))

		got, err := connector.ListTables(context.Background())

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "failed to list tables")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestOracleConnectorClose(t *testing.T) {
	connector := &OracleConnector{}
	require.NoError(t, connector.Close(context.Background()))

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	connector.db = db
	connector.connected = true

	mock.ExpectClose()
	require.NoError(t, connector.Close(context.Background()))
	assert.False(t, connector.connected)
	assert.Nil(t, connector.db)
	require.NoError(t, mock.ExpectationsWereMet())
}

func newOracleConnectorMock(t *testing.T, schemas []string) (*OracleConnector, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	return &OracleConnector{
			config:    &oracleConfig{Schemas: schemas},
			connected: true,
			db:        db,
		}, mock, func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
		}
}
