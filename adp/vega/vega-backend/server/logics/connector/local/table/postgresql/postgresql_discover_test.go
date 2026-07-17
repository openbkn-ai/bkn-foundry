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
)

func TestPostgresqlTableTypeFromRelkind(t *testing.T) {
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
			if got := postgresqlTableTypeFromRelkind(tt.relKind); got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}

func TestListTablesExcludesPartitionChildren(t *testing.T) {
	t.Run("list tables excludes partition children", func(t *testing.T) {
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
			connected: true,
			db:        db,
		}

		rows := sqlmock.NewRows([]string{"table_schema", "table_name", "relkind", "description"}).
			AddRow("public", "orders", "r", "ordinary table").
			AddRow("public", "orders_partitioned", "p", "partitioned parent table").
			AddRow("analytics", "orders_view", "v", "view")

		mock.ExpectQuery("(?s).*pg_catalog\\.pg_inherits.*i\\.inhrelid = c\\.oid.*n\\.nspname IN.*").
			WillReturnRows(rows)

		tables, err := connector.ListTables(context.Background())
		if err != nil {
			t.Fatalf("ListTables returned error: %v", err)
		}

		if len(tables) != 3 {
			t.Fatalf("expected 3 tables, got %d", len(tables))
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
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("sqlmock expectations were not met: %v", err)
		}
	})
}
