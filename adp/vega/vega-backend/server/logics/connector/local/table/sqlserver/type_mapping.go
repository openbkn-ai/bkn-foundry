// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package sqlserver

import (
	"strings"

	"vega-backend/interfaces"
)

// TypeMapping maps SQL Server native types to VEGA unified data types.
var TypeMapping = map[string]string{
	// Integer types
	"tinyint":  interfaces.DataType_Integer,
	"smallint": interfaces.DataType_Integer,
	"int":      interfaces.DataType_Integer,
	"bigint":   interfaces.DataType_Integer,

	// Decimal types
	"decimal":    interfaces.DataType_Decimal,
	"numeric":    interfaces.DataType_Decimal,
	"money":      interfaces.DataType_Decimal,
	"smallmoney": interfaces.DataType_Decimal,

	// Floating-point types
	"real":  interfaces.DataType_Float,
	"float": interfaces.DataType_Float,

	// String types
	"char":             interfaces.DataType_String,
	"varchar":          interfaces.DataType_String,
	"nchar":            interfaces.DataType_String,
	"nvarchar":         interfaces.DataType_String,
	"uniqueidentifier": interfaces.DataType_String,

	// Text types
	"text":  interfaces.DataType_Text,
	"ntext": interfaces.DataType_Text,
	"xml":   interfaces.DataType_Text,

	// Date/time types. SQL Server timestamp is deliberately excluded here: it
	// is a rowversion synonym rather than a temporal type.
	"date":           interfaces.DataType_Date,
	"time":           interfaces.DataType_Time,
	"datetime":       interfaces.DataType_Datetime,
	"datetime2":      interfaces.DataType_Datetime,
	"smalldatetime":  interfaces.DataType_Datetime,
	"datetimeoffset": interfaces.DataType_Datetime,

	// Boolean type
	"bit": interfaces.DataType_Boolean,

	// Binary types
	"binary":    interfaces.DataType_Binary,
	"varbinary": interfaces.DataType_Binary,
	"image":     interfaces.DataType_Binary,
	// rowversion is an automatically generated 8-byte row version value, not a date/time value.
	"rowversion": interfaces.DataType_Binary,
	// timestamp is the deprecated SQL Server synonym for rowversion, not a temporal timestamp.
	"timestamp": interfaces.DataType_Binary,
}

// MapType maps a SQL Server native type to the VEGA unified data type.
func (c *SQLServerConnector) MapType(nativeType string) string {
	normalizedType := strings.ToLower(strings.TrimSpace(nativeType))
	if vegaType, ok := TypeMapping[normalizedType]; ok {
		return vegaType
	}
	return interfaces.DataType_Other
}
