// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestPostgresqlConnectorMetadataAndConfig(t *testing.T) {
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
	require.Contains(t, fields, "password")
	assert.True(t, fields["password"].Encrypted)
	assert.True(t, fields["password"].Required)
	require.Contains(t, fields, "schemas")
	assert.False(t, fields["schemas"].Required)
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
			config: &postgresqlConfig{Schemas: schemas},
			db:     db,
		}, mock, func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
		}
}
