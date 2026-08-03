// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package sqlserver provides the Microsoft SQL Server table connector.
package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
	"github.com/mitchellh/mapstructure"

	"vega-backend/interfaces"
)

const (
	portMin                     = 1
	portMax                     = 65535
	maxConnectionTimeoutSeconds = uint64(math.MaxInt64 / int64(time.Second))
)

var allowedOptions = map[string]struct{}{
	msdsn.Encrypt:                {},
	msdsn.TrustServerCertificate: {},
	msdsn.HostNameInCertificate:  {},
	msdsn.ConnectionTimeout:      {},
	msdsn.AppName:                {},
}

type config struct {
	Host     string         `mapstructure:"host"`
	Port     int            `mapstructure:"port"`
	Username string         `mapstructure:"username"`
	Password string         `mapstructure:"password"`
	Database string         `mapstructure:"database"`
	Schemas  []string       `mapstructure:"schemas"`
	Options  map[string]any `mapstructure:"options"`
}

// SQLServerConnector implements a local SQL Server TableConnector.
type SQLServerConnector struct {
	enabled   bool
	config    *config
	connected bool
	db        *sql.DB
}

func NewSQLServerConnector() interfaces.TableConnector     { return &SQLServerConnector{} }
func (c *SQLServerConnector) GetType() string              { return interfaces.ConnectorTypeSQLServer }
func (c *SQLServerConnector) GetName() string              { return interfaces.ConnectorTypeSQLServer }
func (c *SQLServerConnector) GetMode() string              { return interfaces.ConnectorModeLocal }
func (c *SQLServerConnector) GetCategory() string          { return interfaces.ConnectorCategoryTable }
func (c *SQLServerConnector) GetEnabled() bool             { return c.enabled }
func (c *SQLServerConnector) SetEnabled(enabled bool)      { c.enabled = enabled }
func (c *SQLServerConnector) GetSensitiveFields() []string { return []string{"password"} }

func (c *SQLServerConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return map[string]interfaces.ConnectorFieldConfig{
		"host":     {Name: "主机地址", Type: "string", Description: "SQL Server 服务器主机地址", Required: true},
		"port":     {Name: "端口号", Type: "integer", Description: "SQL Server TCP 端口", Required: true},
		"username": {Name: "用户名", Type: "string", Description: "SQL Server 登录用户名", Required: true},
		"password": {Name: "密码", Type: "string", Description: "SQL Server 登录密码", Required: true, Encrypted: true},
		"database": {Name: "数据库名", Type: "string", Description: "SQL Server 连接目标数据库", Required: true},
		"schemas":  {Name: "Schema 列表", Type: "array", Description: "可选；为空时扫描所有可访问的非系统 schema", Required: false},
		"options":  {Name: "连接参数", Type: "object", Description: "连接参数，如 encrypt、trustservercertificate、connection timeout", Required: false},
	}
}

func (c *SQLServerConnector) New(cfg interfaces.ConnectorConfig) (interfaces.Connector, error) {
	var value config
	if err := mapstructure.Decode(cfg, &value); err != nil {
		return nil, fmt.Errorf("failed to decode sqlserver config: %w", err)
	}
	if strings.TrimSpace(value.Host) == "" || value.Port == 0 || strings.TrimSpace(value.Username) == "" || value.Password == "" || strings.TrimSpace(value.Database) == "" {
		return nil, fmt.Errorf("sqlserver connector config is incomplete")
	}
	if value.Port < portMin || value.Port > portMax {
		return nil, fmt.Errorf("port %d is out of valid range (%d-%d)", value.Port, portMin, portMax)
	}
	seen := make(map[string]struct{}, len(value.Schemas))
	for index, schema := range value.Schemas {
		schema = strings.TrimSpace(schema)
		if schema == "" {
			return nil, fmt.Errorf("schema must not be empty")
		}
		if _, ok := seen[strings.ToLower(schema)]; ok {
			return nil, fmt.Errorf("duplicate element found in schemas: %s", schema)
		}
		seen[strings.ToLower(schema)] = struct{}{}
		value.Schemas[index] = schema
	}
	options, err := normalizeOptions(value.Options)
	if err != nil {
		return nil, err
	}
	value.Options = options
	return &SQLServerConnector{config: &value}, nil
}

