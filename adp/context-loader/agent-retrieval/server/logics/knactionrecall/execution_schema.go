// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knactionrecall

import (
	"fmt"
	"maps"
)

// actionExecutionSchema takes the action service's target and input contract as
// authoritative. Tool metadata may enrich configured inputs, not introduce more
// inputs or a legacy implicit-target execution mode.
func actionExecutionSchema(execution, tool map[string]any) (map[string]any, error) {
	if execution == nil {
		return nil, fmt.Errorf("action service did not return execution_schema; update the action service before use")
	}
	result := maps.Clone(execution)
	properties, ok := execution["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("action execution_schema is missing properties")
	}
	properties = maps.Clone(properties)
	result["properties"] = properties
	dynamic, hasDynamic := properties["dynamic_params"].(map[string]any)
	toolProperties, _ := tool["properties"].(map[string]any)
	toolDynamic, _ := toolProperties["dynamic_params"].(map[string]any)
	toolInputs, _ := toolDynamic["properties"].(map[string]any)
	if hasDynamic {
		dynamic = maps.Clone(dynamic)
		inputs, _ := dynamic["properties"].(map[string]any)
		inputs = maps.Clone(inputs)
		for name, input := range inputs {
			schema, _ := input.(map[string]any)
			enrichment, _ := toolInputs[name].(map[string]any)
			inputs[name] = enrichActionInput(schema, enrichment)
		}
		dynamic["properties"], properties["dynamic_params"] = inputs, dynamic
	}
	return result, nil
}

// Preserve the configured leaf set when enriching nested tool-body properties.
func enrichActionInput(input, tool map[string]any) map[string]any {
	result := maps.Clone(input)
	if result == nil {
		return nil
	}
	properties, nested := input["properties"].(map[string]any)
	if !nested {
		for key, value := range tool {
			if key != "type" {
				result[key] = value
			}
		}
		return result
	}
	toolProperties, _ := tool["properties"].(map[string]any)
	children := maps.Clone(properties)
	for name, child := range children {
		schema, _ := child.(map[string]any)
		rich, _ := toolProperties[name].(map[string]any)
		children[name] = enrichActionInput(schema, rich)
	}
	result["properties"] = children
	return result
}
