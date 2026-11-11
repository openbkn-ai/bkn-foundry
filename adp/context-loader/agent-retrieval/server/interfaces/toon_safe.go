// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"encoding/json"
	"math/big"
)

var maxSafeTOONInteger = big.NewInt(9007199254740991)

// TOONSafeValue is toonSafeValue for callers outside this package: the response
// formatter applies it to every TOON body, so responses whose dynamic data is
// plain map/slice do not each need a TOONValue method.
func TOONSafeValue(value any) (any, bool) {
	return toonSafeValue(value)
}

// toonSafeValue copies only branches that contain integer json.Number values
// outside the IEEE 754 safe range. toon-go currently normalizes json.Number
// through float64 (internal/codec.normalizeNumberString), so those values must
// be rendered as strings.
func toonSafeValue(value any) (any, bool) {
	switch v := value.(type) {
	case json.Number:
		integer, ok := new(big.Int).SetString(v.String(), 10)
		if ok && new(big.Int).Abs(integer).Cmp(maxSafeTOONInteger) > 0 {
			return v.String(), true
		}
		return v, false
	case []any:
		var clone []any
		for i, item := range v {
			converted, changed := toonSafeValue(item)
			if !changed {
				continue
			}
			if clone == nil {
				clone = make([]any, len(v))
				copy(clone, v)
			}
			clone[i] = converted
		}
		if clone != nil {
			return clone, true
		}
	case map[string]any:
		var clone map[string]any
		for key, item := range v {
			converted, changed := toonSafeValue(item)
			if !changed {
				continue
			}
			if clone == nil {
				clone = make(map[string]any, len(v))
				for originalKey, originalValue := range v {
					clone[originalKey] = originalValue
				}
			}
			clone[key] = converted
		}
		if clone != nil {
			return clone, true
		}
	}
	return value, false
}
