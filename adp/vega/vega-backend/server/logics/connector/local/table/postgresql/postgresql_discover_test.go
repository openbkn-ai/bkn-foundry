// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestPostgresqlConnectorTableTypeFromRelKind(t *testing.T) {
	connector := &PostgresqlConnector{}
	tests := []struct {
		name    string
		relKind string
		want    string
	}{
		{name: "regular table", relKind: "r", want: "table"},
		{name: "partitioned table", relKind: "p", want: "table"},
		{name: "foreign table", relKind: "f", want: "table"},
		{name: "view", relKind: "v", want: "view"},
		{name: "materialized view", relKind: "m", want: "materialized_view"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connector.tableTypeFromRelKind(tt.relKind); got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}

func TestPostgresqlConnectorListTables(t *testing.T) {
	t.Run("excludes partition children and accepts null optional description", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
		if err != nil {
			t.Fatalf("sqlmock.New returned error: %v", err)
		}
		defer func() { _ = db.Close() }()

		connector := &PostgresqlConnector{
			config: &postgresqlConfig{
				Database: "appdb",
				Schemas:  []string{"public", "analytics"},
			},
			connected:     true,
			db:            db,
			compatibility: postgresqlCompatibility{serverVersionNum: 100000, checked: true},
		}

		rows := sqlmock.NewRows([]string{"table_schema", "table_name", "relkind", "description"}).
			AddRow("public", "orders", "r", "ordinary table").
			AddRow("public", "orders_partitioned", "p", "partitioned parent table").
			AddRow("analytics", "orders_view", "v", "view").
			AddRow("public", "undocumented", "r", nil)

		mock.ExpectQuery("(?s).*pg_catalog\\.pg_inherits.*i\\.inhrelid = c\\.oid.*n\\.nspname IN.*").
			WillReturnRows(rows)

		tables, err := connector.ListTables(context.Background())
		if err != nil {
			t.Fatalf("ListTables returned error: %v", err)
		}

		if len(tables) != 4 {
			t.Fatalf("expected 4 tables, got %d", len(tables))
		}
		if tables[0].Name != "orders" || tables[0].TableType != "table" || tables[0].Database != "appdb" || tables[0].Schema != "public" {
			t.Fatalf("unexpected ordinary table metadata: %+v", tables[0])
		}
		if tables[1].Name != "orders_partitioned" || tables[1].TableType != "table" {
			t.Fatalf("unexpected partitioned parent metadata: %+v", tables[1])
		}
		if tables[2].Name != "orders_view" || tables[2].TableType != "view" {
			t.Fatalf("unexpected view metadata: %+v", tables[2])
		}
		if tables[3].Name != "undocumented" || tables[3].Description != "" {
			t.Fatalf("unexpected undocumented table metadata: %+v", tables[3])
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("sqlmock expectations were not met: %v", err)
		}
	})
}

