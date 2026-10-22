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
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

var stmtBuilder = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Unsupported relkinds do not occur in pg_class, so this fixed filter works on compatible databases.
var tableRelKinds = []string{"r", "v", "f", "m", "p"}

type postgresqlDomainMetadata struct {
	BaseType        string
	BaseTypeKind    string
	BaseTypmod      int64
	NotNull         bool
	DefaultValue    sql.NullString
	CheckConstraint string
}

// fetchDomainMetadata resolves domain chains to their base types, constraints, and defaults.
func (c *PostgresqlConnector) fetchDomainMetadata(ctx context.Context,
	domainOIDs []int64) (map[int64]postgresqlDomainMetadata, error) {
	metadata := make(map[int64]postgresqlDomainMetadata, len(domainOIDs))
	if len(domainOIDs) == 0 {
		return metadata, nil
	}

	placeholders := make([]string, len(domainOIDs))
	args := make([]any, len(domainOIDs))
	for i, oid := range domainOIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = oid
	}

	//nolint:gosec // PostgreSQL placeholders are generated locally; OID values are driver-bound.
	query := fmt.Sprintf(`
		WITH RECURSIVE domain_chain AS (
			SELECT
				root.oid AS domain_oid,
				root.oid AS current_domain_oid,
				0 AS domain_depth,
				root.typbasetype AS base_type_oid,
				root.typtypmod AS base_typmod,
				root.typnotnull AS domain_not_null
			FROM pg_catalog.pg_type root
				WHERE root.typtype = 'd' AND root.oid IN (%s)
			UNION ALL
			SELECT
				d.domain_oid,
				parent.oid,
				d.domain_depth + 1,
				parent.typbasetype,
				CASE WHEN d.base_typmod >= 0 THEN d.base_typmod ELSE parent.typtypmod END,
				d.domain_not_null OR parent.typnotnull
			FROM domain_chain d
			JOIN pg_catalog.pg_type parent
				ON parent.oid = d.base_type_oid
			WHERE parent.typtype = 'd'
		),
		resolved_domain AS (
			SELECT
				d.domain_oid,
				d.base_type_oid,
				d.base_typmod,
				d.domain_not_null
			FROM domain_chain d
			JOIN pg_catalog.pg_type base_type
				ON base_type.oid = d.base_type_oid
			WHERE base_type.typtype <> 'd'
		),
		domain_checks AS (
			SELECT
				d.domain_oid,
				string_agg(pg_catalog.pg_get_constraintdef(con.oid, false), '; '
					ORDER BY d.domain_depth DESC, con.conname) AS check_constraint
			FROM domain_chain d
			JOIN pg_catalog.pg_constraint con
				ON con.contypid = d.current_domain_oid AND con.contype = 'c'
			GROUP BY d.domain_oid
		)

		SELECT
			root.oid AS domain_oid,
			base_type.typname AS base_type,
			base_type.typtype::text AS base_type_kind,
			resolved.base_typmod,
			resolved.domain_not_null,
			COALESCE(pg_catalog.pg_get_expr(root.typdefaultbin, 0), root.typdefault) AS domain_default,
			COALESCE(checks.check_constraint, '') AS check_constraint
		FROM pg_catalog.pg_type root
		JOIN resolved_domain resolved
			ON resolved.domain_oid = root.oid
		JOIN pg_catalog.pg_type base_type
			ON base_type.oid = resolved.base_type_oid
		LEFT JOIN domain_checks checks
			ON checks.domain_oid = root.oid
		ORDER BY root.oid
	`, strings.Join(placeholders, ", "))

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PostgreSQL domain metadata: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var domainOID, baseTypmod sql.NullInt64
		var baseType, baseTypeKind, defaultValue, checkConstraint sql.NullString
		var notNull sql.NullBool
		if err := rows.Scan(
			&domainOID,
			&baseType,
			&baseTypeKind,
			&baseTypmod,
			&notNull,
			&defaultValue,
			&checkConstraint,
		); err != nil {
			return nil, fmt.Errorf("failed to scan PostgreSQL domain metadata: %w", err)
		}
		if !domainOID.Valid || !baseType.Valid || !baseTypeKind.Valid || !baseTypmod.Valid ||
			!notNull.Valid || !checkConstraint.Valid {
			return nil, fmt.Errorf("required PostgreSQL domain metadata contains NULL")
		}
		metadata[domainOID.Int64] = postgresqlDomainMetadata{
			BaseType:        c.normalizeColumnType(baseType.String),
			BaseTypeKind:    baseTypeKind.String,
			BaseTypmod:      baseTypmod.Int64,
			NotNull:         notNull.Bool,
			DefaultValue:    defaultValue,
			CheckConstraint: checkConstraint.String,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate PostgreSQL domain metadata: %w", err)
	}
	return metadata, nil
}

