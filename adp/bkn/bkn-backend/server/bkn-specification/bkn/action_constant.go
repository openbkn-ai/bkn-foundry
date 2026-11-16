// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bkn

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// NormalizeActionStringConstant converts a string parameter's JSON value into
// text. It also repairs values stored before BKN import respected the declared
// string type. Other parameter types are left unchanged.
func NormalizeActionStringConstant(declared string, value any) (any, error) {
	switch strings.ToLower(declared) {
	case "string", "text", "keyword", "date", "datetime":
	default:
		return value, nil
	}
	if text, ok := value.(string); ok {
		return text, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("cannot encode string constant: %w", err)
	}
	return string(encoded), nil
}

// ValidateActionConstantType checks the JSON value against the action parameter's
// declared type. Null is allowed for non-string types, as it is in the
// execution contract. Unknown tool-specific types keep their pass-through behavior.
func ValidateActionConstantType(declared string, value any) error {
	if value == nil {
		switch strings.ToLower(declared) {
		case "string", "text", "keyword", "date", "datetime":
			return fmt.Errorf("constant does not match declared type %q", declared)
		}
		return nil
	}
	kind := reflect.TypeOf(value).Kind()
	valid := false
	switch strings.ToLower(declared) {
	case "string", "text", "keyword", "date", "datetime":
		_, isNumber := value.(json.Number)
		valid = kind == reflect.String && !isNumber
	case "bool", "boolean":
		valid = kind == reflect.Bool
	case "array":
		valid = kind == reflect.Slice || kind == reflect.Array
	case "object":
		valid = kind == reflect.Map
	case "int", "int8", "int16", "int32", "int64", "integer", "long", "short", "uint", "uint64":
		if number, ok := value.(json.Number); ok {
			_, err := strconv.ParseInt(string(number), 10, 64)
			valid = err == nil
		} else if kind >= reflect.Int && kind <= reflect.Uint64 {
			valid = true
		} else if kind == reflect.Float32 || kind == reflect.Float64 {
			number := reflect.ValueOf(value).Convert(reflect.TypeOf(float64(0))).Float()
			valid = !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number && math.Abs(number) <= 9007199254740991
		}
	case "float", "float32", "float64", "double", "decimal", "number":
		if number, ok := value.(json.Number); ok {
			parsed, err := number.Float64()
			valid = err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
		} else if kind >= reflect.Int && kind <= reflect.Float64 {
			valid = true
			if kind == reflect.Float32 || kind == reflect.Float64 {
				number := reflect.ValueOf(value).Convert(reflect.TypeOf(float64(0))).Float()
				valid = !math.IsNaN(number) && !math.IsInf(number, 0)
			}
		}
	default:
		return nil
	}
	if !valid {
		return fmt.Errorf("constant does not match declared type %q", declared)
	}
	return nil
}
