// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestResourceLocalStateJSON(t *testing.T) {
	zero := int64(0)
	resource := &Resource{
		LocalIndexStatus: ResourceLocalIndexStatusAvailable,
		LocalIndexName:   "vega-build-resource-task",
		SyncMark:         `{"mode":"batch","cursor":[]}`,
		RowCount:         &zero,
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
	if got, exists := payload["row_count"]; !exists || got != float64(0) {
		t.Fatalf("row_count = %v, exists = %v, want an explicit zero", got, exists)
	}
}

func TestResourceSummaryJSONOmitsScale(t *testing.T) {
	typ := reflect.TypeOf(ResourceSummary{})
	for i := 0; i < typ.NumField(); i++ {
		jsonName := typ.Field(i).Tag.Get("json")
		if jsonName == "column_count,omitempty" || jsonName == "row_count,omitempty" {
			t.Fatalf("ResourceSummary must not define scale field %q", jsonName)
		}
	}
}

func TestLocalIndexFieldContract(t *testing.T) {
	if LocalIndexKeywordSubfieldName != "keyword" {
		t.Fatalf("LocalIndexKeywordSubfieldName = %q, want %q", LocalIndexKeywordSubfieldName, "keyword")
	}
	if DefaultTextKeywordIgnoreAbove != 256 {
		t.Fatalf("DefaultTextKeywordIgnoreAbove = %d, want %d", DefaultTextKeywordIgnoreAbove, 256)
	}
	if MaxKeywordIgnoreAbove != 8191 {
		t.Fatalf("MaxKeywordIgnoreAbove = %d, want %d", MaxKeywordIgnoreAbove, 8191)
	}
}
