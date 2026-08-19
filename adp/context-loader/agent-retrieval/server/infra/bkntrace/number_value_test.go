// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bkntrace

import (
	"encoding/json"
	"testing"
)

// Instance payloads are decoded with UseNumber (see drivenadapters.precisionJSON),
// so every number in them arrives as json.Number rather than float64.
func TestScoreBucketReadsJSONNumber(t *testing.T) {
	cases := []struct {
		item map[string]any
		want string
	}{
		{map[string]any{"_score": json.Number("0.91")}, "high"},
		{map[string]any{"_score": json.Number("0.5")}, "medium"},
		{map[string]any{"_score": json.Number("0.1")}, "low"},
		{map[string]any{"score": json.Number("0.85")}, "high"},
		{map[string]any{"_score": 0.91}, "high"},
		{map[string]any{"_score": "n/a"}, "unknown"},
		{map[string]any{}, "unknown"},
	}
	for _, c := range cases {
		if got := scoreBucket(c.item); got != c.want {
			t.Errorf("scoreBucket(%v) = %s, want %s", c.item, got, c.want)
		}
	}
}

func TestIntValueReadsJSONNumber(t *testing.T) {
	cases := []struct {
		value    any
		fallback int
		want     int
	}{
		{json.Number("3"), 1, 3},
		{json.Number("9223372036854775807"), 1, 9223372036854775807},
		{json.Number("1.5"), 7, 7},
		{json.Number("not-a-number"), 7, 7},
		{float64(3), 1, 3},
		{4, 1, 4},
		{nil, 1, 1},
	}
	for _, c := range cases {
		if got := intValue(c.value, c.fallback); got != c.want {
			t.Errorf("intValue(%#v, %d) = %d, want %d", c.value, c.fallback, got, c.want)
		}
	}
}
