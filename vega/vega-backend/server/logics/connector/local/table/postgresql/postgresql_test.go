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
	"maps"
	"math"
	"net/url"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
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
			"host":     {Name: "Host", Type: "string", Description: "Database server host", Required: true, Encrypted: false},
			"port":     {Name: "Port", Type: "integer", Description: "Database server port", Required: true, Encrypted: false},
			"username": {Name: "Username", Type: "string", Description: "Database username", Required: true, Encrypted: false},
			"password": {Name: "Password", Type: "string", Description: "Database password", Required: true, Encrypted: true},
			"database": {Name: "Database", Type: "string", Description: "Target PostgreSQL database", Required: true, Encrypted: false},
			"schemas":  {Name: "Schemas", Type: "array", Description: "Optional schema names; leave empty to discover non-system schemas in the current database, or list schemas to restrict discovery", Required: false, Encrypted: false},
			"options":  {Name: "Connection options", Type: "object", Description: "Optional; only timezone, sslmode, connect_timeout, application_name, fallback_application_name, statement_timeout, lock_timeout and idle_in_transaction_session_timeout are supported. connect_timeout is a non-negative integer in seconds; session timeouts accept milliseconds or strings with PostgreSQL time units, and 0 disables them. sslmode defaults to disable; SSL files are unsupported. Client encoding uses the driver default UTF8 and cannot be configured. timezone: if omitted, use the Vega service TZ (default UTC); an empty or whitespace-only string omits the startup parameter and uses the database session default. Non-empty values are trimmed and validated by PostgreSQL during the connection test, without offset sign conversion.", Required: false, Encrypted: false},
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
		assert.Equal(t, map[string]any{"sslmode": "require"}, got.config.Options)
	})

	t.Run("rejects incomplete config", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{"host": "postgres"})

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "config is incomplete")
	})

	t.Run("rejects invalid port", func(t *testing.T) {
		connector, err := builder.New(validPostgresqlConfig(PORT_MAX + 1))

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "out of valid range")
	})

	t.Run("rejects long database name", func(t *testing.T) {
		cfg := validPostgresqlConfig(5432)
		cfg["database"] = strings.Repeat("a", DATABASE_NAME_MAX_LENGTH+1)

		connector, err := builder.New(cfg)

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.ErrorContains(t, err, "database name exceeds")
	})

	t.Run("rejects long schema name", func(t *testing.T) {
		cfg := validPostgresqlConfig(5432)
		cfg["schemas"] = []string{strings.Repeat("a", SCHEMA_NAME_MAX_LENGTH+1)}

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
	t.Run("rejects invalid option value", func(t *testing.T) {
		cfg := validPostgresqlConfig(5432)
		cfg["options"] = map[string]any{"connect_timeout": -1}
		connector, err := builder.New(cfg)
		require.ErrorContains(t, err, "connect_timeout")
		assert.Nil(t, connector)
	})

	t.Run("rejects unsupported option", func(t *testing.T) {
		cfg := validPostgresqlConfig(5432)
		cfg["options"] = map[string]any{"custom_option": "value"}
		connector, err := builder.New(cfg)
		require.ErrorContains(t, err, "unsupported postgresql option")
		assert.Nil(t, connector)
	})

}

func TestPostgresqlConnectorNormalizeOptions(t *testing.T) {
	connector := &PostgresqlConnector{}
	for _, tc := range []struct {
		name    string
		options map[string]any
		want    map[string]any
	}{
		{"omitted", nil, map[string]any{"sslmode": "disable"}},
		{"empty ssl mode", map[string]any{"sslmode": ""}, map[string]any{"sslmode": "disable"}},
		{"explicit values", map[string]any{"sslmode": "verify-full", "connect_timeout": 5, "application_name": "vega"}, map[string]any{"sslmode": "verify-full", "connect_timeout": 5, "application_name": "vega"}},
		{"session options", map[string]any{"statement_timeout": 1000}, map[string]any{"sslmode": "disable", "statement_timeout": 1000}},
		{"trimmed name", map[string]any{" application_name ": "vega"}, map[string]any{"sslmode": "disable", "application_name": "vega"}},
		{"trimmed application values", map[string]any{"application_name": "  vega query  ", "fallback_application_name": "\tvega fallback\n"}, map[string]any{"sslmode": "disable", "application_name": "vega query", "fallback_application_name": "vega fallback"}},
		{"blank application values", map[string]any{"application_name": " \t", "fallback_application_name": ""}, map[string]any{"sslmode": "disable", "application_name": "", "fallback_application_name": ""}},
		{"zero timeouts", map[string]any{"connect_timeout": 0, "statement_timeout": "0"}, map[string]any{"sslmode": "disable", "connect_timeout": 0, "statement_timeout": "0"}},
		{"session units", map[string]any{"statement_timeout": " 1min ", "lock_timeout": "1.5s", "idle_in_transaction_session_timeout": "2h"}, map[string]any{"sslmode": "disable", "statement_timeout": "1min", "lock_timeout": "1.5s", "idle_in_transaction_session_timeout": "2h"}},
		{"unit range validated by source", map[string]any{"idle_in_transaction_session_timeout": "25d"}, map[string]any{"sslmode": "disable", "idle_in_transaction_session_timeout": "25d"}},
		{"maximum session timeout", map[string]any{"statement_timeout": math.MaxInt32}, map[string]any{"sslmode": "disable", "statement_timeout": math.MaxInt32}},
		{"string connection timeout", map[string]any{"connect_timeout": "+005"}, map[string]any{"sslmode": "disable", "connect_timeout": "+005"}},
		{"maximum connection timeout", map[string]any{"connect_timeout": MAX_TIMEOUT_SECONDS}, map[string]any{"sslmode": "disable", "connect_timeout": MAX_TIMEOUT_SECONDS}},
		{"connection timeout whitespace", map[string]any{"connect_timeout": " 5 "}, map[string]any{"sslmode": "disable", "connect_timeout": "5"}},
		{"numeric connection timeout", map[string]any{"connect_timeout": float64(1000000)}, map[string]any{"sslmode": "disable", "connect_timeout": float64(1000000)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := make(map[string]any, len(tc.options))
			for name, value := range tc.options {
				before[name] = value
			}
			got, err := connector.normalizeOptions(tc.options)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			if tc.options != nil {
				assert.Equal(t, before, tc.options)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		options map[string]any
		wantErr string
	}{
		{"unknown option", map[string]any{"custom_option": "value"}, "unsupported"},
		{"invalid SSL mode", map[string]any{"sslmode": "invalid"}, "sslmode"},
		{"numeric SSL mode", map[string]any{"sslmode": 1}, "sslmode"},
		{"negative connection timeout", map[string]any{"connect_timeout": -1}, "connect_timeout"},
		{"fractional connection timeout", map[string]any{"connect_timeout": 1.5}, "connect_timeout"},
		{"connection timeout duration", map[string]any{"connect_timeout": "5s"}, "connect_timeout"},
		{"connection timeout overflow", map[string]any{"connect_timeout": uint64(math.MaxUint64)}, "connect_timeout"},
		{"connection duration overflow", map[string]any{"connect_timeout": MAX_TIMEOUT_SECONDS + 1}, "connect_timeout"},
		{"negative session timeout", map[string]any{"statement_timeout": "-1s"}, "statement_timeout"},
		{"session timeout overflow", map[string]any{"lock_timeout": int64(math.MaxInt32) + 1}, "lock_timeout"},
		{"millisecond string overflow", map[string]any{"statement_timeout": "2147483648ms"}, "statement_timeout"},
		{"unknown time unit", map[string]any{"statement_timeout": "1week"}, "statement_timeout"},
		{"compound duration", map[string]any{"statement_timeout": "1h30m"}, "statement_timeout"},
		{"numeric application name", map[string]any{"application_name": 1}, "application_name"},
		{"boolean fallback name", map[string]any{"fallback_application_name": true}, "fallback_application_name"},
		{"UTF8 encoding override", map[string]any{"client_encoding": "UTF8"}, "unsupported"},
		{"non-UTF8 encoding override", map[string]any{"client_encoding": "LATIN1"}, "unsupported"},
		{"empty encoding", map[string]any{"client_encoding": " "}, "client_encoding"},
		{"numeric encoding", map[string]any{"client_encoding": 1}, "client_encoding"},
		{"NaN session timeout", map[string]any{"statement_timeout": math.NaN()}, "statement_timeout"},
		{"infinite session timeout", map[string]any{"statement_timeout": math.Inf(1)}, "statement_timeout"},
		{"nil session timeout", map[string]any{"statement_timeout": nil}, "statement_timeout"},
		{"SSL certificate file", map[string]any{"sslcert": "client.crt"}, "unsupported"},
		{"SSL key file", map[string]any{"sslkey": "client.key"}, "unsupported"},
		{"SSL root certificate file", map[string]any{"sslrootcert": "ca.crt"}, "unsupported"},
		{"target override", map[string]any{"host": "other"}, "unsupported"},
		{"numeric time zone", map[string]any{"timezone": 8}, "timezone"},
		{"null time zone", map[string]any{"timezone": nil}, "timezone"},
		{"boolean time zone", map[string]any{"timezone": true}, "timezone"},
		{"MySQL parameter name", map[string]any{"time_zone": "+08:00"}, "unsupported"},
		{"duplicate option", map[string]any{"sslmode": "require", " sslmode ": "disable"}, "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := connector.normalizeOptions(tc.options)
			require.ErrorContains(t, err, tc.wantErr)
			assert.Nil(t, got)
		})
	}

	for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		t.Run("SSL mode "+mode, func(t *testing.T) {
			got, err := connector.normalizeOptions(map[string]any{"sslmode": mode})
			require.NoError(t, err)
			assert.Equal(t, mode, got["sslmode"])
		})
	}

}

func TestPostgresqlConnectorConnectionStringSupportsIPv6(t *testing.T) {
	t.Run("timezone distinguishes absence empty and native database strings", func(t *testing.T) {
		cases := []struct {
			name, server, want string
			options            map[string]any
			present            bool
		}{
			{"absent", "Asia/Shanghai", "Asia/Shanghai", nil, true},
			{"default UTC", "", "UTC", nil, true},
			{"empty", "Asia/Shanghai", "", map[string]any{"timezone": ""}, false},
			{"blank", "Asia/Shanghai", "", map[string]any{"timezone": "  "}, false},
			{"named", "UTC", "Asia/Shanghai", map[string]any{"timezone": "Asia/Shanghai"}, true},
			{"offset unchanged", "UTC", "+08:00", map[string]any{"timezone": "+08:00"}, true},
			{"POSIX unchanged", "UTC", "UTC-08:00", map[string]any{"timezone": "UTC-08:00"}, true},
			{"trimmed", "UTC", "Asia/Shanghai", map[string]any{"timezone": " Asia/Shanghai "}, true},
			{"database validates", "UTC", "Unknown/Zone", map[string]any{"timezone": "Unknown/Zone"}, true},
			{"URL encoding", "UTC", "zone&application_name=other", map[string]any{"timezone": "zone&application_name=other"}, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Setenv("TZ", tc.server)
				cfg := validPostgresqlConfig(5432)
				cfg["options"] = tc.options
				originalOptions := maps.Clone(tc.options)
				instance, err := NewPostgresqlConnector().New(cfg)
				require.NoError(t, err)
				parsed, err := url.Parse(instance.(*PostgresqlConnector).connectionString())
				require.NoError(t, err)
				assert.Equal(t, tc.present, parsed.Query().Has("timezone"))
				assert.Equal(t, tc.want, parsed.Query().Get("timezone"))
				assert.False(t, parsed.Query().Has("time_zone"))
				assert.False(t, parsed.Query().Has("application_name"))
				assert.Equal(t, originalOptions, cfg["options"])
			})
		}
	})
	t.Setenv("TZ", "Asia/Shanghai")
	connector := &PostgresqlConnector{config: &postgresqlConfig{
		Host: "2001:db8::1", Port: 5432, Username: "user", Password: "secret", Database: "app",
	}}

	connectionURL, err := url.Parse(connector.connectionString())
	require.NoError(t, err)
	assert.Equal(t, "2001:db8::1", connectionURL.Hostname())
	assert.Equal(t, "5432", connectionURL.Port())
	assert.Equal(t, "Asia/Shanghai", connectionURL.Query().Get("timezone"))

	t.Run("preserves timeout units when encoding options", func(t *testing.T) {
		options, err := connector.normalizeOptions(map[string]any{
			"connect_timeout":   float64(1000000),
			"statement_timeout": " 1min ",
			"lock_timeout":      "1.5s",
		})
		require.NoError(t, err)
		configured := &PostgresqlConnector{config: &postgresqlConfig{Host: "localhost", Port: 5432, Database: "app", Options: options}}
		dsn, err := url.Parse(configured.connectionString())
		require.NoError(t, err)
		assert.Equal(t, "1000000", dsn.Query().Get("connect_timeout"))
		assert.Equal(t, "1min", dsn.Query().Get("statement_timeout"))
		assert.Equal(t, "1.5s", dsn.Query().Get("lock_timeout"))
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

	err := connector.GetTableMeta(context.Background(), &interfaces.TableMeta{Schema: "public", Name: "deleted_orders"}, true)

	require.Error(t, err)
	assert.ErrorContains(t, err, "table metadata not found or inaccessible: public.deleted_orders")
	require.NoError(t, mock.ExpectationsWereMet())
}

// validPostgresqlConfig provides a valid configuration with the requested port.
func validPostgresqlConfig(port int) interfaces.ConnectorConfig {
	return interfaces.ConnectorConfig{
		"host":     "postgres",
		"port":     port,
		"username": "user",
		"password": "secret",
		"database": "app",
	}
}

// newPostgresqlConnectorMock creates a connected SQL mock with the requested schema scope.
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
