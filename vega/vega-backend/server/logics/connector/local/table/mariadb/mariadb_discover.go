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
	"fmt"
	"sort"
	"strconv"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/bytedance/sonic"
	_ "github.com/go-sql-driver/mysql"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// ListTables lists tables and views within the configured database scope.
// Without a configured scope, discovery excludes system databases.
// Each result retains its database name in both Database and Schema.
func (c *MariaDBConnector) ListTables(ctx context.Context) ([]*interfaces.TableMeta, error) {
	return c.listTables(ctx, "", "")
}

// listTables reads basic table metadata filtered by optional raw database and table names.
// Explicit scope is validated before connecting; an empty result is a valid table list.
func (c *MariaDBConnector) listTables(ctx context.Context, database, tableName string) ([]*interfaces.TableMeta, error) {
	if database != "" && !c.databaseAllowed(database) {
		return nil, fmt.Errorf("database %q is outside the connector scope", database)
	}
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect for table discovery: %w", err)
	}

	builder := sq.Select(
		"TABLE_SCHEMA",
		"TABLE_NAME",
		"TABLE_TYPE",
		"AUTO_INCREMENT",
		"ENGINE",
		"TABLE_COLLATION",
		"TABLE_ROWS",
		"TABLE_COMMENT",
		"CREATE_TIME",
		"UPDATE_TIME",
		"DATA_LENGTH",
		"INDEX_LENGTH",
	).From("information_schema.TABLES")

	// An explicit or configured scope can include system databases; only
	// unrestricted discovery excludes them.
	if database != "" {
		builder = builder.Where(sq.Eq{"TABLE_SCHEMA": database})
	} else if len(c.config.Databases) > 0 {
		builder = builder.Where(sq.Eq{"TABLE_SCHEMA": c.config.Databases})
	} else {
		builder = builder.Where(sq.NotEq{"TABLE_SCHEMA": SYSTEM_DBS})
	}
	if tableName != "" {
		builder = builder.Where(sq.Eq{"TABLE_NAME": tableName})
	}

	sqlStr, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build list tables query: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute list tables query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	tables := make([]*interfaces.TableMeta, 0)
	for rows.Next() {
		var schemaName, tableName, tableType sql.NullString
		var autoIncrement, engine, collation, description sql.NullString
		var tableRows, dataLength, indexLength sql.NullInt64
		var createTime, updateTime sql.NullTime

		if err := rows.Scan(
			&schemaName,
			&tableName,
			&tableType,
			&autoIncrement,
			&engine,
			&collation,
			&tableRows,
			&description,
			&createTime,
			&updateTime,
			&dataLength,
			&indexLength,
		); err != nil {
			return nil, fmt.Errorf("failed to scan table info: %w", err)
		}
		if !schemaName.Valid || !tableName.Valid || !tableType.Valid {
			return nil, fmt.Errorf("required table metadata contains NULL")
		}

		// SQL narrows the candidates; configured scope uses exact source-name matching.
		// Filter here to keep scope independent of source collation without applying
		// BINARY/COLLATE to columns, which can interfere with indexes or predicate pushdown.
		if !c.databaseAllowed(schemaName.String) {
			continue
		}

		meta := &interfaces.TableMeta{
			Name:        tableName.String,
			TableType:   c.mapTableType(tableType.String),
			Description: description.String,
			Database:    schemaName.String,
			Schema:      schemaName.String,
			Properties:  map[string]any{},
		}

		// Retain valid properties; omit NULL and negative statistics but preserve zero.
		// AUTO_INCREMENT can span the full BIGINT UNSIGNED range.
		if autoIncrement.Valid {
			if value, err := strconv.ParseUint(autoIncrement.String, 10, 64); err == nil {
				meta.Properties["auto_increment"] = value
			}
		}
		if engine.Valid && engine.String != "" {
			meta.Properties["engine"] = engine.String
		}
		if collation.Valid && collation.String != "" {
			meta.Properties["collation"] = collation.String
		}
		if tableRows.Valid && tableRows.Int64 >= 0 {
			meta.Properties["estimated_row_count"] = tableRows.Int64
		}
		if createTime.Valid && !createTime.Time.IsZero() {
			meta.Properties["create_time"] = createTime.Time.UnixMilli()
		}
		if updateTime.Valid && !updateTime.Time.IsZero() {
			meta.Properties["update_time"] = updateTime.Time.UnixMilli()
		}
		if dataLength.Valid && dataLength.Int64 >= 0 {
			meta.Properties["data_length"] = dataLength.Int64
		}
		if indexLength.Valid && indexLength.Int64 >= 0 {
			meta.Properties["index_length"] = indexLength.Int64
		}

		// Derive the character set from a collation such as utf8mb4_general_ci.
		if coll := collation.String; coll != "" {
			for i, ch := range coll {
				if ch == '_' {
					meta.Properties["charset"] = coll[:i]
					break
				}
			}
		}

		tables = append(tables, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate table info: %w", err)
	}

	return tables, nil
}

