// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import "testing"

// TestMappedFieldColumn covers extracting the physical column name from an
// untyped mapped_field, with fallback to the logical name.
func TestMappedFieldColumn(t *testing.T) {
	cases := []struct {
		name        string
		mappedField any
		fallback    string
		want        string
	}{
		{"map with name", map[string]any{"name": "family_name"}, "fam", "family_name"},
		{"nil mapped_field falls back", nil, "own_goal", "own_goal"},
		{"map without name falls back", map[string]any{"type": "string"}, "k", "k"},
		{"empty name falls back", map[string]any{"name": ""}, "k", "k"},
		{"non-string name falls back", map[string]any{"name": 123}, "k", "k"},
		{"non-map falls back", "not-a-map", "k", "k"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mappedFieldColumn(c.mappedField, c.fallback); got != c.want {
				t.Fatalf("mappedFieldColumn(%v, %q) = %q, want %q", c.mappedField, c.fallback, got, c.want)
			}
		})
	}
}
