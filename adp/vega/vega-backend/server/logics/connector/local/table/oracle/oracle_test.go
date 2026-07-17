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
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestOracleConnectorMetadata(t *testing.T) {
	t.Run("oracle connector metadata", func(t *testing.T) {
		connector := &OracleConnector{}

		assert.Equal(t, interfaces.ConnectorTypeOracle, connector.GetType())
		assert.Equal(t, interfaces.ConnectorTypeOracle, connector.GetName())
		assert.Equal(t, interfaces.ConnectorModeLocal, connector.GetMode())
		assert.Equal(t, interfaces.ConnectorCategoryTable, connector.GetCategory())
		assert.Equal(t, []string{"password"}, connector.GetSensitiveFields())

		assert.False(t, connector.GetEnabled())
		connector.SetEnabled(true)
		assert.True(t, connector.GetEnabled())

		fields := connector.GetFieldConfig()
		require.Contains(t, fields, "password")
		assert.True(t, fields["password"].Encrypted)
		assert.True(t, fields["password"].Required)
		require.Contains(t, fields, "schemas")
		assert.False(t, fields["schemas"].Required)
	})
}

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
	t.Run("nil db is no-op", func(t *testing.T) {
		connector := &OracleConnector{}

		require.NoError(t, connector.Close(context.Background()))
	})

	t.Run("closes db and clears state", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		connector := &OracleConnector{db: db, connected: true}

		mock.ExpectClose()

		require.NoError(t, connector.Close(context.Background()))
		assert.False(t, connector.connected)
		assert.Nil(t, connector.db)
		require.NoError(t, mock.ExpectationsWereMet())
	})
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

func TestOracleConnectorNew(t *testing.T) {
	builder := &OracleConnector{}

	t.Run("success", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"host":         "127.0.0.1",
			"port":         1521,
			"service_name": "ORCL",
			"username":     "system",
			"password":     "secret",
			"schemas":      []string{"APP"},
			"options":      map[string]any{"ssl": "false"},
		})

		require.NoError(t, err)
		require.IsType(t, &OracleConnector{}, connector)

		oracleConnector := connector.(*OracleConnector)
		require.NotNil(t, oracleConnector.config)
		assert.Equal(t, "127.0.0.1", oracleConnector.config.Host)
		assert.Equal(t, 1521, oracleConnector.config.Port)
		assert.Equal(t, "ORCL", oracleConnector.config.ServiceName)
		assert.Equal(t, []string{"APP"}, oracleConnector.config.Schemas)
	})

	t.Run("rejects incomplete config", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"host": "127.0.0.1",
			"port": 1521,
		})

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.Contains(t, err.Error(), "config is incomplete")
	})

	t.Run("rejects invalid port", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"host":         "127.0.0.1",
			"port":         PORT_MAX + 1,
			"service_name": "ORCL",
			"username":     "system",
			"password":     "secret",
		})

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.Contains(t, err.Error(), "out of valid range")
	})

	t.Run("rejects long schema name", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"host":         "127.0.0.1",
			"port":         1521,
			"service_name": "ORCL",
			"username":     "system",
			"password":     "secret",
			"schemas":      []string{strings.Repeat("A", SCHEMA_NAME_MAX_LENGTH+1)},
		})

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.Contains(t, err.Error(), "exceeds maximum length")
	})
}

func TestOracleConnectorMapType(t *testing.T) {
	connector := &OracleConnector{}

	tests := []struct {
		name       string
		nativeType string
		want       string
	}{
		{
			name:       "integer",
			nativeType: "integer",
			want:       "integer",
		},
		{
			name:       "decimal",
			nativeType: "number",
			want:       "decimal",
		},
		{
			name:       "datetime",
			nativeType: "timestamp with time zone",
			want:       "datetime",
		},
		{
			name:       "binary",
			nativeType: "blob",
			want:       "binary",
		},
		{
			name:       "unknown type",
			nativeType: "geometry",
			want:       "unsupported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, connector.MapType(tt.nativeType))
		})
	}
}
