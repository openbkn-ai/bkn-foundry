// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knfindskills

import (
	"encoding/json"
	"testing"
)

// Recall payloads are decoded with UseNumber (see drivenadapters.precisionJSON),
// so scores arrive as json.Number and used to fall through to the zero value.
func TestFloat64FromMapReadsJSONNumber(t *testing.T) {
	cases := []struct {
		value any
		want  float64
	}{
		{json.Number("0.75"), 0.75},
		{json.Number("3"), 3},
		{json.Number("nope"), 0},
		{float64(0.75), 0.75},
		{int(3), 3},
		{int64(3), 3},
		{"0.75", 0},
	}
	for _, c := range cases {
		if got := float64FromMap(map[string]interface{}{"score": c.value}, "score"); got != c.want {
			t.Errorf("float64FromMap(%#v) = %v, want %v", c.value, got, c.want)
		}
	}
	if got := float64FromMap(map[string]interface{}{}, "score"); got != 0 {
		t.Errorf("missing key = %v, want 0", got)
	}
}
