// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

// 测试用字段映射
func testFieldsMap() map[string]*interfaces.Property {
	return map[string]*interfaces.Property{
		"name":       {Name: "name", Type: interfaces.DataType_String},
		"age":        {Name: "age", Type: interfaces.DataType_Integer},
		"score":      {Name: "score", Type: interfaces.DataType_Float},
		"created_at": {Name: "created_at", Type: interfaces.DataType_Datetime},
		"is_active":  {Name: "is_active", Type: interfaces.DataType_Boolean},
		"tags":       {Name: "tags", Type: interfaces.DataType_Text},
		"other_name": {Name: "other_name", Type: interfaces.DataType_String},
	}
}

func constCfg(name, op string, value any) *interfaces.FilterCondCfg {
	return &interfaces.FilterCondCfg{
		Name:      name,
		Operation: op,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     value,
		},
	}
}

func TestFilterConditionFactory(t *testing.T) {
	t.Run("returns nil for nil config", func(t *testing.T) {
		cond, err := NewFilterCondition(context.Background(), nil, testFieldsMap())

		require.NoError(t, err)
		assert.Nil(t, cond)
	})

	t.Run("returns nil for empty config", func(t *testing.T) {
		cond, err := NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{}, testFieldsMap())

		require.NoError(t, err)
		assert.Nil(t, cond)
	})

	t.Run("returns error for unsupported operation", func(t *testing.T) {
		cond, err := NewFilterCondition(context.Background(), constCfg("name", "unknown_op", "test"), testFieldsMap())

		require.Error(t, err)
		assert.Nil(t, cond)
		assert.Contains(t, err.Error(), "unsupported operation")
	})

	t.Run("creates condition for valid equal operation", func(t *testing.T) {
		cond, err := NewFilterCondition(context.Background(), constCfg("name", "==", "alice"), testFieldsMap())

		require.NoError(t, err)
		require.NotNil(t, cond)
		assert.Equal(t, OperationEqual, cond.GetOperation())
	})
	errorFieldsMap := advancedFieldsMap()
	errorTests := []struct {
		name      string
		cfg       *interfaces.FilterCondCfg
		assertion func(t *testing.T, cond interfaces.FilterCondition)
	}{
		{name: "range accepts numeric field", cfg: constCfg("age", OperationRange, []any{18, 30}), assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*RangeCond)
			assert.Equal(t, "age", got.Lfield.Name)
			assert.Equal(t, []any{18, 30}, got.Value)
		}},
		{name: "out range accepts datetime field", cfg: constCfg("created_at", OperationOutRange, []any{"2026-01-01", "2026-12-31"}), assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*OutRangeCond)
			assert.Equal(t, "created_at", got.Lfield.Name)
		}},
		{name: "before accepts interval pair", cfg: constCfg("created_at", OperationBefore, []any{float64(3), CurrentDay}), assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*BeforeCond)
			assert.Equal(t, []any{float64(3), CurrentDay}, got.Value)
		}},
		{name: "current accepts supported unit", cfg: constCfg("created_at", OperationCurrent, CurrentMonth), assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*CurrentCond)
			assert.Equal(t, CurrentMonth, got.Value)
		}},
		{name: "between creates temporary datetime field for unknown name", cfg: constCfg("unknown_created_at", OperationBetween, []any{"2026-01-01", "2026-01-02"}), assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*BetweenCond)
			assert.Equal(t, "unknown_created_at", got.Lfield.Name)
			assert.Equal(t, interfaces.DataType_Datetime, got.Lfield.Type)
		}},
		{name: "match uses remain fields list", cfg: &interfaces.FilterCondCfg{
			Operation: OperationMatch,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "hello",
			},
			RemainCfg: map[string]any{"fields": []any{"name", "tags"}},
		}, assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*MatchCond)
			require.Len(t, got.Fields, 2)
			assert.Equal(t, "name", got.Fields[0].Name)
			assert.Equal(t, "tags", got.Fields[1].Name)
		}},
		{name: "multi match stores match type", cfg: &interfaces.FilterCondCfg{
			Operation: OperationMultiMatch,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "hello",
			},
			RemainCfg: map[string]any{
				"fields":     []any{"name", "tags"},
				"match_type": "best_fields",
			},
		}, assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*MultiMatchCond)
			require.Len(t, got.Fields, 2)
			assert.Equal(t, "best_fields", got.MatchType)
		}},
		{name: "knn vector accepts vector field and sub conditions", cfg: &interfaces.FilterCondCfg{
			Name:      "embedding",
			Operation: OperationKnnVector,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     []float32{0.1, 0.2},
			},
			SubConds: []*interfaces.FilterCondCfg{
				constCfg("is_active", OperationTrue, nil),
			},
		}, assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*KnnVectorCond)
			assert.Equal(t, "embedding", got.FilterFieldName)
			require.Len(t, got.SubConds, 1)
			assert.Equal(t, OperationTrue, got.SubConds[0].GetOperation())
		}},
		{name: "and ignores empty sub condition", cfg: &interfaces.FilterCondCfg{
			Operation: OperationAnd,
			SubConds: []*interfaces.FilterCondCfg{
				{},
				constCfg("name", OperationEqual, "alice"),
			},
		}, assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*AndCond)
			require.Len(t, got.SubConds, 1)
			assert.Equal(t, OperationEqual, got.SubConds[0].GetOperation())
		}},
	}

	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			cond, err := NewFilterCondition(context.Background(), tt.cfg, errorFieldsMap)

			require.NoError(t, err)
			require.NotNil(t, cond)
			tt.assertion(t, cond)
		})
	}
	fieldsMap := advancedFieldsMap()
	tests := []struct {
		name       string
		cfg        *interfaces.FilterCondCfg
		errContain string
	}{
		{name: "range rejects string field", cfg: constCfg("name", OperationRange, []any{1, 2}), errContain: "not a date/number field"},
		{name: "before rejects integer first interval value", cfg: constCfg("created_at", OperationBefore, []any{3, CurrentDay}), errContain: "interval value should be an number"},
		{name: "current rejects unsupported unit", cfg: constCfg("created_at", OperationCurrent, "quarter"), errContain: "right value should be"},
		{name: "multi match requires fields array", cfg: constCfg("", OperationMultiMatch, "hello"), errContain: "'fields' value should be an array"},
		{name: "multi match rejects unknown match type", cfg: &interfaces.FilterCondCfg{
			Operation: OperationMultiMatch,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "hello",
			},
			RemainCfg: map[string]any{
				"fields":     []any{"name"},
				"match_type": "unknown",
			},
		}, errContain: "'match_type' value should be"},
		{name: "knn vector rejects non-vector field", cfg: constCfg("name", OperationKnnVector, []float32{0.1}), errContain: "type must be vector"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond, err := NewFilterCondition(context.Background(), tt.cfg, fieldsMap)

			require.Error(t, err)
			assert.Nil(t, cond)
			assert.ErrorContains(t, err, tt.errContain)
		})
	}
}

