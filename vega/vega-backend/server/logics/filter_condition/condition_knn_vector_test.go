// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package filter_condition

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnnVectorCondNew(t *testing.T) {
	t.Run("预先绑定已有物理向量字段且保留待解析文本条件", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{Field: "body", Operation: OperationKnnVector, ValueFrom: interfaces.ValueFrom_Const, Value: "query"}
		fields := map[string]*interfaces.Property{
			"body":        {Name: "body", Type: interfaces.DataType_Text, Filterable: new(false), Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, RefProperty: "body_vector"}}},
			"body_vector": {Name: "body_vector", Type: interfaces.DataType_Vector, Filterable: new(true)},
		}
		condition, err := NewFilterCondition(t.Context(), cfg, fields)
		require.NoError(t, err)
		actual := condition.(*KnnVectorCond)
		assert.Equal(t, "body_vector", actual.FilterFieldName)
		assert.Same(t, cfg, actual.Cfg)
		assert.Equal(t, "body", cfg.Field)
		assert.Equal(t, "query", cfg.Value)
		cfg.Field, cfg.Value = "body_vector", []float32{0.1, 0.2}
		assert.Equal(t, []float32{0.1, 0.2}, actual.Cfg.Value)
		delete(fields, "body_vector")
		cfg.Field, cfg.Value = "body", "query"
		_, err = NewFilterCondition(t.Context(), cfg, fields)
		require.Error(t, err)
	})
	for _, tc := range []struct {
		name      string
		value     any
		dimension any
		invalid   bool
	}{
		{"wrong dimension", []any{0.1, 0.2, 0.3}, float64(1024), true},
		{"matching dimension", []any{0.1, 0.2, 0.3}, 3, false},
		{"model vector", []float32{0.1, 0.2, 0.3}, json.Number("3"), false},
		{"empty vector", []float32{}, 3, true},
		{"unknown dimension", []float64{0.1, 0.2, 0.3}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			field := &interfaces.Property{Name: "embedding", Type: interfaces.DataType_Vector, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": tc.dimension}}}}
			cfg := &interfaces.FilterCondCfg{Field: "embedding", Operation: OperationKnnVector, ValueFrom: interfaces.ValueFrom_Const, Value: tc.value}
			cond, err := (&KnnVectorCond{}).New(context.Background(), cfg, map[string]*interfaces.Property{"embedding": field})
			if tc.invalid {
				require.Error(t, err)
				assert.Nil(t, cond)
				if tc.name == "empty vector" {
					assert.Contains(t, err.Error(), "non-empty")
				} else {
					assert.Contains(t, err.Error(), "dimension")
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cond)
			assert.Equal(t, cfg, cond.(*KnnVectorCond).Cfg)
		})
	}
}

func TestValidateKnnValue(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   any
		invalid bool
	}{
		{"query text", "[0.1,0.2]", false},
		{"empty query text", "", false},
		{"float32 vector", []float32{0.1, 0.2}, false},
		{"float64 vector", []float64{0.1, 0.2}, false},
		{"integer vector", []int{1, 2}, false},
		{"unsigned integer vector", []uint64{1, 2}, false},
		{"precise JSON vector", []json.Number{"0.1", "2e-1"}, false},
		{"mixed JSON numeric vector", []any{json.Number("0.1"), float64(0.2)}, false},
		{"number", 42, true},
		{"boolean", true, true},
		{"object", map[string]any{"x": 1}, true},
		{"null", nil, true},
		{"nil vector", []float32(nil), true},
		{"empty vector", []any{}, true},
		{"base64 bytes", []byte{1, 2}, true},
		{"numeric strings", []any{"0.1", "0.2"}, true},
		{"mixed string element", []any{0.1, "0.2"}, true},
		{"null element", []any{0.1, nil}, true},
		{"nested vector", []any{[]any{0.1}, []any{0.2}}, true},
		{"object element", []any{0.1, map[string]any{"x": 1}}, true},
		{"NaN", []float64{0.1, math.NaN()}, true},
		{"positive infinity", []float64{0.1, math.Inf(1)}, true},
		{"negative infinity", []float32{0.1, float32(math.Inf(-1))}, true},
		{"overflow JSON number", []any{0.1, json.Number("1e1000")}, true},
	} {
		for _, knownDimension := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/known_dimension=%v", tc.name, knownDimension), func(t *testing.T) {
				var field *interfaces.Property
				if knownDimension {
					field = &interfaces.Property{Name: "embedding", Type: interfaces.DataType_Vector,
						Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}}}
				}
				err := ValidateKnnValue(field, tc.value)
				if tc.invalid {
					var buildErr *interfaces.ConditionBuildError
					require.ErrorAs(t, err, &buildErr)
					return
				}
				require.NoError(t, err)
			})
		}
	}
}
