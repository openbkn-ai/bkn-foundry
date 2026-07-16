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

	"vega-backend/interfaces"
)

var pgSq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// ListTables 列出表、视图和物化视图；TableMeta.Database 填 database 名，Schema 填 schema 名。
func (c *PostgresqlConnector) ListTables(ctx context.Context) ([]*interfaces.TableMeta, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	builder := pgSq.Select(
		"n.nspname AS table_schema",
		"c.relname AS table_name",
		"c.relkind::text AS relkind",
		"COALESCE(obj_description(c.oid, 'pg_class'), '') AS description",
	).From("pg_catalog.pg_class c").
		Join("pg_catalog.pg_namespace n ON n.oid = c.relnamespace").
		// relkind: r=ordinary table, p=partitioned table, v=view, m=materialized view, f=foreign table.
		Where(sq.Eq{"c.relkind": []string{"r", "p", "v", "m", "f"}}).
		// relpersistence: p=permanent, u=unlogged, t=temporary.
		Where(sq.NotEq{"c.relpersistence": "t"}).
		Where(sq.Expr("has_table_privilege(c.oid, ?)", "SELECT")).
		Where(sq.NotEq{"n.nspname": SYSTEM_SCHEMAS}).
		Where(sq.Expr("NOT pg_is_other_temp_schema(n.oid)")).
		Where(sq.Expr("NOT EXISTS (SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhrelid = c.oid)"))

	if len(c.config.Schemas) > 0 {
		builder = builder.Where(sq.Eq{"n.nspname": c.config.Schemas})
	}

	query, args, err := builder.OrderBy("n.nspname", "c.relname").ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build list tables query: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tables []*interfaces.TableMeta
	for rows.Next() {
		var schema, name, relKind, description string
		if err := rows.Scan(&schema, &name, &relKind, &description); err != nil {
			return nil, fmt.Errorf("failed to scan table info: %w", err)
		}
		tables = append(tables, &interfaces.TableMeta{
			Name:        name,
			TableType:   postgresqlTableTypeFromRelkind(relKind),
			Database:    c.config.Database,
			Schema:      schema,
			Description: description,
			Properties:  map[string]any{},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate table info: %w", err)
	}
	return tables, nil
}

func postgresqlTableTypeFromRelkind(relKind string) string {
	switch relKind {
	case "v":
		return "view"
	case "m":
		return "materialized_view"
	default:
		return "table"
	}
}

// GetTableMeta 填充表元数据。
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

func (c *PostgresqlConnector) fetchTableStatus(ctx context.Context, table *interfaces.TableMeta) error {
	query := `
SELECT c.relkind::text,
       obj_description(c.oid, 'pg_class') AS description,
       COALESCE(s.n_live_tup, 0) AS est_rows,
       pg_total_relation_size(c.oid) AS total_bytes,
       pg_indexes_size(c.oid) AS index_bytes
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'v', 'm', 'p', 'f')`

	var relKind, desc sql.NullString
	var estRows, totalBytes, indexBytes sql.NullInt64
	err := c.db.QueryRowContext(ctx, query, table.Schema, table.Name).Scan(
		&relKind, &desc, &estRows, &totalBytes, &indexBytes)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	switch relKind.String {
	case "r", "p", "f":
		table.TableType = "table"
	case "v":
		table.TableType = "view"
	case "m":
		table.TableType = "materialized_view"
	default:
		table.TableType = "table"
	}

	if table.Properties == nil {
		table.Properties = make(map[string]any)
	}
	if desc.Valid {
		table.Description = desc.String
	}
	table.Properties["row_count"] = estRows.Int64
	table.Properties["data_length"] = totalBytes.Int64
	table.Properties["index_length"] = indexBytes.Int64
	return nil
}

func (c *PostgresqlConnector) fetchColumns(ctx context.Context, table *interfaces.TableMeta) error {
	query := `
SELECT a.attname AS column_name,
       format_type(a.atttypid, a.atttypmod) AS data_type,
       t.typname AS udt_name,
       CASE WHEN a.attnotnull THEN 'NO' ELSE 'YES' END AS is_nullable,
       COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '') AS column_default,
       CASE
           WHEN a.atttypmod > 0 AND t.typname IN ('bpchar', 'varchar') THEN a.atttypmod - 4
           ELSE NULL
       END AS character_maximum_length,
       CASE
           WHEN t.typname = 'numeric' AND a.atttypmod >= 0 THEN ((a.atttypmod - 4) >> 16) & 65535
           WHEN t.typname IN ('int2', 'int4', 'int8') THEN NULL
           WHEN t.typname IN ('float4', 'float8') THEN NULL
           ELSE NULL
       END AS numeric_precision,
       CASE
           WHEN t.typname = 'numeric' AND a.atttypmod >= 0 THEN (a.atttypmod - 4) & 65535
           ELSE NULL
       END AS numeric_scale,
       CASE
           WHEN t.typname IN ('time', 'timetz', 'timestamp', 'timestamptz') AND a.atttypmod >= 0 THEN a.atttypmod
           ELSE NULL
       END AS datetime_precision,
       COALESCE(coll.collname, '') AS collation_name,
       a.attnum AS ordinal_position,
       COALESCE(col_description(a.attrelid, a.attnum), '') AS description
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_attribute a ON a.attrelid = c.oid
JOIN pg_catalog.pg_type t ON t.oid = a.atttypid
LEFT JOIN pg_catalog.pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
LEFT JOIN pg_catalog.pg_collation coll ON coll.oid = a.attcollation
WHERE n.nspname = $1 AND c.relname = $2
  AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY a.attnum`

	rows, err := c.db.QueryContext(ctx, query, table.Schema, table.Name)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	pkSet, err := c.fetchPrimaryKeyColumns(ctx, table.Schema, table.Name)
	if err != nil {
		return err
	}

	var columns []interfaces.TableColumnMeta
	for rows.Next() {
		var name, dataType, udtName, isNullable sql.NullString
		var colDefault, collation, description sql.NullString
		var charMax, numPrec, numScale, dtPrec, ord sql.NullInt64

		if err := rows.Scan(
			&name, &dataType, &udtName, &isNullable, &colDefault,
			&charMax, &numPrec, &numScale, &dtPrec, &collation, &ord,
			&description,
		); err != nil {
			return err
		}

		colKey := ""
		if pkSet[name.String] {
			colKey = "PRI"
		}

		orig := dataType.String
		if udtName.Valid {
			orig = udtName.String
		}

		columns = append(columns, interfaces.TableColumnMeta{
			Name:        name.String,
			Type:        orig,
			Description: description.String,

			Nullable:          strings.EqualFold(isNullable.String, "YES"),
			DefaultValue:      colDefault.String,
			CharMaxLen:        int(charMax.Int64),
			NumPrecision:      int(numPrec.Int64),
			NumScale:          int(numScale.Int64),
			DatetimePrecision: int(dtPrec.Int64),
			Collation:         collation.String,
			OrdinalPosition:   int(ord.Int64),
			ColumnKey:         colKey,
		})
	}
	if err := rows.Err(); err != nil {
		return err
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

func (c *PostgresqlConnector) fetchPrimaryKeyColumns(ctx context.Context, schema, tableName string) (map[string]bool, error) {
	q := `
SELECT kcu.column_name
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON tc.constraint_catalog = kcu.constraint_catalog
 AND tc.constraint_schema = kcu.constraint_schema
 AND tc.constraint_name = kcu.constraint_name
WHERE tc.table_catalog = $1 AND tc.table_schema = $2 AND tc.table_name = $3
  AND tc.constraint_type = 'PRIMARY KEY'
ORDER BY kcu.ordinal_position`

	rows, err := c.db.QueryContext(ctx, q, c.config.Database, schema, tableName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]bool)
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		out[col] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *PostgresqlConnector) fetchIndexes(ctx context.Context, table *interfaces.TableMeta) error {
	q := `
SELECT i.relname AS index_name,
       a.attname AS column_name,
       ix.indisunique,
       ix.indisprimary,
       k.n AS ord
FROM pg_index ix
JOIN pg_class t ON t.oid = ix.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN LATERAL generate_subscripts(ix.indkey::int[], 1) AS k(n) ON true
JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = (ix.indkey::int[])[k.n]
    AND (ix.indkey::int[])[k.n] > 0 AND NOT a.attisdropped
WHERE n.nspname = $1 AND t.relname = $2
ORDER BY i.relname, k.n`

	rows, err := c.db.QueryContext(ctx, q, table.Schema, table.Name)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	indexMap := make(map[string]*interfaces.TableIndexMeta)
	for rows.Next() {
		var indexName, columnName string
		var unique, primary bool
		var ord int
		if err := rows.Scan(&indexName, &columnName, &unique, &primary, &ord); err != nil {
			return err
		}
		if idx, ok := indexMap[indexName]; ok {
			idx.Columns = append(idx.Columns, columnName)
		} else {
			indexMap[indexName] = &interfaces.TableIndexMeta{
				Name:    indexName,
				Columns: []string{columnName},
				Unique:  unique,
				Primary: primary,
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	var indices []interfaces.TableIndexMeta
	for _, idx := range indexMap {
		indices = append(indices, *idx)
	}
	table.Indices = indices
	return nil
}

func (c *PostgresqlConnector) fetchForeignKeys(ctx context.Context, table *interfaces.TableMeta) error {
	q := `
SELECT c.conname,
       a.attname AS col,
       af.attname AS ref_col,
       nf.nspname AS ref_schema,
       cf.relname AS ref_table
FROM pg_constraint c
JOIN pg_namespace n ON n.oid = c.connamespace
JOIN pg_class cl ON cl.oid = c.conrelid AND cl.relnamespace = n.oid
JOIN LATERAL unnest(c.conkey::int[]) WITH ORDINALITY AS u1(attnum, ord1) ON true
JOIN LATERAL unnest(c.confkey::int[]) WITH ORDINALITY AS u2(attnum2, ord2) ON u1.ord1 = u2.ord2
JOIN pg_attribute a ON a.attrelid = cl.oid AND NOT a.attisdropped AND a.attnum = u1.attnum
JOIN pg_class cf ON cf.oid = c.confrelid
JOIN pg_namespace nf ON nf.oid = cf.relnamespace
JOIN pg_attribute af ON af.attrelid = cf.oid AND NOT af.attisdropped AND af.attnum = u2.attnum2
WHERE c.contype = 'f' AND n.nspname = $1 AND cl.relname = $2
ORDER BY c.conname, u1.ord1`

	rows, err := c.db.QueryContext(ctx, q, table.Schema, table.Name)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	fkMap := make(map[string]*interfaces.TableForeignKeyMeta)
	for rows.Next() {
		var cname, col, refCol, refSchema, refTable string
		if err := rows.Scan(&cname, &col, &refCol, &refSchema, &refTable); err != nil {
			return err
		}
		refFull := refSchema + "." + refTable
		if fk, ok := fkMap[cname]; ok {
			fk.Columns = append(fk.Columns, col)
			fk.RefColumns = append(fk.RefColumns, refCol)
		} else {
			fkMap[cname] = &interfaces.TableForeignKeyMeta{
				Name:       cname,
				Columns:    []string{col},
				RefTable:   refFull,
				RefColumns: []string{refCol},
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

// GetMetadata 返回实例/会话级元数据。
func (c *PostgresqlConnector) GetMetadata(ctx context.Context) (map[string]any, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	meta := make(map[string]any)

	var version string
	if err := c.db.QueryRowContext(ctx, `SELECT version()`).Scan(&version); err != nil {
		return nil, err
	}
	meta["version"] = version

	rows, err := c.db.QueryContext(ctx, `
SELECT name, setting FROM pg_settings
WHERE name IN ('server_version','server_version_num','TimeZone','max_connections','data_directory','default_text_search_config')`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		meta[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	meta["cluster_mode"] = "standalone"
	return meta, nil
}
