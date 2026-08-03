// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"vega-backend/interfaces"
)

// GetMetadata returns metadata for the connected SQL Server database.
func (c *SQLServerConnector) GetMetadata(ctx context.Context) (map[string]any, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	var version string
	if err := c.db.QueryRowContext(ctx, "SELECT @@VERSION").Scan(&version); err != nil {
		return nil, err
	}
	return map[string]any{
		"version":  version,
		"database": c.config.Database,
	}, nil
}

// ListTables lists readable user tables and views in the configured database.
func (c *SQLServerConnector) ListTables(ctx context.Context) ([]*interfaces.TableMeta, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	query := `SELECT s.name, o.name, o.type, COALESCE(CONVERT(nvarchar(max), ep.value), '')
FROM sys.objects o
JOIN sys.schemas s ON s.schema_id=o.schema_id
LEFT JOIN sys.extended_properties ep ON ep.class=1 AND ep.major_id=o.object_id AND ep.minor_id=0 AND ep.name=N'MS_Description'
WHERE o.type IN ('U','V') AND o.is_ms_shipped=0
  AND s.name NOT IN ('sys','INFORMATION_SCHEMA')
  AND HAS_PERMS_BY_NAME(QUOTENAME(s.name)+'.'+QUOTENAME(o.name), 'OBJECT', 'SELECT')=1`
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
	defer func() { _ = rows.Close() }()

	result := make([]*interfaces.TableMeta, 0)
	for rows.Next() {
		var schema, name, objectType, description string
		if err := rows.Scan(&schema, &name, &objectType, &description); err != nil {
			return nil, fmt.Errorf("failed to scan table info: %w", err)
		}
		result = append(result, &interfaces.TableMeta{
			Name:        name,
			Description: description,
			Schema:      schema,
			Database:    c.config.Database,
			TableType:   sqlServerTableType(objectType),
			Properties:  map[string]any{},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate table info: %w", err)
	}
	return result, nil
}

func sqlServerTableType(objectType string) string {
	if objectType == "V" {
		return "view"
	}
	return "table"
}

// GetTableMeta fills table, column, index, primary-key, and foreign-key metadata.
func (c *SQLServerConnector) GetTableMeta(ctx context.Context, table *interfaces.TableMeta) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}
	if err := c.fetchTableStatus(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch table status: %w", err)
	}
	if err := c.fetchColumns(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch columns: %w", err)
	}
	if err := c.fetchKeysAndIndexes(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch indexes: %w", err)
	}
	if err := c.fetchForeignKeys(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch foreign keys: %w", err)
	}
	return nil
}

func (c *SQLServerConnector) fetchTableStatus(ctx context.Context, table *interfaces.TableMeta) error {
	row := c.db.QueryRowContext(ctx, `SELECT o.type, COALESCE(CONVERT(nvarchar(max), ep.value), '')
FROM sys.objects o
JOIN sys.schemas s ON s.schema_id=o.schema_id
LEFT JOIN sys.extended_properties ep ON ep.class=1 AND ep.major_id=o.object_id AND ep.minor_id=0 AND ep.name=N'MS_Description'
WHERE s.name=@p1 AND o.name=@p2 AND o.type IN ('U','V')`, table.Schema, table.Name)
	var objectType, description string
	if err := row.Scan(&objectType, &description); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	table.TableType = sqlServerTableType(objectType)
	table.Description = description
	if table.Properties == nil {
		table.Properties = make(map[string]any)
	}
	return nil
}

