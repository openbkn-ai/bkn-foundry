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
		"DELETE FROM orders",
		"SELECT 1; DELETE FROM orders",
		"WITH recent AS (SELECT * FROM orders) SELECT * FROM recent",
		"SELECT 1 UNION SELECT 2",
		"SELECT * FROM orders FOR UPDATE",
		"SELECT * INTO archived_orders FROM orders",
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
