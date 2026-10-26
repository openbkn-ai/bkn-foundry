// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryBuilderBranches(t *testing.T) {
	// 两次读取从同一个逻辑关系派生，不能将条件/窗口写回基础对象。
	base := Select().From(Resource("orders", "a"))
	condition := And(Eq(Field("a", "id"), Int64(1)), Eq(Field("a", "active"), Bool(true)))
	filtered := base.Where(condition)
	columns := []NamedExpr{As(Field("a", "id"), "id")}
	selected := filtered.Columns(columns...)
	columns[0].Alias = "mutated"
	first := selected.OrderBy(SortKey{Expr: Field("id")}).Limit(10, 0)
	second := selected.Limit(5, 20)
	assert.Empty(t, base.WherePart)
	require.Len(t, filtered.WherePart, 1)
	assert.Equal(t, "id", selected.ColumnsPart[0].Alias)
	assert.Nil(t, selected.LimitPart)
	assert.Empty(t, selected.OrderByPart)
	assert.Equal(t, int64(10), first.LimitPart.Count)
	assert.Equal(t, int64(20), second.LimitPart.Offset)
	windowCopy := first.Clone()
	windowCopy.LimitPart.Count = 99
	assert.Equal(t, int64(10), first.LimitPart.Count)
	additional := filtered.Where(Eq(Field("a", "id"), Int64(2)))
	assert.Len(t, filtered.WherePart, 1)
	assert.Len(t, additional.WherePart, 2)
	copy := selected.Clone()
	copy.ColumnsPart[0].Alias = "changed"
	assert.Equal(t, "id", selected.ColumnsPart[0].Alias)
	assert.Equal(t, OperatorAnd, condition.Op)
	assert.Equal(t, "1", condition.Args[0].Args[1].Literal.Text)
	assert.Equal(t, "true", condition.Args[1].Args[1].Literal.Text)
}

func TestSelectBuilderSchema(t *testing.T) {
	precision := 6
	field := &Sqlizer{Kind: ExprField, Field: 1, Type: LogicalType{Kind: TypeInstant, TimePrecision: &precision}, Nullable: true}
	encoding := OutputEncoding{Kind: EncodingScalar, PublicType: "datetime"}
	base := Select(NamedExpr{Expr: field, Alias: "created", Output: 2, Encoding: encoding})
	schema := base.Schema()
	require.Len(t, schema, 1)
	assert.Equal(t, FieldID(2), schema[0].ID)
	assert.Equal(t, "created", schema[0].Name)
	assert.Equal(t, field.Type, schema[0].Type)
	assert.True(t, schema[0].Nullable)
	assert.Equal(t, encoding, schema[0].Encoding)
	assert.Equal(t, []FieldID{1}, schema[0].Lineage)
	// 调用方修改输出描述不能回写投影，新的子句也不依赖旧 Schema 的副本。
	schema[0].Name = "changed"
	schema[0].Lineage[0] = 99
	*schema[0].Type.TimePrecision = 0
	assert.Equal(t, "created", base.Schema()[0].Name)
	assert.Equal(t, []FieldID{1}, base.Schema()[0].Lineage)
	assert.Equal(t, 6, *base.Schema()[0].Type.TimePrecision)
	extended := base.Columns(NamedExpr{Expr: field, Alias: "again", Output: 3, Encoding: encoding})
	require.Len(t, extended.Schema(), 2)
	assert.Equal(t, FieldID(3), extended.Schema()[1].ID)
	assert.Len(t, base.Schema(), 1)
	var absent *SelectBuilder
	assert.Nil(t, absent.Schema())
}
