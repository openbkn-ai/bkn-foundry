//go:build integration

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package postgresql

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// TestPostgresqlConnectorPrimaryKeysIntegration 需要独立测试数据库，
// VEGA_POSTGRESQL_TEST_DSN 提供其超级用户连接。表和只读角色在同一事务中创建并回滚，
// 不依赖已部署的 Vega 服务。
func TestPostgresqlConnectorPrimaryKeysIntegration(t *testing.T) {
	dsn := os.Getenv("VEGA_POSTGRESQL_TEST_DSN")
	require.NotEmpty(t, dsn, "set VEGA_POSTGRESQL_TEST_DSN to a disposable PostgreSQL superuser connection")
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1) // 保证 BEGIN 和 SET LOCAL ROLE 使用同一物理连接。
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	compatibility := detectPostgresqlCompatibility(ctx, db)
	_, err = db.ExecContext(ctx, "BEGIN")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, rollbackErr := db.ExecContext(context.Background(), "ROLLBACK")
		require.NoError(t, rollbackErr)
	})
	_, err = db.ExecContext(ctx, `
 CREATE ROLE issue2308_reader NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
 CREATE SCHEMA issue2308_pk;
 CREATE SCHEMA issue2308_other;
 CREATE TABLE issue2308_pk.tickets (ticket_id integer PRIMARY KEY, body text);
 CREATE TABLE issue2308_pk.orders (a integer, b integer, PRIMARY KEY (b, a));
 CREATE TABLE issue2308_other.orders (a integer PRIMARY KEY, b integer);
 CREATE TABLE issue2308_pk.unique_only (id integer UNIQUE);
 CREATE TABLE issue2308_pk.denied (id integer PRIMARY KEY);
 GRANT USAGE ON SCHEMA issue2308_pk, issue2308_other TO issue2308_reader;
 GRANT SELECT ON issue2308_pk.tickets, issue2308_pk.orders,
   issue2308_other.orders, issue2308_pk.unique_only TO issue2308_reader;
 SET LOCAL ROLE issue2308_reader;
 `)
	require.NoError(t, err)
	var owner, writable bool
	err = db.QueryRowContext(ctx, `SELECT c.relowner = (SELECT oid FROM pg_roles WHERE rolname = current_user),
 has_table_privilege(c.oid, 'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'issue2308_pk' AND c.relname = 'tickets'`).Scan(&owner, &writable)
	require.NoError(t, err)
	require.False(t, owner)
	require.False(t, writable)
	var hiddenConstraints int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.table_constraints
 WHERE table_schema = 'issue2308_pk' AND table_name = 'tickets' AND constraint_type = 'PRIMARY KEY'`).Scan(&hiddenConstraints))
	require.Zero(t, hiddenConstraints, "fixture must reproduce SELECT-only constraint filtering")
	connector := &PostgresqlConnector{db: db, connected: true, config: &postgresqlConfig{}, compatibility: compatibility}
	for _, tc := range []struct {
		schema, table string
		want          []string
	}{
		{"issue2308_pk", "tickets", []string{"ticket_id"}},
		{"issue2308_pk", "orders", []string{"b", "a"}},
		{"issue2308_other", "orders", []string{"a"}},
		{"issue2308_pk", "unique_only", nil},
	} {
		t.Run(tc.schema+"."+tc.table, func(t *testing.T) {
			table := &interfaces.TableMeta{Schema: tc.schema, Name: tc.table, PKs: []string{"stale"}}
			require.NoError(t, connector.GetTableMeta(ctx, table, true))
			assert.Equal(t, tc.want, table.PKs)
			var indexPKs []string
			for _, index := range table.Indices {
				if index.Primary {
					indexPKs = index.Columns
				}
			}
			assert.Equal(t, tc.want, indexPKs)
			for _, column := range table.Columns {
				if column.ColumnKey == "PRI" {
					assert.Contains(t, tc.want, column.Name)
				} else {
					assert.NotContains(t, tc.want, column.Name)
				}
			}
			table.PKs = nil // 刷新此前遗漏主键的旧快照。
			require.NoError(t, connector.GetTableMeta(ctx, table, true))
			assert.Equal(t, tc.want, table.PKs)
		})
	}
	t.Run("permission denied differs from no primary key", func(t *testing.T) {
		keys, err := connector.fetchPrimaryKeyColumns(ctx, "issue2308_pk", "denied")
		require.ErrorContains(t, err, "privileges")
		assert.Nil(t, keys)
	})
}