func TestEqualCond(t *testing.T) {
	t.Run("equal cond valid", func(t *testing.T) {
		cfg := constCfg("name", "==", "alice")
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		eq := cond.(*EqualCond)
		if eq.Lfield.Name != "name" {
			t.Errorf("expected left field 'name', got '%s'", eq.Lfield.Name)
		}
		if eq.Value != "alice" {
			t.Errorf("expected value 'alice', got '%v'", eq.Value)
		}
	})
	t.Run("equal cond empty field name", func(t *testing.T) {
		cfg := constCfg("", "==", "alice")
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for empty field name")
		}
		if !strings.Contains(err.Error(), "left field is empty") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("equal cond field not found", func(t *testing.T) {
		cfg := constCfg("nonexistent", "==", "alice")
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for unknown field")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("equal cond rejects array value", func(t *testing.T) {
		cfg := constCfg("name", "==", []any{"a", "b"})
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for array value in equal condition")
		}
		if !strings.Contains(err.Error(), "single value") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("equal cond field to field", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Name:      "name",
			Operation: "==",
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Field,
				Value:     "other_name",
			},
		}
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		eq := cond.(*EqualCond)
		if eq.Rfield == nil {
			t.Fatal("expected right field to be set")
		}
		if eq.Rfield.Name != "other_name" {
			t.Errorf("expected right field 'other_name', got '%s'", eq.Rfield.Name)
		}
	})
	t.Run("equal cond field to field right not found", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Name:      "name",
			Operation: "==",
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Field,
				Value:     "nonexistent",
			},
		}
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for unknown right field")
		}
		if !strings.Contains(err.Error(), "right field") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("equal cond alias operations", func(t *testing.T) {
		// "eq" 是 "==" 的别名
		cfg := constCfg("name", "eq", "alice")
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cond.GetOperation() != OperationEqual {
			t.Errorf("expected operation '==', got '%s'", cond.GetOperation())
		}
	})
}