// ListTables lists readable relations in the configured discovery scope.
// Each result retains raw database and schema names for later metadata reads.
func (c *PostgresqlConnector) ListTables(ctx context.Context) ([]*interfaces.TableMeta, error) {
	return c.listTables(ctx, "", "")
}

// discoverableRelationsBuilder selects readable, non-temporary relations outside system schemas.
// Inherited and partition child relations are excluded to avoid discovering their data twice.
func discoverableRelationsBuilder() sq.SelectBuilder {
	return stmtBuilder.Select().
		From("pg_catalog.pg_class c").
		Join("pg_catalog.pg_namespace n ON n.oid = c.relnamespace").
		// relkind: r=ordinary table, p=partitioned table, v=view, m=materialized view, f=foreign table.
		Where(sq.Eq{"c.relkind": tableRelKinds}).
		// relpersistence: p=permanent, u=unlogged, t=temporary.
		Where(sq.NotEq{"c.relpersistence": "t"}).
		Where(sq.Expr("has_table_privilege(c.oid, ?)", "SELECT")).
		Where(sq.Expr("has_schema_privilege(n.oid, ?)", "USAGE")).
		Where(sq.NotEq{"n.nspname": SYSTEM_SCHEMAS}).
		Where(sq.Expr("NOT pg_is_other_temp_schema(n.oid)")).
		Where(sq.Expr("NOT EXISTS (SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhrelid = c.oid)"))
}

// listTables filters readable tables and views by optional schema and table name.
func (c *PostgresqlConnector) listTables(ctx context.Context, schema, tableName string) ([]*interfaces.TableMeta, error) {
	if schema != "" && !c.schemaAllowed(schema) {
		return nil, fmt.Errorf("schema %q is outside the connector scope", schema)
	}
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect for table discovery: %w", err)
	}

	builder := discoverableRelationsBuilder().Columns(
		"n.nspname AS table_schema",
		"c.relname AS table_name",
		"c.relkind::text AS relkind",
		"COALESCE(obj_description(c.oid, 'pg_class'), '') AS description",
	)

	if schema != "" {
		builder = builder.Where(sq.Eq{"n.nspname": schema})
	} else if len(c.config.Schemas) > 0 {
		builder = builder.Where(sq.Eq{"n.nspname": c.config.Schemas})
	}
	if tableName != "" {
		builder = builder.Where(sq.Eq{"c.relname": tableName})
	}
	builder = builder.OrderBy("n.nspname", "c.relname")

	query, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build list tables query: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute list tables query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	tables := make([]*interfaces.TableMeta, 0)
	for rows.Next() {
		var schema, name, relKind, description sql.NullString

		if err := rows.Scan(
			&schema,
			&name,
			&relKind,
			&description,
		); err != nil {
			return nil, fmt.Errorf("failed to scan table info: %w", err)
		}
		if !schema.Valid || !name.Valid || !relKind.Valid {
			return nil, fmt.Errorf("required table metadata contains NULL")
		}

		// SQL predicates narrow the results; configured scope uses exact source names.
		// Filter after scanning to preserve exact matching regardless of database comparison rules,
		// without BINARY/COLLATE casts that could prevent index use or predicate pushdown.
		if !c.schemaAllowed(schema.String) {
			continue
		}

		meta := &interfaces.TableMeta{
			Name:        name.String,
			TableType:   c.mapTableType(relKind.String),
			Description: description.String,
			Database:    c.config.Database,
			Schema:      schema.String,
			Properties:  make(map[string]any),
		}

		tables = append(tables, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate table info: %w", err)
	}
	return tables, nil
}