// mapTableType maps VIEW to a view and other source kinds to an ordinary table.
func (c *MariaDBConnector) mapTableType(tableType string) string {
	if strings.ToUpper(tableType) == "VIEW" {
		return interfaces.TableTypeView
	}
	return interfaces.TableTypeTable
}

// databaseAllowed checks configured scope using exact source names.
// An empty scope allows every database; default system-database exclusions belong to discovery.
func (c *MariaDBConnector) databaseAllowed(database string) bool {
	if len(c.config.Databases) == 0 {
		return true
	}
	for _, configured := range c.config.Databases {
		if configured == database {
			return true
		}
	}
	return false
}

// GetTableMeta refreshes status, columns, indexes, and foreign keys using raw name components.
// Scope is checked before connecting; each successful stage updates its part of table.
func (c *MariaDBConnector) GetTableMeta(ctx context.Context, table *interfaces.TableMeta, refreshStatus bool) error {
	if !c.databaseAllowed(table.Database) {
		return fmt.Errorf("database %q is outside the connector scope", table.Database)
	}
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect for table metadata: %w", err)
	}

	// Batch callers reuse the equivalent status snapshot returned by ListTables.
	if refreshStatus {
		if err := c.fetchTableStatus(ctx, table); err != nil {
			return fmt.Errorf("failed to fetch table status: %w", err)
		}
	}

	// Read ordered columns and primary key names.
	if err := c.fetchColumns(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch columns: %w", err)
	}

	// Read indexes and their ordered columns.
	if err := c.fetchIndexes(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch indexes: %w", err)
	}

	// Read foreign key constraints and their ordered column pairs.
	if err := c.fetchForeignKeys(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch foreign keys: %w", err)
	}

	return nil
}

