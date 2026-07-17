package bkn_agent

import (
	"fmt"

	"vega-backend/interfaces"
)

func semanticUnderstandingResponseFormat(scope string) (map[string]any, error) {
	switch scope {
	case interfaces.SemanticUnderstandingTaskScopeResource:
		return resourceSemanticUnderstandingResponseFormat(), nil
	case interfaces.SemanticUnderstandingTaskScopeCatalog:
		return catalogSemanticUnderstandingResponseFormat(), nil
	default:
		return nil, fmt.Errorf("unsupported semantic understanding task scope: %s", scope)
	}
}

func resourceSemanticUnderstandingResponseFormat() map[string]any {
	confidence := confidenceSchema()
	return objectSchema(
		map[string]any{
			"confidence": confidence,
			"resource": objectSchema(map[string]any{
				"display_name": stringSchema(),
				"description":  stringSchema(),
				"confidence":   confidence,
			}, "display_name", "description"),
			"fields": arraySchema(objectSchema(map[string]any{
				"name":         stringSchema(),
				"display_name": stringSchema(),
				"description":  stringSchema(),
				"confidence":   confidence,
			}, "name", "display_name", "description")),
			"warnings": arraySchema(stringSchema()),
		},
		"confidence", "resource", "fields", "warnings",
	)
}

func catalogSemanticUnderstandingResponseFormat() map[string]any {
	confidence := confidenceSchema()
	logicDefinitionNode := objectSchema(map[string]any{
		"id":            stringSchema(),
		"name":          stringSchema(),
		"type":          stringSchema(),
		"inputs":        arraySchema(stringSchema()),
		"config":        map[string]any{"type": "object"},
		"output_fields": arraySchema(stringSchema()),
	}, "id", "type")
	logicView := objectSchema(map[string]any{
		"action":             map[string]any{"type": "string", "enum": []string{"create", "update"}},
		"target_resource_id": stringSchema(),
		"name":               stringSchema(),
		"source_identifier":  stringSchema(),
		"description":        stringSchema(),
		"source_resources":   arraySchema(stringSchema()),
		"logic_definition":   arraySchema(logicDefinitionNode),
		"confidence":         confidence,
	}, "action", "target_resource_id", "name", "source_identifier", "description", "source_resources", "logic_definition")
	obsoleteLogicView := objectSchema(map[string]any{
		"target_resource_id": stringSchema(),
		"reason":             stringSchema(),
		"confidence":         confidence,
	}, "target_resource_id", "reason")
	return objectSchema(
		map[string]any{
			"confidence":           confidence,
			"logic_views":          arraySchema(logicView),
			"obsolete_logic_views": arraySchema(obsoleteLogicView),
			"warnings":             arraySchema(stringSchema()),
		},
		"confidence", "logic_views", "obsolete_logic_views", "warnings",
	)
}

func confidenceSchema() map[string]any {
	return map[string]any{"type": "number", "minimum": 0, "maximum": 1}
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func arraySchema(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}
