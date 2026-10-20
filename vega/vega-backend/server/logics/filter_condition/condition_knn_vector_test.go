// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package filter_condition

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnnVectorCondNew(t *testing.T) {
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
			cfg := &interfaces.FilterCondCfg{Name: "embedding", Operation: OperationKnnVector, ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const, Value: tc.value}}
			cond, err := (&KnnVectorCond{}).New(context.Background(), cfg, map[string]*interfaces.Property{"embedding": field})
			if tc.invalid {
				require.Error(t, err)
				assert.Nil(t, cond)
				assert.Contains(t, err.Error(), "dimension")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cond)
			assert.Equal(t, cfg, cond.(*KnnVectorCond).Cfg)
		})
	}
}
