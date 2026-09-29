// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func TestResourceLocalStateJSON(t *testing.T) {
	zero := int64(0)
	resource := &Resource{
		LocalIndexStatus:  ResourceLocalIndexStatusAvailable,
		LocalIndexName:    "vega-build-resource-task",
		SyncMark:          `{"mode":"batch","cursor":[]}`,
		RowCount:          &zero,
		EstimatedRowCount: &zero,
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
	if got, exists := payload["estimated_row_count"]; !exists || got != float64(0) {
		t.Fatalf("estimated_row_count = %v, exists = %v, want an explicit zero", got, exists)
	}
}

func TestDerivedLogicDefinitionJSON(t *testing.T) {
	precise, err := DecodeDerivedLogicDefinition(map[string]any{
		"source_resource_id": "source",
		"filter_condition":   map[string]any{"value": json.Number("9007199254740993")},
	})
	if err != nil {
		t.Fatalf("decode precise filter: %v", err)
	}
	condition := precise.FilterCondition.(map[string]any)
	if condition["value"] != json.Number("9007199254740993") {
		t.Fatalf("filter value lost precision: %v", condition["value"])
	}
	var request ResourceRequest
	err = json.Unmarshal([]byte(`{"category":"logicview","logic_definition":{"source_resource_id":"source","distinct":false}}`), &request)
	if err != nil {
		t.Fatalf("decode generic definition: %v", err)
	}
	if _, err := DecodeDerivedLogicDefinition(request.LogicDefinition); err == nil {
		t.Fatal("derived definition must reject unsupported fields")
	}
	if err := sonic.Unmarshal([]byte(`{"logic_definition":{"source_resource_id":"source","distinct":false}}`), &request); err != nil {
		t.Fatalf("sonic decode generic definition: %v", err)
	}
	if _, err := DecodeDerivedLogicDefinition(request.LogicDefinition); err == nil {
		t.Fatal("derived conversion must reject unsupported fields from sonic")
	}
	if _, err := DecodeDerivedLogicDefinition([]any{map[string]any{"type": "resource"}}); err == nil {
		t.Fatal("derived conversion must reject composite arrays")
	}
	for _, raw := range []any{
		map[string]any{},
		map[string]any{"source_resource_id": ""},
		map[string]any{"source_resource_id": "  "},
	} {
		if _, err := DecodeDerivedLogicDefinition(raw); err == nil {
			t.Fatalf("derived conversion must reject missing source_resource_id: %#v", raw)
		}
	}
	err = json.Unmarshal([]byte(`{"category":"logicview","logic_definition":{"source_resource_id":"source"}}`), &request)
	definition, decodeErr := DecodeDerivedLogicDefinition(request.LogicDefinition)
	if err != nil || decodeErr != nil || definition.SourceResourceID != "source" {
		t.Fatalf("decode derived definition: definition=%+v, err=%v", request.LogicDefinition, err)
	}
	encoded, err := json.Marshal(&Resource{LogicDefinition: request.LogicDefinition})
	if err != nil || !strings.Contains(string(encoded), `"logic_definition":{"source_resource_id":"source"}`) {
		t.Fatalf("encode derived definition: %s, err=%v", encoded, err)
	}
}

func TestResourceSummaryJSONOmitsScale(t *testing.T) {
	typ := reflect.TypeOf(ResourceSummary{})
	for i := 0; i < typ.NumField(); i++ {
		jsonName := typ.Field(i).Tag.Get("json")
		if jsonName == "column_count,omitempty" || jsonName == "row_count,omitempty" || jsonName == "estimated_row_count,omitempty" {
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
