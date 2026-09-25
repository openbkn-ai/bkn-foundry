// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

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

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestMariaDBConnectorMetadataAndConfig(t *testing.T) {
	t.Run("maria dbconnector metadata and config", func(t *testing.T) {
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
		assert.Equal(t, map[string]interfaces.ConnectorFieldConfig{
			"host":      {Name: "主机地址", Type: "string", Description: "数据库服务器主机地址", Required: true, Encrypted: false},
			"port":      {Name: "端口号", Type: "integer", Description: "数据库服务器端口", Required: true, Encrypted: false},
			"username":  {Name: "用户名", Type: "string", Description: "数据库用户名", Required: true, Encrypted: false},
			"password":  {Name: "密码", Type: "string", Description: "数据库密码", Required: true, Encrypted: true},
			"databases": {Name: "数据库列表", Type: "array", Description: "数据库名称列表（可选，为空则连接实例级别）", Required: false, Encrypted: false},
			"options":   {Name: "连接参数", Type: "object", Description: "连接参数（如 charset, timeout 等）", Required: false, Encrypted: false},
		}, fields)
		require.Contains(t, fields, "password")
		assert.True(t, fields["password"].Encrypted)
		assert.True(t, fields["password"].Required)
		require.Contains(t, fields, "databases")
		assert.False(t, fields["databases"].Required)
	})
}

func TestMySQLConnectorMetadataAndConfig(t *testing.T) {
	connector := NewMySQLConnector()

	assert.Equal(t, interfaces.ConnectorTypeMySQL, connector.GetType())
	assert.Equal(t, interfaces.ConnectorTypeMySQL, connector.GetName())

	instance, err := connector.New(validMariaDBConfig(3306))

	require.NoError(t, err)
	assert.Equal(t, interfaces.ConnectorTypeMySQL, instance.GetType())
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

	t.Run("accepts supported connection options", func(t *testing.T) {
		cfg := validMariaDBConfig(3306)
		cfg["options"] = map[string]any{
			"charset": "utf8mb4", "collation": "utf8mb4_general_ci",
			"timeout": "5s", "readTimeout": "10s", "writeTimeout": "10s", "tls": "true",
		}

		connector, err := builder.New(cfg)

		require.NoError(t, err)
		maria := connector.(*MariaDBConnector)
		assert.Equal(t, cfg["options"], maria.config.Options)
		assert.Contains(t, maria.connectionString(), "timeout=5s")
		assert.Contains(t, maria.connectionString(), "readTimeout=10s")
		assert.Contains(t, maria.connectionString(), "writeTimeout=10s")
	})

	t.Run("normalizes boolean tls for driver", func(t *testing.T) {
		cfg := validMariaDBConfig(3306)
		cfg["options"] = map[string]any{"tls": true}

		connector, err := builder.New(cfg)

		require.NoError(t, err)
		maria := connector.(*MariaDBConnector)
		assert.Equal(t, "true", maria.config.Options["tls"])
		assert.Contains(t, maria.connectionString(), "tls=true")
	})

	t.Run("rejects unsupported connection option", func(t *testing.T) {
		cfg := validMariaDBConfig(3306)
		cfg["options"] = map[string]any{"unknown_option": "value"}

		connector, err := builder.New(cfg)

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "unsupported mariadb option")
		assert.ErrorContains(t, err, "unknown_option")
	})

	t.Run("rejects invalid connection option value", func(t *testing.T) {
		cfg := validMariaDBConfig(3306)
		cfg["options"] = map[string]any{"readTimeout": "-1s"}

		connector, err := builder.New(cfg)

		require.ErrorContains(t, err, "readTimeout")
		assert.Nil(t, connector)
	})

	t.Run("rejects numeric timeout without duration unit", func(t *testing.T) {
		cfg := validMariaDBConfig(3306)
		cfg["options"] = map[string]any{"timeout": float64(5)}

		connector, err := builder.New(cfg)

		require.ErrorContains(t, err, "timeout")
		assert.Nil(t, connector)
	})
}

