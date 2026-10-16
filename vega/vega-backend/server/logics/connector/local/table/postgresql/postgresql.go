// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package postgresql provides a table connector for one database with optional schema discovery scope.
package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/mitchellh/mapstructure"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table"
)

const (
	// PORT_MIN is the lowest valid TCP port.
	PORT_MIN = 1
	// PORT_MAX is the highest valid TCP port.
	PORT_MAX = 65535
	// DATABASE_NAME_MAX_LENGTH is the maximum byte length accepted for a configured database name.
	DATABASE_NAME_MAX_LENGTH = 63
	// SCHEMA_NAME_MAX_LENGTH is the maximum byte length accepted for a configured schema name.
	SCHEMA_NAME_MAX_LENGTH = 63
	// MAX_TIMEOUT_SECONDS keeps the driver timeout within a time.Duration.
	MAX_TIMEOUT_SECONDS = int64(math.MaxInt64 / int64(time.Second))
)

var (
	sessionTimeoutPattern = regexp.MustCompile(`^\+?([0-9]+(?:\.[0-9]*)?|\.[0-9]+)\s*(us|ms|s|min|h|d)?$`)

	SYSTEM_SCHEMAS = []string{
		"information_schema",
		"pg_catalog",
		"pg_toast",
	}
	SYSTEM_SCHEMAS_MAP = map[string]bool{
		"information_schema": true,
		"pg_catalog":         true,
		"pg_toast":           true,
	}
	allowedOptions = map[string]struct{}{
		"sslmode":                             {},
		"connect_timeout":                     {},
		"application_name":                    {},
		"fallback_application_name":           {},
		"statement_timeout":                   {},
		"lock_timeout":                        {},
		"idle_in_transaction_session_timeout": {},
	}
)

type postgresqlConfig struct {
	Host     string         `mapstructure:"host"`
	Port     int            `mapstructure:"port"`
	Username string         `mapstructure:"username"`
	Password string         `mapstructure:"password"`
	Database string         `mapstructure:"database"`
	Schemas  []string       `mapstructure:"schemas"`
	Options  map[string]any `mapstructure:"options"`
}

// PostgresqlConnector implements the PostgreSQL table connector.
type PostgresqlConnector struct {
	enabled       bool
	config        *postgresqlConfig
	connected     bool
	db            *sql.DB
	compatibility postgresqlCompatibility
}

// NewPostgresqlConnector creates the PostgreSQL connector builder.
func NewPostgresqlConnector() interfaces.TableConnector {
	return &PostgresqlConnector{}
}

// ValidateBuildSchema adds no source-specific restrictions; the service validates common build-key rules.
func (c *PostgresqlConnector) ValidateBuildSchema(_ *interfaces.Resource) error {
	return nil
}

// GetType returns the PostgreSQL connector type.
func (c *PostgresqlConnector) GetType() string {
	return interfaces.ConnectorTypePostgreSQL
}

// GetName returns the connector name.
func (c *PostgresqlConnector) GetName() string {
	return interfaces.ConnectorTypePostgreSQL
}

// GetMode returns the local connector mode.
func (c *PostgresqlConnector) GetMode() string {
	return interfaces.ConnectorModeLocal
}

// GetCategory returns the table connector category.
func (c *PostgresqlConnector) GetCategory() string {
	return interfaces.ConnectorCategoryTable
}

// GetEnabled reports whether the connector is enabled.
func (c *PostgresqlConnector) GetEnabled() bool {
	return c.enabled
}

// SetEnabled updates the connector enabled state.
func (c *PostgresqlConnector) SetEnabled(enabled bool) {
	c.enabled = enabled
}

// GetSensitiveFields lists configuration fields that require encryption.
func (c *PostgresqlConnector) GetSensitiveFields() []string {
	return []string{"password"}
}

