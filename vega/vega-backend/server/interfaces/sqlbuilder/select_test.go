// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package sqlbuilder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectBuilderComposition(t *testing.T) {
	query := Select(&Column{Name: "id"})
	result := query.From(&Table{ResourceID: "orders", Alias: "a"}).Columns(&Alias{Expr: &Param{Value: 1}, Name: "n"}).
		Where(&Eq{Left: &Column{Name: "id"}, Right: &Param{Value: 2}}, nil).GroupBy(&Column{Name: "id"}).
		Having(&Gt{Left: &Column{Name: "id"}, Right: &Param{Value: 0}}, nil).
		OrderBy(Order{Expr: &Column{Name: "id"}, Nulls: NullsLast}).Distinct().Limit(2).Offset(1)
	assert.Same(t, query, result)
	assert.Equal(t, &Table{ResourceID: "orders", Alias: "a"}, query.FromPart)
	assert.Len(t, query.ColumnsPart, 2)
	assert.Len(t, query.WherePart, 1)
	assert.Len(t, query.GroupByPart, 1)
	assert.Len(t, query.HavingPart, 1)
	assert.Len(t, query.OrderByPart, 1)
	assert.True(t, query.DistinctPart)
	require.NotNil(t, query.LimitPart)
	assert.Equal(t, uint64(2), *query.LimitPart)
	assert.Equal(t, uint64(1), query.OffsetPart)
	query.Where(&IsNull{Expr: &Column{Name: "name"}}).RemoveLimit().RemoveOffset()
	assert.Len(t, query.WherePart, 2)
	assert.Nil(t, query.LimitPart)
	assert.Zero(t, query.OffsetPart)
	query.Join(&Table{ResourceID: "inner"}, &Param{Value: true}).
		LeftJoin(&Table{ResourceID: "left"}, &Param{Value: true}).
		RightJoin(&Table{ResourceID: "right"}, &Param{Value: true}).
		CrossJoin(&Table{ResourceID: "cross"})
	require.Len(t, query.JoinsPart, 4)
	assert.Equal(t, []JoinKind{InnerJoin, LeftJoin, RightJoin, CrossJoin}, []JoinKind{query.JoinsPart[0].Kind, query.JoinsPart[1].Kind, query.JoinsPart[2].Kind, query.JoinsPart[3].Kind})
	assert.Nil(t, query.JoinsPart[3].On)
}

func TestSelectBuilderFromSelect(t *testing.T) {
	inner := Select(&Column{Name: "id"}).From(&Table{ResourceID: "items"}).Limit(2)
	outer := Select(&Star{}).FromSelect(inner, "q")
	source := outer.FromPart.(*Subquery)
	assert.Same(t, inner, source.Query)
	assert.Equal(t, "q", source.Alias)
	inner.Where(&Param{Value: true})
	assert.Len(t, source.Query.WherePart, 1)
	require.NotNil(t, source.Query.LimitPart)
	assert.Equal(t, uint64(2), *source.Query.LimitPart)
	assert.Nil(t, outer.LimitPart)
}

func TestUnionSource(t *testing.T) {
	left := Select(&Column{Name: "id"}).From(&Table{ResourceID: "a"})
	right := Select(&Column{Name: "id"}).From(&Table{ResourceID: "b"})
	query := UnionQuery(left, right, true)
	assert.Same(t, query.FromPart, UnionSource(query))
	for _, changed := range []*SelectBuilder{
		nil,
		left,
		UnionQuery(left, right, true).Columns(&Column{Name: "extra"}),
		Select(&Star{Qualifier: "q"}).From(query.FromPart),
		UnionQuery(left, right, true).Where(&Param{Value: true}),
		UnionQuery(left, right, true).GroupBy(&Column{Name: "id"}),
		UnionQuery(left, right, true).Having(&Param{Value: true}),
		UnionQuery(left, right, true).Distinct(),
		UnionQuery(left, right, true).OrderBy(Order{Position: 1}),
		UnionQuery(left, right, true).Limit(0),
		UnionQuery(left, right, true).Offset(1),
		UnionQuery(left, right, true).CrossJoin(&Table{ResourceID: "c"}),
	} {
		assert.Nil(t, UnionSource(changed))
	}
}