// fetchTableStatus refreshes one table's type, description, and optional properties.
// Missing rows retain sql.ErrNoRows; failed reads leave the existing snapshot unchanged.
func (c *MariaDBConnector) fetchTableStatus(ctx context.Context, table *interfaces.TableMeta) error {
	sqlStr, args, err := sq.Select(
		"TABLE_TYPE",
		"AUTO_INCREMENT",
		"ENGINE",
		"TABLE_COLLATION",
		"TABLE_ROWS",
		"TABLE_COMMENT",
		"CREATE_TIME",
		"UPDATE_TIME",
		"DATA_LENGTH",
		"INDEX_LENGTH",
	).From("information_schema.TABLES").
		Where(sq.Eq{"TABLE_SCHEMA": table.Database}).
		Where(sq.Eq{"TABLE_NAME": table.Name}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build table status query: %w", err)
	}

	var tableType, autoIncrement, engine, collation, description sql.NullString
	var tableRows, dataLength, indexLength sql.NullInt64
	var createTime, updateTime sql.NullTime

	row := c.db.QueryRowContext(ctx, sqlStr, args...)
	if err := row.Scan(
		&tableType,
		&autoIncrement,
		&engine,
		&collation,
		&tableRows,
		&description,
		&createTime,
		&updateTime,
		&dataLength,
		&indexLength,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("table metadata not found or inaccessible: %s.%s: %w", table.Database, table.Name, err)
		}
		return fmt.Errorf("failed to read table status: %w", err)
	}
	if !tableType.Valid {
		return fmt.Errorf("required table metadata contains NULL")
	}

	table.TableType = c.mapTableType(tableType.String)

	table.Description = description.String

	// Replace properties with the current snapshot so obsolete values are removed.
	properties := make(map[string]any)
	// AUTO_INCREMENT can span the full BIGINT UNSIGNED range.
	if autoIncrement.Valid {
		if value, err := strconv.ParseUint(autoIncrement.String, 10, 64); err == nil {
			properties["auto_increment"] = value
		}
	}
	if engine.Valid && engine.String != "" {
		properties["engine"] = engine.String
	}
	if collation.Valid && collation.String != "" {
		properties["collation"] = collation.String
	}
	if tableRows.Valid && tableRows.Int64 >= 0 {
		properties["estimated_row_count"] = tableRows.Int64
	}
	if createTime.Valid && !createTime.Time.IsZero() {
		properties["create_time"] = createTime.Time.UnixMilli()
	}
	if updateTime.Valid && !updateTime.Time.IsZero() {
		properties["update_time"] = updateTime.Time.UnixMilli()
	}
	if dataLength.Valid && dataLength.Int64 >= 0 {
		properties["data_length"] = dataLength.Int64
	}
	if indexLength.Valid && indexLength.Int64 >= 0 {
		properties["index_length"] = indexLength.Int64
	}
	// Derive the character set from a collation such as utf8mb4_general_ci.
	if coll := collation.String; coll != "" {
		for i, ch := range coll {
			if ch == '_' {
				properties["charset"] = coll[:i]
				break
			}
		}
	}
	table.Properties = properties
	return nil
}

