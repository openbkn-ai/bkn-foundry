// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package mariadb provides MariaDB database connector implementation.
package mariadb

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"

	_ "github.com/go-sql-driver/mysql"
	"github.com/mitchellh/mapstructure"

	"vega-backend/interfaces"
)

type mariadbConfig struct {
	Host      string         `mapstructure:"host"`
	Port      int            `mapstructure:"port"`
	Username  string         `mapstructure:"username"`
	Password  string         `mapstructure:"password"`
	Databases []string       `mapstructure:"databases"`
	Options   map[string]any `mapstructure:"options"`
}

var (
	SYSTEM_DBS = []string{
		"information_schema",
		"mariadb",
		"mysql",
		"performance_schema",
		"sys",
	}
	SYSTEM_DBS_MAP = map[string]bool{
		"information_schema": true,
		"mariadb":            true,
		"mysql":              true,
		"performance_schema": true,
		"sys":                true,
	}
)

const (
	// DATABASE_NAME_MAX_LENGTH the maximum length of the MariaDB database name
	DATABASE_NAME_MAX_LENGTH = 64
	// The minimum valid port value of PORT_MIN
	PORT_MIN = 1
	// PORT_MAX is the maximum value of the valid port
	PORT_MAX = 65535
)

// MariaDBConnector implements TableConnector for MariaDB.
type MariaDBConnector struct {
	enabled bool

	config *mariadbConfig

	connected bool
	db        *sql.DB
}

// NewMariaDBConnector creates the MariaDB connector builder
func NewMariaDBConnector() interfaces.TableConnector {
	return &MariaDBConnector{}
}

// GetType returns the data source type.
func (c *MariaDBConnector) GetType() string {
	return interfaces.ConnectorTypeMariaDB
}

// GetName returns the connector name.
func (c *MariaDBConnector) GetName() string {
	return interfaces.ConnectorTypeMariaDB
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

// GetSensitiveFields returns the sensitive fields for MariaDB connector.
func (c *MariaDBConnector) GetSensitiveFields() []string {
	return []string{"password"}
}

// GetFieldConfig returns the field configuration for MariaDB connector.
func (c *MariaDBConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return map[string]interfaces.ConnectorFieldConfig{
		"host":      {Name: "Host", Type: "string", Description: "Database server host", Required: true, Encrypted: false},
		"port":      {Name: "Port", Type: "integer", Description: "Database server port", Required: true, Encrypted: false},
		"username":  {Name: "Username", Type: "string", Description: "Database username", Required: true, Encrypted: false},
		"password":  {Name: "Password", Type: "string", Description: "Database password", Required: true, Encrypted: true},
		"databases": {Name: "Databases", Type: "array", Description: "Optional database names; when empty, connect at the instance level", Required: false, Encrypted: false},
		"options":   {Name: "Connection options", Type: "object", Description: "Connection options, such as charset and timeout", Required: false, Encrypted: false},
	}
}

// New creates a new MariaDB connector.
// Databases is an optional field. When not specified, it connects to the instance level.
func (c *MariaDBConnector) New(cfg interfaces.ConnectorConfig) (interfaces.Connector, error) {
	var mCfg mariadbConfig
	if err := mapstructure.Decode(cfg, &mCfg); err != nil {
		return nil, fmt.Errorf("failed to decode mariadb config: %w", err)
	}

	if mCfg.Host == "" || mCfg.Port == 0 || mCfg.Username == "" || mCfg.Password == "" {
		return nil, fmt.Errorf("mariadb connector config is incomplete")
	}

	// Verify the range of port numbers
	if mCfg.Port < PORT_MIN || mCfg.Port > PORT_MAX {
		return nil, fmt.Errorf("port %d is out of valid range (%d-%d)", mCfg.Port, PORT_MIN, PORT_MAX)
	}

	seen := make(map[string]bool)
	for _, db := range mCfg.Databases {
		// Verify the length of the databases name (the maximum MariaDB database name is 64 characters)
		if len(db) > DATABASE_NAME_MAX_LENGTH {
			return nil, fmt.Errorf("database name '%s' exceeds maximum length of %d characters", db, DATABASE_NAME_MAX_LENGTH)
		}
		// Check whether there are duplicate elements in the array
		if seen[db] {
			return nil, fmt.Errorf("duplicate element found in 'databases': %s", db)
		}
		seen[db] = true
	}

	return &MariaDBConnector{
		config: &mCfg,
	}, nil
}

func (c *MariaDBConnector) connectionString() string {
	values := url.Values{}
	values.Set("charset", "utf8mb4")
	values.Set("parseTime", "true")

	// Apply options
	for k, v := range c.config.Options {
		values.Set(k, fmt.Sprintf("%v", v))
	}

	return fmt.Sprintf("%s:%s@tcp(%s)/?%s",
		c.config.Username,
		c.config.Password,
		net.JoinHostPort(c.config.Host, strconv.Itoa(c.config.Port)),
		values.Encode())
}

// Connect establishes connection to MariaDB database.
// If Config.Database is empty, connect to the instance level (without specifying the database).
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

// Close closes the database connection.
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

// TestConnection tests the connection to MariaDB database.
func (c *MariaDBConnector) TestConnection(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	// If the databases list is configured, verify whether these databases exist
	if len(c.config.Databases) > 0 {
		if err := c.validateDatabases(ctx); err != nil {
			return err
		}
	}

	return nil
}

// validateDatabases verifies whether the configured database exists
func (c *MariaDBConnector) validateDatabases(ctx context.Context) error {
	// Obtain the list of all databases/schemas; In MariaDB, database and schema are equivalent.
	rows, err := c.db.QueryContext(ctx,
		"SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME")
	if err != nil {
		return fmt.Errorf("failed to list databases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	existingDBs := make(map[string]bool)
	for rows.Next() {
		var dbName string
		if err := rows.Scan(&dbName); err != nil {
			return fmt.Errorf("failed to scan database name: %w", err)
		}
		existingDBs[dbName] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate databases: %w", err)
	}

	// Check whether all the configured databases exist
	var notFoundDBs []string
	for _, db := range c.config.Databases {
		if !existingDBs[db] {
			notFoundDBs = append(notFoundDBs, db)
		}
	}

	if len(notFoundDBs) > 0 {
		return fmt.Errorf("databases not found: %v", notFoundDBs)
	}

	return nil
}
