// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package mariadb provides table connectors for MariaDB and MySQL.
package mariadb

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	_ "github.com/go-sql-driver/mysql"
	"github.com/mitchellh/mapstructure"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table"
)

const (
	// PORT_MIN is the lowest valid TCP port.
	PORT_MIN = 1
	// PORT_MAX is the highest valid TCP port.
	PORT_MAX = 65535
	// DATABASE_NAME_MAX_LENGTH is the maximum byte length accepted for a configured database name.
	DATABASE_NAME_MAX_LENGTH = 64
)

var (
	// SYSTEM_DBS are excluded from discovery when no databases are configured.
	SYSTEM_DBS = []string{
		"information_schema",
		"mariadb",
		"mysql",
		"performance_schema",
		"sys",
	}
	// allowedOptions lists the driver settings accepted by the connector.
	allowedOptions = map[string]struct{}{
		"charset":      {},
		"collation":    {},
		"timeout":      {},
		"readTimeout":  {},
		"writeTimeout": {},
		"tls":          {},
	}
)

// mariadbConfig holds the connection settings and optional database scope.
type mariadbConfig struct {
	Host      string         `mapstructure:"host"`
	Port      int            `mapstructure:"port"`
	Username  string         `mapstructure:"username"`
	Password  string         `mapstructure:"password"`
	Databases []string       `mapstructure:"databases"`
	Options   map[string]any `mapstructure:"options"`
}

// MariaDBConnector implements TableConnector for MariaDB and MySQL.
type MariaDBConnector struct {
	connectorType string
	enabled       bool
	config        *mariadbConfig
	connected     bool
	db            *sql.DB
}

// NewMariaDBConnector creates the MariaDB connector builder.
func NewMariaDBConnector() interfaces.TableConnector {
	return &MariaDBConnector{}
}

// NewMySQLConnector creates an independent MySQL connector builder backed by
// the MariaDB-compatible implementation.
func NewMySQLConnector() interfaces.TableConnector {
	return &MariaDBConnector{connectorType: interfaces.ConnectorTypeMySQL}
}

// GetType returns the data source type.
func (c *MariaDBConnector) GetType() string {
	if c.connectorType != "" {
		return c.connectorType
	}
	return interfaces.ConnectorTypeMariaDB
}

// GetName returns the connector name.
func (c *MariaDBConnector) GetName() string {
	return c.GetType()
}

// GetMode returns the connector mode.
func (c *MariaDBConnector) GetMode() string {
	return interfaces.ConnectorModeLocal
}

// GetCategory returns the connector category.
func (c *MariaDBConnector) GetCategory() string {
	return interfaces.ConnectorCategoryTable
}

// GetEnabled returns the enabled status.
func (c *MariaDBConnector) GetEnabled() bool {
	return c.enabled
}

// SetEnabled sets the enabled status.
func (c *MariaDBConnector) SetEnabled(enabled bool) {
	c.enabled = enabled
}

// GetSensitiveFields identifies the credential fields that must be protected.
func (c *MariaDBConnector) GetSensitiveFields() []string {
	return []string{"password"}
}

// GetFieldConfig describes the fields shared by the MariaDB and MySQL connectors.
func (c *MariaDBConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return map[string]interfaces.ConnectorFieldConfig{
		"host":      {Name: "主机地址", Type: "string", Description: "数据库服务器主机地址", Required: true, Encrypted: false},
		"port":      {Name: "端口号", Type: "integer", Description: "数据库服务器端口", Required: true, Encrypted: false},
		"username":  {Name: "用户名", Type: "string", Description: "数据库用户名", Required: true, Encrypted: false},
		"password":  {Name: "密码", Type: "string", Description: "数据库密码", Required: true, Encrypted: true},
		"databases": {Name: "数据库列表", Type: "array", Description: "数据库名称列表（可选，为空则连接实例级别）", Required: false, Encrypted: false},
		"options":   {Name: "连接参数", Type: "object", Description: "连接参数（如 charset, timeout 等）", Required: false, Encrypted: false},
	}
}

// New validates MariaDB settings and creates a connector with an optional database scope.
func (c *MariaDBConnector) New(cfg interfaces.ConnectorConfig) (interfaces.Connector, error) {
	var mCfg mariadbConfig
	if err := mapstructure.Decode(cfg, &mCfg); err != nil {
		return nil, fmt.Errorf("failed to decode mariadb config: %w", err)
	}

	mCfg.Host = strings.TrimSpace(mCfg.Host)
	mCfg.Username = strings.TrimSpace(mCfg.Username)
	if mCfg.Host == "" || mCfg.Username == "" || mCfg.Password == "" {
		return nil, fmt.Errorf("mariadb connector config is incomplete")
	}

	// Verify the TCP port range.
	if mCfg.Port < PORT_MIN || mCfg.Port > PORT_MAX {
		return nil, fmt.Errorf("port %d is out of valid range (%d-%d)", mCfg.Port, PORT_MIN, PORT_MAX)
	}

	seen := make(map[string]struct{})
	for idx, db := range mCfg.Databases {
		// Trim each database name before applying the connector's byte limit.
		db = strings.TrimSpace(db)
		if db == "" || len(db) > DATABASE_NAME_MAX_LENGTH {
			return nil, fmt.Errorf("database name '%s' exceeds maximum length of %d characters", db, DATABASE_NAME_MAX_LENGTH)
		}
		// Reject duplicate database names after trimming.
		if _, exists := seen[db]; exists {
			return nil, fmt.Errorf("duplicate element found in 'databases': %s", db)
		}
		seen[db] = struct{}{}
		mCfg.Databases[idx] = db
	}
	options, err := normalizeOptions(mCfg.Options)
	if err != nil {
		return nil, err
	}
	mCfg.Options = options

	return &MariaDBConnector{
		connectorType: c.GetType(),
		config:        &mCfg,
	}, nil
}

