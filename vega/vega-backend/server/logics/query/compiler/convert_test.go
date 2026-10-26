// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

func TestConvertToIR(t *testing.T) {
	t.Run("self join retains distinct source identities", func(t *testing.T) {
		tree, err := ParseSQL("SELECT a.* FROM {{id}} a LEFT JOIN {{id}} b ON a.id=b.id WHERE b.id IS NULL")
		require.NoError(t, err)
		query, err := ConvertToIR(tree)
		require.NoError(t, err)
		project := query
		join := project.FromPart.(*qm.Join)
		left, right := join.Left.(*qm.Scan), join.Right.(*qm.Scan)
		assert.Equal(t, left.ResourceID, right.ResourceID)
		assert.NotEqual(t, left.Instance, right.Instance)
		assert.Equal(t, qm.JoinLeft, join.Kind)
		assert.Equal(t, []string{"a", "id"}, join.On.Args[0].Name)
		assert.Equal(t, qm.ExprWildcard, project.ColumnsPart[0].Expr.Kind)
		assert.Equal(t, qm.OriginSQL, project.ColumnsPart[0].Expr.Origin.Kind)
		assert.Zero(t, project.ColumnsPart[0].Expr.Origin.Span)
		// IR 转换 输出不再借用 Query Tree 的可变 slice 或节点。
		tree.Projections[0].Expr.Name[0] = "changed"
		tree.From.Left.ResourceID = "changed"
		assert.Equal(t, []string{"a"}, project.ColumnsPart[0].Expr.Name)
		assert.Equal(t, "id", left.ResourceID)
	})
	t.Run("aggregate slots are shared by projection having and order", func(t *testing.T) {
		tree, err := ParseSQL("SELECT DISTINCT a, MIN(b) m FROM {{x}} WHERE b>0 GROUP BY a HAVING MIN(b)>1 ORDER BY MIN(b) DESC LIMIT 10 OFFSET 2")
		require.NoError(t, err)
		query, err := ConvertToIR(tree)
		require.NoError(t, err)
		parts := query
		assert.Equal(t, int64(10), parts.LimitPart.Count)
		assert.Equal(t, int64(2), parts.LimitPart.Offset)
		assert.True(t, parts.DistinctPart)
		require.Len(t, parts.GroupByPart, 1)
		require.Len(t, parts.HavingPart, 1)
		require.Len(t, parts.WherePart, 1)
		for _, call := range []*qm.Sqlizer{parts.ColumnsPart[1].Expr, parts.HavingPart[0].Args[0], parts.OrderByPart[0].Expr} {
			assert.Equal(t, qm.ExprCall, call.Kind)
			assert.Equal(t, []string{"MIN"}, call.Name)
		}
	})
	t.Run("derived query owns independent scope and aggregate slots", func(t *testing.T) {
		tree, err := ParseSQL("SELECT MAX(q.m) FROM (SELECT MIN(a) m FROM {{x}}) q")
		require.NoError(t, err)
		query, err := ConvertToIR(tree)
		require.NoError(t, err)
		parts := query
		derived := parts.FromPart.(*qm.Subquery)
		inner := derived.Query
		assert.NotSame(t, query, derived.Query)
		assert.Equal(t, "q", derived.Alias)
		assert.Equal(t, []string{"MAX"}, parts.ColumnsPart[0].Expr.Name)
		assert.Equal(t, []string{"MIN"}, inner.ColumnsPart[0].Expr.Name)
	})
	t.Run("exact literal and case metadata survive conversion", func(t *testing.T) {
		tree, err := ParseSQL("SELECT 18446744073709551616, 1.234567890123456789, CASE a WHEN 1 THEN 2 ELSE 3 END, CAST(a AS DECIMAL(20,6)) FROM {{x}}")
		require.NoError(t, err)
		query, err := ConvertToIR(tree)
		require.NoError(t, err)
		cols := query.ColumnsPart
		assert.Equal(t, "18446744073709551616", cols[0].Expr.Literal.Text)
		assert.Equal(t, qm.LiteralDecimal, cols[1].Expr.Literal.Kind)
		assert.Equal(t, "1.234567890123456789", cols[1].Expr.Literal.Text)
		assert.True(t, cols[2].Expr.CaseBase)
		assert.True(t, cols[2].Expr.CaseElse)
		assert.Equal(t, []int{20, 6}, cols[3].Expr.CastParameters)
		tree.Projections[3].Expr.CastParameters[0] = 9
		assert.Equal(t, 20, cols[3].Expr.CastParameters[0])
	})
	t.Run("validation failure produces no IR", func(t *testing.T) {
		tree, err := ParseSQL("SELECT RAND() FROM {{x}}")
		require.NoError(t, err)
		query, err := ConvertToIR(tree)
		require.Error(t, err)
		assert.Nil(t, query)
	})
	t.Run("nil tree rejected", func(t *testing.T) {
		query, err := ConvertToIR(nil)
		require.Error(t, err)
		assert.Nil(t, query)
	})
}