func TestInCond(t *testing.T) {
	t.Run("in cond valid", func(t *testing.T) {
		cfg := constCfg("name", "in", []any{"alice", "bob"})
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		in := cond.(*InCond)
		if len(in.Value) != 2 {
			t.Errorf("expected 2 values, got %d", len(in.Value))
		}
	})
	t.Run("in cond empty array", func(t *testing.T) {
		cfg := constCfg("name", "in", []any{})
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for empty array")
		}
		if !strings.Contains(err.Error(), "length >= 1") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("in cond non array value", func(t *testing.T) {
		cfg := constCfg("name", "in", "single_value")
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for non-array value")
		}
		if !strings.Contains(err.Error(), "should be an array") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("in cond rejects field value from", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Name:      "name",
			Operation: "in",
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Field,
				Value:     []any{"alice"},
			},
		}
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for non-const value_from")
		}
		if !strings.Contains(err.Error(), "does not support value_from") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestLikeCond(t *testing.T) {
	t.Run("like cond valid", func(t *testing.T) {
		cfg := constCfg("name", "like", "ali%")
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		like := cond.(*LikeCond)
		if like.Value != "ali%" {
			t.Errorf("expected value 'ali%%', got '%s'", like.Value)
		}
	})
	t.Run("like cond non string field", func(t *testing.T) {
		cfg := constCfg("age", "like", "test")
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for non-string field")
		}
		if !strings.Contains(err.Error(), "not a string field") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("like cond non string value", func(t *testing.T) {
		cfg := constCfg("name", "like", 123)
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for non-string value")
		}
		if !strings.Contains(err.Error(), "not a string value") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRangeCond(t *testing.T) {
	t.Run("range cond valid", func(t *testing.T) {
		cfg := constCfg("age", "range", []any{18, 65})
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := cond.(*RangeCond)
		if len(r.Value) != 2 {
			t.Errorf("expected 2 values, got %d", len(r.Value))
		}
	})
	t.Run("range cond wrong array length", func(t *testing.T) {
		cfg := constCfg("age", "range", []any{18})
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for wrong array length")
		}
		if !strings.Contains(err.Error(), "length 2") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("range cond non numeric field", func(t *testing.T) {
		cfg := constCfg("name", "range", []any{1, 2})
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for non-numeric field")
		}
		if !strings.Contains(err.Error(), "not a date/number field") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("range cond date field", func(t *testing.T) {
		cfg := constCfg("created_at", "range", []any{"2024-01-01", "2024-12-31"})
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestAndCond(t *testing.T) {
	t.Run("and cond valid", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Operation: "and",
			SubConds: []*interfaces.FilterCondCfg{
				constCfg("name", "==", "alice"),
				constCfg("age", ">", 18),
			},
		}
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		and := cond.(*AndCond)
		if len(and.SubConds) != 2 {
			t.Errorf("expected 2 sub-conditions, got %d", len(and.SubConds))
		}
	})
	t.Run("and cond empty sub conds", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Operation: "and",
			SubConds:  []*interfaces.FilterCondCfg{},
		}
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for empty sub-conditions")
		}
		if !strings.Contains(err.Error(), "size is 0") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("and cond invalid sub cond", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Operation: "and",
			SubConds: []*interfaces.FilterCondCfg{
				constCfg("nonexistent", "==", "test"),
			},
		}
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for invalid sub-condition")
		}
	})
	t.Run("and cond nested and or", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Operation: "and",
			SubConds: []*interfaces.FilterCondCfg{
				constCfg("name", "==", "alice"),
				{
					Operation: "or",
					SubConds: []*interfaces.FilterCondCfg{
						constCfg("age", ">", 18),
						constCfg("age", "<", 65),
					},
				},
			},
		}
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		and := cond.(*AndCond)
		if len(and.SubConds) != 2 {
			t.Errorf("expected 2 sub-conditions, got %d", len(and.SubConds))
		}
		if and.SubConds[1].GetOperation() != OperationOr {
			t.Errorf("expected nested 'or', got '%s'", and.SubConds[1].GetOperation())
		}
	})
}

func TestComparisonOps(t *testing.T) {
	t.Run("comparison ops empty field", func(t *testing.T) {
		ops := []string{"==", "!=", ">", ">=", "<", "<="}
		for _, op := range ops {
			cfg := constCfg("", op, "test")
			_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
			if err == nil {
				t.Errorf("operation '%s': expected error for empty field name", op)
			}
		}
	})
	t.Run("comparison ops field not found", func(t *testing.T) {
		// 只测试 == 操作符，因为目前只有 EqualCond 实现了对不存在字段的严格检查
		ops := []string{"=="}
		for _, op := range ops {
			cfg := constCfg("nonexistent", op, "test")
			_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
			if err == nil {
				t.Errorf("operation '%s': expected error for unknown field", op)
			}
		}
	})
}

func TestIsSlice(t *testing.T) {
	t.Run("returns true for slices and arrays", func(t *testing.T) {
		assert.True(t, IsSlice([]int{1, 2, 3}))
		assert.True(t, IsSlice([]string{"a"}))
		assert.True(t, IsSlice([2]int{1, 2}))
	})

	t.Run("returns false for scalar values", func(t *testing.T) {
		assert.False(t, IsSlice("not a slice"))
		assert.False(t, IsSlice(42))
	})
}

func TestIsSameType(t *testing.T) {
	t.Run("returns true for empty and same-type values", func(t *testing.T) {
		assert.True(t, IsSameType([]any{}))
		assert.True(t, IsSameType([]any{1, 2, 3}))
		assert.True(t, IsSameType([]any{"a", "b"}))
	})

	t.Run("returns false for mixed types", func(t *testing.T) {
		assert.False(t, IsSameType([]any{1, "two", 3}))
	})
}

func TestAllOperationsRegistered(t *testing.T) {
	t.Run("all operations registered", func(t *testing.T) {
		expectedOps := []string{
			"and", "or",
			"==", "eq", "!=", "not_eq",
			">", "gt", ">=", "gte", "<", "lt", "<=", "lte",
			"in", "not_in",
			"like", "not_like",
			"contain", "not_contain",
			"range", "out_range",
			"exist", "not_exist",
			"empty", "not_empty",
			"regex", "match", "match_phrase",
			"prefix", "not_prefix",
			"null", "not_null",
			"true", "false",
			"before", "current", "between",
			"knn_vector", "multi_match",
		}

		for _, op := range expectedOps {
			if _, exists := OperationMap[op]; !exists {
				t.Errorf("operation '%s' not registered in OperationMap", op)
			}
		}
	})
}

func TestNullCondValid(t *testing.T) {
	t.Run("null cond valid", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Name:      "name",
			Operation: "null",
		}
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cond.GetOperation() != OperationNull {
			t.Errorf("expected operation 'null', got '%s'", cond.GetOperation())
		}
		if cond.NeedValue() {
			t.Error("null condition should not need value")
		}
	})
}