func TestPostgresqlConnectorFetchColumns(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New returned error: %v", err)
	}
	defer func() { _ = db.Close() }()

	connector := &PostgresqlConnector{
		config:        &postgresqlConfig{Database: "appdb"},
		connected:     true,
		db:            db,
		compatibility: postgresqlCompatibility{serverVersionNum: 90400, checked: true},
	}
	mock.ExpectQuery("(?s)SELECT a.attname AS column_name.*a.atttypid AS type_oid.*FROM pg_catalog.pg_class.*c\\.relkind IN \\('r', 'v', 'f', 'm'\\)").
		WithArgs("public", "orders").
		WillReturnRows(sqlmock.NewRows([]string{
			"column_name", "type_oid", "type_kind", "type_name", "type_modifier",
			"column_not_null", "column_default", "collation_name", "ordinal_position", "description",
		}).
			AddRow("id", 23, "b", "int4", -1, true, nil, "", 1, "").
			AddRow("customer_id", 9100, "d", "customer_id_domain", -1, false, nil, "", 2, "").
			AddRow("code", 1043, "b", "varchar", 68, false, nil, "C", 3, "").
			AddRow("amount", 1700, "b", "numeric", int64((10<<16)|2)+4, false, nil, "", 4, "").
			AddRow("occurred_at", 1114, "b", "timestamp", 3, false, nil, "", 5, ""))
	mock.ExpectQuery(`(?s)WITH RECURSIVE domain_chain.*root\.oid IN \(\$1\).*pg_catalog\.pg_constraint`).
		WithArgs(int64(9100)).
		WillReturnRows(sqlmock.NewRows([]string{
			"domain_oid", "base_type", "base_typmod", "domain_not_null", "domain_default", "check_constraint",
		}).
			AddRow(9100, "int4", -1, true, "42", "CHECK ((VALUE > 0)); CHECK ((VALUE < 1000))"))
	mock.ExpectQuery("SELECT kcu.column_name").
		WithArgs("appdb", "public", "orders").
		WillReturnRows(sqlmock.NewRows([]string{"column_name"}).AddRow("id"))

	table := &interfaces.TableMeta{Schema: "public", Name: "orders"}
	if err := connector.fetchColumns(context.Background(), table); err != nil {
		t.Fatalf("fetchColumns returned error: %v", err)
	}
	if len(table.Columns) != 5 {
		t.Fatalf("expected 5 columns, got %d", len(table.Columns))
	}
	column := table.Columns[0]
	if column.Name != "id" || column.DefaultValue != "" || column.Description != "" || column.Collation != "" {
		t.Fatalf("unexpected column metadata: %+v", column)
	}
	assert.Empty(t, column.AliasType)
	assert.Empty(t, column.CheckConstraint)

	domainColumn := table.Columns[1]
	assert.Equal(t, "customer_id", domainColumn.Name)
	assert.Equal(t, "int4", domainColumn.Type)
	assert.Equal(t, "customer_id_domain", domainColumn.AliasType)
	assert.Equal(t, "CHECK ((VALUE > 0)); CHECK ((VALUE < 1000))", domainColumn.CheckConstraint)
	assert.Equal(t, "42", domainColumn.DefaultValue)
	assert.False(t, domainColumn.Nullable)
	assert.Equal(t, interfaces.DataType_Integer, connector.MapType(domainColumn.Type))
	assert.Equal(t, 64, table.Columns[2].CharMaxLen)
	assert.Equal(t, "C", table.Columns[2].Collation)
	assert.Equal(t, 10, table.Columns[3].NumPrecision)
	assert.Equal(t, 2, table.Columns[3].NumScale)
	assert.Equal(t, 3, table.Columns[4].DatetimePrecision)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations were not met: %v", err)
	}
}

func TestPostgresqlConnectorFetchDomainMetadata(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	rows := sqlmock.NewRows([]string{
		"domain_oid", "base_type", "base_typmod", "domain_not_null", "domain_default", "check_constraint",
	}).
		AddRow(9100, "int4", -1, true, "42",
			"CHECK ((VALUE > 0)); CHECK (((VALUE)::integer < 1000))")

	mock.ExpectQuery(`(?s)WITH RECURSIVE domain_chain.*root\.oid IN \(\$1\).*pg_catalog\.pg_constraint`).
		WithArgs(int64(9100)).
		WillReturnRows(rows)

	connector := &PostgresqlConnector{db: db}
	metadata, err := connector.fetchDomainMetadata(context.Background(), []int64{9100})
	require.NoError(t, err)
	require.Contains(t, metadata, int64(9100))

	domain := metadata[9100]
	assert.Equal(t, "int4", domain.BaseType)
	assert.Equal(t, int64(-1), domain.BaseTypmod)
	assert.True(t, domain.NotNull)
	require.True(t, domain.DefaultValue.Valid)
	assert.Equal(t, "42", domain.DefaultValue.String)
	assert.Equal(t,
		"CHECK ((VALUE > 0)); CHECK (((VALUE)::integer < 1000))",
		domain.CheckConstraint,
	)
}

