// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

// TypeKind 表示计算类型；响应中的类型名称及编码不决定计算语义。
type TypeKind string

const (
	TypeNull          TypeKind = "null"
	TypeBoolean       TypeKind = "boolean"
	TypeInteger       TypeKind = "integer"
	TypeFloat         TypeKind = "float"
	TypeDecimal       TypeKind = "decimal"
	TypeString        TypeKind = "string"
	TypeText          TypeKind = "text"
	TypeDate          TypeKind = "date"
	TypeLocalDateTime TypeKind = "local_datetime"
	TypeInstant       TypeKind = "instant"
	TypeTimeOfDay     TypeKind = "time_of_day"
	TypeDuration      TypeKind = "duration"
	TypeBinary        TypeKind = "binary"
	TypeJSON          TypeKind = "json"
	TypeVector        TypeKind = "vector"
	TypeOther         TypeKind = "other"
)

type TimeZoneCategory string

const (
	TimeZoneNone    TimeZoneCategory = "none"
	TimeZoneLocal   TimeZoneCategory = "local"
	TimeZoneInstant TimeZoneCategory = "instant"
)

// LogicalType 不包含可空性。TimePrecision=nil 表示未知，不能当作秒精度。
// Precision/Scale 用于 decimal，BitWidth/Unsigned 用于数值，VectorDimensions 用于向量。
type LogicalType struct {
	Kind             TypeKind
	BitWidth         int
	Unsigned         bool
	Precision        int
	Scale            int
	TimePrecision    *int
	TimeZone         TimeZoneCategory
	VectorDimensions int
}

// SemanticDomain 区分已明确绑定的 Vega、源端原生及索引比较语义。
type SemanticDomain string

const (
	DomainVega          SemanticDomain = "vega"
	DomainNativeNumeric SemanticDomain = "native_numeric"
	DomainIndexKeyword  SemanticDomain = "index_keyword"
)

// NativeType 保留原生类型身份，不能凭对外 string 类型丢失 LARGEINT 等语义。
type NativeType struct {
	ConnectorType string
	Name          string
	Domain        SemanticDomain
	Precision     *int
	Scale         *int
	TimePrecision *int
	TimeZone      TimeZoneCategory
}

// SemanticRequirement 只保存受控语义设置及其值，不包含 SQL 或连接凭据。
// 例如 Doris enable_strict_cast 的已确定设置；兑现责任属于目标准备阶段。
type SemanticRequirement struct {
	Name  string
	Value string
}
