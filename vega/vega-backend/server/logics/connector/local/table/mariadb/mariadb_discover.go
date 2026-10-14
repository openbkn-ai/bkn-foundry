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
	"sort"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/bytedance/sonic"
	_ "github.com/go-sql-driver/mysql"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// ListTables returns all the tables in the database.
// If Config.Database is not empty, only list the tables of this database;
// If Config.Database is empty (instance level connection), traverse all user databases, and the returned TableMeta.Database field marks the library to which it belongs.
func (c *MariaDBConnector) ListTables(ctx context.Context) ([]*interfaces.TableMeta, error) {
	return c.listTables(ctx, "", "")
}

// listTables filters readable tables by optional database and table name.
func (c *MariaDBConnector) listTables(ctx context.Context, database, tableName string) ([]*interfaces.TableMeta, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	if database != "" && !c.databaseAllowed(database) {
		return nil, fmt.Errorf("database %q is outside the connector scope", database)
	}

	builder := sq.Select(
		"TABLE_SCHEMA",
		"TABLE_NAME",
		"TABLE_TYPE",
		"ENGINE",
		"TABLE_COLLATION",
		"TABLE_ROWS",
		"TABLE_COMMENT",
		"CREATE_TIME",
		"UPDATE_TIME",
		"DATA_LENGTH",
		"INDEX_LENGTH",
	).From("information_schema.TABLES")

	// A qualified source identifier selects one database. It must still remain
	// within the connector's configured database scope.
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

	query, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build list tables query: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}
	defer func() { _ = rows.Close() }()

	tables := make([]*interfaces.TableMeta, 0)
	for rows.Next() {
		var schemaName, tableName, tableType sql.NullString
		var engine, collation, description sql.NullString
		var tableRows, dataLength, indexLength sql.NullInt64
		var createTime, updateTime sql.NullTime

		if err := rows.Scan(
			&schemaName,
			&tableName,
			&tableType,
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

		// SQL 条件用于缩小范围；配置范围统一按源端原始名称精确匹配。
		// 此处过滤可避免数据库比较规则与配置语义不一致，
		// 并保留普通 SQL 条件，避免 BINARY/COLLATE 影响索引使用或条件下推。
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

		// Populate Properties
		meta.Properties["engine"] = engine.String
		meta.Properties["collation"] = collation.String
		meta.Properties["estimated_row_count"] = tableRows.Int64
		meta.Properties["data_length"] = dataLength.Int64
		meta.Properties["index_length"] = indexLength.Int64

		if createTime.Valid {
			meta.Properties["create_time"] = createTime.Time.UnixMilli()
		}
		if updateTime.Valid {
			meta.Properties["update_time"] = updateTime.Time.UnixMilli()
		}

		// Infer Charset from Collation
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

// mapTableType maps a MariaDB or MySQL table type to a table metadata type.
func (c *MariaDBConnector) mapTableType(tableType string) string {
	if strings.ToUpper(tableType) == "VIEW" {
		return interfaces.TableTypeView
	}
	return interfaces.TableTypeTable
}

// databaseAllowed checks whether a database is inside the configured scope.
func (c *MariaDBConnector) databaseAllowed(database string) bool {
	if len(c.config.Databases) == 0 {
		return true
	}
	for _, configuredDatabase := range c.config.Databases {
		if configuredDatabase == database {
			return true
		}
	}
	return false
}

// GetTableMeta returns metadata for a specific table.
// table format: "table_name" or "database.table_name"
func (c *MariaDBConnector) GetTableMeta(ctx context.Context, table *interfaces.TableMeta) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}
	if !c.databaseAllowed(table.Database) {
		return fmt.Errorf("database %q is outside the connector scope", table.Database)
	}

	// Obtain the basic information of the table (engine, character set, number of rows, comments)
	if err := c.fetchTableStatus(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch table status: %w", err)
	}

	// 2. Obtain field information
	if err := c.fetchColumns(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch columns: %w", err)
	}

	// 3. Obtain index information
	if err := c.fetchIndexes(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch indexes: %w", err)
	}

	// 4. Obtain foreign key information
	if err := c.fetchForeignKeys(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch foreign keys: %w", err)
	}
	return nil
}

