// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"

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
			BaseType:        baseType.String,
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

// ListTables lists tables, views and materialized views. Fill in the database name for TableMeta.Database and the Schema name for schema.
func (c *PostgresqlConnector) ListTables(ctx context.Context) ([]*interfaces.TableMeta, error) {
	return c.listTables(ctx, "", "")
}

// listTables filters readable tables and views by optional schema and table name.
func (c *PostgresqlConnector) listTables(ctx context.Context, schema, tableName string) ([]*interfaces.TableMeta, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	builder := stmtBuilder.Select(
		"n.nspname AS table_schema",
		"c.relname AS table_name",
		"c.relkind::text AS relkind",
		"COALESCE(obj_description(c.oid, 'pg_class'), '') AS description",
	).From("pg_catalog.pg_class c").
		Join("pg_catalog.pg_namespace n ON n.oid = c.relnamespace").
		// relkind: r=ordinary table, p=partitioned table, v=view, m=materialized view, f=foreign table.
		Where(sq.Eq{"c.relkind": tableRelKinds}).
		// relpersistence: p=permanent, u=unlogged, t=temporary.
		Where(sq.NotEq{"c.relpersistence": "t"}).
		Where(sq.Expr("has_table_privilege(c.oid, ?)", "SELECT")).
		Where(sq.NotEq{"n.nspname": SYSTEM_SCHEMAS}).
		Where(sq.Expr("NOT pg_is_other_temp_schema(n.oid)")).
		Where(sq.Expr("NOT EXISTS (SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhrelid = c.oid)"))

	if schema != "" {
		if len(c.config.Schemas) > 0 && !containsSchema(c.config.Schemas, schema) {
			return nil, fmt.Errorf("schema %q is outside the connector scope", schema)
		}
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
		return nil, fmt.Errorf("failed to list tables: %w", err)
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

// containsSchema reports whether a schema is included in the connector configuration.
func containsSchema(schemas []string, schema string) bool {
	for _, configuredSchema := range schemas {
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

// GetTableMeta fills the table metadata.
func (c *PostgresqlConnector) GetTableMeta(ctx context.Context, table *interfaces.TableMeta) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	if err := c.fetchTableStatus(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch table status: %w", err)
	}

	if err := c.fetchColumns(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch columns: %w", err)
	}

	if err := c.fetchIndexes(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch indexes: %w", err)
	}

	if err := c.fetchForeignKeys(ctx, table); err != nil {
		return fmt.Errorf("failed to fetch foreign keys: %w", err)
	}

	return nil
}

// GetTableMetaByIdentifier loads complete metadata for a schema.table identifier.
func (c *PostgresqlConnector) GetTableMetaByIdentifier(ctx context.Context, sourceIdentifier string) (*interfaces.TableMeta, error) {
	schema, tableName, err := c.splitTableIdentifier(sourceIdentifier)
	if err != nil {
		return nil, err
	}
	table, err := c.findTableByIdentifier(ctx, schema, tableName)
	if err != nil {
		return nil, err
	}
	if err := c.GetTableMeta(ctx, table); err != nil {
		return nil, err
	}
	return table, nil
}

// splitTableIdentifier parses a schema.table identifier into schema and table names.
func (c *PostgresqlConnector) splitTableIdentifier(sourceIdentifier string) (schema, tableName string, err error) {
	separator := strings.LastIndex(sourceIdentifier, ".")
	if separator <= 0 || separator == len(sourceIdentifier)-1 {
		return "", "", fmt.Errorf("invalid PostgreSQL table source identifier %q", sourceIdentifier)
	}
	return sourceIdentifier[:separator], sourceIdentifier[separator+1:], nil
}

// findTableByIdentifier locates a table within the connector scope.
func (c *PostgresqlConnector) findTableByIdentifier(ctx context.Context, schema, tableName string) (*interfaces.TableMeta, error) {
	tables, err := c.listTables(ctx, schema, tableName)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	for _, table := range tables {
		if table.Name == tableName && table.Schema == schema {
			return table, nil
		}
	}
	return nil, fmt.Errorf("table %q in schema %q not found", tableName, schema)
}

// fetchTableStatus retrieves the table type, description, estimated row count, and storage sizes.
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
		if err == sql.ErrNoRows {
			return fmt.Errorf("table metadata not found or inaccessible: %s.%s", table.Schema, table.Name)
		}
		return err
	}
	if !relKind.Valid {
		return fmt.Errorf("required table metadata contains NULL")
	}

	table.TableType = c.mapTableType(relKind.String)
	if description.Valid {
		table.Description = description.String
	}

	if table.Properties == nil {
		table.Properties = make(map[string]any)
	}
	table.Properties["estimated_row_count"] = estimatedRowCount.Int64
	table.Properties["data_length"] = totalBytes.Int64
	table.Properties["index_length"] = indexBytes.Int64
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
		return err
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
		return err
	}
	pkSet, err := c.fetchPrimaryKeyColumns(ctx, table.Schema, table.Name)
	if err != nil {
		return err
	}

	columns := make([]interfaces.TableColumnMeta, 0, len(rawColumns))
	for _, raw := range rawColumns {
		typeModifier := raw.typeModifier.Int64
		column := interfaces.TableColumnMeta{
			Name:            raw.name.String,
			Type:            raw.typeName.String,
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

	table.Columns = columns
	var pks []string
	for _, col := range columns {
		if col.ColumnKey == "PRI" {
			pks = append(pks, col.Name)
		}
	}
	table.PKs = pks
	return nil
}

// fetchPrimaryKeyColumns retrieves the primary-key column names for a table.
func (c *PostgresqlConnector) fetchPrimaryKeyColumns(ctx context.Context, schema, tableName string) (map[string]bool, error) {
	query := `
		SELECT
			kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_catalog = kcu.constraint_catalog
				AND tc.constraint_schema = kcu.constraint_schema
				AND tc.constraint_name = kcu.constraint_name
		WHERE tc.table_catalog = $1
			AND tc.table_schema = $2
			AND tc.table_name = $3
			AND tc.constraint_type = 'PRIMARY KEY'
		ORDER BY kcu.ordinal_position
	`

	rows, err := c.db.QueryContext(ctx, query, c.config.Database, schema, tableName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]bool)
	for rows.Next() {
		var col sql.NullString
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		if !col.Valid {
			return nil, fmt.Errorf("required primary key metadata contains NULL")
		}
		out[col.String] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// fetchIndexes retrieves index metadata for a table.
func (c *PostgresqlConnector) fetchIndexes(ctx context.Context, table *interfaces.TableMeta) error {
	query := c.indexMetadataQuery()

	rows, err := c.db.QueryContext(ctx, query, table.Schema, table.Name)
	if err != nil {
		return err
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
			return err
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
		return err
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
		return err
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
			return err
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
		return err
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
		return nil, err
	}

	metadata := make(map[string]any)

	var version sql.NullString
	versionRow := c.db.QueryRowContext(ctx, `SELECT version()`)
	if err := versionRow.Scan(&version); err != nil {
		return nil, err
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
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var k, v sql.NullString
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		if !k.Valid {
			return nil, fmt.Errorf("required database setting metadata contains NULL")
		}
		metadata[k.String] = v.String
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	schemas, err := c.listSchemas(ctx)
	if err != nil {
		return nil, err
	}
	metadata["schemas"] = schemas

	metadata["cluster_mode"] = "standalone"
	return metadata, nil
}

// listSchemas lists non-system schemas within the connector scope.
func (c *PostgresqlConnector) listSchemas(ctx context.Context) ([]string, error) {
	builder := stmtBuilder.Select("n.nspname").
		From("pg_catalog.pg_namespace n").
		Where(sq.NotEq{"n.nspname": SYSTEM_SCHEMAS}).
		Where(sq.Expr("NOT pg_is_other_temp_schema(n.oid)"))
	if len(c.config.Schemas) > 0 {
		builder = builder.Where(sq.Eq{"n.nspname": c.config.Schemas})
	}
	builder = builder.OrderBy("n.nspname")

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
		schemas = append(schemas, schema.String)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schemas: %w", err)
	}
	return schemas, nil
}
