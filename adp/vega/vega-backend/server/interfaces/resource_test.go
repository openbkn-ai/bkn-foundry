// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "testing"

func TestLocalIndexGeneratedFields(t *testing.T) {
	res := &Resource{
		LocalIndexName: "vega-build-abc",
		SchemaDefinition: []*Property{
			{Name: "stadium_name", Features: []PropertyFeature{{FeatureType: PropertyFeatureType_Vector}}},
			{Name: "city_name", Features: []PropertyFeature{{FeatureType: PropertyFeatureType_Fulltext}}},
		},
	}

	generated := LocalIndexGeneratedFields(res)

	if len(generated) != 1 {
		t.Fatalf("only vector features generate index-only fields, got %+v", generated)
	}
	field, ok := generated["stadium_name_vector"]
	if !ok || field.Type != DataType_Vector {
		t.Fatalf("generated vector field missing or mistyped: %+v", generated)
	}

	res.LocalIndexName = ""
	if got := LocalIndexGeneratedFields(res); got != nil {
		t.Fatalf("without a built index nothing is generated yet, got %+v", got)
	}
}