// fetchTableStatus retrieves table status from information_schema.TABLES.
func (c *MariaDBConnector) fetchTableStatus(ctx context.Context, table *interfaces.TableMeta) error {
	query, args, err := sq.Select(
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
		return err
	}

	var tableType, engine, collation, description sql.NullString
	var autoIncrement, tableRows, dataLength, indexLength sql.NullInt64
	var createTime, updateTime sql.NullTime

	row := c.db.QueryRowContext(ctx, query, args...)
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
		if err == sql.ErrNoRows {
			return fmt.Errorf("table metadata not found or inaccessible: %s.%s", table.Database, table.Name)
		}
		return err
	}
	if !tableType.Valid {
		return fmt.Errorf("required table metadata contains NULL")
	}

	table.TableType = c.mapTableType(tableType.String)

	// Properties 以本次读取结果为准，成功后整体替换。
	properties := map[string]any{
		"engine":              engine.String,
		"collation":           collation.String,
		"estimated_row_count": tableRows.Int64,
		"data_length":         dataLength.Int64,
		"index_length":        indexLength.Int64,
	}
	if autoIncrement.Valid {
		properties["auto_increment"] = autoIncrement.Int64
	}
	table.Description = description.String

	if createTime.Valid {
		properties["create_time"] = createTime.Time.UnixMilli()
	}
	if updateTime.Valid {
		properties["update_time"] = updateTime.Time.UnixMilli()
	}

	// Infer the Charset from Collation
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
func (c *MariaDBConnector) fetchColumns(ctx context.Context, table *interfaces.TableMeta) error {
	query, args, err := sq.Select(
		"COLUMN_NAME",
		"DATA_TYPE",
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
		"ORDINAL_POSITION",
		"COLUMN_KEY",
	).From("information_schema.COLUMNS").
		Where(sq.Eq{"TABLE_SCHEMA": table.Database}).
		Where(sq.Eq{"TABLE_NAME": table.Name}).
		OrderBy("ORDINAL_POSITION").
		ToSql()
	if err != nil {
		return err
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	columns := make([]interfaces.TableColumnMeta, 0)
	pkColumns := make([]string, 0)

	for rows.Next() {
		var name, columnType, dataType, isNullable, columnKey sql.NullString
		var columnDefault, description, charset, collation sql.NullString
		var position, charMaxLen, numPrecision, numScale, datetimePrecision sql.NullInt64

		if err := rows.Scan(
			&name,
			&dataType,
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
			&position,
			&columnKey,
		); err != nil {
			return err
		}
		if !name.Valid || !columnType.Valid || !isNullable.Valid || !position.Valid {
			return fmt.Errorf("required column metadata contains NULL")
		}

		column := interfaces.TableColumnMeta{
			Name:              name.String,
			Type:              columnType.String, // Use COLUMN_TYPE to correctly identify unsigned (such as "int unsigned")
			Description:       description.String,
			Nullable:          isNullable.String == "YES",
			DefaultValue:      columnDefault.String,
			CharMaxLen:        int(charMaxLen.Int64),
			NumPrecision:      int(numPrecision.Int64),
			NumScale:          int(numScale.Int64),
			DatetimePrecision: int(datetimePrecision.Int64),
			Charset:           charset.String,
			Collation:         collation.String,
			OrdinalPosition:   int(position.Int64),
			ColumnKey:         columnKey.String,
		}
		columns = append(columns, column)

		// Check if it is the primary key
		if columnKey.String == "PRI" {
			pkColumns = append(pkColumns, column.Name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	table.Columns = columns
	table.PKs = pkColumns
	return nil
}

// fetchIndexes retrieves index metadata from information_schema.STATISTICS.
func (c *MariaDBConnector) fetchIndexes(ctx context.Context, table *interfaces.TableMeta) error {
	query, args, err := sq.Select(
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
		return err
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
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
			return err
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
		return err
	}

	indices := make([]interfaces.TableIndexMeta, 0)
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
func (c *MariaDBConnector) fetchForeignKeys(ctx context.Context, table *interfaces.TableMeta) error {
	query, args, err := sq.Select(
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
		return err
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
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
			return err
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
		return err
	}

	// Note: Handling OnDelete/OnUpdate requires joining with REFERENTIAL_CONSTRAINTS, skipping for simplicity unless requested.

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

// GetMetadata returns the metadata for the catalog.
func (c *MariaDBConnector) GetMetadata(ctx context.Context) (map[string]any, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	// 2. Fetch critical global variables
	// It includes basic information, character set, time zone, case sensitivity, SQL mode and cluster-related information
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
		// Cluster related
		"wsrep_on",                     // Galera Cluster
		"group_replication_group_name", // Group Replication / InnoDB Cluster
		"read_only",
		"super_read_only",
	}

	// Construct placeholders
	placeholders := make([]string, len(targetVars))
	args := make([]any, len(targetVars))
	for i, v := range targetVars {
		placeholders[i] = "?"
		args[i] = v
	}

	query := fmt.Sprintf("SHOW GLOBAL VARIABLES WHERE Variable_name IN (%s)", strings.Join(placeholders, ",")) //nolint:gosec // Placeholders are generated locally; values are driver-bound.
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		// Just log error and return partial metadata if SHOW VARIABLES fails (unlikely)
		// But for now, we return error to be safe
		return nil, err
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
		return nil, err
	}

	schemas, err := c.listSchemas(ctx)
	if err != nil {
		return nil, err
	}
	metadata["schemas"] = schemas

	// 3. Infer Cluster Mode
	metadata["cluster_mode"] = "standalone" // Default
	if val, ok := metadata["wsrep_on"]; ok && strings.EqualFold(fmt.Sprint(val), "ON") {
		metadata["cluster_mode"] = "galera"
	} else if val, ok := metadata["group_replication_group_name"]; ok && fmt.Sprint(val) != "" {
		metadata["cluster_mode"] = "group_replication"
	}

	return metadata, nil
}

// listSchemas lists configured databases or databases with discoverable tables.
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

		// SQL 条件用于缩小范围；配置范围统一按源端原始名称精确匹配。
		// 此处过滤可避免数据库比较规则与配置语义不一致，
		// 并保留普通 SQL 条件，避免 BINARY/COLLATE 影响索引使用或条件下推。
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

// CountRows 使用源 SQL 表引用获取表或视图的精确数量。
func (c *MariaDBConnector) CountRows(ctx context.Context, sourceIdentifier string) (int64, error) {
	if sourceIdentifier == "" {
		return 0, fmt.Errorf("empty source identifier")
	}
	if err := c.Connect(ctx); err != nil {
		return 0, err
	}

	query, args, err := sq.Select("COUNT(*)").
		From(sourceIdentifier).
		ToSql()
	if err != nil {
		return 0, err
	}
	var count int64
	if err := c.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	if count < 0 {
		return 0, fmt.Errorf("invalid negative row count")
	}
	return count, nil
}

// BuildSourceIdentifier 生成源 SQL 表引用，不连接源数据库。
func (c *MariaDBConnector) BuildSourceIdentifier(meta *interfaces.TableMeta) string {
	namespace := meta.Database
	if namespace == "" {
		namespace = meta.Schema
	}

	return mariadbQuoteIdent(namespace) + "." + mariadbQuoteIdent(meta.Name)
}

// BuildOriginalName 生成不含 SQL 引用符的源端名称，不连接源数据库。
func (c *MariaDBConnector) BuildOriginalName(meta *interfaces.TableMeta) string {
	namespace := meta.Database
	if namespace == "" {
		namespace = meta.Schema
	}
	return namespace + "." + meta.Name
}

// DecodeTableMeta decodes source metadata and restores missing legacy name components.
// Historical components are assumed not to contain dots or SQL quoting characters.
func (c *MariaDBConnector) DecodeTableMeta(metadata map[string]any) (*interfaces.TableMeta, error) {
	data, err := sonic.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("serialize table metadata: %w", err)
	}
	table := &interfaces.TableMeta{}
	if err := common.UnmarshalPreciseJSON(data, table); err != nil {
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
	return table, nil
}
