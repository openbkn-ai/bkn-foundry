// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package querypolicy

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLGlotAdapterValidateSQL(t *testing.T) {
	requireSQLGlotRuntime(t)

	adapter := NewSQLGlotAdapter()
	for _, sql := range []string{
		"SELECT id, name FROM orders WHERE id = 1",
		"SELECT COUNT(*) AS total FROM orders",
		"SELECT LOWER(name) FROM orders",
	} {
		t.Run(sql, func(t *testing.T) {
			require.NoError(t, adapter.ValidateSQL(context.Background(), sql, "trino"))
		})
	}

	for _, sql := range []string{
		"INSERT INTO orders VALUES (1)",
		"UPDATE orders SET status = 'closed'",
		"DELETE FROM orders",
		"MERGE INTO orders USING updates ON orders.id = updates.id WHEN MATCHED THEN UPDATE SET status = 'closed'",
		"COPY orders TO '/tmp/orders.csv'",
		"CREATE TABLE archived_orders AS SELECT * FROM orders",
		"ALTER TABLE orders ADD COLUMN note VARCHAR",
		"DROP TABLE orders",
		"TRUNCATE TABLE orders",
		"GRANT SELECT ON orders TO analyst",
		"REVOKE SELECT ON orders FROM analyst",
		"BEGIN",
		"SET ROLE analyst",
		"CALL refresh_orders()",
		"SELECT 1; DELETE FROM orders",
		"/* comment */ DELETE FROM orders",
		"DeLeTe FROM orders",
		"WITH recent AS (SELECT * FROM orders) SELECT * FROM recent",
		"SELECT 1 UNION SELECT 2",
		"SELECT * FROM orders FOR UPDATE",
		"SELECT * INTO archived_orders FROM orders",
		"SELECT pg_sleep(1)",
		"SELECT load_file('/etc/passwd')",
		"SELECT * FROM read_csv_auto('/etc/passwd')",
		"SELECT nextval('orders_id_seq')",
		"SELECT set_config('search_path', 'public', false)",
		"SELECT pg_advisory_lock(1)",
		"SELECT dblink_exec('connection', 'DELETE FROM orders')",
	} {
		t.Run(sql, func(t *testing.T) {
			err := adapter.ValidateSQL(context.Background(), sql, "trino")
			require.Error(t, err)

			var validationErr *ReadOnlySQLValidationError
			assert.True(t, errors.As(err, &validationErr))
		})
	}

	rejectedTSQLTests := []struct {
		name string
		sql  string
	}{
		{name: "exec", sql: "EXEC xp_cmdshell 'whoami'"},
		{name: "select into", sql: "SELECT * INTO archived_orders FROM orders"},
		{name: "insert", sql: "INSERT INTO orders(id) VALUES (1)"},
		{name: "update", sql: "UPDATE orders SET status = 'closed'"},
		{name: "delete", sql: "DELETE FROM orders"},
		{name: "create", sql: "CREATE TABLE archived_orders(id INT)"},
		{name: "alter", sql: "ALTER TABLE orders ADD note NVARCHAR(100)"},
		{name: "drop", sql: "DROP TABLE orders"},
		{name: "multiple statements", sql: "SELECT * FROM orders; DELETE FROM orders"},
		{name: "top with ties", sql: "SELECT TOP (10) WITH TIES id FROM orders ORDER BY score"},
		{name: "top percent", sql: "SELECT TOP (10) PERCENT id FROM orders ORDER BY score"},
		{name: "for json", sql: "SELECT id FROM orders ORDER BY id FOR JSON PATH"},
		{name: "for xml", sql: "SELECT id FROM orders FOR XML PATH"},
		{name: "next sequence value", sql: "SELECT NEXT VALUE FOR dbo.order_seq OVER (ORDER BY id), id FROM orders ORDER BY id"},
		{name: "offset fetch", sql: "SELECT id FROM orders ORDER BY id OFFSET 10 ROWS FETCH NEXT 20 ROWS ONLY"},
		{name: "table lock hint", sql: "SELECT id FROM orders WITH (TABLOCKX)"},
		{name: "non-literal top", sql: "SELECT TOP (5 + 5) id FROM orders ORDER BY id"},
	}
	for _, test := range rejectedTSQLTests {
		t.Run("rejects tsql statement: "+test.name, func(t *testing.T) {
			err := adapter.ValidateSQL(context.Background(), test.sql, "tsql")
			require.Error(t, err)

			var validationErr *ReadOnlySQLValidationError
			require.ErrorAs(t, err, &validationErr)
		})
	}
	t.Run("accepts numeric tsql top", func(t *testing.T) {
		require.NoError(t, adapter.ValidateSQL(context.Background(),
			"SELECT TOP (10) id FROM orders ORDER BY id", "tsql"))
	})

	t.Run("honors canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := adapter.ValidateSQL(ctx, "SELECT 1", "postgres")
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestSQLGlotAdapterValidateDerivedTable(t *testing.T) {
	requireSQLGlotRuntime(t)

	adapter := NewSQLGlotAdapter()
	for _, test := range []struct {
		name string
		sql  string
	}{
		{name: "unnamed aggregate projection", sql: "SELECT COUNT(*) FROM orders"},
		{name: "unnamed expression projection", sql: "SELECT price * quantity FROM orders"},
		{name: "unnamed cast projection", sql: "SELECT CAST(price AS INT) FROM orders"},
		{name: "unnamed try cast projection", sql: "SELECT TRY_CAST(price AS INT) FROM orders"},
		{name: "unnamed numeric literal projection", sql: "SELECT 1 FROM orders"},
		{name: "unnamed string literal projection", sql: "SELECT 'value' FROM orders"},
		{name: "duplicate projection names", sql: "SELECT id, customer_id AS id FROM orders"},
		{name: "wildcard combined with another column", sql: "SELECT *, id AS order_id FROM orders"},
		{name: "unqualified joined wildcard", sql: "SELECT * FROM orders JOIN customers ON orders.customer_id = customers.id"},
	} {
		t.Run("rejects tsql: "+test.name, func(t *testing.T) {
			err := adapter.ValidateDerivedTable(context.Background(), test.sql, "tsql")
			require.Error(t, err)
			var validationErr *ReadOnlySQLValidationError
			require.ErrorAs(t, err, &validationErr)
		})
	}

	t.Run("accepts named tsql expressions", func(t *testing.T) {
		require.NoError(t, adapter.ValidateDerivedTable(context.Background(),
			"SELECT COUNT(*) AS total, price * quantity AS amount FROM orders", "tsql"))
	})
	t.Run("accepts a single-table tsql wildcard", func(t *testing.T) {
		require.NoError(t, adapter.ValidateDerivedTable(context.Background(),
			"SELECT * FROM orders", "tsql"))
	})
	t.Run("accepts a table-qualified tsql wildcard in a join", func(t *testing.T) {
		require.NoError(t, adapter.ValidateDerivedTable(context.Background(),
			"SELECT orders.* FROM orders JOIN customers ON orders.customer_id = customers.id", "tsql"))
	})
	t.Run("does not restrict non-tsql projections", func(t *testing.T) {
		require.NoError(t, adapter.ValidateDerivedTable(context.Background(),
			"SELECT COUNT(*) FROM orders", "postgres"))
	})
}

func TestSQLGlotAdapterValidateTableReferences(t *testing.T) {
	requireSQLGlotRuntime(t)

	adapter := NewSQLGlotAdapter()
	t.Run("accepts allowed table references", func(t *testing.T) {
		require.NoError(t, adapter.ValidateTableReferences(context.Background(),
			"SELECT * FROM public.orders JOIN public.customers ON orders.customer_id = customers.id",
			"postgres", []string{"public.orders", "public.customers"},
		))
	})
	t.Run("accepts quoted tsql special identifiers", func(t *testing.T) {
		require.NoError(t, adapter.ValidateTableReferences(context.Background(),
			"SELECT * FROM [sales data].[Order.Archive]]]",
			"tsql", []string{"[sales data].[Order.Archive]]]"},
		))
	})
	t.Run("rejects unbound physical table", func(t *testing.T) {
		err := adapter.ValidateTableReferences(context.Background(),
			"SELECT * FROM public.orders JOIN private.secret_customers ON true",
			"postgres", []string{"public.orders"},
		)
		require.Error(t, err)
		var validationErr *ReadOnlySQLValidationError
		require.ErrorAs(t, err, &validationErr)
		assert.Contains(t, validationErr.Reason, "unbound physical table")
	})

	t.Run("honors canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := adapter.ValidateTableReferences(ctx, "SELECT * FROM orders", "postgres", []string{"orders"})
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestExtractTableResourceIDs(t *testing.T) {
	requireSQLGlotRuntime(t)

	t.Run("extracts hyphenated resource IDs", func(t *testing.T) {
		ids, err := ExtractTableResourceIDs(context.Background(),
			"SELECT * FROM {{orders-2026}} JOIN {{.customer_data}} ON true", "postgres")
		require.NoError(t, err)
		assert.Equal(t, []string{"orders-2026", "customer_data"}, ids)
	})

	for _, sql := range []string{
		"SELECT * FROM public.orders /* {{orders-2026}} */",
		"SELECT * FROM public.orders -- {{orders-2026}}\n",
		"SELECT '{{orders-2026}}' FROM public.orders",
	} {
		t.Run(sql, func(t *testing.T) {
			ids, err := ExtractTableResourceIDs(context.Background(), sql, "postgres")
			require.NoError(t, err)
			assert.Empty(t, ids)
		})
	}

	t.Run("honors mysql string escaping", func(t *testing.T) {
		ids, err := ExtractTableResourceIDs(context.Background(),
			"SELECT 'it\\'s {{ignored-resource}}' FROM {{orders-2026}}", "mysql")
		require.NoError(t, err)
		assert.Equal(t, []string{"orders-2026"}, ids)
	})
}

func requireSQLGlotRuntime(t *testing.T) {
	t.Helper()
	if err := exec.Command("python3", "-c", "import sqlglot").Run(); err != nil {
		t.Skip("sqlglot Python runtime is not installed")
	}
}