func TestConditionMetadata(t *testing.T) {
	tests := []struct {
		name             string
		condition        interfaces.FilterCondition
		operation        string
		supportSubCond   bool
		needName         bool
		needValue        bool
		needConstValue   bool
		singleValue      bool
		fixedArrayValue  bool
		requiredValueLen int
	}{
		{name: "and", condition: &AndCond{}, operation: OperationAnd, supportSubCond: true, requiredValueLen: -1},
		{name: "or", condition: &OrCond{}, operation: OperationOr, supportSubCond: true, requiredValueLen: -1},
		{name: "equal", condition: &EqualCond{}, operation: OperationEqual, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "not equal", condition: &NotEqualCond{}, operation: OperationNotEqual, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "gt", condition: &GtCond{}, operation: OperationGt, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "gte", condition: &GteCond{}, operation: OperationGte, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "lt", condition: &LtCond{}, operation: OperationLt, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "lte", condition: &LteCond{}, operation: OperationLte, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "in", condition: &InCond{}, operation: OperationIn, needName: true, needValue: true, needConstValue: true, requiredValueLen: -1},
		{name: "not in", condition: &NotInCond{}, operation: OperationNotIn, needName: true, needValue: true, needConstValue: true, requiredValueLen: -1},
		{name: "like", condition: &LikeCond{}, operation: OperationLike, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "not like", condition: &NotLikeCond{}, operation: OperationNotLike, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "contain", condition: &ContainCond{}, operation: OperationContain, needName: true, needValue: true, needConstValue: true, requiredValueLen: -1},
		{name: "not contain", condition: &NotContainCond{}, operation: OperationNotContain, needName: true, needValue: true, needConstValue: true, requiredValueLen: -1},
		{name: "range", condition: &RangeCond{}, operation: OperationRange, needName: true, needValue: true, needConstValue: true, fixedArrayValue: true, requiredValueLen: 2},
		{name: "out range", condition: &OutRangeCond{}, operation: OperationOutRange, needName: true, needValue: true, needConstValue: true, fixedArrayValue: true, requiredValueLen: 2},
		{name: "exist", condition: &ExistCond{}, operation: OperationExist, needName: true, requiredValueLen: -1},
		{name: "not exist", condition: &NotExistCond{}, operation: OperationNotExist, needName: true, requiredValueLen: -1},
		{name: "empty", condition: &EmptyCond{}, operation: OperationEmpty, needName: true, requiredValueLen: -1},
		{name: "not empty", condition: &NotEmptyCond{}, operation: OperationNotEmpty, needName: true, requiredValueLen: -1},
		{name: "regex", condition: &RegexCond{}, operation: OperationRegex, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "match", condition: &MatchCond{}, operation: OperationMatch, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "match phrase", condition: &MatchPhraseCond{}, operation: OperationMatchPhrase, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "prefix", condition: &PrefixCond{}, operation: OperationPrefix, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "not prefix", condition: &NotPrefixCond{}, operation: OperationNotPrefix, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "null", condition: &NullCond{}, operation: OperationNull, needName: true, requiredValueLen: -1},
		{name: "not null", condition: &NotNullCond{}, operation: OperationNotNull, needName: true, requiredValueLen: -1},
		{name: "true", condition: &TrueCond{}, operation: OperationTrue, needName: true, requiredValueLen: -1},
		{name: "false", condition: &FalseCond{}, operation: OperationFalse, needName: true, requiredValueLen: -1},
		{name: "before", condition: &BeforeCond{}, operation: OperationBefore, needName: true, needValue: true, needConstValue: true, fixedArrayValue: true, requiredValueLen: 2},
		{name: "current", condition: &CurrentCond{}, operation: OperationCurrent, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: 1},
		{name: "between", condition: &BetweenCond{}, operation: OperationBetween, needName: true, needValue: true, needConstValue: true, fixedArrayValue: true, requiredValueLen: 2},
		{name: "knn vector", condition: &KnnVectorCond{}, operation: OperationKnnVector, supportSubCond: true, needName: true, needValue: true, needConstValue: true, requiredValueLen: -1},
		{name: "multi match", condition: &MultiMatchCond{}, operation: OperationMultiMatch, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.operation, tt.condition.GetOperation())
			assert.Equal(t, tt.supportSubCond, tt.condition.SupportSubCond())
			assert.Equal(t, tt.needName, tt.condition.NeedName())
			assert.Equal(t, tt.needValue, tt.condition.NeedValue())
			assert.Equal(t, tt.needConstValue, tt.condition.NeedConstValue())
			assert.Equal(t, tt.singleValue, tt.condition.IsSingleValue())
			assert.Equal(t, tt.fixedArrayValue, tt.condition.IsFixedLenArrayValue())
			assert.Equal(t, tt.requiredValueLen, tt.condition.RequiredValueLen())
		})
	}
}

func advancedFieldsMap() map[string]*interfaces.Property {
	fields := testFieldsMap()
	fields["embedding"] = &interfaces.Property{Name: "embedding", Type: interfaces.DataType_Vector}
	return fields
}
