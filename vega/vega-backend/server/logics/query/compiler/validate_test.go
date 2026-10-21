// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	qt "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler/querytree"
)

func TestValidateQueryTree(t *testing.T) {
	for _, sql := range []string{
		"SELECT a.*, COUNT(*), COUNT(DISTINCT a.id) FROM {{x}} a GROUP BY a.id",
		"SELECT COALESCE(a, b, 0), IFNULL(a,0), NULLIF(a,b), ABS(a), NOW(), CURRENT_TIMESTAMP FROM {{x}}",
		"SELECT CASE a WHEN 1 THEN b ELSE 0 END, CAST(a AS DECIMAL(20,6)) FROM {{x}}",
		"SELECT DISTINCT id FROM (SELECT id FROM {{x}} WHERE a IN (1,-2,NULL,'3')) q ORDER BY id LIMIT 0",
	} {
		t.Run(sql, func(t *testing.T) {
			tree, err := ParseSQL(sql)
			require.NoError(t, err)
			assert.NoError(t, ValidateQueryTree(tree))
		})
	}
	for _, sql := range []string{
		"SELECT RAND() FROM {{x}}", "SELECT unknown(a) FROM {{x}}", "SELECT COUNT() FROM {{x}}", "SELECT NOW(3) FROM {{x}}", "SELECT ABS(a,b) FROM {{x}}", "SELECT IFNULL(a) FROM {{x}}",
		"SELECT SUM(DISTINCT a) FROM {{x}}", "SELECT COUNT(DISTINCT *) FROM {{x}}", "SELECT ABS(*) FROM {{x}}", "SELECT SUM(MAX(a)) FROM {{x}}",
		"SELECT * FROM {{x}} WHERE COUNT(*) > 0", "SELECT * FROM {{x}} x JOIN {{y}} y ON SUM(x.id) = y.id", "SELECT a FROM {{x}} GROUP BY SUM(a)", "SELECT a FROM {{x}} GROUP BY 1",
		"SELECT * FROM {{x}} WHERE a IN (b)", "SELECT * FROM {{x}} WHERE a IN (NOW())", "SELECT CAST(a AS unsupported_type) FROM {{x}}",
		"SELECT a FROM (SELECT unknown(a) FROM {{x}}) q",
	} {
		t.Run("reject "+sql, func(t *testing.T) {
			tree, err := ParseSQL(sql)
			require.NoError(t, err)
			err = ValidateQueryTree(tree)
			require.Error(t, err)
			var e *qm.CompileError
			require.ErrorAs(t, err, &e)
			assert.Equal(t, qm.ErrorUnsupported, e.Kind)
		})
	}
	t.Run("cycle", func(t *testing.T) {
		tree, err := ParseSQL("SELECT a FROM {{x}}")
		require.NoError(t, err)
		tree.From = &qt.Relation{Kind: qt.Derived, Alias: "q", Query: tree}
		err = ValidateQueryTree(tree)
		require.Error(t, err)
		var e *qm.CompileError
		require.ErrorAs(t, err, &e)
		assert.Equal(t, qm.ErrorInvalidTree, e.Kind)
	})
	t.Run("expression cycle", func(t *testing.T) {
		tree, err := ParseSQL("SELECT -a FROM {{x}}")
		require.NoError(t, err)
		expr := tree.Projections[0].Expr
		expr.Args[0] = expr
		err = ValidateQueryTree(tree)
		require.Error(t, err)
	})
	for _, mutate := range []struct {
		name string
		run  func(*qt.Select)
	}{
		{"unknown relation", func(q *qt.Select) { q.From.Kind = "invalid" }},
		{"unknown expression", func(q *qt.Select) { q.Projections[0].Expr.Kind = "invalid" }},
		{"missing expression", func(q *qt.Select) { q.Projections[0].Expr = nil }},
		{"invalid id", func(q *qt.Select) { q.From.ResourceID = "Upper" }},
		{"missing source", func(q *qt.Select) { q.From = nil }},
		{"empty projection", func(q *qt.Select) { q.Projections = nil }},
		{"relation payload overlap", func(q *qt.Select) { q.From.Query = &qt.Select{} }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			tree, err := ParseSQL("SELECT a FROM {{x}}")
			require.NoError(t, err)
			mutate.run(tree)
			assert.Error(t, ValidateQueryTree(tree))
		})
	}
	t.Run("tree limits apply independently", func(t *testing.T) {
		tree, err := ParseSQL("SELECT a FROM {{x}}")
		require.NoError(t, err)
		// 直接构造超限树，验证策略入口独立执行固定预算。
		projection := tree.Projections[0]
		tree.Projections = make([]qt.Projection, qm.MaxNodes)
		for i := range tree.Projections {
			tree.Projections[i] = projection
		}
		err = ValidateQueryTree(tree)
		require.Error(t, err)
		var e *qm.CompileError
		require.ErrorAs(t, err, &e)
		assert.Equal(t, qm.ErrorLimit, e.Kind)
	})
}
