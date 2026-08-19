// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knrerank

import (
	"encoding/json"
	"testing"
)

// Intent payloads are decoded with UseNumber (see drivenadapters.precisionJSON),
// so the confidence arrives as json.Number and used to be dropped from the prompt.
func TestConfidenceValueReadsJSONNumber(t *testing.T) {
	cases := []struct {
		value  any
		want   float64
		wantOK bool
	}{
		{json.Number("0.9"), 0.9, true},
		{json.Number("1"), 1, true},
		{json.Number("nope"), 0, false},
		{float64(0.9), 0.9, true},
		{"0.9", 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		got, ok := confidenceValue(c.value)
		if ok != c.wantOK || got != c.want {
			t.Errorf("confidenceValue(%#v) = (%v, %v), want (%v, %v)", c.value, got, ok, c.want, c.wantOK)
		}
	}
}
