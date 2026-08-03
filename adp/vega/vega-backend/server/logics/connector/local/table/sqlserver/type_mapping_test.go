// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.

package sqlserver

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"vega-backend/interfaces"
)

func TestSQLServerConnectorMapType(t *testing.T) {
	connector := &SQLServerConnector{}
	assert.Equal(t, interfaces.DataType_Binary, TypeMapping["timestamp"])
	tests := []struct {
		name       string
		nativeType string
		want       string
	}{
		{name: "integer", nativeType: "int", want: interfaces.DataType_Integer},
		{name: "decimal ignores case and whitespace", nativeType: " DECIMAL ", want: interfaces.DataType_Decimal},
		{name: "floating point", nativeType: "float", want: interfaces.DataType_Float},
		{name: "unicode string", nativeType: "nvarchar", want: interfaces.DataType_String},
		{name: "large text", nativeType: "xml", want: interfaces.DataType_Text},
		{name: "date", nativeType: "date", want: interfaces.DataType_Date},
		{name: "time", nativeType: "time", want: interfaces.DataType_Time},
		{name: "datetime", nativeType: "datetime", want: interfaces.DataType_Datetime},
		{name: "datetime2", nativeType: "datetime2", want: interfaces.DataType_Datetime},
		{name: "small datetime", nativeType: "smalldatetime", want: interfaces.DataType_Datetime},
		{name: "datetime with offset", nativeType: "datetimeoffset", want: interfaces.DataType_Datetime},
		{name: "boolean", nativeType: "bit", want: interfaces.DataType_Boolean},
		{name: "rowversion synonym", nativeType: "timestamp", want: interfaces.DataType_Binary},
		{name: "unsupported spatial type", nativeType: "geography", want: interfaces.DataType_Other},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, connector.MapType(test.nativeType))
		})
	}
}
