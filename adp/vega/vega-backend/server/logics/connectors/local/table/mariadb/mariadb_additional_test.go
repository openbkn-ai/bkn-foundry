// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

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

func TestMariaDBConnectorMetadataAndConfig(t *testing.T) {
	connector := &MariaDBConnector{}

	assert.Equal(t, interfaces.ConnectorTypeMariaDB, connector.GetType())
	assert.Equal(t, interfaces.ConnectorTypeMariaDB, connector.GetName())
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
	require.Contains(t, fields, "databases")
	assert.False(t, fields["databases"].Required)
}

func TestMariaDBConnectorNew(t *testing.T) {
	builder := &MariaDBConnector{}

	t.Run("success", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"host":      "127.0.0.1",
			"port":      3306,
			"username":  "root",
			"password":  "secret",
			"databases": []string{"app"},
			"options":   map[string]any{"timeout": "5s"},
		})

		require.NoError(t, err)
		require.IsType(t, &MariaDBConnector{}, connector)
		got := connector.(*MariaDBConnector)
		assert.Equal(t, "127.0.0.1", got.config.Host)
		assert.Equal(t, []string{"app"}, got.config.Databases)
		assert.Equal(t, map[string]any{"timeout": "5s"}, got.config.Options)
	})

	t.Run("rejects incomplete config", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{"host": "127.0.0.1"})

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "config is incomplete")
	})

	t.Run("rejects invalid port", func(t *testing.T) {
		connector, err := builder.New(validMariaDBConfig(PORT_MAX + 1))

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "out of valid range")
	})

	t.Run("rejects long database name", func(t *testing.T) {
		cfg := validMariaDBConfig(3306)
		cfg["databases"] = []string{strings.Repeat("a", DATABASE_NAME_MAX_LENGTH+1)}

		connector, err := builder.New(cfg)

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "exceeds maximum length")
	})

	t.Run("rejects duplicate databases", func(t *testing.T) {
		cfg := validMariaDBConfig(3306)
		cfg["databases"] = []string{"app", "app"}

		connector, err := builder.New(cfg)

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "duplicate element")
	})
}

func TestMariaDBConnectorValidateDatabases(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app", "audit"})
		defer cleanup()

		mock.ExpectQuery("SHOW DATABASES").
			WillReturnRows(sqlmock.NewRows([]string{"Database"}).AddRow("app").AddRow("audit").AddRow("mysql"))

		require.NoError(t, connector.validateDatabases(context.Background()))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
		defer cleanup()

		mock.ExpectQuery("SHOW DATABASES").
			WillReturnError(errors.New("db down"))

		err := connector.validateDatabases(context.Background())

		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to list databases")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing configured database", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"missing"})
		defer cleanup()

		mock.ExpectQuery("SHOW DATABASES").
			WillReturnRows(sqlmock.NewRows([]string{"Database"}).AddRow("app"))

		err := connector.validateDatabases(context.Background())

		require.Error(t, err)
		assert.ErrorContains(t, err, "databases not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestMariaDBConnectorClose(t *testing.T) {
	connector := &MariaDBConnector{}
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

func validMariaDBConfig(port int) interfaces.ConnectorConfig {
	return interfaces.ConnectorConfig{
		"host":     "127.0.0.1",
		"port":     port,
		"username": "root",
		"password": "secret",
	}
}

func newMariaDBConnectorMock(t *testing.T, databases []string) (*MariaDBConnector, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	return &MariaDBConnector{
			config: &mariadbConfig{Databases: databases},
			db:     db,
		}, mock, func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
		}
}
