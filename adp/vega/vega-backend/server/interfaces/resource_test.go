// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"testing"
)

func TestResourceLocalStateJSON(t *testing.T) {
	resource := &Resource{
		LocalIndexStatus: ResourceLocalIndexStatusAvailable,
		LocalIndexName:   "vega-build-resource-task",
		SyncMark:         `{"mode":"batch","cursor":[]}`,
	}

	data, err := json.Marshal(resource)
	if err != nil {
		t.Fatalf("marshal Resource: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal Resource JSON: %v", err)
	}
	if got := payload["local_status"]; got != ResourceLocalIndexStatusAvailable {
		t.Fatalf("local_status = %v, want %q", got, ResourceLocalIndexStatusAvailable)
	}
	if got := payload["index_name"]; got != resource.LocalIndexName {
		t.Fatalf("index_name = %v, want %q", got, resource.LocalIndexName)
	}
	if _, exists := payload["sync_mark"]; exists {
		t.Fatalf("internal sync_mark must not be exposed: %s", data)
	}
}

func TestLocalIndexGeneratedFields(t *testing.T) {
	res := &Resource{
		LocalIndexStatus: ResourceLocalIndexStatusAvailable,
		LocalIndexName:   "vega-build-abc",
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

	res.LocalIndexName = "vega-build-abc"
	res.LocalIndexStatus = ResourceLocalIndexStatusStale
	if got := LocalIndexGeneratedFields(res); got != nil {
		t.Fatalf("a stale index must not expose generated fields, got %+v", got)
	}
}

func TestBuildTaskIDFromIndexName(t *testing.T) {
	name := BuildIndexName("d8sl8edr563s73afv2mg", "d9q4gng1gnis73fmet10")

	if got := BuildTaskIDFromIndexName(name); got != "d9q4gng1gnis73fmet10" {
		t.Fatalf("must recover the build task that produced the index, got %q", got)
	}
	if got := BuildTaskIDFromIndexName("someone-elses-index"); got != "" {
		t.Fatalf("foreign index names yield nothing, got %q", got)
	}
	if got := BuildTaskIDFromIndexName(""); got != "" {
		t.Fatalf("empty index name yields nothing, got %q", got)
	}
}
