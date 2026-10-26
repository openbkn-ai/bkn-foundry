// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

import "time"

const (
	NativeValueText   NativeValueEncoding = "text"
	NativeValueJSON   NativeValueEncoding = "json"
	NativeValueBinary NativeValueEncoding = "binary"
)

// Value 是带计算类型的值。Data 只能来自本包定义的联合类型分支；
// NULL 必须使用 NullValue，nil Data 表示未构造的非法值。
type Value struct {
	Type LogicalType
	Data ValueData
}

type ValueData interface{ queryValue() }

type NullValue struct{}

type BooleanValue bool

// IntegerValue、DecimalValue 使用精确十进制文本，不经过 float64。
type IntegerValue string

type DecimalValue string

type FloatValue float64

type StringValue string

type BinaryValue []byte

// JSONValue 保留原始 JSON 文本。外部源的 JSON 字符串不在读取时强制解析；
// Dataset 的文档类型与外部源展示策略由 OutputEncoding 区分。
type JSONValue string

type VectorValue []float64

type DateValue struct {
	Year  int
	Month time.Month
	Day   int
}

type TimeOfDayValue struct {
	Hour       int
	Minute     int
	Second     int
	Nanosecond int
}

type LocalDateTimeValue struct {
	Date DateValue
	Time TimeOfDayValue
}

type InstantValue struct{ Time time.Time }

// DurationValue 保存整数纳秒的精确十进制表示，允许负值和超 24 小时时长。
// 日历月/年不能未经明确语义转换写入此类型。
type DurationValue string

type NativeValueEncoding string

// OtherValue 仅承载受限原生投影值；可展示不代表可参与比较、聚合或交换计算。
type OtherValue struct {
	Encoding NativeValueEncoding
	Data     []byte
}

func (NullValue) queryValue() {}

func (BooleanValue) queryValue() {}

func (IntegerValue) queryValue() {}

func (DecimalValue) queryValue() {}

func (FloatValue) queryValue() {}

func (StringValue) queryValue() {}

func (BinaryValue) queryValue() {}

func (JSONValue) queryValue() {}

func (VectorValue) queryValue() {}

func (DateValue) queryValue() {}

func (TimeOfDayValue) queryValue() {}

func (LocalDateTimeValue) queryValue() {}

func (InstantValue) queryValue() {}

func (DurationValue) queryValue() {}

func (OtherValue) queryValue() {}
