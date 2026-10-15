// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"regexp"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// TypeMapping maps normalized MariaDB and MySQL type names to VEGA types.
var TypeMapping = map[string]string{
	// Signed integer types.
	"tinyint":   interfaces.DataType_Integer,
	"smallint":  interfaces.DataType_Integer,
	"mediumint": interfaces.DataType_Integer,
	"int":       interfaces.DataType_Integer,
	"integer":   interfaces.DataType_Integer,
	"bigint":    interfaces.DataType_Integer,
	"year":      interfaces.DataType_Integer,

	// Unsigned integer types.
	"tinyint unsigned":   interfaces.DataType_UnsignedInteger,
	"smallint unsigned":  interfaces.DataType_UnsignedInteger,
	"mediumint unsigned": interfaces.DataType_UnsignedInteger,
	"int unsigned":       interfaces.DataType_UnsignedInteger,
	"integer unsigned":   interfaces.DataType_UnsignedInteger,
	"bigint unsigned":    interfaces.DataType_UnsignedInteger,

	// Floating-point types.
	"float":                     interfaces.DataType_Float,
	"double":                    interfaces.DataType_Float,
	"real":                      interfaces.DataType_Float,
	"double precision":          interfaces.DataType_Float,
	"float unsigned":            interfaces.DataType_Float,
	"double unsigned":           interfaces.DataType_Float,
	"real unsigned":             interfaces.DataType_Float,
	"double precision unsigned": interfaces.DataType_Float,

	// Exact numeric types.
	"decimal":          interfaces.DataType_Decimal,
	"numeric":          interfaces.DataType_Decimal,
	"fixed":            interfaces.DataType_Decimal,
	"dec":              interfaces.DataType_Decimal,
	"decimal unsigned": interfaces.DataType_Decimal,
	"numeric unsigned": interfaces.DataType_Decimal,
	"fixed unsigned":   interfaces.DataType_Decimal,
	"dec unsigned":     interfaces.DataType_Decimal,

	// String types.
	"char":    interfaces.DataType_String,
	"varchar": interfaces.DataType_String,
	"enum":    interfaces.DataType_String,
	"set":     interfaces.DataType_String,

	// Text types.
	"tinytext":   interfaces.DataType_Text,
	"text":       interfaces.DataType_Text,
	"mediumtext": interfaces.DataType_Text,
	"longtext":   interfaces.DataType_Text,

	// Date and time types.
	"date":      interfaces.DataType_Date,
	"datetime":  interfaces.DataType_Datetime,
	"timestamp": interfaces.DataType_Timestamp,
	"time":      interfaces.DataType_Time,

	// Boolean types.
	"boolean": interfaces.DataType_Boolean,
	"bool":    interfaces.DataType_Boolean,
	"bit":     interfaces.DataType_Boolean,

	// Binary types.
	"binary":     interfaces.DataType_Binary,
	"varbinary":  interfaces.DataType_Binary,
	"tinyblob":   interfaces.DataType_Binary,
	"blob":       interfaces.DataType_Binary,
	"mediumblob": interfaces.DataType_Binary,
	"longblob":   interfaces.DataType_Binary,

	// JSON types.
	"json": interfaces.DataType_Json,
}

var columnTypeComments = regexp.MustCompile(`(?s)/\*.*?\*/`)

// MapType maps a source COLUMN_TYPE to a VEGA type, or Other when no mapping exists.
// Parameters, comments, and trailing ZEROFILL are removed before looking up the type.
func (c *MariaDBConnector) MapType(nativeType string) string {
	normalizedType := strings.ToLower(nativeType)

	// Remove parameters without losing suffixes: int(11) unsigned becomes int unsigned.
	// ENUM/SET labels, including words such as 'unsigned', stay inside the removed list.
	if idx := strings.Index(normalizedType, "("); idx != -1 {
		end := strings.LastIndexByte(normalizedType, ')')
		if end < idx {
			return interfaces.DataType_Other
		}
		normalizedType = normalizedType[:idx] + normalizedType[end+1:]
	}

	// MariaDB exposes compression attributes as comments in COLUMN_TYPE.
	normalizedType = strings.TrimSpace(columnTypeComments.ReplaceAllString(normalizedType, ""))
	normalizedType = strings.TrimSpace(strings.TrimSuffix(normalizedType, "zerofill"))

	if vegaType, ok := TypeMapping[normalizedType]; ok {
		return vegaType
	}
	return interfaces.DataType_Other
}

// normalizeColumnType trims source type text and lowercases the name before the first parenthesis.
// Parameters and suffixes remain intact: ENUM('Active') becomes enum('Active').
func (c *MariaDBConnector) normalizeColumnType(nativeType string) string {
	normalizedType := strings.TrimSpace(nativeType)
	if idx := strings.IndexByte(normalizedType, '('); idx >= 0 {
		return strings.ToLower(normalizedType[:idx]) + normalizedType[idx:]
	}
	return strings.ToLower(normalizedType)
}
