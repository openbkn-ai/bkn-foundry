// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bkn_agent

import (
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// apiNameDescription 告诉模型 api_name 的格式。schema 不带 pattern：结构化输出不合 schema
// 会让整条结果失败，一个不合格的短名不应连带丢掉显示名和说明，所以格式在写回时校验。
const apiNameDescription = "English short name in snake_case for use as a machine identifier: " +
	"a lowercase letter, then lowercase letters, digits or underscores, at most 40 characters."

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
				"api_name":     apiNameSchema(),
				"confidence":   confidence,
			}, "display_name", "description"),
			"fields": arraySchema(objectSchema(map[string]any{
				"name":         stringSchema(),
				"display_name": stringSchema(),
				"description":  stringSchema(),
				"api_name":     apiNameSchema(),
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
	logicalView := objectSchema(map[string]any{
		"action":             map[string]any{"type": "string", "enum": []string{"create", "update"}},
		"target_resource_id": stringSchema(),
		"name":               stringSchema(),
		"source_identifier":  stringSchema(),
		"description":        stringSchema(),
		"source_resources":   arraySchema(stringSchema()),
		"logic_definition":   arraySchema(logicDefinitionNode),
		"confidence":         confidence,
	}, "action", "target_resource_id", "name", "source_identifier", "description", "source_resources", "logic_definition")
	obsoleteLogicalView := objectSchema(map[string]any{
		"target_resource_id": stringSchema(),
		"reason":             stringSchema(),
		"confidence":         confidence,
	}, "target_resource_id", "reason")
	return objectSchema(
		map[string]any{
			"confidence":             confidence,
			"logical_views":          arraySchema(logicalView),
			"obsolete_logical_views": arraySchema(obsoleteLogicalView),
			"warnings":               arraySchema(stringSchema()),
		},
		"confidence", "logical_views", "obsolete_logical_views", "warnings",
	)
}

func confidenceSchema() map[string]any {
	return map[string]any{"type": "number", "minimum": 0, "maximum": 1}
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func apiNameSchema() map[string]any {
	return map[string]any{"type": "string", "description": apiNameDescription}
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
