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

func TestJSONNumbersEqual(t *testing.T) {
	for _, tc := range []struct {
		name        string
		left, right any
		equal       bool
	}{
		{"numeric representations", map[string]any{"value": json.Number("1.0")}, map[string]any{"value": float64(1)}, true},
		{"large exact integer", json.Number("9007199254740993"), json.Number("9007199254740992"), false},
		{"exact decimal", json.Number("9007199254740993.123456789"), json.Number("9007199254740993.123456788"), false},
		{"large equivalent positive exponents", json.Number("1e1000002"), json.Number("10e1000001"), true},
		{"large equivalent negative exponents", json.Number("-100e-1000004"), json.Number("-1e-1000002"), true},
		{"equivalent exponents beyond int64", json.Number("1e9223372036854775808"), json.Number("10e9223372036854775807"), true},
		{"distinct large exponents", json.Number("1e1000002"), json.Number("1e1000003"), false},
		{"zero with a large exponent", json.Number("-0.0e9223372036854775808"), 0, true},
		{"text is not numeric", "1", json.Number("1"), false},
		{"null remains null", map[string]any{"value": nil}, map[string]any{"value": nil}, true},
		{"array order remains significant", []int{1, 2}, []int{2, 1}, false},
		{"unencodable value", make(chan int), nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.equal, JSONNumbersEqual(tc.left, tc.right)) })
	}
	input := map[string]any{"value": json.Number("1.0")}
	assert.True(t, JSONNumbersEqual(input, map[string]any{"value": 1}))
	assert.Equal(t, json.Number("1.0"), input["value"], "comparison must preserve caller-owned JSON")
}

func TestDecodeJSONPreservesLargeInteger(t *testing.T) {
	var target map[string]any
	if err := DecodeJSON(strings.NewReader(`{"value":110101199001152345}`), &target); err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}
	if got, ok := target["value"].(json.Number); !ok || got.String() != "110101199001152345" {
		t.Fatalf("value = %#v, want json.Number preserving literal", target["value"])
	}
}

func TestJSONValidatesStringSyntax(t *testing.T) {
	for _, tc := range []struct {
		name   string
		decode func(string, any) error
	}{
		{"reader", func(input string, target any) error { return DecodeJSON(strings.NewReader(input), target) }},
		{"bytes", func(input string, target any) error { return UnmarshalJSON([]byte(input), target) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var invalid map[string]any
			require.Error(t, tc.decode("{\"name\":\"night\tly\"}", &invalid))
			var valid map[string]any
			require.NoError(t, tc.decode(`{"name":"night\tly","value":9007199254740993}`, &valid))
			assert.Equal(t, "night\tly", valid["name"])
			assert.Equal(t, json.Number("9007199254740993"), valid["value"])
		})
	}
}

func TestJSONNumberConversions(t *testing.T) {
	for _, tc := range []struct {
		literal json.Number
		want    int64
	}{
		{"30", 30},
		{"9007199254740993", 9007199254740993},
		{"9223372036854775807", math.MaxInt64},
		{"-9223372036854775808", math.MinInt64},
		{"-0", 0},
	} {
		t.Run(string(tc.literal), func(t *testing.T) {
			got, ok := NumberAsInt64(tc.literal)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}
	for _, literal := range []json.Number{
		"30.0", "30.000", "-0.0", "9223372036854775807.0", "-9223372036854775808.0",
		"3e1", "300e-1", "30.0E0", "0e0", "42.5", "30.000000000000000001",
		"1e-1000002", "1e1000002", "9223372036854775808", "-9223372036854775809", "30.", "030.0", "invalid",
	} {
		t.Run(string(literal), func(t *testing.T) {
			got, ok := NumberAsInt64(literal)
			assert.False(t, ok)
			assert.Zero(t, got)
		})
	}
	floating, ok := NumberAsFloat64(json.Number("0.25"))
	if !ok || floating != 0.25 {
		t.Fatalf("NumberAsFloat64() = %v, %v", floating, ok)
	}
	if _, ok = NumberAsFloat64(json.Number("invalid")); ok {
		t.Fatal("NumberAsFloat64() accepted an invalid number")
	}
	if _, ok := NumberAsInt64(math.Inf(1)); ok {
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
				require.NoError(t, UnmarshalJSON([]byte(got.(string)), &actual))
				require.NoError(t, UnmarshalJSON([]byte(tc.want.(string)), &expected))
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

func TestDecodeMapToStruct(t *testing.T) {
	type fixtureOptions struct {
		Timeout int `mapstructure:"timeout"`
	}
	type fixtureConfig struct {
		Label     string         `mapstructure:"label"`
		Port      int            `mapstructure:"port"`
		Options   fixtureOptions `mapstructure:"options"`
		Variables map[string]any `mapstructure:"variables"`
	}

	t.Run("only exact fixed fields decode and dynamic names remain distinct", func(t *testing.T) {
		input := map[string]any{
			"label": "canonical", "Label": map[string]any{"ignored": true},
			"port": 443, "PORT": map[string]any{"ignored": true},
			"options":   map[string]any{"timeout": 30, "Timeout": "ignored"},
			"variables": map[string]any{"AppID": "UpperValue", "appid": "lowerValue"},
			"Unknown":   "ignored",
		}
		var result fixtureConfig
		require.NoError(t, DecodeMapToStruct(input, &result))
		assert.Equal(t, "canonical", result.Label)
		assert.Equal(t, 443, result.Port)
		assert.Equal(t, 30, result.Options.Timeout)
		assert.Equal(t, map[string]any{"AppID": "UpperValue", "appid": "lowerValue"}, result.Variables)
		assert.Contains(t, input, "Label")
		assert.Contains(t, input["options"], "Timeout")
	})

	t.Run("case variants alone cannot populate fixed fields", func(t *testing.T) {
		result := fixtureConfig{Label: "default", Options: fixtureOptions{Timeout: 30}}
		require.NoError(t, DecodeMapToStruct(map[string]any{
			"Label": "ignored", "PORT": 443,
			"options":   map[string]any{"Timeout": 5},
			"Variables": map[string]any{"AppID": "ignored"},
		}, &result))
		assert.Equal(t, "default", result.Label)
		assert.Zero(t, result.Port)
		assert.Equal(t, 30, result.Options.Timeout)
		assert.Nil(t, result.Variables)
	})

	t.Run("each call binds its own output", func(t *testing.T) {
		var first, second fixtureConfig
		require.NoError(t, DecodeMapToStruct(map[string]any{"label": "first"}, &first))
		require.NoError(t, DecodeMapToStruct(map[string]any{"label": "second"}, &second))
		assert.Equal(t, "first", first.Label)
		assert.Equal(t, "second", second.Label)
	})

	t.Run("string numbers are not weakly converted", func(t *testing.T) {
		var result fixtureConfig
		input := map[string]any{"port": "443"}
		require.Error(t, DecodeMapToStruct(input, &result))
		assert.Zero(t, result.Port)
		assert.Equal(t, "443", input["port"])
		require.NoError(t, DecodeMapToStruct(map[string]any{"port": 443}, &result))
		assert.Equal(t, 443, result.Port)
	})

	t.Run("invalid output is rejected", func(t *testing.T) {
		require.Error(t, DecodeMapToStruct(map[string]any{}, fixtureConfig{}))
	})
}
