// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"encoding/json"
	"encoding/json/jsontext"
	"io"
	"math"
	"math/big"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin/binding"
	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
)

type normalizedJSONNumber struct {
	significand string
	exponent    string
}

// DecodeMapToStruct 精确匹配结构体字段名，并为每次解码创建绑定到独立结果的 Decoder。
// 动态 map 的成员名和值保持不变，未知结构体字段沿用默认忽略行为。
func DecodeMapToStruct(input, result any) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:    result,
		MatchName: exactMapField,
	})
	if err != nil {
		return err
	}
	return decoder.Decode(input)
}

func exactMapField(mapKey, fieldName string) bool {
	return mapKey == fieldName
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
		encoded, err := jsoncodec.Marshal(value)
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
// JSON numbers must use integer syntax without a fraction or exponent.
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
		if integer, err := number.Int64(); err == nil {
			return integer, true
		}
		return 0, false
	}
	floating, ok := NumberAsFloat64(value)
	if !ok || floating < math.MinInt64 || floating >= float64(math.MaxInt64) || math.Trunc(floating) != floating {
		return 0, false
	}
	return int64(floating), true
}

// BindJSON decodes an HTTP request body and applies Gin's validator.
func BindJSON(reader io.Reader, target any) error {
	if err := jsoncodec.UnmarshalDecode(jsontext.NewDecoder(reader), target); err != nil {
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

// JSONNumbersEqual compares JSON v2 values without treating different numeric
// representations of the same number as a schema change.
func JSONNumbersEqual(current, next any) bool {
	currentValue, err := comparableJSONValue(current)
	if err != nil {
		return false
	}
	nextValue, err := comparableJSONValue(next)
	return err == nil && reflect.DeepEqual(currentValue, nextValue)
}

func comparableJSONValue(value any) (any, error) {
	encoded, err := jsoncodec.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := jsoncodec.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return normalizeJSONNumbers(decoded), nil
}

func normalizeJSONNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		if number, ok := normalizeJSONNumber(v); ok {
			return number
		}
		return v
	case []any:
		for i := range v {
			v[i] = normalizeJSONNumbers(v[i])
		}
	case map[string]any:
		for key := range v {
			v[key] = normalizeJSONNumbers(v[key])
		}
	}
	return value
}

// normalizeJSONNumber represents a number as a signed significand times a
// power of ten without constructing the power or expanding the significand.
func normalizeJSONNumber(number json.Number) (normalizedJSONNumber, bool) {
	text := string(number)
	if text == "" || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) || !json.Valid([]byte(text)) {
		return normalizedJSONNumber{}, false
	}
	negative := text[0] == '-'
	text = strings.TrimPrefix(text, "-")
	exponentText := "0"
	if position := strings.IndexAny(text, "eE"); position >= 0 {
		text, exponentText = text[:position], text[position+1:]
	}
	whole, fraction, _ := strings.Cut(text, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return normalizedJSONNumber{significand: "0", exponent: "0"}, true
	}
	significand := strings.TrimRight(digits, "0")
	var exponent big.Int
	if _, ok := exponent.SetString(exponentText, 10); !ok {
		return normalizedJSONNumber{}, false
	}
	exponent.Add(&exponent, big.NewInt(int64(len(digits)-len(significand)-len(fraction))))
	if negative {
		significand = "-" + significand
	}
	return normalizedJSONNumber{
		significand: significand,
		exponent:    exponent.String(),
	}, true
}
