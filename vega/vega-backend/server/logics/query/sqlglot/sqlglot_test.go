// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package sqlglot

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestMapDataSourceTypeToDialect(t *testing.T) {
	cases := []struct {
		name       string
		sourceType string
		want       string
	}{
		{name: "mysql", sourceType: interfaces.ConnectorTypeMySQL, want: "mysql"},
		{name: "upper mysql", sourceType: "MYSQL", want: "mysql"},
		{name: "postgres alias", sourceType: "postgres", want: "postgres"},
		{name: "postgres connector type", sourceType: interfaces.ConnectorTypePostgreSQL, want: "postgres"},
		{name: "mariadb", sourceType: interfaces.ConnectorTypeMariaDB, want: "mysql"},
		{name: "sqlserver", sourceType: interfaces.ConnectorTypeSQLServer, want: "tsql"},
		{name: "oracle", sourceType: interfaces.ConnectorTypeOracle, want: "oracle"},
		{name: "hana", sourceType: interfaces.ConnectorTypeHANA, want: GenericDialect},
		{name: "generic dialect", sourceType: GenericDialect, want: GenericDialect},
		{name: "tsql target dialect", sourceType: "tsql", want: "tsql"},
		{name: "maria alias", sourceType: "maria", want: "mysql"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MapDataSourceTypeToDialect(tc.sourceType)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	t.Run("returns error for unsupported source type", func(t *testing.T) {
		got, err := MapDataSourceTypeToDialect("unknown")

		require.Error(t, err)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "unsupported dataSourceType: unknown")
	})
}

func TestTranspileSQL(t *testing.T) {
	t.Run("transpiles postgres input to tsql target dialect", func(t *testing.T) {
		requireSQLGlotRuntime(t)
		got, err := TranspileSQL(context.Background(), "SELECT id FROM orders WHERE active = TRUE", "postgres", "tsql")

		require.NoError(t, err)
		assert.Equal(t, "tsql", got.Dialect)
		assert.Equal(t, "SELECT id FROM orders WHERE active = 1", got.SQL)
	})

	t.Run("transpiles mysql input to tsql target dialect", func(t *testing.T) {
		requireSQLGlotRuntime(t)
		got, err := TranspileSQL(context.Background(), "SELECT `id` FROM `orders` LIMIT 10", "mysql", "tsql")

		require.NoError(t, err)
		assert.Equal(t, "tsql", got.Dialect)
		assert.Equal(t, "SELECT TOP 10 [id] FROM [orders]", got.SQL)
	})

	t.Run("transpiles trino input to oracle target dialect", func(t *testing.T) {
		requireSQLGlotRuntime(t)
		got, err := TranspileSQL(context.Background(), "SELECT id FROM orders LIMIT 10", "trino", interfaces.ConnectorTypeOracle)

		require.NoError(t, err)
		assert.Equal(t, "oracle", got.Dialect)
		assert.Equal(t, "SELECT id FROM orders FETCH FIRST 10 ROWS ONLY", got.SQL)
	})

	t.Run("transpiles postgres input to generic target dialect", func(t *testing.T) {
		requireSQLGlotRuntime(t)
		got, err := TranspileSQL(context.Background(), `SELECT "id" FROM "SCHEMA"."orders" LIMIT 10`, "postgres", GenericDialect)

		require.NoError(t, err)
		assert.Equal(t, GenericDialect, got.Dialect)
		assert.Equal(t, `SELECT "id" FROM "SCHEMA"."orders" LIMIT 10`, got.SQL)
	})

	t.Run("returns mapping error before invoking sqlglot", func(t *testing.T) {
		got, err := TranspileSQL(context.Background(), "select * from t", "mysql", "unknown")

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "unsupported dataSourceType: unknown")
	})

	t.Run("honors canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		got, err := TranspileSQL(ctx, "select * from t", "mysql", "postgres")

		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, got)
	})
}

func requireSQLGlotRuntime(t *testing.T) {
	t.Helper()
	if err := exec.Command("python3", "-c",
		"import sqlglot; assert callable(getattr(sqlglot, 'transpile', None))").Run(); err != nil {
		t.Skip("sqlglot Python runtime is not installed")
	}
}
