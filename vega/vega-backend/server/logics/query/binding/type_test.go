// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

func TestNewSemanticContext(t *testing.T) {
	utc, err := NewSemanticContext("  ")
	require.NoError(t, err)
	assert.Equal(t, "UTC", utc.TimeZone)
	assert.Equal(t, qm.CurrentSemanticsVersion, utc.Version)
	configured, err := NewSemanticContext(" Asia/Shanghai ")
	require.NoError(t, err)
	assert.Equal(t, "Asia/Shanghai", configured.TimeZone)
	assert.NotEqual(t, utc.ConfigFingerprint, configured.ConfigFingerprint)
	invalid, err := NewSemanticContext("not-a-time-zone")
	require.Error(t, err)
	assert.Nil(t, invalid.Location)
	var e *qm.CompileError
	require.ErrorAs(t, err, &e)
	assert.Equal(t, qm.ErrorConfiguration, e.Kind)
}
func TestCommonType(t *testing.T) {
	signed := qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}
	unsigned := signed
	unsigned.Unsigned = true
	typ, err := commonType(signed, unsigned, qm.Origin{})
	require.NoError(t, err)
	assert.Equal(t, qm.LogicalType{Kind: qm.TypeDecimal, Precision: 20}, typ)
	typ, err = commonType(qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 32}, qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 32, Unsigned: true}, qm.Origin{})
	require.NoError(t, err)
	assert.Equal(t, signed, typ)
	_, err = commonType(signed, qm.LogicalType{Kind: qm.TypeFloat, BitWidth: 64}, qm.Origin{})
	require.Error(t, err)
}
func TestArithmeticType(t *testing.T) {
	left := qm.LogicalType{Kind: qm.TypeDecimal, Precision: 12, Scale: 2}
	right := qm.LogicalType{Kind: qm.TypeDecimal, Precision: 8, Scale: 3}
	for _, test := range []struct {
		op   qm.ScalarOperator
		p, s int
	}{{qm.OperatorAdd, 14, 3}, {qm.OperatorSubtract, 14, 3}, {qm.OperatorMultiply, 20, 5}, {qm.OperatorDivide, 24, 11}} {
		typ, err := arithmeticType(test.op, left, right, qm.Origin{})
		require.NoError(t, err)
		assert.Equal(t, qm.LogicalType{Kind: qm.TypeDecimal, Precision: test.p, Scale: test.s}, typ)
	}
	_, err := arithmeticType(qm.OperatorMultiply, qm.LogicalType{Kind: qm.TypeDecimal, Precision: 30}, left, qm.Origin{})
	require.Error(t, err)
}
func TestDecimalLiteral(t *testing.T) {
	for _, test := range []struct {
		in, out string
		p, s    int
	}{{"1.2300", "1.2300", 5, 4}, {"1.23e2", "123", 3, 0}, {"1e-6", "0.000001", 6, 6}, {"0000.00", "0.00", 2, 2}, {"-1.20e1", "-12.0", 3, 1}} {
		text, p, s, err := decimalLiteral(test.in, qm.Origin{})
		require.NoError(t, err)
		assert.Equal(t, test.out, text)
		assert.Equal(t, test.p, p)
		assert.Equal(t, test.s, s)
	}
	for _, in := range []string{"1e10000000", "1e-10000000", "1.234567890123456789012345678901234567890", "NaN", "1.2.3"} {
		_, _, _, err := decimalLiteral(in, qm.Origin{})
		require.Error(t, err)
	}
}
func TestPropertyType(t *testing.T) {
	typ, native, err := propertyType(&interfaces.Property{Type: interfaces.DataType_Decimal, OriginalType: "decimal(12, 2)"}, qm.Origin{})
	require.NoError(t, err)
	assert.Equal(t, 12, typ.Precision)
	assert.Equal(t, 2, *native.Scale)
	typ, native, err = propertyType(&interfaces.Property{Type: interfaces.DataType_String, OriginalType: "LARGEINT"}, qm.Origin{})
	require.NoError(t, err)
	assert.Equal(t, qm.TypeOther, typ.Kind)
	assert.Equal(t, qm.DomainNativeNumeric, native.Domain)
	typ, _, err = propertyType(&interfaces.Property{Type: interfaces.DataType_Datetime, OriginalType: "DATETIME(9)"}, qm.Origin{})
	require.NoError(t, err)
	require.NotNil(t, typ.TimePrecision)
	assert.Equal(t, 9, *typ.TimePrecision)
	for _, property := range []*interfaces.Property{
		{Type: interfaces.DataType_Decimal, OriginalType: "DECIMAL"}, {Type: interfaces.DataType_Decimal, OriginalType: "DECIMAL(39,0)"},
		{Type: interfaces.DataType_Decimal, OriginalType: "DECIMAL(10,11)"}, {Type: interfaces.DataType_Datetime, OriginalType: "timestamp without time zone"},
		{Type: interfaces.DataType_Time, OriginalType: "TIME"},
	} {
		_, _, err := propertyType(property, qm.Origin{})
		require.Error(t, err)
	}
}