// schemaAllowed checks configured scope using exact source names.
// An empty scope allows every schema; default system-schema exclusions belong to discovery.
func (c *PostgresqlConnector) schemaAllowed(schema string) bool {
	if len(c.config.Schemas) == 0 {
		return true
	}
	for _, configuredSchema := range c.config.Schemas {
		if configuredSchema == schema {
			return true
		}
	}
	return false
}

// mapTableType maps a PostgreSQL relkind to a table metadata type.
func (c *PostgresqlConnector) mapTableType(relKind string) string {
	switch relKind {
	case "v":
		return "view"
	case "m":
		return "materialized_view"
	default:
		return "table"
	}
}

// GetTableMeta refreshes status, columns, indexes, and foreign keys using raw name components.
// Scope is checked before connecting; each successful stage updates its part of table.
func (c *PostgresqlConnector) GetTableMeta(ctx context.Context, table *interfaces.TableMeta, _ bool) error {
	if !c.schemaAllowed(table.Schema) {
		return fmt.Errorf("schema %q is outside the connector scope", table.Schema)
	}
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect for table metadata: %w", err)
	}

	// ListTables omits row counts and storage sizes, so status must still be read.
	if err := c.fetchTableStatus(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch table status: %w", err)
	}

	// Refresh columns and primary-key column names.
	if err := c.fetchColumns(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch columns: %w", err)
	}

	// Refresh secondary and primary index metadata.
	if err := c.fetchIndexes(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch indexes: %w", err)
	}

	// Refresh foreign-key constraints and referenced columns.
	if err := c.fetchForeignKeys(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch foreign keys: %w", err)
	}
	return nil
}

// fetchTableStatus retrieves the table type, description, estimated row count, and storage sizes.
// Missing rows retain sql.ErrNoRows; failed reads leave the existing snapshot unchanged.
func (c *PostgresqlConnector) fetchTableStatus(ctx context.Context, table *interfaces.TableMeta) error {
	relKinds := "'" + strings.Join(tableRelKinds, "', '") + "'"
	//nolint:gosec // Relation kinds are static constants, not user input.
	query := fmt.Sprintf(`
		SELECT c.relkind::text,
			obj_description(c.oid, 'pg_class') AS description,
			COALESCE(s.n_live_tup, 0) AS est_rows,
			pg_total_relation_size(c.oid) AS total_bytes,
			pg_indexes_size(c.oid) AS index_bytes
		FROM pg_class c
		JOIN pg_namespace n
			ON n.oid = c.relnamespace
		LEFT JOIN pg_stat_user_tables s
			ON s.relid = c.oid
		WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN (%s)
	`, relKinds)

	var relKind, description sql.NullString
	var estimatedRowCount, totalBytes, indexBytes sql.NullInt64

	row := c.db.QueryRowContext(ctx, query, table.Schema, table.Name)
	if err := row.Scan(
		&relKind,
		&description,
		&estimatedRowCount,
		&totalBytes,
		&indexBytes,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("table metadata not found or inaccessible: %s.%s: %w", table.Schema, table.Name, err)
		}
		return fmt.Errorf("failed to read table status: %w", err)
	}
	if !relKind.Valid {
		return fmt.Errorf("required table metadata contains NULL")
	}

	table.TableType = c.mapTableType(relKind.String)
	table.Description = description.String

	properties := make(map[string]any)
	if estimatedRowCount.Valid && estimatedRowCount.Int64 >= 0 {
		properties["estimated_row_count"] = estimatedRowCount.Int64
	}
	if totalBytes.Valid && totalBytes.Int64 >= 0 {
		properties["data_length"] = totalBytes.Int64
	}
	if indexBytes.Valid && indexBytes.Int64 >= 0 {
		properties["index_length"] = indexBytes.Int64
	}
	table.Properties = properties
	return nil
}

