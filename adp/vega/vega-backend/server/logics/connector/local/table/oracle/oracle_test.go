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

func TestOracleConnectorBuildPagedSQL(t *testing.T) {
	connector := &OracleConnector{}
	assert.Equal(t,
		"SELECT * FROM (SELECT id FROM orders) _raw_query_page OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY",
		connector.BuildPagedSQL("SELECT id FROM orders", 20, 10),
	)
}

func TestOracleConnectorGetTableMetaRejectsMissingTable(t *testing.T) {
	connector, mock, cleanup := newOracleConnectorMock(t, nil)
	defer cleanup()
	connector.connected = true

	mock.ExpectQuery("FROM ALL_OBJECTS O").
		WithArgs("APP", "DELETED_ORDERS").
		WillReturnError(sql.ErrNoRows)

	err := connector.GetTableMeta(context.Background(), &interfaces.TableMeta{Database: "app", Name: "deleted_orders"})

	require.Error(t, err)
	assert.ErrorContains(t, err, "table metadata not found or inaccessible: app.deleted_orders")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOracleConnectorFetchTableStatusSupportsViews(t *testing.T) {
	connector, mock, cleanup := newOracleConnectorMock(t, nil)
	defer cleanup()

	mock.ExpectQuery("FROM ALL_OBJECTS O").
		WithArgs("APP", "ACTIVE_ORDERS").
		WillReturnRows(sqlmock.NewRows([]string{"OBJECT_TYPE", "NUM_ROWS", "COMMENTS", "LAST_ANALYZED"}).
			AddRow("VIEW", nil, "active orders", nil))

	table := &interfaces.TableMeta{Database: "app", Name: "active_orders"}
	require.NoError(t, connector.fetchTableStatus(context.Background(), table))
	assert.Equal(t, "view", table.TableType)
	assert.Equal(t, "active orders", table.Description)
	require.NoError(t, mock.ExpectationsWereMet())
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

		mock.ExpectQuery("SELECT OWNER,OBJECT_NAME AS TABLE_NAME,OBJECT_TYPE AS TABLE_TYPE,LAST_DDL_TIME AS LAST_ANALYZED FROM all_objects WHERE OBJECT_TYPE IN \\('TABLE', 'VIEW', 'MATERIALIZED VIEW'\\) AND OWNER IN \\('APP'\\)").
			WillReturnRows(sqlmock.NewRows([]string{"OWNER", "TABLE_NAME", "TABLE_TYPE", "LAST_ANALYZED"}).
				AddRow("APP", "ORDERS", "TABLE", sql.NullTime{Time: lastAnalyzed, Valid: true}).
				AddRow("APP", "ACTIVE_ORDERS", "VIEW", sql.NullTime{}))

		got, err := connector.ListTables(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "ORDERS", got[0].Name)
		assert.Equal(t, "table", got[0].TableType)
		assert.Equal(t, "APP", got[0].Database)
		assert.Equal(t, lastAnalyzed.UnixMilli(), got[0].Properties["last_analyzed"])
		assert.Equal(t, "view", got[1].TableType)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rejects null required metadata without exposing scan conversion error", func(t *testing.T) {
		connector, mock, cleanup := newOracleConnectorMock(t, nil)
		defer cleanup()

		mock.ExpectQuery("SELECT OWNER,OBJECT_NAME AS TABLE_NAME,OBJECT_TYPE AS TABLE_TYPE,LAST_DDL_TIME AS LAST_ANALYZED FROM all_objects WHERE OBJECT_TYPE IN \\('TABLE', 'VIEW', 'MATERIALIZED VIEW'\\)").
			WillReturnRows(sqlmock.NewRows([]string{"OWNER", "TABLE_NAME", "TABLE_TYPE", "LAST_ANALYZED"}).
				AddRow("APP", "ORDERS", nil, nil))

		got, err := connector.ListTables(context.Background())

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "required table metadata contains NULL")
		assert.NotContains(t, err.Error(), "converting NULL to string")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		connector, mock, cleanup := newOracleConnectorMock(t, nil)
		defer cleanup()
		connector.connected = true

		mock.ExpectQuery("SELECT OWNER,OBJECT_NAME AS TABLE_NAME,OBJECT_TYPE AS TABLE_TYPE,LAST_DDL_TIME AS LAST_ANALYZED FROM all_objects WHERE OBJECT_TYPE IN \\('TABLE', 'VIEW', 'MATERIALIZED VIEW'\\)").
			WillReturnError(errors.New("db down"))

		got, err := connector.ListTables(context.Background())

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "failed to list tables")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestOracleConnectorFetchColumns(t *testing.T) {
	connector, mock, cleanup := newOracleConnectorMock(t, []string{"APP"})
	defer cleanup()
	mock.ExpectQuery("FROM ALL_TAB_COLUMNS C").
		WithArgs("APP", "ORDERS").
		WillReturnRows(sqlmock.NewRows([]string{
			"COLUMN_NAME", "DATA_TYPE", "CHAR_LENGTH", "DATA_PRECISION", "DATA_SCALE", "NULLABLE",
			"DATA_DEFAULT", "COMMENTS", "CHARACTER_SET_NAME", "COLUMN_ID",
		}).AddRow("ID", "NUMBER", nil, nil, nil, "N", nil, nil, nil, 1))

	table := &interfaces.TableMeta{Database: "APP", Name: "ORDERS"}
	require.NoError(t, connector.fetchColumns(context.Background(), table))
	require.Len(t, table.Columns, 1)
	assert.Equal(t, "", table.Columns[0].DefaultValue)
	assert.Equal(t, "", table.Columns[0].Description)
	assert.Equal(t, "", table.Columns[0].Charset)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOracleConnectorGetMetadata(t *testing.T) {
	t.Run("returns scan error", func(t *testing.T) {
		connector, mock, cleanup := newOracleConnectorMock(t, nil)
		defer cleanup()
		mock.ExpectQuery("FROM V\\$PARAMETER").
			WillReturnRows(sqlmock.NewRows([]string{"NAME"}).AddRow("db_name"))

		metadata, err := connector.GetMetadata(context.Background())

		require.Error(t, err)
		assert.Nil(t, metadata)
		assert.ErrorContains(t, err, "failed to scan database metadata")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rejects null required name", func(t *testing.T) {
		connector, mock, cleanup := newOracleConnectorMock(t, nil)
		defer cleanup()
		mock.ExpectQuery("FROM V\\$PARAMETER").
			WillReturnRows(sqlmock.NewRows([]string{"NAME", "VALUE"}).AddRow(nil, "OPENBKN"))

		metadata, err := connector.GetMetadata(context.Background())

		require.Error(t, err)
		assert.Nil(t, metadata)
		assert.ErrorContains(t, err, "required database metadata contains NULL")
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

func TestOracleConnectorPing(t *testing.T) {
	t.Run("honors context deadline", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		connector := &OracleConnector{db: db, connected: true}
		mock.ExpectPing().WillDelayFor(time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		startedAt := time.Now()
		err = connector.Ping(ctx)

		require.Error(t, err)
		require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
		assert.Less(t, time.Since(startedAt), 500*time.Millisecond)
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