// GetFieldConfig returns connection form fields matching the connector-type migration.
func (c *PostgresqlConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return map[string]interfaces.ConnectorFieldConfig{
		"host":     {Name: "主机地址", Type: "string", Description: "数据库服务器主机地址", Required: true, Encrypted: false},
		"port":     {Name: "端口号", Type: "integer", Description: "数据库服务器端口", Required: true, Encrypted: false},
		"username": {Name: "用户名", Type: "string", Description: "数据库用户名", Required: true, Encrypted: false},
		"password": {Name: "密码", Type: "string", Description: "数据库密码", Required: true, Encrypted: true},
		"database": {Name: "数据库名", Type: "string", Description: "PostgreSQL 连接目标 database", Required: true, Encrypted: false},
		"schemas":  {Name: "Schema 列表", Type: "array", Description: "可选；为空则扫描当前库下除系统 schema 外的用户 schema；非空则仅扫描列出的 schema", Required: false, Encrypted: false},
		"options":  {Name: "i18n:VegaBackend.ConnectorType.PostgreSQL.Options.Name", Type: "object", Description: "i18n:VegaBackend.ConnectorType.PostgreSQL.Options.Description", Required: false, Encrypted: false},
	}
}

// New creates a connector instance from the configuration.
func (c *PostgresqlConnector) New(cfg interfaces.ConnectorConfig) (interfaces.Connector, error) {
	var pCfg postgresqlConfig
	if err := mapstructure.Decode(cfg, &pCfg); err != nil {
		return nil, fmt.Errorf("failed to decode postgresql config: %w", err)
	}

	if pCfg.Host == "" || pCfg.Port == 0 || pCfg.Username == "" || pCfg.Password == "" || pCfg.Database == "" {
		return nil, fmt.Errorf("postgresql connector config is incomplete")
	}

	if pCfg.Port < PORT_MIN || pCfg.Port > PORT_MAX {
		return nil, fmt.Errorf("port %d is out of valid range (%d-%d)", pCfg.Port, PORT_MIN, PORT_MAX)
	}

	if len(pCfg.Database) > DATABASE_NAME_MAX_LENGTH {
		return nil, fmt.Errorf("database name exceeds maximum length of %d characters", DATABASE_NAME_MAX_LENGTH)
	}

	seen := make(map[string]bool)
	for _, s := range pCfg.Schemas {
		if len(s) > SCHEMA_NAME_MAX_LENGTH {
			return nil, fmt.Errorf("schema name '%s' exceeds maximum length of %d characters", s, SCHEMA_NAME_MAX_LENGTH)
		}
		// Reject duplicate schema names using exact source spelling.
		if seen[s] {
			return nil, fmt.Errorf("duplicate element found in 'schemas': %s", s)
		}
		seen[s] = true
	}

	options, err := c.normalizeOptions(pCfg.Options)
	if err != nil {
		return nil, err
	}
	pCfg.Options = options

	return &PostgresqlConnector{
		config: &pCfg,
	}, nil
}

// normalizeOptions validates allowed options and trims text values without changing timeout units.
func (c *PostgresqlConnector) normalizeOptions(options map[string]any) (map[string]any, error) {
	normalized := make(map[string]any, len(options)+1)
	for key, value := range options {
		name := strings.TrimSpace(key)
		if _, allowed := allowedOptions[name]; !allowed {
			return nil, fmt.Errorf("unsupported postgresql option %q", key)
		}
		if _, duplicate := normalized[name]; duplicate {
			return nil, fmt.Errorf("duplicate postgresql option %q", name)
		}
		switch name {
		case "sslmode":
			mode, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("postgresql option sslmode must be a string")
			}
			mode = strings.TrimSpace(mode)
			switch mode {
			case "", "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
				value = mode
			default:
				return nil, fmt.Errorf("postgresql option sslmode must be disable, allow, prefer, require, verify-ca or verify-full")
			}
		case "connect_timeout":
			var seconds int64
			var ok bool
			if text, isString := value.(string); isString {
				var err error
				text = strings.TrimSpace(text)
				seconds, err = strconv.ParseInt(text, 10, 64)
				value = text
				ok = err == nil
			} else {
				seconds, ok = common.NumberAsInt64(value)
			}
			// lib/pq converts seconds to time.Duration; reject overflow before opening a connection.
			if !ok || seconds < 0 || seconds > MAX_TIMEOUT_SECONDS {
				return nil, fmt.Errorf("postgresql option connect_timeout must be a non-negative integer not greater than %d seconds", MAX_TIMEOUT_SECONDS)
			}
		case "statement_timeout", "lock_timeout", "idle_in_transaction_session_timeout":
			var amount float64
			var ok bool
			milliseconds := true
			if text, isString := value.(string); isString {
				text = strings.TrimSpace(text)
				match := sessionTimeoutPattern.FindStringSubmatch(text)
				if match == nil {
					return nil, fmt.Errorf("postgresql option %s must be a non-negative timeout in milliseconds or with a PostgreSQL time unit", name)
				}
				var err error
				amount, err = strconv.ParseFloat(match[1], 64)
				ok = err == nil
				milliseconds = match[2] == "" || match[2] == "ms"
				value = text
			} else {
				amount, ok = common.NumberAsFloat64(value)
			}
			// Validate millisecond values locally; PostgreSQL checks ranges for other units.
			if !ok || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || (milliseconds && amount > math.MaxInt32) {
				return nil, fmt.Errorf("postgresql option %s must be a non-negative finite timeout; millisecond values must not exceed %d", name, math.MaxInt32)
			}
		case "application_name", "fallback_application_name":
			applicationName, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("postgresql option %s must be a string", name)
			}
			value = strings.TrimSpace(applicationName)
		}
		normalized[name] = value
	}
	if mode, exists := normalized["sslmode"]; !exists || mode == "" {
		normalized["sslmode"] = "disable"
	}
	return normalized, nil
}