func TestPostgresqlConnectorFetchDomainMetadataSkipsQueryWithoutDomains(t *testing.T) {
	connector := &PostgresqlConnector{}
	metadata, err := connector.fetchDomainMetadata(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, metadata)
}

func TestPostgresqlConnectorFetchIndexes(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	connector := &PostgresqlConnector{
		db:            db,
		compatibility: postgresqlCompatibility{serverVersionNum: 90204, checked: true},
	}
	mock.ExpectQuery(`(?s)generate_subscripts.*q\.indkey\[q\.ord\].*ORDER BY q\.index_name, q\.ord`).
		WithArgs("public", "orders").
		WillReturnRows(sqlmock.NewRows([]string{
			"index_name", "column_name", "indisunique", "indisprimary", "ord",
		}).
			AddRow("orders_line_warehouse_idx", "line_id", false, false, 1).
			AddRow("orders_line_warehouse_idx", "warehouse_id", false, false, 2))

	table := &interfaces.TableMeta{Schema: "public", Name: "orders"}
	require.NoError(t, connector.fetchIndexes(context.Background(), table))
	require.Len(t, table.Indices, 1)
	assert.Equal(t, []string{"line_id", "warehouse_id"}, table.Indices[0].Columns)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresqlConnectorFetchForeignKeys(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	connector := &PostgresqlConnector{
		db:            db,
		compatibility: postgresqlCompatibility{serverVersionNum: 90204, checked: true},
	}
	mock.ExpectQuery(`(?s)generate_subscripts.*q\.conkey\[q\.ord\].*q\.confkey\[q\.ord\].*ORDER BY q\.conname, q\.ord`).
		WithArgs("public", "orders").
		WillReturnRows(sqlmock.NewRows([]string{
			"conname", "col", "ref_col", "ref_schema", "ref_table",
		}).
			AddRow("orders_location_fk", "warehouse_id", "warehouse_id", "inventory", "locations").
			AddRow("orders_location_fk", "bin_id", "bin_id", "inventory", "locations"))

	table := &interfaces.TableMeta{Schema: "public", Name: "orders"}
	require.NoError(t, connector.fetchForeignKeys(context.Background(), table))
	require.Len(t, table.ForeignKeys, 1)
	assert.Equal(t, []string{"warehouse_id", "bin_id"}, table.ForeignKeys[0].Columns)
	assert.Equal(t, []string{"warehouse_id", "bin_id"}, table.ForeignKeys[0].RefColumns)
	assert.Equal(t, "inventory.locations", table.ForeignKeys[0].RefTable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresqlConnectorMetadataQueryCompatibility(t *testing.T) {
	t.Run("uses legacy queries on PostgreSQL 9.2", func(t *testing.T) {
		connector := &PostgresqlConnector{
			compatibility: postgresqlCompatibility{serverVersionNum: 90204, checked: true},
		}

		assert.NotContains(t, connector.indexMetadataQuery(), "LATERAL")
		assert.Contains(t, connector.indexMetadataQuery(), "generate_subscripts")
		assert.NotContains(t, connector.foreignKeyMetadataQuery(), "WITH ORDINALITY")
		assert.Contains(t, connector.foreignKeyMetadataQuery(), "generate_subscripts")
	})

	t.Run("uses available modern queries by server version", func(t *testing.T) {
		connector := &PostgresqlConnector{
			compatibility: postgresqlCompatibility{serverVersionNum: 90300, checked: true},
		}

		assert.Contains(t, connector.indexMetadataQuery(), "LATERAL")
		assert.NotContains(t, connector.foreignKeyMetadataQuery(), "WITH ORDINALITY")

		connector.compatibility = postgresqlCompatibility{serverVersionNum: 90400, checked: true}
		assert.Contains(t, connector.foreignKeyMetadataQuery(), "WITH ORDINALITY")
	})
}
