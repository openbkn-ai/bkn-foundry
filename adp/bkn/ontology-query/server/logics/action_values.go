// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// ValidateActionValue checks the declared scalar/container type before external
// invocation. It does not coerce arbitrary strings into numbers or booleans.
func ValidateActionValue(declared string, value any) error {
	if value == nil || declared == "" || declared == "any" {
		return nil
	}
	valid := false
	kind := reflect.TypeOf(value).Kind()
	switch strings.ToLower(declared) {
	case "string", "text", "keyword", "date", "datetime":
		valid = kind == reflect.String
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
		return fmt.Errorf("value does not match declared type %q", declared)
	}
	return nil
}
