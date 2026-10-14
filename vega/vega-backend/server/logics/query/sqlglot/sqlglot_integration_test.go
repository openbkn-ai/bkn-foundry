//go:build integration

// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package sqlglot

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os/exec"
	"testing"
)

func TestBindTableReferencesIntegration(t *testing.T) {
	require.NoError(t, exec.Command("python3", "-c", "import sqlglot").Run(), "integration tests require the production SQLGlot runtime")
	for _, tc := range []struct{ name, dialect, reference, sql, want string }{
		{"postgres quoted dots", "postgres", `"sales.eu"."order.items"`, "SELECT * FROM marker AS o", `SELECT * FROM "sales.eu"."order.items" AS o`},
		{"mysql quoted name", "mysql", "`Sales`.`ord``ers`", "SELECT * FROM marker", "SELECT * FROM `Sales`.`ord``ers`"},
		{"sqlserver bracket", "tsql", "[sales data].[Order.Archive]]]", "SELECT * FROM marker", "SELECT * FROM [sales data].[Order.Archive]]]"},
		{"qualified column", "postgres", `"Sales"."Orders"`, "SELECT marker.id FROM marker", `SELECT "Sales"."Orders".id FROM "Sales"."Orders"`},
		{"three components", "mysql", "`catalog`.`sales.eu`.`orders`", "SELECT * FROM marker", "SELECT * FROM `catalog`.`sales.eu`.`orders`"},
		{"same resource twice", "postgres", `"public"."orders"`, "SELECT a.id FROM marker AS a JOIN marker AS b ON a.id = b.id", `SELECT a.id FROM "public"."orders" AS a JOIN "public"."orders" AS b ON a.id = b.id`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BindTableReferences(context.Background(), tc.sql, tc.dialect, map[string]string{"marker": tc.reference})
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.SQL)
		})
	}
	for _, sql := range []string{"SELECT * FROM secrets", "SELECT * FROM marker JOIN secrets ON 1=1", "SELECT * FROM public.marker", "SELECT marker.id FROM marker AS a JOIN marker AS b ON a.id = b.id"} {
		t.Run("rejects unbound or ambiguous reference: "+sql, func(t *testing.T) {
			got, err := BindTableReferences(context.Background(), sql, "postgres", map[string]string{"marker": `"public"."orders"`})
			require.Error(t, err)
			assert.Nil(t, got)
		})
	}
}
