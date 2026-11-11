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

	// 超出 64 位的整数，以十进制字符串无损传输。
	DataType_LargeInteger = "large_integer"

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
		DataType_LargeInteger:    {},
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

// DataType_HasDatePart 判断类型是否包含日历日期部分，不包含仅表示日内时间的 time。
func DataType_HasDatePart(t string) bool {
	switch t {
	case DataType_Date, DataType_Datetime, DataType_Timestamp:
		return true
	default:
		return false
	}
}

// legacyTypeFilterable 为历史字段和手动创建时缺失开关提供统一类型兼容值。
// 扫描默认值由各连接器的 type_mapping.go 负责，不调用此兼容函数。
func legacyTypeFilterable(dataType string) bool {
	switch dataType {
	case DataType_Integer, DataType_UnsignedInteger, DataType_LargeInteger,
		DataType_Float, DataType_Decimal, DataType_String, DataType_Text,
		DataType_Date, DataType_Time, DataType_Datetime, DataType_Timestamp,
		DataType_Ip, DataType_Boolean, DataType_Vector:
		return true
	default:
		return false
	}
}

// EffectiveFilterable resolves historical omission without mutating the schema.
func (p *Property) EffectiveFilterable() bool {
	if p.Filterable != nil {
		return *p.Filterable
	}
	return legacyTypeFilterable(p.Type)
}

// SetDefaultFilterable fills only an omitted switch, preserving user settings.
func (p *Property) SetDefaultFilterable() {
	if p.Filterable == nil {
		value := legacyTypeFilterable(p.Type)
		p.Filterable = &value
	}
}
