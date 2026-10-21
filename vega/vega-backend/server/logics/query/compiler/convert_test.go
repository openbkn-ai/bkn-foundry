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
		project := query.Root.(*qm.Project)
		filter := project.Input.(*qm.Filter)
		join := filter.Input.(*qm.Join)
		left, right := join.Left.(*qm.Scan), join.Right.(*qm.Scan)
		assert.Equal(t, left.ResourceID, right.ResourceID)
		assert.NotEqual(t, left.Instance, right.Instance)
		assert.Equal(t, qm.JoinLeft, join.Kind)
		assert.Equal(t, []string{"a", "id"}, join.On.Args[0].Name)
		assert.Equal(t, qm.ExprWildcard, project.Columns[0].Expr.Kind)
		// IR 转换 输出不再借用 Query Tree 的可变 slice 或节点。
		tree.Projections[0].Expr.Name[0] = "changed"
		tree.From.Left.ResourceID = "changed"
		assert.Equal(t, []string{"a"}, project.Columns[0].Expr.Name)
		assert.Equal(t, "id", left.ResourceID)
	})
	t.Run("aggregate slots are shared by projection having and order", func(t *testing.T) {
		tree, err := ParseSQL("SELECT DISTINCT a, MIN(b) m FROM {{x}} WHERE b>0 GROUP BY a HAVING MIN(b)>1 ORDER BY MIN(b) DESC LIMIT 10 OFFSET 2")
		require.NoError(t, err)
		query, err := ConvertToIR(tree)
		require.NoError(t, err)
		limit := query.Root.(*qm.Limit)
		assert.Equal(t, int64(10), limit.Count)
		assert.Equal(t, int64(2), limit.Offset)
		sort := limit.Input.(*qm.Sort)
		project := sort.Input.(*qm.Distinct).Input.(*qm.Project)
		having := project.Input.(*qm.Filter)
		require.True(t, having.Having)
		aggregate := having.Input.(*qm.Aggregate)
		require.Len(t, aggregate.Calls, 1)
		assert.Equal(t, []string{"MIN"}, aggregate.Calls[0].Name)
		assert.Len(t, aggregate.GroupKeys, 1)
		assert.False(t, aggregate.Input.(*qm.Filter).Having)
		for _, ref := range []*qm.Expr{project.Columns[1].Expr, having.Predicate.Args[0], sort.Keys[0].Expr} {
			assert.Equal(t, qm.ExprAggregateRef, ref.Kind)
			assert.Equal(t, 0, ref.AggregateSlot)
		}
	})
	t.Run("derived query owns independent scope and aggregate slots", func(t *testing.T) {
		tree, err := ParseSQL("SELECT MAX(q.m) FROM (SELECT MIN(a) m FROM {{x}}) q")
		require.NoError(t, err)
		query, err := ConvertToIR(tree)
		require.NoError(t, err)
		aggregate := query.Root.(*qm.Project).Input.(*qm.Aggregate)
		derived := aggregate.Input.(*qm.Subquery)
		inner := derived.Input.(*qm.Project).Input.(*qm.Aggregate)
		assert.NotEqual(t, query.Scope, derived.Scope)
		assert.Equal(t, "q", derived.Alias)
		assert.Equal(t, []string{"MAX"}, aggregate.Calls[0].Name)
		assert.Equal(t, []string{"MIN"}, inner.Calls[0].Name)
	})
	t.Run("exact literal and case metadata survive conversion", func(t *testing.T) {
		tree, err := ParseSQL("SELECT 18446744073709551616, 1.234567890123456789, CASE a WHEN 1 THEN 2 ELSE 3 END, CAST(a AS DECIMAL(20,6)) FROM {{x}}")
		require.NoError(t, err)
		query, err := ConvertToIR(tree)
		require.NoError(t, err)
		cols := query.Root.(*qm.Project).Columns
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
