// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package querypolicy

import (
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
	} {
		t.Run(sql, func(t *testing.T) {
			require.NoError(t, adapter.ValidateSQL(sql, "trino"))
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
	} {
		t.Run(sql, func(t *testing.T) {
			err := adapter.ValidateSQL(sql, "trino")
			require.Error(t, err)

			var validationErr *ReadOnlySQLValidationError
			assert.True(t, errors.As(err, &validationErr))
		})
	}
}

func requireSQLGlotRuntime(t *testing.T) {
	t.Helper()
	if err := exec.Command("python3", "-c", "import sqlglot").Run(); err != nil {
		t.Skip("sqlglot Python runtime is not installed")
	}
}
