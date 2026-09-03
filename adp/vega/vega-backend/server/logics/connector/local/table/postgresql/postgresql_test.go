// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestPostgresqlConnectorMetadataAndConfig(t *testing.T) {
	t.Run("postgresql connector metadata and config", func(t *testing.T) {
		connector := &PostgresqlConnector{}

		assert.Equal(t, interfaces.ConnectorTypePostgreSQL, connector.GetType())
		assert.Equal(t, interfaces.ConnectorTypePostgreSQL, connector.GetName())
		assert.Equal(t, interfaces.ConnectorModeLocal, connector.GetMode())
		assert.Equal(t, interfaces.ConnectorCategoryTable, connector.GetCategory())
		assert.Equal(t, []string{"password"}, connector.GetSensitiveFields())
		assert.False(t, connector.GetEnabled())
		connector.SetEnabled(true)
		assert.True(t, connector.GetEnabled())

		fields := connector.GetFieldConfig()
		assert.Equal(t, map[string]interfaces.ConnectorFieldConfig{
			"host":     {Name: "主机地址", Type: "string", Description: "数据库服务器主机地址", Required: true, Encrypted: false},
			"port":     {Name: "端口号", Type: "integer", Description: "数据库服务器端口", Required: true, Encrypted: false},
			"username": {Name: "用户名", Type: "string", Description: "数据库用户名", Required: true, Encrypted: false},
			"password": {Name: "密码", Type: "string", Description: "数据库密码", Required: true, Encrypted: true},
			"database": {Name: "数据库名", Type: "string", Description: "PostgreSQL 连接目标 database", Required: true, Encrypted: false},
			"schemas":  {Name: "Schema 列表", Type: "array", Description: "可选；为空则扫描当前库下除系统 schema 外的用户 schema；非空则仅扫描列出的 schema", Required: false, Encrypted: false},
			"options":  {Name: "连接参数", Type: "object", Description: "连接参数（如 sslmode、connect_timeout 等）", Required: false, Encrypted: false},
		}, fields)
		require.Contains(t, fields, "password")
		assert.True(t, fields["password"].Encrypted)
		assert.True(t, fields["password"].Required)
		require.Contains(t, fields, "schemas")
		assert.False(t, fields["schemas"].Required)
	})
}

func TestPostgresqlConnectorNew(t *testing.T) {
	builder := &PostgresqlConnector{}

	t.Run("success", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"host":     "postgres",
			"port":     5432,
			"username": "user",
			"password": "secret",
			"database": "app",
			"schemas":  []string{"public"},
			"options":  map[string]any{"sslmode": "require"},
		})

		require.NoError(t, err)
		require.IsType(t, &PostgresqlConnector{}, connector)
		got := connector.(*PostgresqlConnector)
		assert.Equal(t, "postgres", got.config.Host)
		assert.Equal(t, "app", got.config.Database)
		assert.Equal(t, []string{"public"}, got.config.Schemas)
	})

	t.Run("rejects incomplete config", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{"host": "postgres"})

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "config is incomplete")
	})

	t.Run("rejects invalid port", func(t *testing.T) {
		connector, err := builder.New(validPostgresqlConfig(portMax + 1))

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "out of valid range")
	})

	t.Run("rejects long database name", func(t *testing.T) {
		cfg := validPostgresqlConfig(5432)
		cfg["database"] = strings.Repeat("a", databaseNameMaxLength+1)

		connector, err := builder.New(cfg)

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "database name exceeds")
	})

	t.Run("rejects long schema name", func(t *testing.T) {
		cfg := validPostgresqlConfig(5432)
		cfg["schemas"] = []string{strings.Repeat("a", databaseNameMaxLength+1)}

		connector, err := builder.New(cfg)

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "exceeds maximum length")
	})

	t.Run("rejects duplicate schemas", func(t *testing.T) {
		cfg := validPostgresqlConfig(5432)
		cfg["schemas"] = []string{"public", "public"}

		connector, err := builder.New(cfg)

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "duplicate element")
	})
}

