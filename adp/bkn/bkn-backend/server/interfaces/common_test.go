// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "testing"

func TestBKNConceptSchemaKeywordIgnoreAboveWithinVegaLimit(t *testing.T) {
	const vegaMaxKeywordIgnoreAbove = 8191

	for _, property := range GetBKNConceptSchemaDefinition(768, false) {
		for _, feature := range property.Features {
			if feature.FeatureType != FieldFeatureType_Keyword {
				continue
			}

			value, exists := feature.Config[FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE]
			if !exists {
				t.Fatalf("keyword feature %q on property %q has no %q config",
					feature.FeatureName, property.Name, FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE)
			}
			limit, ok := value.(int)
			if !ok {
				t.Fatalf("keyword feature %q on property %q has non-integer %q value %T",
					feature.FeatureName, property.Name, FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE, value)
			}
			if limit < 1 || limit > vegaMaxKeywordIgnoreAbove {
				t.Errorf("keyword feature %q on property %q has %s=%d, want 1..%d",
					feature.FeatureName, property.Name, FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE, limit,
					vegaMaxKeywordIgnoreAbove)
			}
		}
	}
}
