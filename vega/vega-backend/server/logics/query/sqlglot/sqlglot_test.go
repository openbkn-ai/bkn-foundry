// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package sqlglot

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
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
	for _, inputDialect := range []string{"mysql", "postgres", "tsql"} {
		for _, tc := range []struct {
			name string
			sql  string
			want string
		}{
			{name: "Chinese", sql: "SELECT '中文测试'", want: "SELECT N'中文测试'"},
			{name: "emoji", sql: "SELECT '刘🙂备'", want: "SELECT N'刘🙂备'"},
			{name: "escaped quote", sql: "SELECT 'O''Brien中文'", want: "SELECT N'O''Brien中文'"},
			{name: "existing national literal", sql: "SELECT N'中文', N'ascii'", want: "SELECT N'中文', N'ascii'"},
			{name: "ASCII and empty literals", sql: "SELECT 'ascii', '', 42", want: "SELECT 'ascii', '', 42"},
			{name: "literal comment", sql: "SELECT '中文' /* preserve */", want: "SELECT N'中文' /* preserve */"},
			{
				name: "nested expressions and predicates",
				sql:  "SELECT CASE WHEN name IN ('中文', 'ascii') THEN '刘🙂备' ELSE '' END AS label FROM orders WHERE name LIKE '%中文%'",
				want: "SELECT CASE WHEN name IN (N'中文', 'ascii') THEN N'刘🙂备' ELSE '' END AS label FROM orders WHERE name LIKE N'%中文%'",
			},
		} {
			t.Run(inputDialect+" to tsql preserves Unicode: "+tc.name, func(t *testing.T) {
				requireSQLGlotRuntime(t)
				got, err := TranspileSQL(context.Background(), tc.sql, inputDialect, interfaces.ConnectorTypeSQLServer)

				require.NoError(t, err)
				assert.Equal(t, "tsql", got.Dialect)
				assert.Equal(t, tc.want, got.SQL)
			})
		}
	}
	for _, target := range []string{"mysql", "postgres", "oracle", GenericDialect} {
		t.Run("preserves ordinary literals for target "+target, func(t *testing.T) {
			requireSQLGlotRuntime(t)
			got, err := TranspileSQL(context.Background(), "SELECT '中文', '刘🙂备', 'ascii', ''", "postgres", target)

			require.NoError(t, err)
			assert.Equal(t, "SELECT '中文', '刘🙂备', 'ascii', ''", got.SQL)
		})
	}
	t.Run("preserves quoted identifiers", func(t *testing.T) {
		requireSQLGlotRuntime(t)
		got, err := TranspileSQL(context.Background(), "SELECT `中文`, '中文' AS `别名` FROM `表`", "mysql", "tsql")

		require.NoError(t, err)
		assert.Equal(t, "SELECT [中文], N'中文' AS [别名] FROM [表]", got.SQL)
	})
	t.Run("rejects invalid tsql compilation input", func(t *testing.T) {
		requireSQLGlotRuntime(t)
		got, err := TranspileSQL(context.Background(), "SELECT 'unterminated", "mysql", "tsql")

		require.Error(t, err)
		assert.Nil(t, got)
	})
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

func TestBindTableReferences(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		commandError   error
		wantError      bool
	}{
		{name: "returns bound SQL", response: `{"sql":"SELECT * FROM \"public\".\"orders\"","dialect":"postgres"}`},
		{name: "returns command failure", commandError: errors.New("command failed"), wantError: true},
		{name: "rejects malformed response", response: "invalid JSON", wantError: true},
		{name: "returns binding rejection", response: `{"error":"invalid SQL resource binding"}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			commandCalled := false
			patches := gomonkey.ApplyFunc(exec.CommandContext, func(_ context.Context, name string, args ...string) *exec.Cmd {
				commandCalled = true
				assert.Equal(t, "python3", name)
				require.Len(t, args, 5)
				assert.Equal(t, "SELECT * FROM marker", args[2])
				assert.Equal(t, "postgres", args[3])
				assert.JSONEq(t, `{"marker":"\"public\".\"orders\""}`, args[4])
				return &exec.Cmd{}
			})
			defer patches.Reset()
			patches.ApplyMethod((*exec.Cmd)(nil), "Run", func(cmd *exec.Cmd) error {
				if tc.commandError != nil {
					return tc.commandError
				}
				_, err := io.WriteString(cmd.Stdout, tc.response)
				return err
			})
			got, err := BindTableReferences(context.Background(), "SELECT * FROM marker", "postgres", map[string]string{"marker": `"public"."orders"`})
			assert.True(t, commandCalled)
			if tc.wantError {
				require.Error(t, err)
				assert.Nil(t, got)
				if tc.commandError != nil {
					assert.ErrorIs(t, err, tc.commandError)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, `SELECT * FROM "public"."orders"`, got.SQL)
				assert.Equal(t, "postgres", got.Dialect)
			}
		})
	}
}
