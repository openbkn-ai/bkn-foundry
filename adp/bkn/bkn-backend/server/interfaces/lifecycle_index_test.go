// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "testing"

// A deprecated concept is marked in the indexed lifecycle_status field; any other is not.
func TestIndexLifecycleStatus(t *testing.T) {
	cases := []struct {
		name string
		doc  map[string]any
		want any
	}{
		{"deprecated", map[string]any{"lifecycle": map[string]any{"status": "deprecated", "reason": "retired"}}, "deprecated"},
		{"no lifecycle", map[string]any{}, nil},
		{"active", map[string]any{"lifecycle": map[string]any{"status": "active"}}, nil},
		{"stale field cleared", map[string]any{CONCEPT_FIELD_LIFECYCLE_STATUS: "deprecated"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			IndexLifecycleStatus(tc.doc)
			if got := tc.doc[CONCEPT_FIELD_LIFECYCLE_STATUS]; got != tc.want {
				t.Fatalf("lifecycle_status = %v, want %v", got, tc.want)
			}
		})
	}
}

// The concept index declares lifecycle_status as a keyword, so recall can filter on it.
func TestConceptSchemaDeclaresLifecycleStatus(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		found := false
		for _, property := range GetBKNConceptSchemaDefinition(768, enabled) {
			if property.Name == CONCEPT_FIELD_LIFECYCLE_STATUS {
				found = len(property.Features) == 1 && property.Features[0].FeatureType == FieldFeatureType_Keyword
			}
		}
		if !found {
			t.Fatalf("concept schema (small model %v) lacks a keyword lifecycle_status", enabled)
		}
	}
}