func normalizeOptions(options map[string]any) (map[string]any, error) {
	normalized := make(map[string]any, len(options))
	for option, value := range options {
		name := strings.ToLower(strings.TrimSpace(option))
		if _, ok := allowedOptions[name]; !ok {
			return nil, fmt.Errorf("unsupported sqlserver option: %s", option)
		}
		if _, ok := normalized[name]; ok {
			return nil, fmt.Errorf("duplicate sqlserver option: %s", name)
		}
		switch name {
		case msdsn.Encrypt, msdsn.TrustServerCertificate:
			if _, ok := value.(bool); !ok {
				return nil, fmt.Errorf("sqlserver option %s must be a boolean", name)
			}
		case msdsn.HostNameInCertificate, msdsn.AppName:
			if _, ok := value.(string); !ok {
				return nil, fmt.Errorf("sqlserver option %s must be a string", name)
			}
		case msdsn.ConnectionTimeout:
			timeout, ok := connectionTimeout(value)
			if !ok {
				return nil, fmt.Errorf(
					"sqlserver option %s must be a non-negative integer not greater than %d",
					name, maxConnectionTimeoutSeconds,
				)
			}
			value = timeout
		}
		normalized[name] = value
	}
	return normalized, nil
}

func connectionTimeout(value any) (uint64, bool) {
	switch timeout := value.(type) {
	case int:
		if timeout < 0 {
			return 0, false
		}
		return validConnectionTimeout(uint64(timeout))
	case int8:
		if timeout < 0 {
			return 0, false
		}
		return validConnectionTimeout(uint64(timeout))
	case int16:
		if timeout < 0 {
			return 0, false
		}
		return validConnectionTimeout(uint64(timeout))
	case int32:
		if timeout < 0 {
			return 0, false
		}
		return validConnectionTimeout(uint64(timeout))
	case int64:
		if timeout < 0 {
			return 0, false
		}
		return validConnectionTimeout(uint64(timeout))
	case uint:
		return validConnectionTimeout(uint64(timeout))
	case uint8:
		return validConnectionTimeout(uint64(timeout))
	case uint16:
		return validConnectionTimeout(uint64(timeout))
	case uint32:
		return validConnectionTimeout(uint64(timeout))
	case uint64:
		return validConnectionTimeout(timeout)
	case float32:
		converted := float64(timeout)
		return floatConnectionTimeout(converted)
	case float64:
		return floatConnectionTimeout(timeout)
	default:
		return 0, false
	}
}

func validConnectionTimeout(timeout uint64) (uint64, bool) {
	return timeout, timeout <= maxConnectionTimeoutSeconds
}

func floatConnectionTimeout(timeout float64) (uint64, bool) {
	if math.IsNaN(timeout) || math.IsInf(timeout, 0) || timeout < 0 ||
		timeout > float64(maxConnectionTimeoutSeconds) || timeout != math.Trunc(timeout) {
		return 0, false
	}
	return uint64(timeout), true
}

func (c *SQLServerConnector) connectionString() string {
	u := &url.URL{
		Scheme: "sqlserver",
		User:   url.UserPassword(c.config.Username, c.config.Password),
		Host:   net.JoinHostPort(c.config.Host, strconv.Itoa(c.config.Port)),
	}
	q := u.Query()
	q.Set("database", c.config.Database)
	for key, value := range c.config.Options {
		q.Set(key, fmt.Sprint(value))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *SQLServerConnector) Connect(ctx context.Context) error {
	if c.connected {
		return nil
	}
	db, err := sql.Open("sqlserver", c.connectionString())
	if err != nil {
		return err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return err
	}
	c.db, c.connected = db, true
	return nil
}

func (c *SQLServerConnector) Ping(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}
	return c.db.PingContext(ctx)
}
func (c *SQLServerConnector) Close(_ context.Context) error {
	if c.db == nil {
		return nil
	}
	err := c.db.Close()
	c.db, c.connected = nil, false
	return err
}

func (c *SQLServerConnector) TestConnection(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}
	for _, schema := range c.config.Schemas {
		var name string
		if err := c.db.QueryRowContext(ctx, "SELECT name FROM sys.schemas WHERE name = @p1", schema).Scan(&name); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("schema not found: %s", schema)
			}
			return fmt.Errorf("failed to validate schema: %w", err)
		}
	}
	return nil
}
