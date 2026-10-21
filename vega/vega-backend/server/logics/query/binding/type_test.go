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
	for _, tc := range []struct {
		public, native string
		kind           qm.TypeKind
	}{
		{interfaces.DataType_Decimal, "DECIMAL", qm.TypeOther},
		{interfaces.DataType_Decimal, "DECIMAL(65,30)", qm.TypeOther},
		{interfaces.DataType_Datetime, "timestamp without time zone", qm.TypeLocalDateTime},
		{interfaces.DataType_Datetime, "timestamp(7) with time zone", qm.TypeInstant},
		{interfaces.DataType_Datetime, "datetimeoffset(7)", qm.TypeInstant},
		{interfaces.DataType_Datetime, "DATETIME2(7)", qm.TypeLocalDateTime},
		{interfaces.DataType_Datetime, "date_nanos", qm.TypeInstant},
		{interfaces.DataType_Timestamp, "TIMESTAMP", qm.TypeOther},
		{interfaces.DataType_Time, "TIME", qm.TypeOther},
		{interfaces.DataType_Time, "TIME WITHOUT TIME ZONE", qm.TypeTimeOfDay},
		{interfaces.DataType_Time, "TIME WITH TIME ZONE", qm.TypeOther},
		{interfaces.DataType_Vector, "dense_vector", qm.TypeVector},
		{interfaces.DataType_Other, "opaque", qm.TypeOther},
	} {
		typ, _, err := propertyType(&interfaces.Property{Type: tc.public, OriginalType: tc.native}, qm.Origin{})
		require.NoError(t, err, tc.native)
		assert.Equal(t, tc.kind, typ.Kind, tc.native)
	}
	for _, property := range []*interfaces.Property{
		{Type: interfaces.DataType_Decimal, OriginalType: "DECIMAL(10,11)"},
		{Type: interfaces.DataType_Datetime, OriginalType: "DATETIME(10)"},
	} {
		_, _, err := propertyType(property, qm.Origin{})
		require.Error(t, err)
	}
}

func TestSourcePropertyType(t *testing.T) {
	for _, tc := range []struct {
		name, connector, public, native string
		want                            qm.TypeKind
	}{
		{"mysql timestamp", interfaces.ConnectorTypeMySQL, interfaces.DataType_Datetime, "TIMESTAMP(6)", qm.TypeInstant},
		{"postgres timestamp", interfaces.ConnectorTypePostgreSQL, interfaces.DataType_Datetime, "TIMESTAMP(6)", qm.TypeLocalDateTime},
		{"mysql duration", interfaces.ConnectorTypeMySQL, interfaces.DataType_Time, "TIME(6)", qm.TypeDuration},
		{"postgres clock", interfaces.ConnectorTypePostgreSQL, interfaces.DataType_Time, "TIME(6)", qm.TypeTimeOfDay},
		{"oracle date", interfaces.ConnectorTypeOracle, interfaces.DataType_Datetime, "DATE", qm.TypeLocalDateTime},
		{"doris datetime", interfaces.ConnectorTypeDoris, interfaces.DataType_Datetime, "DATETIMEV2(6)", qm.TypeLocalDateTime},
		{"binary sqlserver timestamp", interfaces.ConnectorTypeSQLServer, interfaces.DataType_Binary, "timestamp", qm.TypeBinary},
		{"mismatched clock metadata", interfaces.ConnectorTypeMySQL, interfaces.DataType_Time, "TIMESTAMP(6)", qm.TypeOther},
		{"malformed native timestamp", interfaces.ConnectorTypeMySQL, interfaces.DataType_Datetime, "TIMESTAMP(6) invalid", qm.TypeOther},
		{"unknown source", "", interfaces.DataType_Datetime, "TIMESTAMP(6)", qm.TypeOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ, native, err := sourcePropertyType(&interfaces.Property{Type: tc.public, OriginalType: tc.native}, tc.connector, qm.Origin{})
			require.NoError(t, err)
			assert.Equal(t, tc.want, typ.Kind)
			assert.Equal(t, tc.native, native.Name)
			assert.Equal(t, tc.connector, native.ConnectorType)
		})
	}
	typ, _, err := sourcePropertyType(&interfaces.Property{Type: interfaces.DataType_Time, OriginalType: "TIME"}, interfaces.ConnectorTypeMySQL, qm.Origin{})
	require.NoError(t, err)
	assert.Nil(t, typ.TimePrecision)
}
