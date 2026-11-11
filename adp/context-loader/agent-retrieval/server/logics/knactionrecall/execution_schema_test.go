// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knactionrecall

import "testing"

func testActionExecutionSchema(names ...string) map[string]any {
	inputs := make(map[string]any)
	for _, name := range names {
		inputs[name] = map[string]any{"type": "string"}
	}
	return map[string]any{"type": "object", "properties": map[string]any{
		"dynamic_params":       map[string]any{"type": "object", "properties": inputs, "additionalProperties": false},
		"_instance_identities": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object"}},
	}, "required": []string{"_instance_identities"}, "additionalProperties": false}
}

func TestActionExecutionSchemaUsesOnlyConfiguredInputs(t *testing.T) {
	execution := testActionExecutionSchema("message")
	tool := map[string]any{"properties": map[string]any{"dynamic_params": map[string]any{"properties": map[string]any{
		"message": map[string]any{"type": "string", "maxLength": 100}, "fixed_parameter": map[string]any{"type": "integer"},
	}}}}
	result, err := actionExecutionSchema(execution, tool)
	if err != nil {
		t.Fatal(err)
	}
	properties := result["properties"].(map[string]any)
	inputs := properties["dynamic_params"].(map[string]any)["properties"].(map[string]any)
	if len(inputs) != 1 || inputs["fixed_parameter"] != nil {
		t.Fatalf("unconfigured tool inputs leaked into execution schema: %#v", inputs)
	}
	if inputs["message"].(map[string]any)["maxLength"] != 100 {
		t.Fatal("configured input lost tool constraints")
	}
	original := execution["properties"].(map[string]any)["dynamic_params"].(map[string]any)["properties"].(map[string]any)
	if original["message"].(map[string]any)["maxLength"] != nil {
		t.Fatal("upstream response was mutated")
	}
	if _, err := actionExecutionSchema(nil, tool); err == nil {
		t.Fatal("missing new-model schema must not select a legacy fallback")
	}
}