func TestPostgresqlConnectorConnectionStringSupportsIPv6(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	connector := &PostgresqlConnector{config: &postgresqlConfig{
		Host: "2001:db8::1", Port: 5432, Username: "user", Password: "secret", Database: "app",
	}}

	connectionURL, err := url.Parse(connector.connectionString())
	require.NoError(t, err)
	assert.Equal(t, "2001:db8::1", connectionURL.Hostname())
	assert.Equal(t, "5432", connectionURL.Port())
	assert.Equal(t, "Asia/Shanghai", connectionURL.Query().Get("timezone"))
}

func TestPostgresqlConnectorValidateSchemas(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		connector, mock, cleanup := newPostgresqlConnectorMock(t, []string{"public", "audit"})
		defer cleanup()

		mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname = \$1\)`).
			WithArgs("public").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname = \$1\)`).
			WithArgs("audit").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		require.NoError(t, connector.validateSchemas(context.Background()))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		connector, mock, cleanup := newPostgresqlConnectorMock(t, []string{"public"})
		defer cleanup()

		mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname = \$1\)`).
			WithArgs("public").
			WillReturnError(errors.New("db down"))

		err := connector.validateSchemas(context.Background())

		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to validate schema")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("schema not found", func(t *testing.T) {
		connector, mock, cleanup := newPostgresqlConnectorMock(t, []string{"missing"})
		defer cleanup()

		mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname = \$1\)`).
			WithArgs("missing").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		err := connector.validateSchemas(context.Background())

		require.Error(t, err)
		assert.ErrorContains(t, err, "schema not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPostgresqlConnectorClose(t *testing.T) {
	t.Run("postgresql connector close", func(t *testing.T) {
		connector := &PostgresqlConnector{}
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
	})
}

func TestPostgresqlConnectorConnectDetectsCapabilities(t *testing.T) {
	db, mock, err := sqlmock.New(
		sqlmock.MonitorPingsOption(true),
		sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual),
	)
	require.NoError(t, err)
	mock.ExpectPing()
	mock.ExpectQuery(postgresqlLateralProbe).
		WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow(1))
	mock.ExpectQuery(postgresqlWithOrdinalityProbe).
		WillReturnError(errors.New("WITH ORDINALITY is unavailable"))
	patches := gomonkey.ApplyFunc(sql.Open, func(string, string) (*sql.DB, error) {
		return db, nil
	})
	t.Cleanup(patches.Reset)

	connector := &PostgresqlConnector{config: &postgresqlConfig{}}
	require.NoError(t, connector.Connect(context.Background()))
	assert.True(t, connector.connected)
	assert.Same(t, db, connector.db)
	assert.True(t, connector.compatibility.supportsLateral())
	assert.False(t, connector.compatibility.supportsWithOrdinality())

	mock.ExpectClose()
	require.NoError(t, connector.Close(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresqlConnectorGetTableMetaRejectsMissingTable(t *testing.T) {
	connector, mock, cleanup := newPostgresqlConnectorMock(t, nil)
	defer cleanup()
	connector.connected = true
	connector.compatibility = postgresqlCompatibility{}

	mock.ExpectQuery(`(?s)SELECT c\.relkind::text.*c\.relkind IN \('r', 'v', 'f', 'm', 'p'\)`).
		WithArgs("public", "deleted_orders").
		WillReturnError(sql.ErrNoRows)

	err := connector.GetTableMeta(context.Background(), &interfaces.TableMeta{Schema: "public", Name: "deleted_orders"})

	require.Error(t, err)
	assert.ErrorContains(t, err, "table metadata not found or inaccessible: public.deleted_orders")
	require.NoError(t, mock.ExpectationsWereMet())
}

func validPostgresqlConfig(port int) interfaces.ConnectorConfig {
	return interfaces.ConnectorConfig{
		"host":     "postgres",
		"port":     port,
		"username": "user",
		"password": "secret",
		"database": "app",
	}
}

func newPostgresqlConnectorMock(t *testing.T, schemas []string) (*PostgresqlConnector, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	return &PostgresqlConnector{
			config:        &postgresqlConfig{Schemas: schemas},
			db:            db,
			compatibility: postgresqlCompatibility{lateral: true, withOrdinality: true},
		}, mock, func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
		}
}