// normalizeOptions validates supported driver options and converts their values to DSN strings.
func normalizeOptions(options map[string]any) (map[string]any, error) {
	normalized := make(map[string]any, len(options))
	for key, value := range options {
		name := strings.TrimSpace(key)
		if _, allowed := allowedOptions[name]; !allowed {
			return nil, fmt.Errorf("unsupported mariadb option %q", name)
		}
		if _, duplicate := normalized[name]; duplicate {
			return nil, fmt.Errorf("duplicate mariadb option %q", name)
		}
		switch name {
		case "charset", "collation":
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("mariadb option %q must be a non-empty string", name)
			}
			value = strings.TrimSpace(text)
		case "timeout", "readTimeout", "writeTimeout":
			duration, ok := value.(string)
			if !ok || strings.TrimSpace(duration) == "" {
				return nil, fmt.Errorf("mariadb option %q must be a non-negative duration", name)
			}
			value = strings.TrimSpace(duration)
			parsed, err := time.ParseDuration(duration)
			if err != nil || parsed < 0 {
				return nil, fmt.Errorf("mariadb option %q must be a non-negative duration", name)
			}
		case "tls":
			switch tls := value.(type) {
			case bool:
				value = strconv.FormatBool(tls)
			case string:
				value = strings.TrimSpace(tls)
				if value != "true" && value != "false" && value != "skip-verify" && value != "preferred" {
					return nil, fmt.Errorf("mariadb option %q must be true, false, skip-verify, or preferred", name)
				}
			default:
				return nil, fmt.Errorf("mariadb option %q must be a boolean or supported TLS mode", name)
			}
		}
		normalized[name] = value
	}
	return normalized, nil
}

// connectionString builds a driver DSN without selecting a default database.
func (c *MariaDBConnector) connectionString() string {
	values := url.Values{}
	values.Set("charset", "utf8mb4")
	values.Set("parseTime", "true")

	// Apply validated options before setting the server time zone.
	for k, v := range c.config.Options {
		values.Set(k, v.(string))
	}
	values.Set("loc", table.ServerTimeZone())
	values.Set("time_zone", "'"+table.ServerTimeZone()+"'")

	return fmt.Sprintf("%s:%s@tcp(%s)/?%s",
		c.config.Username,
		c.config.Password,
		net.JoinHostPort(c.config.Host, strconv.Itoa(c.config.Port)),
		values.Encode())
}

// Connect opens an instance-level pool; configured databases scope discovery and validation.
func (c *MariaDBConnector) Connect(ctx context.Context) error {
	if c.connected {
		return nil
	}

	db, err := sql.Open("mysql", c.connectionString())
	if err != nil {
		return err
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return err
	}

	c.db = db
	c.connected = true
	return nil
}

// Close releases the database connection pool and clears the connected state.
func (c *MariaDBConnector) Close(ctx context.Context) error {
	if c.db != nil {
		err := c.db.Close()
		c.connected = false
		c.db = nil
		return err
	}
	return nil
}

// Ping checks the database connection.
func (c *MariaDBConnector) Ping(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	return c.db.PingContext(ctx)
}

// TestConnection checks connectivity and the existence of configured databases.
func (c *MariaDBConnector) TestConnection(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	// Validate the configured database scope when present.
	if len(c.config.Databases) > 0 {
		if err := c.validateDatabases(ctx); err != nil {
			return err
		}
	}

	return nil
}

// validateDatabases checks all configured databases with one catalog query.
func (c *MariaDBConnector) validateDatabases(ctx context.Context) error {
	if len(c.config.Databases) == 0 {
		return nil
	}

	// In MariaDB, databases and schemas are equivalent.
	query, args, err := sq.Select("SCHEMA_NAME").
		From("information_schema.SCHEMATA").
		Where(sq.Eq{"SCHEMA_NAME": c.config.Databases}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build database validation query: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to list databases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	found := make(map[string]struct{})
	for rows.Next() {
		var dbName sql.NullString
		if err := rows.Scan(&dbName); err != nil {
			return fmt.Errorf("failed to scan database name: %w", err)
		}
		if !dbName.Valid {
			return fmt.Errorf("required schema metadata contains NULL")
		}
		found[dbName.String] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate databases: %w", err)
	}

	// Report the first configured database missing from the query result.
	for _, db := range c.config.Databases {
		if _, exists := found[db]; !exists {
			return fmt.Errorf("databases not found: %v", db)
		}
	}

	return nil
}
