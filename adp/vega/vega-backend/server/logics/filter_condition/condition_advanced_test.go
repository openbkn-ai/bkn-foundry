package filter_condition

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

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

func TestNewFilterConditionAdvancedSuccess(t *testing.T) {
	fieldsMap := advancedFieldsMap()
	tests := []struct {
		name      string
		cfg       *interfaces.FilterCondCfg
		assertion func(t *testing.T, cond interfaces.FilterCondition)
	}{
		{
			name: "range accepts numeric field",
			cfg:  constCfg("age", OperationRange, []any{18, 30}),
			assertion: func(t *testing.T, cond interfaces.FilterCondition) {
				got := cond.(*RangeCond)
				assert.Equal(t, "age", got.Lfield.Name)
				assert.Equal(t, []any{18, 30}, got.Value)
			},
		},
		{
			name: "out range accepts datetime field",
			cfg:  constCfg("created_at", OperationOutRange, []any{"2026-01-01", "2026-12-31"}),
			assertion: func(t *testing.T, cond interfaces.FilterCondition) {
				got := cond.(*OutRangeCond)
				assert.Equal(t, "created_at", got.Lfield.Name)
			},
		},
		{
			name: "before accepts interval pair",
			cfg:  constCfg("created_at", OperationBefore, []any{float64(3), CurrentDay}),
			assertion: func(t *testing.T, cond interfaces.FilterCondition) {
				got := cond.(*BeforeCond)
				assert.Equal(t, []any{float64(3), CurrentDay}, got.Value)
			},
		},
		{
			name: "current accepts supported unit",
			cfg:  constCfg("created_at", OperationCurrent, CurrentMonth),
			assertion: func(t *testing.T, cond interfaces.FilterCondition) {
				got := cond.(*CurrentCond)
				assert.Equal(t, CurrentMonth, got.Value)
			},
		},
		{
			name: "between creates temporary datetime field for unknown name",
			cfg:  constCfg("unknown_created_at", OperationBetween, []any{"2026-01-01", "2026-01-02"}),
			assertion: func(t *testing.T, cond interfaces.FilterCondition) {
				got := cond.(*BetweenCond)
				assert.Equal(t, "unknown_created_at", got.Lfield.Name)
				assert.Equal(t, interfaces.DataType_Datetime, got.Lfield.Type)
			},
		},
		{
			name: "match uses remain fields list",
			cfg: &interfaces.FilterCondCfg{
				Operation: OperationMatch,
				ValueOptCfg: interfaces.ValueOptCfg{
					ValueFrom: interfaces.ValueFrom_Const,
					Value:     "hello",
				},
				RemainCfg: map[string]any{"fields": []any{"name", "tags"}},
			},
			assertion: func(t *testing.T, cond interfaces.FilterCondition) {
				got := cond.(*MatchCond)
				require.Len(t, got.Fields, 2)
				assert.Equal(t, "name", got.Fields[0].Name)
				assert.Equal(t, "tags", got.Fields[1].Name)
			},
		},
		{
			name: "multi match stores match type",
			cfg: &interfaces.FilterCondCfg{
				Operation: OperationMultiMatch,
				ValueOptCfg: interfaces.ValueOptCfg{
					ValueFrom: interfaces.ValueFrom_Const,
					Value:     "hello",
				},
				RemainCfg: map[string]any{
					"fields":     []any{"name", "tags"},
					"match_type": "best_fields",
				},
			},
			assertion: func(t *testing.T, cond interfaces.FilterCondition) {
				got := cond.(*MultiMatchCond)
				require.Len(t, got.Fields, 2)
				assert.Equal(t, "best_fields", got.MatchType)
			},
		},
		{
			name: "knn vector accepts vector field and sub conditions",
			cfg: &interfaces.FilterCondCfg{
				Name:      "embedding",
				Operation: OperationKnnVector,
				ValueOptCfg: interfaces.ValueOptCfg{
					ValueFrom: interfaces.ValueFrom_Const,
					Value:     []float32{0.1, 0.2},
				},
				SubConds: []*interfaces.FilterCondCfg{
					constCfg("is_active", OperationTrue, nil),
				},
			},
			assertion: func(t *testing.T, cond interfaces.FilterCondition) {
				got := cond.(*KnnVectorCond)
				assert.Equal(t, "embedding", got.FilterFieldName)
				require.Len(t, got.SubConds, 1)
				assert.Equal(t, OperationTrue, got.SubConds[0].GetOperation())
			},
		},
		{
			name: "and ignores empty sub condition",
			cfg: &interfaces.FilterCondCfg{
				Operation: OperationAnd,
				SubConds: []*interfaces.FilterCondCfg{
					{},
					constCfg("name", OperationEqual, "alice"),
				},
			},
			assertion: func(t *testing.T, cond interfaces.FilterCondition) {
				got := cond.(*AndCond)
				require.Len(t, got.SubConds, 1)
				assert.Equal(t, OperationEqual, got.SubConds[0].GetOperation())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond, err := NewFilterCondition(context.Background(), tt.cfg, fieldsMap)

			require.NoError(t, err)
			require.NotNil(t, cond)
			tt.assertion(t, cond)
		})
	}
}

func TestNewFilterConditionAdvancedErrors(t *testing.T) {
	fieldsMap := advancedFieldsMap()
	tests := []struct {
		name       string
		cfg        *interfaces.FilterCondCfg
		errContain string
	}{
		{
			name:       "range rejects string field",
			cfg:        constCfg("name", OperationRange, []any{1, 2}),
			errContain: "not a date/number field",
		},
		{
			name:       "before rejects integer first interval value",
			cfg:        constCfg("created_at", OperationBefore, []any{3, CurrentDay}),
			errContain: "interval value should be an number",
		},
		{
			name:       "current rejects unsupported unit",
			cfg:        constCfg("created_at", OperationCurrent, "quarter"),
			errContain: "right value should be",
		},
		{
			name:       "multi match requires fields array",
			cfg:        constCfg("", OperationMultiMatch, "hello"),
			errContain: "'fields' value should be an array",
		},
		{
			name: "multi match rejects unknown match type",
			cfg: &interfaces.FilterCondCfg{
				Operation: OperationMultiMatch,
				ValueOptCfg: interfaces.ValueOptCfg{
					ValueFrom: interfaces.ValueFrom_Const,
					Value:     "hello",
				},
				RemainCfg: map[string]any{
					"fields":     []any{"name"},
					"match_type": "unknown",
				},
			},
			errContain: "'match_type' value should be",
		},
		{
			name:       "knn vector rejects non-vector field",
			cfg:        constCfg("name", OperationKnnVector, []float32{0.1}),
			errContain: "type must be vector",
		},
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

func advancedFieldsMap() map[string]*interfaces.Property {
	fields := testFieldsMap()
	fields["embedding"] = &interfaces.Property{Name: "embedding", Type: interfaces.DataType_Vector}
	return fields
}