func TestNormalizeOptions(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]any
		wantErr string
	}{
		{"empty", nil, ""},
		{"charset fallback", map[string]any{"charset": "utf8mb4,utf8"}, ""},
		{"collation", map[string]any{"collation": "utf8mb4_general_ci"}, ""},
		{"timeout", map[string]any{"timeout": "500ms"}, ""},
		{"read timeout", map[string]any{"readTimeout": "1.5s"}, ""},
		{"write timeout", map[string]any{"writeTimeout": "0s"}, ""},
		{"tls string", map[string]any{"tls": "true"}, ""},
		{"tls mode", map[string]any{"tls": "preferred"}, ""},
		{"empty charset", map[string]any{"charset": ""}, "charset"},
		{"non-string charset", map[string]any{"charset": 123}, "charset"},
		{"charset content passed through", map[string]any{"charset": "utf8mb4,"}, ""},
		{"empty collation", map[string]any{"collation": ""}, "collation"},
		{"collation content passed through", map[string]any{"collation": "custom-collation"}, ""},
		{"non-string collation", map[string]any{"collation": 123}, "collation"},
		{"invalid timeout", map[string]any{"timeout": "five seconds"}, "timeout"},
		{"unitless timeout", map[string]any{"timeout": "5"}, "timeout"},
		{"numeric timeout", map[string]any{"timeout": 5}, "timeout"},
		{"fractional timeout", map[string]any{"timeout": 1.5}, "timeout"},
		{"negative timeout", map[string]any{"timeout": "-1s"}, "timeout"},
		{"negative read timeout", map[string]any{"readTimeout": "-1s"}, "readTimeout"},
		{"unitless read timeout", map[string]any{"readTimeout": "5"}, "readTimeout"},
		{"non-string write timeout", map[string]any{"writeTimeout": 5}, "writeTimeout"},
		{"unitless write timeout", map[string]any{"writeTimeout": "5"}, "writeTimeout"},
		{"invalid tls mode", map[string]any{"tls": "unknown"}, "tls"},
		{"non-boolean tls", map[string]any{"tls": 1}, "tls"},
		{"unknown option", map[string]any{"unknown_option": "value"}, "unknown_option"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeOptions(tt.options)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			if tt.options == nil {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tt.options, got)
			}
		})
	}

	for _, tt := range []struct {
		name  string
		input bool
		want  string
	}{
		{"true", true, "true"},
		{"false", false, "false"},
	} {
		t.Run("boolean "+tt.name, func(t *testing.T) {
			got, err := normalizeOptions(map[string]any{"tls": tt.input})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got["tls"])
		})
	}
}

func TestMariaDBConnectorConnectionStringSupportsIPv6(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	connector := &MariaDBConnector{config: &mariadbConfig{
		Host: "2001:db8::1", Port: 3306, Username: "root", Password: "secret",
	}}

	connectionString := connector.connectionString()
	assert.Contains(t, connectionString, "@tcp([2001:db8::1]:3306)/")
	assert.Contains(t, connectionString, "loc=Asia%2FShanghai")
	assert.Contains(t, connectionString, "time_zone=%27Asia%2FShanghai%27")
}

func TestMariaDBConnectorValidateDatabases(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app", "audit"})
		defer cleanup()

		mock.ExpectQuery("SELECT SCHEMA_NAME FROM information_schema\\.SCHEMATA WHERE SCHEMA_NAME IN").
			WithArgs("app", "audit").
			WillReturnRows(sqlmock.NewRows([]string{"Database"}).AddRow("audit").AddRow("app"))

		require.NoError(t, connector.validateDatabases(context.Background()))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"app"})
		defer cleanup()

		mock.ExpectQuery("SELECT SCHEMA_NAME FROM information_schema\\.SCHEMATA WHERE SCHEMA_NAME IN").
			WithArgs("app").
			WillReturnError(errors.New("db down"))

		err := connector.validateDatabases(context.Background())

		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to list databases")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing configured database", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, []string{"missing"})
		defer cleanup()

		mock.ExpectQuery("SELECT SCHEMA_NAME FROM information_schema\\.SCHEMATA WHERE SCHEMA_NAME IN").
			WithArgs("missing").
			WillReturnRows(sqlmock.NewRows([]string{"Database"}))

		err := connector.validateDatabases(context.Background())

		require.Error(t, err)
		assert.ErrorContains(t, err, "databases not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty database scope skips query", func(t *testing.T) {
		connector, mock, cleanup := newMariaDBConnectorMock(t, nil)
		defer cleanup()
		require.NoError(t, connector.validateDatabases(context.Background()))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestMariaDBConnectorClose(t *testing.T) {
	t.Run("maria dbconnector close", func(t *testing.T) {
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
	})
}

func TestMariaDBConnectorPing(t *testing.T) {
	t.Run("honors context deadline", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		connector := &MariaDBConnector{db: db, connected: true}
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

func TestMariaDBConnectorGetTableMetaRejectsMissingTable(t *testing.T) {
	connector, mock, cleanup := newMariaDBConnectorMock(t, nil)
	defer cleanup()
	connector.connected = true

	mock.ExpectQuery("SELECT TABLE_TYPE").
		WithArgs("app", "deleted_orders").
		WillReturnError(sql.ErrNoRows)

	err := connector.GetTableMeta(context.Background(), &interfaces.TableMeta{Database: "app", Name: "deleted_orders"})

	require.Error(t, err)
	assert.ErrorContains(t, err, "table metadata not found or inaccessible: app.deleted_orders")
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
