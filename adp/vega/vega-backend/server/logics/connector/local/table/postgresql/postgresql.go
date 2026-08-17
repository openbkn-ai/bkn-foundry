// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package postgresql provides a PostgreSQL table connector: the connection target is a single database; The configuration item "schemas" represents the schema whitelist.
package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	_ "github.com/lib/pq"
	"github.com/mitchellh/mapstructure"

	"vega-backend/interfaces"
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

var (
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
)

const (
	databaseNameMaxLength = 63 // The default upper limit of PostgreSQL identifiers
	portMin               = 1
	portMax               = 65535
)

// The PostgresqlConnector implements TableConnector (PostgreSQL).
type PostgresqlConnector struct {
	enabled bool

	config *postgresqlConfig

	connected bool
	db        *sql.DB
}

// NewPostgresqlConnector creates the PostgreSQL connector builder
func NewPostgresqlConnector() interfaces.TableConnector {
	return &PostgresqlConnector{}
}

// GetType returns the data source type key (consistent with t_connector_type.f_type, factory registration key).
func (c *PostgresqlConnector) GetType() string {
	return interfaces.ConnectorTypePostgreSQL
}

// GetName returns the connector name.
func (c *PostgresqlConnector) GetName() string {
	return interfaces.ConnectorTypePostgreSQL
}

// GetMode returns the connector mode.
func (c *PostgresqlConnector) GetMode() string {
	return interfaces.ConnectorModeLocal
}

// GetCategory returns the connector category.
func (c *PostgresqlConnector) GetCategory() string {
	return interfaces.ConnectorCategoryTable
}

// Whether GetEnabled is enabled.
func (c *PostgresqlConnector) GetEnabled() bool {
	return c.enabled
}

// Set the enabled status of SetEnabled.
func (c *PostgresqlConnector) SetEnabled(enabled bool) {
	c.enabled = enabled
}

// GetSensitiveFields: Sensitive fields.
func (c *PostgresqlConnector) GetSensitiveFields() []string {
	return []string{"password"}
}

// The GetFieldConfig connection form field (must be exactly the same as the JSON of t_connector_type in the migration).
func (c *PostgresqlConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return map[string]interfaces.ConnectorFieldConfig{
		"host":     {Name: "Host", Type: "string", Description: "Database server host", Required: true, Encrypted: false},
		"port":     {Name: "Port", Type: "integer", Description: "Database server port", Required: true, Encrypted: false},
		"username": {Name: "Username", Type: "string", Description: "Database username", Required: true, Encrypted: false},
		"password": {Name: "Password", Type: "string", Description: "Database password", Required: true, Encrypted: true},
		"database": {Name: "Database", Type: "string", Description: "PostgreSQL target database", Required: true, Encrypted: false},
		"schemas":  {Name: "Schemas", Type: "array", Description: "Optional schema allowlist; when empty, scan non-system schemas in the current database", Required: false, Encrypted: false},
		"options":  {Name: "Connection options", Type: "object", Description: "Connection options, such as sslmode and connect_timeout", Required: false, Encrypted: false},
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

	if pCfg.Port < portMin || pCfg.Port > portMax {
		return nil, fmt.Errorf("port %d is out of valid range (%d-%d)", pCfg.Port, portMin, portMax)
	}

	if len(pCfg.Database) > databaseNameMaxLength {
		return nil, fmt.Errorf("database name exceeds maximum length of %d characters", databaseNameMaxLength)
	}

	seen := make(map[string]bool)
	for _, s := range pCfg.Schemas {
		if len(s) > databaseNameMaxLength {
			return nil, fmt.Errorf("schema name '%s' exceeds maximum length of %d characters", s, databaseNameMaxLength)
		}
		// Check whether there are duplicate elements in the array
		if seen[s] {
			return nil, fmt.Errorf("duplicate element found in 'schemas': %s", s)
		}
		seen[s] = true
	}

	return &PostgresqlConnector{
		config: &pCfg,
	}, nil
}

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
			q.Set(k, fmt.Sprintf("%v", v))
		}
	}
	if q.Get("sslmode") == "" {
		q.Set("sslmode", "disable")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Connect establishes a connection.
func (c *PostgresqlConnector) Connect(ctx context.Context) error {
	if c.connected {
		return nil
	}

	db, err := sql.Open("postgres", c.connectionString())
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

// Close to close the connection.
func (c *PostgresqlConnector) Close(ctx context.Context) error {
	if c.db != nil {
		err := c.db.Close()
		c.connected = false
		c.db = nil
		return err
	}
	return nil
}

// Ping detects the connection.
func (c *PostgresqlConnector) Ping(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}
	return c.db.PingContext(ctx)
}

// TestConnection test connection; If a schema whitelist is configured, verify the existence.
func (c *PostgresqlConnector) TestConnection(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}
	if len(c.config.Schemas) > 0 {
		if err := c.validateSchemas(ctx); err != nil {
			return err
		}
	}
	return nil
}

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
