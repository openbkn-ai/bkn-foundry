// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"strings"

	"vega-backend/interfaces"
)

// TypeMapping maps PostgreSQL udt_name/common data_type to VEGA types.
var TypeMapping = map[string]string{
	// Integer
	"int2":        interfaces.DataType_Integer,
	"int4":        interfaces.DataType_Integer,
	"int8":        interfaces.DataType_Integer,
	"serial":      interfaces.DataType_Integer,
	"serial2":     interfaces.DataType_Integer,
	"serial4":     interfaces.DataType_Integer,
	"serial8":     interfaces.DataType_Integer,
	"smallserial": interfaces.DataType_Integer,
	"bigserial":   interfaces.DataType_Integer,
	"smallint":    interfaces.DataType_Integer,
	"integer":     interfaces.DataType_Integer,
	"bigint":      interfaces.DataType_Integer,

	// Floating-point
	"float4":           interfaces.DataType_Float,
	"float8":           interfaces.DataType_Float,
	"double precision": interfaces.DataType_Float,
	"real":             interfaces.DataType_Float,
	"money":            interfaces.DataType_Float,

	// decimal
	"numeric": interfaces.DataType_Decimal,
	"decimal": interfaces.DataType_Decimal,

	// String types.
	"varchar":           interfaces.DataType_String,
	"bpchar":            interfaces.DataType_String,
	"name":              interfaces.DataType_String,
	"uuid":              interfaces.DataType_String,
	"char":              interfaces.DataType_String,
	"character":         interfaces.DataType_String,
	"interval":          interfaces.DataType_String,
	"character varying": interfaces.DataType_String,
	"inet":              interfaces.DataType_Ip,
	"cidr":              interfaces.DataType_Ip,

	// Text
	"text": interfaces.DataType_Text,

	// Date and Time
	"date":                        interfaces.DataType_Date,
	"time":                        interfaces.DataType_Time,
	"timetz":                      interfaces.DataType_Time,
	"time without time zone":      interfaces.DataType_Time,
	"time with time zone":         interfaces.DataType_Time,
	"timestamp":                   interfaces.DataType_Timestamp,
	"timestamptz":                 interfaces.DataType_Timestamp,
	"smalldatetime":               interfaces.DataType_Timestamp,
	"timestamp without time zone": interfaces.DataType_Timestamp,
	"timestamp with time zone":    interfaces.DataType_Timestamp,

	// Bull
	"bool":    interfaces.DataType_Boolean,
	"boolean": interfaces.DataType_Boolean,

	// Binary
	"bytea": interfaces.DataType_Binary,

	// JSON
	"json":  interfaces.DataType_Json,
	"jsonb": interfaces.DataType_Json,
}

// MapType maps to VEGA type based on data_type of information_schema.
func (c *PostgresqlConnector) MapType(dataType string) string {
	d := strings.ToLower(strings.TrimSpace(dataType))
	if vegaType, ok := TypeMapping[d]; ok {
		return vegaType
	}
	return interfaces.DataType_Other
}
