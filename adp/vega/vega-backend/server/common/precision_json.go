// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"encoding/json"
	"io"
	"math"

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
