// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Convert a string to []string.
func StringToStringSlice(str string) []string {
	if str == "" {
		return []string{}
	}

	strSlice := []string{}
	strs := strings.Split(str, ",")
	for _, v := range strs {
		v = strings.Trim(v, " ")
		if v != "" {
			strSlice = append(strSlice, v)
		}
	}
	return strSlice
}

const (
	oneGiB = 1024 * 1024 * 1024 // 1073741824.0 bytes in one GiB.
)

func BytesToGiB(bytes int64) float64 {
	return math.Round(float64(bytes)/oneGiB*100) / 100 // Round to two decimal places.
}

func GiBToBytes(gib int64) int64 {
	return gib * oneGiB
}

// AnyToFloat64 converts supported numeric values to float64.
func AnyToFloat64(value any) (float64, error) {
	switch v := value.(type) {
	case json.Number:
		return v.Float64()
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int8:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return 0, fmt.Errorf("cannot convert type %T to float64", value)
	}
}

// Deduplicate a string slice.
func DuplicateSlice(strSlice []string) []string {
	keys := make(map[string]struct{})
	list := make([]string, 0, len(strSlice))

	for _, item := range strSlice {
		if _, ok := keys[item]; !ok {
			keys[item] = struct{}{}
			list = append(list, item)
		}
	}
	return list
}
