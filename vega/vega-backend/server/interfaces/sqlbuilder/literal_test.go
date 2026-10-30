// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package sqlbuilder

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiteralNumber(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  json.Number
	}{
		{json.Number("1.23000000000000000001"), "1.23000000000000000001"},
		{uint64(math.MaxUint64), "18446744073709551615"},
		{int64(math.MinInt64), "-9223372036854775808"},
		{float32(1.25), "1.25e+00"}, {float64(1.25), "1.25e+00"},
	} {
		number, err := LiteralNumber(tc.value)
		require.NoError(t, err)
		assert.Equal(t, tc.want, number)
	}
	for _, value := range []any{json.Number("1); DROP TABLE t"), json.Number("01"), json.Number("true"), json.Number("1e999"), math.Inf(1), math.NaN(), "1", []byte("1"), &Column{Name: "x"}} {
		number, err := LiteralNumber(value)
		require.Error(t, err)
		assert.Empty(t, number)
	}
}

func TestScalarValue(t *testing.T) {
	for _, node := range []SqlNode{&Literal{Value: "x"}, &Param{Value: "x"}} {
		value, ok := ScalarValue(node)
		require.True(t, ok)
		assert.Equal(t, "x", value)
	}
	for _, node := range []SqlNode{nil, (*Literal)(nil), (*Param)(nil), &Column{Name: "x"}} {
		_, ok := ScalarValue(node)
		assert.False(t, ok)
	}
	value, ok := ScalarValue(&Literal{})
	assert.True(t, ok)
	assert.Nil(t, value)
}
