// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package sqlserver provides the Microsoft SQL Server table connector.
package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/mitchellh/mapstructure"

	"vega-backend/interfaces"
)

const (
	portMin = 1
	portMax = 65535
)

var systemSchemas = map[string]bool{"sys": true, "information_schema": true}

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
	for _, schema := range value.Schemas {
		schema = strings.TrimSpace(schema)
		if schema == "" {
			return nil, fmt.Errorf("schema must not be empty")
		}
		if _, ok := seen[strings.ToLower(schema)]; ok {
			return nil, fmt.Errorf("duplicate element found in schemas: %s", schema)
		}
		seen[strings.ToLower(schema)] = struct{}{}
	}
	return &SQLServerConnector{config: &value}, nil
}

func (c *SQLServerConnector) connectionString() string {
	u := &url.URL{Scheme: "sqlserver", User: url.UserPassword(c.config.Username, c.config.Password), Host: c.config.Host + ":" + strconv.Itoa(c.config.Port)}
	q := u.Query()
	q.Set("database", c.config.Database)
	if _, ok := c.config.Options["encrypt"]; !ok {
		q.Set("encrypt", "true")
	}
	if _, ok := c.config.Options["trustservercertificate"]; !ok {
		q.Set("trustservercertificate", "false")
	}
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

func (c *SQLServerConnector) ListTables(ctx context.Context) ([]*interfaces.TableMeta, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	query := `SELECT s.name, o.name, o.type FROM sys.objects o JOIN sys.schemas s ON s.schema_id=o.schema_id
WHERE o.type IN ('U','V') AND s.name NOT IN ('sys','INFORMATION_SCHEMA') AND HAS_PERMS_BY_NAME(QUOTENAME(s.name)+'.'+QUOTENAME(o.name), 'OBJECT', 'SELECT')=1`
	args := make([]any, 0, len(c.config.Schemas))
	if len(c.config.Schemas) > 0 {
		marks := make([]string, len(c.config.Schemas))
		for i, schema := range c.config.Schemas {
			marks[i] = fmt.Sprintf("@p%d", i+1)
			args = append(args, schema)
		}
		query += " AND s.name IN (" + strings.Join(marks, ",") + ")"
	}
	query += " ORDER BY s.name, o.name"
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}
	defer rows.Close()
	result := make([]*interfaces.TableMeta, 0)
	for rows.Next() {
		var schema, name, objectType string
		if err := rows.Scan(&schema, &name, &objectType); err != nil {
			return nil, err
		}
		tableType := "table"
		if objectType == "V" {
			tableType = "view"
		}
		result = append(result, &interfaces.TableMeta{Name: name, Schema: schema, Database: c.config.Database, TableType: tableType, Properties: map[string]any{}})
	}
	return result, rows.Err()
}

func (c *SQLServerConnector) GetTableMeta(ctx context.Context, table *interfaces.TableMeta) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT c.name, t.name, c.is_nullable, COALESCE(c.max_length,0), COALESCE(c.precision,0), COALESCE(c.scale,0), c.column_id
FROM sys.columns c JOIN sys.types t ON c.user_type_id=t.user_type_id JOIN sys.objects o ON c.object_id=o.object_id JOIN sys.schemas s ON o.schema_id=s.schema_id
WHERE s.name=@p1 AND o.name=@p2 ORDER BY c.column_id`, table.Schema, table.Name)
	if err != nil {
		return fmt.Errorf("failed to get columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var column, typ string
		var nullable bool
		var maxLen, precision, scale, ordinal int
		if err := rows.Scan(&column, &typ, &nullable, &maxLen, &precision, &scale, &ordinal); err != nil {
			return err
		}
		table.Columns = append(table.Columns, interfaces.TableColumnMeta{Name: column, Type: c.MapType(typ), Nullable: nullable, CharMaxLen: maxLen, NumPrecision: precision, NumScale: scale, OrdinalPosition: ordinal})
	}
	return rows.Err()
}

func (c *SQLServerConnector) GetMetadata(ctx context.Context) (map[string]any, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	var version string
	if err := c.db.QueryRowContext(ctx, "SELECT @@VERSION").Scan(&version); err != nil {
		return nil, err
	}
	return map[string]any{"version": version, "database": c.config.Database}, nil
}

func (c *SQLServerConnector) ExecuteRawSQL(ctx context.Context, statement string) (*interfaces.RawQueryResponse, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}
	rows, err := c.db.QueryContext(ctx, statement)
	if err != nil {
		return nil, fmt.Errorf("execute query failed: %w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	result := &interfaces.RawQueryResponse{Columns: make([]interfaces.ColumnInfo, len(columns)), Entries: make([]map[string]any, 0)}
	for i, name := range columns {
		result.Columns[i] = interfaces.ColumnInfo{Name: name, Type: c.MapType(types[i].DatabaseTypeName())}
	}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		entry := make(map[string]any, len(columns))
		for i, name := range columns {
			if b, ok := values[i].([]byte); ok {
				entry[name] = string(b)
			} else {
				entry[name] = values[i]
			}
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	total := int64(len(result.Entries))
	result.TotalCount = &total
	return result, nil
}

// ExecuteQuery is completed with the common resource-data filter compiler in a follow-up change.
func (c *SQLServerConnector) ExecuteQuery(context.Context, *interfaces.Resource, *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
	return nil, fmt.Errorf("sqlserver resource-data query is not implemented")
}

func (c *SQLServerConnector) MapType(nativeType string) string {
	switch strings.ToLower(strings.TrimSpace(nativeType)) {
	case "tinyint", "smallint", "int", "bigint":
		return interfaces.DataType_Integer
	case "decimal", "numeric", "money", "smallmoney":
		return interfaces.DataType_Decimal
	case "real", "float":
		return interfaces.DataType_Float
	case "char", "varchar", "nchar", "nvarchar", "uniqueidentifier":
		return interfaces.DataType_String
	case "text", "ntext", "xml":
		return interfaces.DataType_Text
	case "date":
		return interfaces.DataType_Date
	case "time":
		return interfaces.DataType_Time
	case "datetime", "datetime2", "smalldatetime", "datetimeoffset":
		return interfaces.DataType_Timestamp
	case "bit":
		return interfaces.DataType_Boolean
	case "binary", "varbinary", "image", "rowversion":
		return interfaces.DataType_Binary
	default:
		return interfaces.DataType_Other
	}
}