// fetchColumns retrieves column metadata and resolves domain types and primary-key columns.
func (c *PostgresqlConnector) fetchColumns(ctx context.Context, table *interfaces.TableMeta) error {
	relKinds := "'" + strings.Join(tableRelKinds, "', '") + "'"
	//nolint:gosec // Relation kinds are static constants, not user input.
	query := fmt.Sprintf(`
		SELECT
			a.attname AS column_name,
			a.atttypid AS type_oid,
			t.typtype::text AS type_kind,
			t.typname AS type_name,
			a.atttypmod AS type_modifier,
			a.attnotnull AS column_not_null,
			pg_catalog.pg_get_expr(ad.adbin, ad.adrelid) AS column_default,
			COALESCE(coll.collname, '') AS collation_name,
			a.attnum AS ordinal_position,
			COALESCE(pg_catalog.col_description(a.attrelid, a.attnum), '') AS description
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n
			ON n.oid = c.relnamespace
		JOIN pg_catalog.pg_attribute a
			ON a.attrelid = c.oid
		JOIN pg_catalog.pg_type t
			ON t.oid = a.atttypid
		LEFT JOIN pg_catalog.pg_attrdef ad
			ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
		LEFT JOIN pg_catalog.pg_collation coll
			ON coll.oid = a.attcollation
		WHERE n.nspname = $1
			AND c.relname = $2
			AND c.relkind IN (%s)
			AND a.attnum > 0
			AND NOT a.attisdropped
		ORDER BY a.attnum
	`, relKinds)

	rows, err := c.db.QueryContext(ctx, query, table.Schema, table.Name)
	if err != nil {
		return fmt.Errorf("failed to execute column metadata query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type columnMetadata struct {
		name, typeKind, typeName, collation, description sql.NullString
		typeOID, typeModifier, ordinalPosition           sql.NullInt64
		notNull                                          sql.NullBool
		defaultValue                                     sql.NullString
	}

	rawColumns := make([]columnMetadata, 0)
	domainOIDs := make([]int64, 0)
	seenDomainOIDs := make(map[int64]bool)
	for rows.Next() {
		var column columnMetadata
		if err := rows.Scan(
			&column.name,
			&column.typeOID,
			&column.typeKind,
			&column.typeName,
			&column.typeModifier,
			&column.notNull,
			&column.defaultValue,
			&column.collation,
			&column.ordinalPosition,
			&column.description,
		); err != nil {
			return fmt.Errorf("failed to scan PostgreSQL column metadata: %w", err)
		}
		if !column.name.Valid || !column.typeOID.Valid || !column.typeKind.Valid ||
			!column.typeName.Valid || !column.typeModifier.Valid || !column.notNull.Valid ||
			!column.collation.Valid || !column.ordinalPosition.Valid || !column.description.Valid {
			return fmt.Errorf("required PostgreSQL column metadata contains NULL")
		}
		rawColumns = append(rawColumns, column)
		if column.typeKind.String == "d" && !seenDomainOIDs[column.typeOID.Int64] {
			domainOIDs = append(domainOIDs, column.typeOID.Int64)
			seenDomainOIDs[column.typeOID.Int64] = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate PostgreSQL column metadata: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("failed to close PostgreSQL column metadata rows: %w", err)
	}

	domains, err := c.fetchDomainMetadata(ctx, domainOIDs)
	if err != nil {
		return fmt.Errorf("failed to fetch domain metadata: %w", err)
	}
	pks, err := c.fetchPrimaryKeyColumns(ctx, table.Schema, table.Name)
	if err != nil {
		return fmt.Errorf("failed to fetch primary key columns: %w", err)
	}

	pkSet := make(map[string]bool, len(pks))
	for _, name := range pks {
		pkSet[name] = true
	}

	columns := make([]interfaces.TableColumnMeta, 0, len(rawColumns))
	for _, raw := range rawColumns {
		typeModifier := raw.typeModifier.Int64
		column := interfaces.TableColumnMeta{
			Name:            raw.name.String,
			Type:            c.normalizeColumnType(raw.typeName.String),
			Description:     raw.description.String,
			Nullable:        !raw.notNull.Bool,
			DefaultValue:    raw.defaultValue.String,
			Collation:       raw.collation.String,
			OrdinalPosition: int(raw.ordinalPosition.Int64),
		}
		if pkSet[raw.name.String] {
			column.ColumnKey = "PRI"
		}
		switch raw.typeKind.String {
		case "d":
			domain, ok := domains[raw.typeOID.Int64]
			if ok {
				column.AliasType = raw.typeName.String
				column.Type = domain.BaseType
				column.CheckConstraint = domain.CheckConstraint
				column.Nullable = column.Nullable && !domain.NotNull
				if !raw.defaultValue.Valid && domain.DefaultValue.Valid {
					column.DefaultValue = domain.DefaultValue.String
				}
				typeModifier = domain.BaseTypmod
				if domain.BaseTypeKind == "e" {
					column.Type = "enum"
				}
			}
		case "e":
			column.AliasType = raw.typeName.String
			column.Type = "enum"
		}
		switch column.Type {
		case "bpchar", "varchar":
			if typeModifier > 0 {
				column.CharMaxLen = int(typeModifier - 4)
			}
		case "numeric":
			if typeModifier >= 0 {
				column.NumPrecision = int(((typeModifier - 4) >> 16) & 65535)
				column.NumScale = int((typeModifier - 4) & 65535)
			}
		case "time", "timetz", "timestamp", "timestamptz":
			if typeModifier >= 0 {
				column.DatetimePrecision = int(typeModifier)
			}
		}
		columns = append(columns, column)
	}

	// PostgreSQL supports zero-column tables; an empty column list is valid.
	table.Columns = columns
	table.PKs = pks
	return nil
}

// fetchPrimaryKeyColumns retrieves the primary-key column names for a table.
func (c *PostgresqlConnector) fetchPrimaryKeyColumns(ctx context.Context, schema, tableName string) ([]string, error) {
	// 将目标表不可访问与可访问但没有主键的情况区分开。
	const accessQuery = `
		SELECT pg_catalog.has_schema_privilege(n.oid, 'USAGE')
			AND pg_catalog.has_table_privilege(t.oid, 'SELECT')
		FROM pg_catalog.pg_class t
		JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = $1 AND t.relname = $2
	`
	var readable bool
	if err := c.db.QueryRowContext(ctx, accessQuery, schema, tableName).Scan(&readable); err != nil {
		return nil, fmt.Errorf("failed to check primary key metadata access: %w", err)
	}
	if !readable {
		return nil, fmt.Errorf("primary key metadata requires schema USAGE and table SELECT privileges")
	}

	// table_constraints 会隐藏只读账号的约束，因此直接读取系统目录。
	// 使用子查询兼容不支持 LATERAL 或 WITH ORDINALITY 的数据库。
	query := `
		SELECT a.attname
		FROM (
			SELECT con.conrelid, con.conkey,
				generate_subscripts(con.conkey, 1) AS position
			FROM pg_catalog.pg_constraint con
			JOIN pg_catalog.pg_class t ON t.oid = con.conrelid
			JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace
			WHERE n.nspname = $1 AND t.relname = $2
				AND con.contype = 'p'
		) pk
		JOIN pg_catalog.pg_attribute a
			ON a.attrelid = pk.conrelid AND a.attnum = pk.conkey[pk.position]
			AND NOT a.attisdropped
		ORDER BY pk.position
	`

	rows, err := c.db.QueryContext(ctx, query, schema, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to execute primary key metadata query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var col sql.NullString
		if err := rows.Scan(&col); err != nil {
			return nil, fmt.Errorf("failed to scan primary key metadata: %w", err)
		}
		if !col.Valid {
			return nil, fmt.Errorf("required primary key metadata contains NULL")
		}
		out = append(out, col.String)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate primary key metadata: %w", err)
	}
	return out, nil
}

// fetchIndexes retrieves index metadata for a table.
func (c *PostgresqlConnector) fetchIndexes(ctx context.Context, table *interfaces.TableMeta) error {
	query := c.indexMetadataQuery()

	rows, err := c.db.QueryContext(ctx, query, table.Schema, table.Name)
	if err != nil {
		return fmt.Errorf("failed to execute index metadata query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	indexMap := make(map[string]*interfaces.TableIndexMeta)
	for rows.Next() {
		var indexName, columnName sql.NullString
		var isUnique, isPrimary sql.NullBool
		var ord sql.NullInt64
		if err := rows.Scan(
			&indexName,
			&columnName,
			&isUnique,
			&isPrimary,
			&ord,
		); err != nil {
			return fmt.Errorf("failed to scan index metadata: %w", err)
		}
		if !indexName.Valid || !columnName.Valid || !isUnique.Valid || !isPrimary.Valid || !ord.Valid {
			return fmt.Errorf("required index metadata contains NULL")
		}

		if idx, ok := indexMap[indexName.String]; ok {
			idx.Columns = append(idx.Columns, columnName.String)
		} else {
			indexMap[indexName.String] = &interfaces.TableIndexMeta{
				Name:    indexName.String,
				Columns: []string{columnName.String},
				Unique:  isUnique.Bool,
				Primary: isPrimary.Bool,
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate index metadata: %w", err)
	}

	indices := make([]interfaces.TableIndexMeta, 0)
	for _, idx := range indexMap {
		indices = append(indices, *idx)
	}
	table.Indices = indices
	return nil
}

// indexMetadataQuery selects an index metadata query based on database compatibility.
func (c *PostgresqlConnector) indexMetadataQuery() string {
	if !c.compatibility.supportsLateral() {
		return `
			SELECT
				q.index_name,
				a.attname AS column_name,
				q.indisunique,
				q.indisprimary,
				q.ord
			FROM (
				SELECT
					i.relname AS index_name,
					t.oid AS table_oid,
					ix.indkey::int[] AS indkey,
					ix.indisunique,
					ix.indisprimary,
					generate_subscripts(ix.indkey::int[], 1) AS ord
				FROM pg_index ix
				JOIN pg_class t
					ON t.oid = ix.indrelid
				JOIN pg_namespace n
					ON n.oid = t.relnamespace
				JOIN pg_class i
					ON i.oid = ix.indexrelid
				WHERE n.nspname = $1 AND t.relname = $2
			) q
			JOIN pg_attribute a
				ON a.attrelid = q.table_oid
					AND a.attnum = q.indkey[q.ord]
					AND q.indkey[q.ord] > 0
					AND NOT a.attisdropped
			ORDER BY q.index_name, q.ord
		`
	}

	return `
		SELECT
			i.relname AS index_name,
			a.attname AS column_name,
			ix.indisunique,
			ix.indisprimary,
			k.n AS ord
		FROM pg_index ix
		JOIN pg_class t
			ON t.oid = ix.indrelid
		JOIN pg_namespace n
			ON n.oid = t.relnamespace
		JOIN pg_class i
			ON i.oid = ix.indexrelid
		JOIN LATERAL generate_subscripts(ix.indkey::int[], 1) AS k(n)
			ON true
		JOIN pg_attribute a
			ON a.attrelid = t.oid
				AND a.attnum = (ix.indkey::int[])[k.n]
				AND (ix.indkey::int[])[k.n] > 0
				AND NOT a.attisdropped
		WHERE n.nspname = $1 AND t.relname = $2
		ORDER BY i.relname, k.n
	`
}

// fetchForeignKeys retrieves foreign keys and their referenced columns.
func (c *PostgresqlConnector) fetchForeignKeys(ctx context.Context, table *interfaces.TableMeta) error {
	query := c.foreignKeyMetadataQuery()

	rows, err := c.db.QueryContext(ctx, query, table.Schema, table.Name)
	if err != nil {
		return fmt.Errorf("failed to execute foreign key metadata query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	fkMap := make(map[string]*interfaces.TableForeignKeyMeta)
	for rows.Next() {
		var constraintName, columnName, refColumnName, refSchemaName, refTableName sql.NullString
		if err := rows.Scan(
			&constraintName,
			&columnName,
			&refColumnName,
			&refSchemaName,
			&refTableName,
		); err != nil {
			return fmt.Errorf("failed to scan foreign key metadata: %w", err)
		}
		if !constraintName.Valid || !columnName.Valid || !refColumnName.Valid || !refSchemaName.Valid || !refTableName.Valid {
			return fmt.Errorf("required foreign key metadata contains NULL")
		}

		refFull := refSchemaName.String + "." + refTableName.String
		if fk, ok := fkMap[constraintName.String]; ok {
			fk.Columns = append(fk.Columns, columnName.String)
			fk.RefColumns = append(fk.RefColumns, refColumnName.String)
		} else {
			fkMap[constraintName.String] = &interfaces.TableForeignKeyMeta{
				Name:       constraintName.String,
				Columns:    []string{columnName.String},
				RefTable:   refFull,
				RefColumns: []string{refColumnName.String},
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate foreign key metadata: %w", err)
	}

	var fks []interfaces.TableForeignKeyMeta
	for _, fk := range fkMap {
		fks = append(fks, *fk)
	}
	table.ForeignKeys = fks
	return nil
}

// foreignKeyMetadataQuery selects a foreign-key metadata query based on database compatibility.
func (c *PostgresqlConnector) foreignKeyMetadataQuery() string {
	if !c.compatibility.supportsWithOrdinality() {
		return `
			SELECT
				q.conname,
				a.attname AS col,
				af.attname AS ref_col,
				nf.nspname AS ref_schema,
				cf.relname AS ref_table
			FROM (
				SELECT
					c.conname,
					c.conrelid,
					c.confrelid,
					c.conkey::int[] AS conkey,
					c.confkey::int[] AS confkey,
					generate_subscripts(c.conkey::int[], 1) AS ord
				FROM pg_constraint c
				JOIN pg_namespace n
					ON n.oid = c.connamespace
				JOIN pg_class cl
					ON cl.oid = c.conrelid AND cl.relnamespace = n.oid
				WHERE c.contype = 'f'
					AND n.nspname = $1
					AND cl.relname = $2
			) q
			JOIN pg_attribute a
				ON a.attrelid = q.conrelid
					AND NOT a.attisdropped
					AND a.attnum = q.conkey[q.ord]
			JOIN pg_class cf
				ON cf.oid = q.confrelid
			JOIN pg_namespace nf
				ON nf.oid = cf.relnamespace
			JOIN pg_attribute af
				ON af.attrelid = cf.oid
					AND NOT af.attisdropped
					AND af.attnum = q.confkey[q.ord]
			ORDER BY q.conname, q.ord
		`
	}

	return `
		SELECT
			c.conname,
			a.attname AS col,
			af.attname AS ref_col,
			nf.nspname AS ref_schema,
			cf.relname AS ref_table
		FROM pg_constraint c
		JOIN pg_namespace n
			ON n.oid = c.connamespace
		JOIN pg_class cl
			ON cl.oid = c.conrelid AND cl.relnamespace = n.oid
		JOIN LATERAL unnest(c.conkey::int[]) WITH ORDINALITY AS u1(attnum, ord1)
			ON true
		JOIN LATERAL unnest(c.confkey::int[]) WITH ORDINALITY AS u2(attnum2, ord2)
			ON u1.ord1 = u2.ord2
		JOIN pg_attribute a
			ON a.attrelid = cl.oid AND NOT a.attisdropped AND a.attnum = u1.attnum
		JOIN pg_class cf
			ON cf.oid = c.confrelid
		JOIN pg_namespace nf
			ON nf.oid = cf.relnamespace
		JOIN pg_attribute af
			ON af.attrelid = cf.oid AND NOT af.attisdropped AND af.attnum = u2.attnum2
		WHERE c.contype = 'f' AND n.nspname = $1 AND cl.relname = $2
		ORDER BY c.conname, u1.ord1
	`
}

// GetMetadata returns instance/session-level metadata.
func (c *PostgresqlConnector) GetMetadata(ctx context.Context) (map[string]any, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect for database metadata: %w", err)
	}

	metadata := make(map[string]any)

	var version sql.NullString
	versionRow := c.db.QueryRowContext(ctx, `SELECT version()`)
	if err := versionRow.Scan(&version); err != nil {
		return nil, fmt.Errorf("failed to read database version: %w", err)
	}
	if !version.Valid {
		return nil, fmt.Errorf("required database metadata contains NULL")
	}
	metadata["version"] = version.String

	query := `
		SELECT name, setting
		FROM pg_settings
		WHERE name IN (
			'server_version',
			'server_version_num',
			'TimeZone',
			'max_connections',
			'data_directory',
			'default_text_search_config'
		)
	`
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to execute database settings query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var k, v sql.NullString
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("failed to scan database setting: %w", err)
		}
		if !k.Valid {
			return nil, fmt.Errorf("required database setting metadata contains NULL")
		}
		metadata[k.String] = v.String
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate database settings: %w", err)
	}

	schemas, err := c.listSchemas(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list schemas: %w", err)
	}
	metadata["schemas"] = schemas

	metadata["cluster_mode"] = "standalone"
	return metadata, nil
}

// listSchemas lists configured schemas or schemas with discoverable tables.
func (c *PostgresqlConnector) listSchemas(ctx context.Context) ([]string, error) {
	var builder sq.SelectBuilder
	if len(c.config.Schemas) > 0 {
		builder = stmtBuilder.Select("n.nspname").
			From("pg_catalog.pg_namespace n").
			Where(sq.NotEq{"n.nspname": SYSTEM_SCHEMAS}).
			Where(sq.Expr("NOT pg_is_other_temp_schema(n.oid)"))
		builder = builder.Where(sq.Eq{"n.nspname": c.config.Schemas})
	} else {
		builder = discoverableRelationsBuilder().Columns("DISTINCT n.nspname")
	}
	builder = builder.OrderBy("n.nspname")

	sqlStr, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build list schemas query: %w", err)
	}
	rows, err := c.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list schemas: %w", err)
	}
	defer func() { _ = rows.Close() }()

	schemas := make([]string, 0)
	for rows.Next() {
		var schema sql.NullString
		if err := rows.Scan(&schema); err != nil {
			return nil, fmt.Errorf("failed to scan schema name: %w", err)
		}
		if !schema.Valid {
			return nil, fmt.Errorf("required schema metadata contains NULL")
		}

		// SQL predicates narrow the results; configured scope uses exact source names.
		// Filter after scanning to preserve exact matching regardless of database comparison rules,
		// without BINARY/COLLATE casts that could prevent index use or predicate pushdown.
		if !c.schemaAllowed(schema.String) {
			continue
		}

		schemas = append(schemas, schema.String)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate schemas: %w", err)
	}
	return schemas, nil
}

// CountRows returns an exact row count using the SQL-ready source identifier.
func (c *PostgresqlConnector) CountRows(ctx context.Context, sourceIdentifier string) (int64, error) {
	if sourceIdentifier == "" {
		return 0, fmt.Errorf("empty source identifier")
	}
	if err := c.Connect(ctx); err != nil {
		return 0, fmt.Errorf("failed to connect for row count: %w", err)
	}

	query, args, err := sq.Select("COUNT(*)").
		From(sourceIdentifier).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("failed to build row count query: %w", err)
	}
	var count int64
	if err := c.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to read row count: %w", err)
	}
	if count < 0 {
		return 0, fmt.Errorf("invalid negative row count")
	}
	return count, nil
}

// DecodeTableMeta decodes source metadata and restores missing legacy name components.
// Historical components are assumed not to contain dots or SQL quoting characters.
func (c *PostgresqlConnector) DecodeTableMeta(metadata map[string]any) (*interfaces.TableMeta, error) {
	data, err := sonic.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize table metadata: %w", err)
	}
	table := &interfaces.TableMeta{}
	if err := common.UnmarshalPreciseJSON(data, table); err != nil {
		return nil, fmt.Errorf("failed to decode table metadata: %w", err)
	}
	if _, ok := metadata["description"]; !ok {
		table.Description, _ = metadata["original_description"].(string)
	}
	originalName, _ := metadata["original_name"].(string)
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
	if table.Database == "" && c.config != nil {
		table.Database = c.config.Database
	}
	if table.Name == "" || table.Schema == "" {
		return nil, fmt.Errorf("incomplete table metadata")
	}
	return table, nil
}
