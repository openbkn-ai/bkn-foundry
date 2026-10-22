// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"encoding/json"
	"io"
	"math"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin/binding"
)

var precisionJSON = sonic.Config{UseNumber: true}.Froze()

// DecodePreciseJSON preserves number literals in dynamic JSON fields.
func DecodePreciseJSON(reader io.Reader, target any) error {
	return precisionJSON.NewDecoder(reader).Decode(target)
}

// UnmarshalPreciseJSON preserves number literals in byte-backed dynamic JSON.
func UnmarshalPreciseJSON(data []byte, target any) error {
	return precisionJSON.Unmarshal(data, target)
}

// JSONSourceValueText 归一化数据库返回的完整 JSON 文本中的顶层 null。
// 不用于已解码的字符串值；带引号的 JSON 字符串和空字符串保持为内容。
func JSONSourceValueText(value any) any {
	if raw, ok := value.([]byte); ok {
		value = string(raw)
	}
	if text, ok := value.(string); ok && strings.TrimSpace(text) == "null" {
		return nil
	}
	return value
}

// JSONQueryValueText 保留文本和空值，仅将协议已解码的 JSON 值序列化为文本。
// 不解析文本内容；不启用 HTML 转义，避免改写源字段的字符表示。
func JSONQueryValueText(value any) (any, error) {
	switch value := value.(type) {
	case nil, string:
		return value, nil
	case []byte:
		return string(value), nil
	default:
		encoded, err := sonic.Marshal(value)
		if err != nil {
			return nil, err
		}
		return string(encoded), nil
	}
}

// NumberAsFloat64 converts JSON and Go numeric values to float64.
func NumberAsFloat64(value any) (float64, bool) {
	switch number := value.(type) {
	case json.Number:
		converted, err := number.Float64()
		return converted, err == nil && !math.IsNaN(converted) && !math.IsInf(converted, 0)
	case float64:
		return number, !math.IsNaN(number) && !math.IsInf(number, 0)
	case float32:
		converted := float64(number)
		return converted, !math.IsNaN(converted) && !math.IsInf(converted, 0)
	case int:
		return float64(number), true
	case int8:
		return float64(number), true
	case int16:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint:
		return float64(number), true
	case uint8:
		return float64(number), true
	case uint16:
		return float64(number), true
	case uint32:
		return float64(number), true
	case uint64:
		return float64(number), true
	default:
		return 0, false
	}
}

// NumberAsInt64 converts JSON and Go numeric values that represent an integer.
func NumberAsInt64(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int8:
		return int64(number), true
	case int16:
		return int64(number), true
	case int32:
		return int64(number), true
	case int64:
		return number, true
	case uint:
		if uint64(number) <= math.MaxInt64 {
			return int64(number), true
		}
		return 0, false
	case uint8:
		return int64(number), true
	case uint16:
		return int64(number), true
	case uint32:
		return int64(number), true
	case uint64:
		if number <= math.MaxInt64 {
			return int64(number), true
		}
		return 0, false
	case json.Number:
		if converted, err := number.Int64(); err == nil {
			return converted, true
		}
	}
	floating, ok := NumberAsFloat64(value)
	if !ok || floating < math.MinInt64 || floating >= float64(math.MaxInt64) || math.Trunc(floating) != floating {
		return 0, false
	}
	return int64(floating), true
}

// BindPreciseJSON decodes an HTTP request body and applies Gin's validator.
func BindPreciseJSON(reader io.Reader, target any) error {
	if err := DecodePreciseJSON(reader, target); err != nil {
		return err
	}
	return binding.Validator.ValidateStruct(target)
}