// fetchColumns retrieves column metadata from information_schema.COLUMNS.
// Columns and primary keys are replaced only after all rows have been read successfully.
func (c *MariaDBConnector) fetchColumns(ctx context.Context, table *interfaces.TableMeta) error {
	sqlStr, args, err := sq.Select(
		"COLUMN_NAME",
		"COLUMN_TYPE",
		"IS_NULLABLE",
		"COLUMN_DEFAULT",
		"COLUMN_COMMENT",
		"CHARACTER_MAXIMUM_LENGTH",
		"NUMERIC_PRECISION",
		"NUMERIC_SCALE",
		"DATETIME_PRECISION",
		"CHARACTER_SET_NAME",
		"COLLATION_NAME",
		"COLUMN_KEY",
		"ORDINAL_POSITION",
	).From("information_schema.COLUMNS").
		Where(sq.Eq{"TABLE_SCHEMA": table.Database}).
		Where(sq.Eq{"TABLE_NAME": table.Name}).
		OrderBy("ORDINAL_POSITION").
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build column metadata query: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return fmt.Errorf("failed to execute column metadata query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	columns := make([]interfaces.TableColumnMeta, 0)
	pkColumns := make([]string, 0)

	for rows.Next() {
		var columnName, columnType, isNullable, columnDefault sql.NullString
		var description, charset, collation, columnKey sql.NullString
		var charMaxLen, numPrecision, numScale, datetimePrecision, ordinal sql.NullInt64

		if err := rows.Scan(
			&columnName,
			&columnType,
			&isNullable,
			&columnDefault,
			&description,
			&charMaxLen,
			&numPrecision,
			&numScale,
			&datetimePrecision,
			&charset,
			&collation,
			&columnKey,
			&ordinal,
		); err != nil {
			return fmt.Errorf("failed to scan column metadata: %w", err)
		}
		if !columnName.Valid || !columnType.Valid || !isNullable.Valid || !ordinal.Valid {
			return fmt.Errorf("required column metadata contains NULL")
		}

		// Normalize the type syntax for storage while preserving ENUM/SET literal casing.
		column := interfaces.TableColumnMeta{
			Name:              columnName.String,
			Type:              c.normalizeColumnType(columnType.String),
			Description:       description.String,
			Nullable:          isNullable.String == "YES",
			DefaultValue:      columnDefault.String,
			CharMaxLen:        int(charMaxLen.Int64),
			NumPrecision:      int(numPrecision.Int64),
			NumScale:          int(numScale.Int64),
			DatetimePrecision: int(datetimePrecision.Int64),
			Charset:           charset.String,
			Collation:         collation.String,
			ColumnKey:         columnKey.String,
			OrdinalPosition:   int(ordinal.Int64),
		}
		columns = append(columns, column)

		// Only PRI marks a primary key; UNI is a unique-column marker.
		if columnKey.String == "PRI" {
			pkColumns = append(pkColumns, column.Name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate column metadata: %w", err)
	}

	if len(columns) == 0 {
		return fmt.Errorf("column metadata is empty")
	}

	table.Columns = columns
	table.PKs = pkColumns
	return nil
}

// fetchIndexes retrieves index metadata from information_schema.STATISTICS.
// Rows are ordered by index and column position; the result is sorted by index name.
// An empty result clears previously stored indexes.
func (c *MariaDBConnector) fetchIndexes(ctx context.Context, table *interfaces.TableMeta) error {
	sqlStr, args, err := sq.Select(
		"INDEX_NAME",
		"COLUMN_NAME",
		"NON_UNIQUE",
		"SEQ_IN_INDEX",
	).From("information_schema.STATISTICS").
		Where(sq.Eq{"TABLE_SCHEMA": table.Database}).
		Where(sq.Eq{"TABLE_NAME": table.Name}).
		OrderBy("INDEX_NAME", "SEQ_IN_INDEX").
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build index metadata query: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return fmt.Errorf("failed to execute index metadata query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	indexMap := make(map[string]*interfaces.TableIndexMeta)

	for rows.Next() {
		var indexName, columnName sql.NullString
		var nonUnique, seqInIndex sql.NullInt64

		if err := rows.Scan(
			&indexName,
			&columnName,
			&nonUnique,
			&seqInIndex,
		); err != nil {
			return fmt.Errorf("failed to scan index metadata: %w", err)
		}
		if !indexName.Valid || !columnName.Valid || !nonUnique.Valid || !seqInIndex.Valid {
			return fmt.Errorf("required index metadata contains NULL")
		}

		if idx, ok := indexMap[indexName.String]; ok {
			idx.Columns = append(idx.Columns, columnName.String)
		} else {
			indexMap[indexName.String] = &interfaces.TableIndexMeta{
				Name:    indexName.String,
				Columns: []string{columnName.String},
				Unique:  nonUnique.Int64 == 0,
				Primary: indexName.String == "PRIMARY",
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate index metadata: %w", err)
	}

	indices := make([]interfaces.TableIndexMeta, 0, len(indexMap))
	for _, idx := range indexMap {
		indices = append(indices, *idx)
	}
	sort.Slice(indices, func(i, j int) bool {
		return indices[i].Name < indices[j].Name
	})

	table.Indices = indices
	return nil
}

// fetchForeignKeys retrieves foreign key metadata from information_schema.KEY_COLUMN_USAGE.
// Rows are grouped by constraint in column order; an empty result clears previous constraints.
func (c *MariaDBConnector) fetchForeignKeys(ctx context.Context, table *interfaces.TableMeta) error {
	sqlStr, args, err := sq.Select(
		"CONSTRAINT_NAME",
		"COLUMN_NAME",
		"REFERENCED_TABLE_NAME",
		"REFERENCED_COLUMN_NAME",
	).From("information_schema.KEY_COLUMN_USAGE").
		Where(sq.Eq{"TABLE_SCHEMA": table.Database}).
		Where(sq.Eq{"TABLE_NAME": table.Name}).
		Where(sq.NotEq{"REFERENCED_TABLE_NAME": nil}).
		OrderBy("CONSTRAINT_NAME", "ORDINAL_POSITION").
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build foreign key metadata query: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return fmt.Errorf("failed to execute foreign key metadata query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	fkMap := make(map[string]*interfaces.TableForeignKeyMeta)
	for rows.Next() {
		var constraintName, columnName, refTableName, refColumnName sql.NullString

		if err := rows.Scan(
			&constraintName,
			&columnName,
			&refTableName,
			&refColumnName,
		); err != nil {
			return fmt.Errorf("failed to scan foreign key metadata: %w", err)
		}
		if !constraintName.Valid || !columnName.Valid || !refTableName.Valid || !refColumnName.Valid {
			return fmt.Errorf("required foreign key metadata contains NULL")
		}

		if fk, ok := fkMap[constraintName.String]; ok {
			fk.Columns = append(fk.Columns, columnName.String)
			fk.RefColumns = append(fk.RefColumns, refColumnName.String)
		} else {
			fkMap[constraintName.String] = &interfaces.TableForeignKeyMeta{
				Name:       constraintName.String,
				Columns:    []string{columnName.String},
				RefTable:   refTableName.String,
				RefColumns: []string{refColumnName.String},
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate foreign key metadata: %w", err)
	}

	// KEY_COLUMN_USAGE supplies column pairs; referential actions are not returned
	// by this query and are left unset.

	fks := make([]interfaces.TableForeignKeyMeta, 0)
	for _, fk := range fkMap {
		fks = append(fks, *fk)
	}
	sort.Slice(fks, func(i, j int) bool {
		return fks[i].Name < fks[j].Name
	})
	table.ForeignKeys = fks
	return nil
}

// GetMetadata reads server variables, discoverable databases, and inferred cluster mode.
// Failed reads return an error rather than partial metadata.
func (c *MariaDBConnector) GetMetadata(ctx context.Context) (map[string]any, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect for database metadata: %w", err)
	}

	// Read server version, character settings, time zones, SQL mode, and cluster variables.
	targetVars := []string{
		"version",
		"version_comment",
		"version_compile_os",
		"character_set_server",
		"collation_server",
		"time_zone",
		"system_time_zone",
		"lower_case_table_names",
		"sql_mode",
		// Replication and read-only settings.
		"wsrep_on",                     // Galera Cluster
		"group_replication_group_name", // Group Replication / InnoDB Cluster
		"read_only",
		"super_read_only",
	}

	// Bind variable names instead of interpolating them into SHOW syntax.
	placeholders := make([]string, len(targetVars))
	args := make([]any, len(targetVars))
	for i, v := range targetVars {
		placeholders[i] = "?"
		args[i] = v
	}

	sqlStr := fmt.Sprintf("SHOW GLOBAL VARIABLES WHERE Variable_name IN (%s)", strings.Join(placeholders, ",")) //nolint:gosec // Placeholders are generated locally; values are driver-bound.
	rows, err := c.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute database metadata query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	metadata := make(map[string]any)
	for rows.Next() {
		var varName, varValue sql.NullString
		if err := rows.Scan(&varName, &varValue); err != nil {
			return nil, fmt.Errorf("failed to scan database metadata: %w", err)
		}
		if !varName.Valid {
			return nil, fmt.Errorf("required database metadata contains NULL")
		}
		metadata[varName.String] = varValue.String
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate database metadata: %w", err)
	}

	schemas, err := c.listSchemas(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list schemas: %w", err)
	}
	metadata["schemas"] = schemas

	// Default to standalone unless replication variables identify a cluster.
	metadata["cluster_mode"] = "standalone"
	if val, ok := metadata["wsrep_on"]; ok && strings.EqualFold(fmt.Sprint(val), "ON") {
		metadata["cluster_mode"] = "galera"
	} else if val, ok := metadata["group_replication_group_name"]; ok && fmt.Sprint(val) != "" {
		metadata["cluster_mode"] = "group_replication"
	}

	return metadata, nil
}

// listSchemas lists configured databases, including those without tables.
// Without a configured scope, it lists databases with tables and excludes system databases.
func (c *MariaDBConnector) listSchemas(ctx context.Context) ([]string, error) {
	var builder sq.SelectBuilder
	if len(c.config.Databases) > 0 {
		builder = sq.Select("SCHEMA_NAME").
			From("information_schema.SCHEMATA").
			Where(sq.Eq{"SCHEMA_NAME": c.config.Databases}).
			OrderBy("SCHEMA_NAME")
	} else {
		builder = sq.Select("DISTINCT TABLE_SCHEMA").
			From("information_schema.TABLES").
			Where(sq.NotEq{"TABLE_SCHEMA": SYSTEM_DBS}).
			OrderBy("TABLE_SCHEMA")
	}
	sqlStr, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build list schemas query: %w", err)
	}
	rows, err := c.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}
	defer func() { _ = rows.Close() }()

	schemas := make([]string, 0)
	for rows.Next() {
		var schema sql.NullString
		if err := rows.Scan(&schema); err != nil {
			return nil, fmt.Errorf("scan schema: %w", err)
		}
		if !schema.Valid {
			return nil, fmt.Errorf("required schema metadata contains NULL")
		}

		// SQL narrows the candidates; configured scope uses exact source-name matching.
		// Filter here to keep scope independent of source collation without applying
		// BINARY/COLLATE to columns, which can interfere with indexes or predicate pushdown.
		if !c.databaseAllowed(schema.String) {
			continue
		}

		schemas = append(schemas, schema.String)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schemas: %w", err)
	}
	return schemas, nil
}

// CountRows counts a table or view using the supplied SQL-ready source reference.
// The reference is already quoted by the connector and must not be rebuilt here.
func (c *MariaDBConnector) CountRows(ctx context.Context, sourceIdentifier string) (int64, error) {
	if sourceIdentifier == "" {
		return 0, fmt.Errorf("empty source identifier")
	}
	if err := c.Connect(ctx); err != nil {
		return 0, fmt.Errorf("failed to connect for row count: %w", err)
	}

	sqlStr, args, err := sq.Select("COUNT(*)").
		From(sourceIdentifier).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("failed to build row count query: %w", err)
	}
	var count int64
	if err := c.db.QueryRowContext(ctx, sqlStr, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to read row count: %w", err)
	}
	if count < 0 {
		return 0, fmt.Errorf("invalid negative row count")
	}
	return count, nil
}

// DecodeTableMeta decodes source metadata and restores missing legacy name components.
// Historical components are assumed not to contain dots or SQL quoting characters.
func (c *MariaDBConnector) DecodeTableMeta(metadata map[string]any) (*interfaces.TableMeta, error) {
	data, err := sonic.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("serialize table metadata: %w", err)
	}

	table := interfaces.TableMeta{}
	if err := common.UnmarshalPreciseJSON(data, &table); err != nil {
		return nil, fmt.Errorf("decode table metadata: %w", err)
	}
	if _, ok := metadata["description"]; !ok {
		table.Description, _ = metadata["original_description"].(string)
	}
	originalName, _ := metadata["original_name"].(string)
	if table.Schema == "" {
		table.Schema = table.Database
	}
	if table.Name == "" || table.Schema == "" {
		namespace, name, qualified := strings.Cut(originalName, ".")
		if qualified {
			if namespace == "" || name == "" || strings.Contains(name, ".") {
				return nil, fmt.Errorf("invalid legacy table name")
			}
			if table.Schema == "" {
				table.Schema = namespace
			}
		} else {
			name = originalName
		}
		if table.Name == "" {
			table.Name = name
		}
	}
	if table.Schema == "" && c.config != nil && len(c.config.Databases) == 1 {
		table.Schema = c.config.Databases[0]
	}
	if table.Database == "" {
		table.Database = table.Schema
	}
	if table.Name == "" || table.Schema == "" {
		return nil, fmt.Errorf("incomplete table metadata")
	}
	return &table, nil
}
