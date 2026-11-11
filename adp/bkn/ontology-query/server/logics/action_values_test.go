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
		{"boolean", false, true}, {"boolean", "false", false}, {"string", "", true},
		{"number", math.Inf(1), false}, {"array", []any{1, 2}, true}, {"array", "[1,2]", false},
		{"object", map[string]any{"id": "1"}, true}, {"object", "{}", false},
	} {
		err := ValidateActionValue(tc.kind, tc.value)
		if (err == nil) != tc.valid {
			t.Errorf("ValidateActionValue(%s, %T) = %v", tc.kind, tc.value, err)
		}
	}
}