// connectionString builds the PostgreSQL connection URL from connector settings.
func (c *PostgresqlConnector) connectionString() string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.config.Username, c.config.Password),
		Host:   net.JoinHostPort(c.config.Host, strconv.Itoa(c.config.Port)),
		Path:   "/" + strings.TrimPrefix(c.config.Database, "/"),
	}
	q := u.Query()
	if c.config.Options != nil {
		for k, v := range c.config.Options {
			if integer, ok := common.NumberAsInt64(v); ok {
				// Encode integer options without scientific notation when serializing JSON numbers.
				q.Set(k, strconv.FormatInt(integer, 10))
			} else {
				q.Set(k, fmt.Sprintf("%v", v))
			}
		}
	}
	if q.Get("sslmode") == "" {
		q.Set("sslmode", "disable")
	}
	q.Set("timezone", table.ServerTimeZone())
	u.RawQuery = q.Encode()
	return u.String()
}

// Connect opens and verifies a pool, then detects metadata-query compatibility.
// Failed initialization closes the candidate pool before returning the error.
func (c *PostgresqlConnector) Connect(ctx context.Context) error {
	if c.connected {
		return nil
	}

	db, err := sql.Open("postgres", c.connectionString())
	if err != nil {
		return fmt.Errorf("failed to open PostgreSQL connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to ping PostgreSQL connection: %w", err)
	}

	compatibility := detectPostgresqlCompatibility(ctx, db)
	if ctxErr := ctx.Err(); ctxErr != nil {
		_ = db.Close()
		return fmt.Errorf("failed to detect PostgreSQL compatibility: %w", ctxErr)
	}

	c.db, c.compatibility, c.connected = db, compatibility, true
	return nil
}

// Close releases the connection pool and clears connection state.
func (c *PostgresqlConnector) Close(ctx context.Context) error {
	if c.db != nil {
		err := c.db.Close()
		c.connected = false
		c.db = nil
		if err != nil {
			return fmt.Errorf("failed to close PostgreSQL connection: %w", err)
		}
		return nil
	}
	return nil
}

// Ping connects if necessary and checks source availability.
func (c *PostgresqlConnector) Ping(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect for ping: %w", err)
	}
	if err := c.db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping PostgreSQL connection: %w", err)
	}
	return nil
}

// TestConnection verifies the connection and checks configured schemas.
func (c *PostgresqlConnector) TestConnection(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect for connection test: %w", err)
	}
	if len(c.config.Schemas) > 0 {
		if err := c.validateSchemas(ctx); err != nil {
			return fmt.Errorf("failed to validate configured schemas: %w", err)
		}
	}
	return nil
}

// validateSchemas checks that every configured schema exists in the database.
func (c *PostgresqlConnector) validateSchemas(ctx context.Context) error {
	for _, s := range c.config.Schemas {
		var exists bool
		err := c.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname = $1)`, s).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to validate schema %q: %w", s, err)
		}
		if !exists {
			return fmt.Errorf("schema not found: %s", s)
		}
	}
	return nil
}
