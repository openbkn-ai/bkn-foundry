// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
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

}

func TestSQLGlotAdapterValidateTableReferences(t *testing.T) {
	requireSQLGlotRuntime(t)

	adapter := NewSQLGlotAdapter()
	require.NoError(t, adapter.ValidateTableReferences(context.Background(),
		"SELECT * FROM public.orders JOIN public.customers ON orders.customer_id = customers.id",
		"postgres", []string{"public.orders", "public.customers"},
	))

	err := adapter.ValidateTableReferences(context.Background(),
		"SELECT * FROM public.orders JOIN private.secret_customers ON true",
		"postgres", []string{"public.orders"},
	)
	require.Error(t, err)
	var validationErr *ReadOnlySQLValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Contains(t, validationErr.Reason, "unbound physical table")
}

func TestExtractTableResourceIDs(t *testing.T) {
	requireSQLGlotRuntime(t)

	ids, err := ExtractTableResourceIDs(context.Background(),
		"SELECT * FROM {{orders-2026}} JOIN {{.customer_data}} ON true", "postgres")
	require.NoError(t, err)
	assert.Equal(t, []string{"orders-2026", "customer_data"}, ids)

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

	ids, err = ExtractTableResourceIDs(context.Background(),
		"SELECT 'it\\'s {{ignored-resource}}' FROM {{orders-2026}}", "mysql")
	require.NoError(t, err)
	assert.Equal(t, []string{"orders-2026"}, ids)
}

func TestSQLGlotAdapterHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	adapter := NewSQLGlotAdapter()

	err := adapter.ValidateSQL(ctx, "SELECT 1", "postgres")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	err = adapter.ValidateTableReferences(ctx, "SELECT * FROM orders", "postgres", []string{"orders"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func requireSQLGlotRuntime(t *testing.T) {
	t.Helper()
	if err := exec.Command("python3", "-c", "import sqlglot").Run(); err != nil {
		t.Skip("sqlglot Python runtime is not installed")
	}
}
