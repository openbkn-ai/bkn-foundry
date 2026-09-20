// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const (
	// Integer type
	DataType_Integer         = "integer"
	DataType_UnsignedInteger = "unsigned integer"

	// Floating-point type
	DataType_Float = "float"

	// Arbitrary precision number
	DataType_Decimal = "decimal"

	// String type
	DataType_String = "string"
	DataType_Text   = "text"

	// Time type
	DataType_Date      = "date"
	DataType_Time      = "time"
	DataType_Datetime  = "datetime"
	DataType_Timestamp = "timestamp"

	// IP address type
	DataType_Ip = "ip"

	// Boolean type
	DataType_Boolean = "boolean"

	// Binary data type
	DataType_Binary = "binary"

	// json type
	DataType_Json = "json"

	// Spatial type
	DataType_Point = "point"
	DataType_Shape = "shape"

	// Vector type
	DataType_Vector = "vector"

	// Other types
	DataType_Other = "other"
)

var (
	STRING_TYPES = map[string]struct{}{
		DataType_String: {},
		DataType_Text:   {},
	}

	NUMBER_TYPES = map[string]struct{}{
		DataType_Integer:         {},
		DataType_UnsignedInteger: {},
		DataType_Float:           {},
		DataType_Decimal:         {},
	}

	DATE_TYPES = map[string]struct{}{
		DataType_Date:      {},
		DataType_Time:      {},
		DataType_Datetime:  {},
		DataType_Timestamp: {},
	}
)

func DataType_IsString(t string) bool {
	_, ok := STRING_TYPES[t]
	return ok
}

func DataType_IsNumber(t string) bool {
	_, ok := NUMBER_TYPES[t]
	return ok
}

func DataType_IsDate(t string) bool {
	_, ok := DATE_TYPES[t]
	return ok
}
