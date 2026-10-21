// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

func TestFilterExpression(t *testing.T) {
	origin := qm.Origin{Kind: qm.OriginDTO, Path: "filter_condition"}
	t.Run("exact numeric values and field sources", func(t *testing.T) {
		input := map[string]any{"field": "amount", "operation": ">=", "value": json.Number("12345678901234567890.123456789")}
		expr, err := FilterExpression(input, origin)
		require.NoError(t, err)
		assert.Equal(t, "12345678901234567890.123456789", expr.Args[1].Literal.Text)
		assert.Equal(t, qm.LiteralDecimal, expr.Args[1].Literal.Kind)
		assert.Equal(t, "filter_condition.value", expr.Args[1].Origin.Path)
		expr, err = FilterExpression(map[string]any{"field": "id", "operation": "eq", "value_from": "field", "value": "other.id"}, origin)
		require.NoError(t, err)
		assert.Equal(t, []string{"other.id"}, expr.Args[1].Name)
	})
	t.Run("shared condition structure preserves values and input", func(t *testing.T) {
		input := &interfaces.FilterCondCfg{Operation: "and", SubConds: []*interfaces.FilterCondCfg{
			{Name: "amount", Operation: ">=", Value: json.Number("12345678901234567890.123456789")},
			{Name: "enabled", Operation: "==", Value: false},
		}}
		before, err := json.Marshal(input)
		require.NoError(t, err)
		expr, err := FilterExpression(input, origin)
		require.NoError(t, err)
		assert.Equal(t, "AND", expr.Op)
		assert.Equal(t, "12345678901234567890.123456789", expr.Args[0].Args[1].Literal.Text)
		assert.Equal(t, "false", expr.Args[1].Args[1].Literal.Text)
		after, err := json.Marshal(input)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
		assert.Empty(t, input.SubConds[0].ValueFrom)
	})
	t.Run("like is literal contains not SQL wildcard", func(t *testing.T) {
		expr, err := FilterExpression(map[string]any{"field": "name", "operation": "like", "value": "%_\\"}, origin)
		require.NoError(t, err)
		assert.Equal(t, qm.ExprLiteralContains, expr.Kind)
		assert.Equal(t, "%_\\", expr.Args[1].Literal.Text)
		assert.Nil(t, expr.Escape)
	})
	t.Run("boolean trees ranges and null checks", func(t *testing.T) {
		input := map[string]any{"operation": "and", "sub_conditions": []any{
			map[string]any{"field": "id", "operation": "range", "value": []any{json.Number("1"), json.Number("10")}},
			map[string]any{"field": "name", "operation": "not_null"},
		}}
		expr, err := FilterExpression(input, origin)
		require.NoError(t, err)
		assert.Equal(t, "AND", expr.Op)
		assert.Equal(t, qm.ExprBetween, expr.Args[0].Kind)
		assert.True(t, expr.Args[1].Negated)
		assert.Equal(t, "filter_condition.sub_conditions[0].value[1]", expr.Args[0].Args[2].Origin.Path)
		expr, err = FilterExpression(map[string]any{}, origin)
		require.NoError(t, err)
		assert.Equal(t, "true", expr.Literal.Text)
	})
	for name, raw := range map[string]any{
		"not object": []any{}, "unknown key": map[string]any{"extra": 1}, "real_value": map[string]any{"field": "id", "operation": "==", "value": 1, "real_value": 2},
		"internal remaining config": map[string]any{"RemainCfg": nil},
		"case variant":              map[string]any{"Field": "id", "operation": "null"},
		"typed unknown extension":   &interfaces.FilterCondCfg{Name: "id", Operation: "null", RemainCfg: map[string]any{"extra": true}},
		"nested removed field":      map[string]any{"operation": "and", "sub_conditions": []any{map[string]any{"field": "id", "operation": "null", "real_value": 2}}},
		"null without operation":    map[string]any{"value": nil},
		"missing op":                map[string]any{"field": "id"}, "missing value": map[string]any{"field": "id", "operation": "=="},
		"null scalar": map[string]any{"field": "id", "operation": "==", "value": nil}, "object value": map[string]any{"field": "id", "operation": "==", "value": map[string]any{}},
		"null child": map[string]any{"operation": "and", "sub_conditions": []any{nil}}, "empty logic": map[string]any{"operation": "and", "sub_conditions": []any{}},
		"empty set": map[string]any{"field": "id", "operation": "in", "value": []any{}}, "null set item": map[string]any{"field": "id", "operation": "in", "value": []any{nil}},
		"bad range": map[string]any{"field": "id", "operation": "range", "value": []any{1}}, "user identity pending": map[string]any{"field": "id", "operation": "==", "value_from": "user", "value": "forged"},
		"unknown operation": map[string]any{"field": "id", "operation": "external", "value": 1},
	} {
		t.Run(name, func(t *testing.T) {
			expr, err := FilterExpression(raw, origin)
			require.Error(t, err)
			assert.Nil(t, expr)
		})
	}
	t.Run("budget counts generated literal nodes", func(t *testing.T) {
		values := make([]any, 100)
		for i := range values {
			values[i] = i
		}
		children := make([]any, 100)
		for i := range children {
			children[i] = map[string]any{"field": "id", "operation": "in", "value": values}
		}
		expr, err := FilterExpression(map[string]any{"operation": "and", "sub_conditions": children}, origin)
		require.Error(t, err)
		assert.Nil(t, expr)
		var compile *qm.CompileError
		require.ErrorAs(t, err, &compile)
		assert.Equal(t, qm.ErrorLimit, compile.Kind)
	})
	t.Run("depth limit before constructing unbounded IR", func(t *testing.T) {
		var raw any = map[string]any{"field": "id", "operation": "null"}
		for i := 0; i < qm.MaxDepth; i++ {
			raw = map[string]any{"operation": "and", "sub_conditions": []any{raw}}
		}
		expr, err := FilterExpression(raw, origin)
		require.Error(t, err)
		assert.Nil(t, expr)
		var compile *qm.CompileError
		require.ErrorAs(t, err, &compile)
		assert.Equal(t, qm.ErrorLimit, compile.Kind)
	})
}
