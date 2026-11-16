// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"encoding/json"
	"math"
	"testing"
)

func TestActionValueTypesBeforeInvocation(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		value any
		valid bool
	}{
		{"integer", float64(2), true}, {"integer", 2.5, false}, {"integer", "2", false},
		{"integer", json.Number("9223372036854775807"), true}, {"integer", float64(9007199254740992), false},
		{"boolean", false, true}, {"boolean", "false", false}, {"string", "", true}, {"string", json.Number("123"), false},
		{"number", math.Inf(1), false}, {"array", []any{1, 2}, true}, {"array", "[1,2]", false},
		{"object", map[string]any{"id": "1"}, true}, {"object", "{}", false},
	} {
		err := ValidateActionValue(tc.kind, tc.value)
		if (err == nil) != tc.valid {
			t.Errorf("ValidateActionValue(%s, %T) = %v", tc.kind, tc.value, err)
		}
	}
}

func TestNormalizeActionConstantForStoredStringValues(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{json.Number("123"), "123"},
		{true, "true"},
		{nil, "null"},
		{[]any{json.Number("1")}, "[1]"},
		{map[string]any{"a": json.Number("1")}, `{"a":1}`},
	} {
		got := NormalizeActionConstant("string", tc.value)
		if got != tc.want {
			t.Errorf("NormalizeActionConstant(%T) = %v, want %q", tc.value, got, tc.want)
		}
	}
	if got := NormalizeActionConstant("integer", json.Number("123")); got != json.Number("123") {
		t.Fatalf("numeric constant changed: %v", got)
	}
}
