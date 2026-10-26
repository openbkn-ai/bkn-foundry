// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSqlizerOriginAndNames(t *testing.T) {
	parts := []string{"a", "field"}
	node := Field(parts...)
	parts[1] = "changed"
	saved := node.WithOrigin(Origin{Kind: OriginSavedView, ResourceID: "view"})
	assert.Equal(t, []string{"a", "field"}, node.Name)
	assert.Empty(t, node.Origin.Kind)
	assert.Equal(t, OriginSavedView, saved.Origin.Kind)
	assert.Equal(t, LiteralString, String("value").Literal.Kind)
	assert.Equal(t, LiteralNull, Null().Literal.Kind)
	assert.Equal(t, "false", Or().Literal.Text)
	assert.Equal(t, "true", And().Literal.Text)
}

func TestSqlizerFieldIDs(t *testing.T) {
	first := &Sqlizer{Kind: ExprField, Field: 7}
	second := &Sqlizer{Kind: ExprField, Field: 3}
	expr := And(Eq(first, second), Eq(first, first))
	assert.Equal(t, []FieldID{7, 3}, expr.FieldIDs())
	// 非法环由准备阶段拒绝；Schema 派生本身不能无限递归。
	expr.Args = append(expr.Args, expr, nil)
	assert.Equal(t, []FieldID{7, 3}, expr.FieldIDs())
	var absent *Sqlizer
	assert.Empty(t, absent.FieldIDs())
}
