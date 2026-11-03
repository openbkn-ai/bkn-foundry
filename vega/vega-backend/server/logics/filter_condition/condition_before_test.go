// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package filter_condition

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestBeforeCondNew(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"timestamp unit", []any{1, "2026-10-03T02:00:00Z"}, "unit"},
		{"unknown unit", []any{1, "fortnight"}, "unit"},
		{"empty unit", []any{1, ""}, "unit"},
		{"blank unit", []any{1, " \t"}, "unit"},
		{"nonstring unit", []any{1, 2}, "unit"},
		{"zero", []any{0, "day"}, "positive integer"},
		{"negative", []any{-1, "day"}, "positive integer"},
		{"fraction", []any{1.5, "day"}, "positive integer"},
		{"json fraction", []any{json.Number("1.5"), "day"}, "positive integer"},
		{"precise json fraction", []any{json.Number("1.0000000000000001"), "day"}, "positive integer"},
		{"string interval", []any{"1", "day"}, "positive integer"},
		{"null interval", []any{nil, "day"}, "positive integer"},
		{"boolean interval", []any{true, "day"}, "positive integer"},
		{"overflow", []any{json.Number("9223372036854775808"), "day"}, "positive integer"},
		{"nan", []any{math.NaN(), "day"}, "positive integer"},
		{"infinity", []any{math.Inf(1), "day"}, "positive integer"},
		{"not array", 1, "array"},
		{"missing unit", []any{1}, "length 2"},
		{"extra value", []any{1, "day", "extra"}, "length 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := constCfg("created_at", OperationBefore, tc.value)
			got, err := (&BeforeCond{}).New(context.Background(), cfg, testFieldsMap())
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, got)
		})
	}
	for _, unit := range []string{"microsecond", "second", "minute", "hour", "day", "week", "month", "quarter", "year", "days", "WEEKS", " Day "} {
		t.Run(unit, func(t *testing.T) {
			cfg := constCfg("created_at", OperationBefore, []any{json.Number("9007199254740993"), unit})
			got, err := (&BeforeCond{}).New(context.Background(), cfg, testFieldsMap())
			require.NoError(t, err)
			assert.Equal(t, []any{int64(9007199254740993), unit}, got.(*BeforeCond).Value)
			assert.Equal(t, []any{json.Number("9007199254740993"), unit}, cfg.Value)
		})
	}
	t.Run("nested invalid condition", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{Operation: OperationAnd, SubConds: []*interfaces.FilterCondCfg{
			{Operation: OperationOr, SubConds: []*interfaces.FilterCondCfg{
				constCfg("created_at", OperationBefore, []any{1, "2026-10-03T02:00:00Z"}),
			}},
		}}
		got, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		require.ErrorContains(t, err, "unit")
		assert.Nil(t, got)
	})
}

func TestNormalizeBeforeInterval(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value json.Number
		want  int64
	}{
		{"integer", "1", 1},
		{"decimal integer", "1.0", 1},
		{"exponent integer", "1e3", 1000},
		{"max integer", "9223372036854775807", math.MaxInt64},
		{"max decimal integer", "9223372036854775807.0", math.MaxInt64},
		{"overflow", "9223372036854775808", 0},
		{"precise fraction", "1.0000000000000001", 0},
		{"tiny fraction", "1e-1000000000", 0},
		{"huge interval", "1e1000000000", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := []any{tc.value, "day"}
			err := NormalizeBeforeInterval(values)
			if tc.want == 0 {
				require.ErrorContains(t, err, "positive integer")
				assert.Equal(t, tc.value, values[0])
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, values[0])
			}
		})
	}
}