func (c *SQLServerConnector) fetchColumns(ctx context.Context, table *interfaces.TableMeta) error {
	rows, err := c.db.QueryContext(ctx, `SELECT c.name, t.name, c.is_nullable,
CASE
  WHEN COALESCE(bt.name,t.name) IN ('nchar','nvarchar') AND c.max_length=-1 THEN -1
  WHEN COALESCE(bt.name,t.name) IN ('nchar','nvarchar') AND c.max_length > 0 THEN c.max_length/2
  WHEN COALESCE(bt.name,t.name) IN ('char','varchar','binary','varbinary') THEN c.max_length
  WHEN COALESCE(bt.name,t.name) IN ('text','ntext','image') THEN -1
  ELSE 0
END AS character_maximum_length,
CASE WHEN COALESCE(bt.name,t.name) IN ('tinyint','smallint','int','bigint','decimal','numeric','money','smallmoney','real','float') THEN COALESCE(c.precision,0) ELSE 0 END AS numeric_precision,
CASE WHEN COALESCE(bt.name,t.name) IN ('decimal','numeric','money','smallmoney') THEN COALESCE(c.scale,0) ELSE 0 END AS numeric_scale,
CASE WHEN COALESCE(bt.name,t.name) IN ('time','datetime2','datetimeoffset') THEN COALESCE(c.scale,0) ELSE 0 END AS datetime_precision,
COALESCE(dc.definition,''),
COALESCE(CONVERT(nvarchar(max), ep.value), ''),
COALESCE(c.collation_name,''),
c.column_id
FROM sys.columns c
JOIN sys.types t ON c.user_type_id=t.user_type_id
LEFT JOIN sys.types bt ON c.system_type_id=bt.user_type_id AND bt.is_user_defined=0
JOIN sys.objects o ON c.object_id=o.object_id
JOIN sys.schemas s ON o.schema_id=s.schema_id
LEFT JOIN sys.default_constraints dc ON c.default_object_id=dc.object_id
LEFT JOIN sys.extended_properties ep ON ep.class=1 AND ep.major_id=c.object_id AND ep.minor_id=c.column_id AND ep.name=N'MS_Description'
WHERE s.name=@p1 AND o.name=@p2 AND o.type IN ('U','V')
ORDER BY c.column_id`, table.Schema, table.Name)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	columns := make([]interfaces.TableColumnMeta, 0)
	for rows.Next() {
		var name, nativeType, defaultValue, description, collation string
		var nullable bool
		var charMaxLen, numPrecision, numScale, datetimePrecision, ordinal int
		if err := rows.Scan(
			&name, &nativeType, &nullable, &charMaxLen, &numPrecision, &numScale,
			&datetimePrecision, &defaultValue, &description, &collation, &ordinal,
		); err != nil {
			return err
		}
		columns = append(columns, interfaces.TableColumnMeta{
			Name:              name,
			Type:              nativeType,
			Description:       description,
			Nullable:          nullable,
			DefaultValue:      defaultValue,
			CharMaxLen:        charMaxLen,
			NumPrecision:      numPrecision,
			NumScale:          numScale,
			DatetimePrecision: datetimePrecision,
			Collation:         collation,
			OrdinalPosition:   ordinal,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	table.Columns = columns
	return nil
}

func (c *SQLServerConnector) fetchKeysAndIndexes(ctx context.Context, table *interfaces.TableMeta) error {
	rows, err := c.db.QueryContext(ctx, `SELECT i.name, i.is_unique, i.is_primary_key, col.name
FROM sys.indexes i
JOIN sys.index_columns ic ON i.object_id=ic.object_id AND i.index_id=ic.index_id
JOIN sys.columns col ON ic.object_id=col.object_id AND ic.column_id=col.column_id
JOIN sys.objects o ON i.object_id=o.object_id
JOIN sys.schemas s ON o.schema_id=s.schema_id
WHERE s.name=@p1 AND o.name=@p2 AND i.name IS NOT NULL
  AND i.is_hypothetical=0 AND ic.key_ordinal > 0
ORDER BY i.is_primary_key DESC, i.name, ic.key_ordinal`, table.Schema, table.Name)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	indexes := make(map[string]*interfaces.TableIndexMeta)
	order := make([]string, 0)
	primaryColumns := make([]string, 0)
	primarySet := make(map[string]bool)
	for rows.Next() {
		var name, column string
		var unique, primary bool
		if err := rows.Scan(&name, &unique, &primary, &column); err != nil {
			return err
		}
		index := indexes[name]
		if index == nil {
			index = &interfaces.TableIndexMeta{Name: name, Unique: unique, Primary: primary}
			indexes[name] = index
			order = append(order, name)
		}
		index.Columns = append(index.Columns, column)
		if primary {
			primaryColumns = append(primaryColumns, column)
			primarySet[column] = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	table.PKs = primaryColumns
	table.Indices = make([]interfaces.TableIndexMeta, 0, len(order))
	for _, name := range order {
		table.Indices = append(table.Indices, *indexes[name])
	}
	for i := range table.Columns {
		if primarySet[table.Columns[i].Name] {
			table.Columns[i].ColumnKey = "PRI"
		}
	}
	return nil
}

func (c *SQLServerConnector) fetchForeignKeys(ctx context.Context, table *interfaces.TableMeta) error {
	rows, err := c.db.QueryContext(ctx, `SELECT fk.name, parent_col.name, ref_schema.name, ref_table.name, ref_col.name,
fk.delete_referential_action_desc, fk.update_referential_action_desc
FROM sys.foreign_keys fk
JOIN sys.foreign_key_columns fkc ON fk.object_id=fkc.constraint_object_id
JOIN sys.tables parent_table ON fk.parent_object_id=parent_table.object_id
JOIN sys.schemas parent_schema ON parent_table.schema_id=parent_schema.schema_id
JOIN sys.columns parent_col ON fkc.parent_object_id=parent_col.object_id AND fkc.parent_column_id=parent_col.column_id
JOIN sys.tables ref_table ON fk.referenced_object_id=ref_table.object_id
JOIN sys.schemas ref_schema ON ref_table.schema_id=ref_schema.schema_id
JOIN sys.columns ref_col ON fkc.referenced_object_id=ref_col.object_id AND fkc.referenced_column_id=ref_col.column_id
WHERE parent_schema.name=@p1 AND parent_table.name=@p2
ORDER BY fk.name, fkc.constraint_column_id`, table.Schema, table.Name)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	foreignKeys := make(map[string]*interfaces.TableForeignKeyMeta)
	order := make([]string, 0)
	for rows.Next() {
		var name, column, refSchema, refTable, refColumn, onDelete, onUpdate string
		if err := rows.Scan(&name, &column, &refSchema, &refTable, &refColumn, &onDelete, &onUpdate); err != nil {
			return err
		}
		foreignKey := foreignKeys[name]
		if foreignKey == nil {
			foreignKey = &interfaces.TableForeignKeyMeta{
				Name: name, RefTable: refSchema + "." + refTable, OnDelete: onDelete, OnUpdate: onUpdate,
			}
			foreignKeys[name] = foreignKey
			order = append(order, name)
		}
		foreignKey.Columns = append(foreignKey.Columns, column)
		foreignKey.RefColumns = append(foreignKey.RefColumns, refColumn)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	table.ForeignKeys = make([]interfaces.TableForeignKeyMeta, 0, len(order))
	for _, name := range order {
		table.ForeignKeys = append(table.ForeignKeys, *foreignKeys[name])
	}
	return nil
}
