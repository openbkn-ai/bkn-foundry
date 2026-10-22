// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodePreciseJSONPreservesLargeInteger(t *testing.T) {
	var target map[string]any
	if err := DecodePreciseJSON(strings.NewReader(`{"value":110101199001152345}`), &target); err != nil {
		t.Fatalf("DecodePreciseJSON() error = %v", err)
	}
	if got, ok := target["value"].(json.Number); !ok || got.String() != "110101199001152345" {
		t.Fatalf("value = %#v, want json.Number preserving literal", target["value"])
	}
}

func TestJSONNumberConversions(t *testing.T) {
	integer, ok := NumberAsInt64(json.Number("9007199254740993"))
	if !ok || integer != 9007199254740993 {
		t.Fatalf("NumberAsInt64() = %d, %v", integer, ok)
	}
	integer, ok = NumberAsInt64(json.Number("42.0"))
	if !ok || integer != 42 {
		t.Fatalf("NumberAsInt64(decimal integer) = %d, %v", integer, ok)
	}
	if _, ok = NumberAsInt64(json.Number("42.5")); ok {
		t.Fatal("NumberAsInt64() accepted a fractional number")
	}
	floating, ok := NumberAsFloat64(json.Number("0.25"))
	if !ok || floating != 0.25 {
		t.Fatalf("NumberAsFloat64() = %v, %v", floating, ok)
	}
	if _, ok = NumberAsFloat64(json.Number("invalid")); ok {
		t.Fatal("NumberAsFloat64() accepted an invalid number")
	}
	if _, ok = NumberAsInt64(math.Inf(1)); ok {
		t.Fatal("NumberAsInt64() accepted infinity")
	}
}

func TestJSONQueryValueText(t *testing.T) {
	for _, tc := range []struct {
		name        string
		value, want any
		invalid     bool
	}{
		{"null", nil, nil, false},
		{"decoded string null is content", "null", "null", false},
		{"empty", "", "", false},
		{"incomplete text", "{incomplete", "{incomplete", false},
		{"string content", `"scalar"`, `"scalar"`, false},
		{"bytes", []byte("<>&\n中文"), "<>&\n中文", false},
		{"large number", json.Number("9007199254740993"), "9007199254740993", false},
		{"boolean", true, "true", false},
		{"object", map[string]any{"z": map[string]any{"b": true, "a": "<>&"}, "a": json.Number("9007199254740993")}, `{"a":9007199254740993,"z":{"a":"<>&","b":true}}`, false},
		{"array", []any{map[string]any{"b": 2, "a": 1}, "<>&"}, `[{"a":1,"b":2},"<>&"]`, false},
		{"unsupported value", make(chan int), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := JSONQueryValueText(tc.value)
			if tc.invalid {
				require.Error(t, err)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			switch tc.value.(type) {
			case map[string]any, []any:
				require.IsType(t, "", got)
				var actual, expected any
				require.NoError(t, UnmarshalPreciseJSON([]byte(got.(string)), &actual))
				require.NoError(t, UnmarshalPreciseJSON([]byte(tc.want.(string)), &expected))
				assert.Equal(t, expected, actual)
				assert.Contains(t, got, "<>&")
			default:
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestJSONSourceValueText(t *testing.T) {
	for _, tc := range []struct {
		name        string
		value, want any
	}{
		{"SQL NULL", nil, nil},
		{"JSON null", "null", nil},
		{"JSON null bytes", []byte(" \nnull\t"), nil},
		{"string null", `"null"`, `"null"`},
		{"empty JSON string", `""`, `""`},
		{"empty source text", "", ""},
		{"object", []byte(`{"n":null}`), `{"n":null}`},
		{"opaque text", "incomplete", "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, JSONSourceValueText(tc.value))
		})
	}
}
