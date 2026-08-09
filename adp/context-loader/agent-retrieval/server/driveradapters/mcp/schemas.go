// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed schemas/*.json schemas/locales/*/*.json schemas/locales/*/*.txt
var schemasFS embed.FS

// ToolMeta defines tool metadata: the wire identity (name, description) plus
// the presentation hints a UI needs to render a catalogue — a display name, a
// group, and a position within it.
//
// Name is what a call carries and never changes for cosmetic reasons; Title is
// free to. The two are separate fields in the MCP protocol itself since the
// 2025-06-18 revision, and that is where Title is published. Group/GroupTitle/
// Order have no protocol field, so they travel in the tool's `_meta` under an
// openbkn.ai/ prefix — a client that does not know them ignores them, which is
// what `_meta` is for.
type ToolMeta struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Group       string `json:"group,omitempty"`
	GroupTitle  string `json:"group_title,omitempty"`
	Order       int    `json:"order,omitempty"`
	Description string `json:"description,omitempty"`
}

// loadToolMeta loads a tool's metadata from schemas/tools_meta.json.
func loadToolMeta(toolKey string) ToolMeta {
	t, ok := allToolMeta()[toolKey]
	if !ok {
		panic("tool meta not found: " + toolKey)
	}
	return t
}

// allToolMeta returns the decoded tools_meta.json.
//
// Decoded once: the file is embedded and immutable, while /mcp/info resolves
// every tool's metadata on every request — without this, one request re-parses
// the whole file once per tool. The returned map is shared, so callers must
// treat it as read-only.
var allToolMeta = sync.OnceValue(func() map[string]ToolMeta {
	data, err := schemasFS.ReadFile("schemas/tools_meta.json")
	if err != nil {
		panic("cannot read tools_meta.json: " + err.Error())
	}
	var meta map[string]ToolMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		panic("invalid tools_meta.json: " + err.Error())
	}
	return meta
})

// toolSchemaFile defines the structure of a merged tool schema JSON file.
type toolSchemaFile struct {
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

// loadToolSchemas loads input and output schema for a tool from its merged JSON file.
// File: schemas/<toolKey>.json, containing input_schema and output_schema keys.
func loadToolSchemas(toolKey string) (input, output json.RawMessage) {
	if input, output, ok := lifecycleToolSchemas(toolKey); ok {
		return input, output
	}
	path := fmt.Sprintf("schemas/%s.json", toolKey)
	data, err := schemasFS.ReadFile(path)
	if err != nil {
		panic("cannot read " + path + ": " + err.Error())
	}
	var wrapper toolSchemaFile
	if err := json.Unmarshal(data, &wrapper); err != nil {
		panic("invalid " + path + ": " + err.Error())
	}
	if len(wrapper.InputSchema) == 0 {
		panic(path + ": missing input_schema")
	}
	if isBusinessTool(toolKey) {
		wrapper.InputSchema = offerBKNContext(wrapper.InputSchema)
	}
	return wrapper.InputSchema, wrapper.OutputSchema
}

func lifecycleToolSchemas(toolKey string) (json.RawMessage, json.RawMessage, bool) {
	if _, ok := lifecycleToolNames[toolKey]; !ok {
		return nil, nil, false
	}
	properties := map[string]any{}
	required := []string{}
	switch toolKey {
	case "bkn_start_interaction":
		properties["conversation_id"] = map[string]any{
			"type": "string", "description": "Omit on the first turn; otherwise reuse the ID returned by start.",
		}
		properties["question"] = map[string]any{
			"type": "string", "description": "Use the user's question for this turn.",
		}
		required = append(required, "question")
		properties["agent_name"] = map[string]any{
			"type": "string", "maxLength": 128,
			"description": "Optionally declare a display name on the first turn; do not change it later.",
		}
	case "bkn_finish_interaction":
		properties["interaction_id"] = map[string]any{
			"type": "string", "description": "Use the exact interaction_id returned by start.",
		}
		required = append(required, "interaction_id")
		properties["outcome"] = enumSchema("completed", "failed", "cancelled", "handed_off")
		properties["outcome"].(map[string]any)["description"] = "Set the final outcome for this turn."
		required = append(required, "outcome")
		properties["answer"] = map[string]any{
			"type": "string", "description": "Provide the final answer when outcome is completed.",
		}
		properties["reason"] = map[string]any{
			"type": "string", "description": "Briefly explain a non-completed outcome.",
		}
	}
	input, _ := json.Marshal(map[string]any{
		"type": "object", "properties": properties, "required": required,
		"additionalProperties": false,
	})
	output, _ := json.Marshal(lifecycleOutputSchema(toolKey))
	return input, output, true
}

func lifecycleOutputSchema(toolKey string) map[string]any {
	switch toolKey {
	case "bkn_start_interaction":
		return closedSchema(map[string]any{
			"interaction_id":   stringSchema(),
			"conversation_id":  stringSchema(),
			"execution_status": enumSchema("active"),
		}, []string{"interaction_id", "conversation_id", "execution_status"})
	case "bkn_finish_interaction":
		return closedSchema(map[string]any{
			"interaction_id":   stringSchema(),
			"conversation_id":  stringSchema(),
			"execution_status": enumSchema("completed", "failed", "canceled", "handed_off", "abandoned"),
			"evidence_status":  enumSchema("not_applicable", "assembling", "complete", "partial", "failed"),
		}, []string{"interaction_id", "conversation_id", "execution_status", "evidence_status"})
	case "bkn_get_operation":
		return operationOutputSchema()
	case "bkn_retry_operation":
		return closedSchema(
			map[string]any{
				"operation": operationOutputSchema(),
				"receipt":   receiptOutputSchema(),
				"created":   map[string]any{"type": "boolean"},
				"execute":   map[string]any{"type": "boolean"},
			},
			[]string{"operation", "receipt", "created", "execute"},
		)
	case "bkn_get_receipt":
		return receiptOutputSchema()
	default:
		panic("unsupported lifecycle output schema: " + toolKey)
	}
}

func conversationOutputSchema() map[string]any {
	properties := map[string]any{
		"conversation_id":           stringSchema(),
		"owner":                     ownerOutputSchema(),
		"external_conversation_key": stringSchema(),
		"generation":                integerSchema(),
		"status":                    enumSchema("active", "closed", "expired"),
		"one_shot":                  booleanSchema(),
		"row_version":               integerSchema(),
		"created_at":                dateTimeSchema(),
		"updated_at":                dateTimeSchema(),
		"closed_at":                 dateTimeSchema(),
	}
	return closedSchema(properties, []string{
		"conversation_id", "owner", "external_conversation_key", "generation", "status",
		"one_shot", "row_version", "created_at", "updated_at",
	})
}

func interactionOutputSchema() map[string]any {
	properties := map[string]any{
		"interaction_id":   stringSchema(),
		"conversation_id":  stringSchema(),
		"ordinal":          integerSchema(),
		"execution_status": enumSchema("active", "completed", "failed", "canceled", "handed_off", "abandoned"),
		"evidence_status":  enumSchema("not_applicable", "assembling", "complete", "partial", "failed"),
		"closure_manifest": closureManifestOutputSchema(),
		"lease_token":      stringSchema(),
		"lease_epoch":      integerSchema(),
		"lease_version":    integerSchema(),
		"lease_expires_at": dateTimeSchema(),
		"row_version":      integerSchema(),
		"created_at":       dateTimeSchema(),
		"updated_at":       dateTimeSchema(),
		"terminal_at":      dateTimeSchema(),
	}
	return closedSchema(properties, []string{
		"interaction_id", "conversation_id", "ordinal", "execution_status", "evidence_status",
		"lease_token", "lease_epoch", "lease_version", "lease_expires_at", "row_version",
		"created_at", "updated_at",
	})
}

func operationOutputSchema() map[string]any {
	properties := map[string]any{
		"operation_id":        stringSchema(),
		"conversation_id":     stringSchema(),
		"interaction_id":      stringSchema(),
		"operation_key":       stringSchema(),
		"tool_name":           stringSchema(),
		"parent_operation_id": stringSchema(),
		"causation_event_ids": stringArraySchema(),
		"attempt":             integerSchema(),
		"attempt_status":      enumSchema("ready", "pending", "completed", "failed"),
		"retryable":           booleanSchema(),
		"row_version":         integerSchema(),
		"created_at":          dateTimeSchema(),
		"updated_at":          dateTimeSchema(),
	}
	return closedSchema(properties, []string{
		"operation_id", "conversation_id", "interaction_id", "operation_key", "tool_name",
		"attempt", "attempt_status", "retryable", "row_version",
		"created_at", "updated_at",
	})
}

func receiptOutputSchema() map[string]any {
	properties := map[string]any{
		"receipt_id":             stringSchema(),
		"schema_version":         stringSchema(),
		"owner":                  ownerOutputSchema(),
		"conversation_id":        stringSchema(),
		"interaction_id":         stringSchema(),
		"operation_id":           stringSchema(),
		"attempt":                integerSchema(),
		"operation_key":          stringSchema(),
		"tool_name":              stringSchema(),
		"receipt_status":         enumSchema("pending", "completed", "failed"),
		"evidence_durability":    enumSchema("pending", "durable", "failed"),
		"required":               booleanSchema(),
		"request_id":             stringSchema(),
		"trace_id":               stringSchema(),
		"causation_event_ids":    stringArraySchema(),
		"observed_evidence_refs": stringArraySchema(),
		"business_refs": map[string]any{
			"type": "array", "items": businessRefOutputSchema(),
		},
		"artifact_refs":   stringArraySchema(),
		"partial_reasons": stringArraySchema(),
		"row_version":     integerSchema(),
		"issued_at":       dateTimeSchema(),
		"terminal_at":     dateTimeSchema(),
	}
	return closedSchema(properties, []string{
		"receipt_id", "schema_version", "owner", "conversation_id", "interaction_id",
		"operation_id", "attempt", "operation_key", "tool_name",
		"receipt_status", "evidence_durability", "required", "request_id", "trace_id",
		"causation_event_ids", "observed_evidence_refs", "business_refs", "artifact_refs",
		"partial_reasons", "row_version", "issued_at",
	})
}

func ownerOutputSchema() map[string]any {
	properties := map[string]any{
		"tenant_id":                stringSchema(),
		"business_domain_id":       stringSchema(),
		"application_principal_id": stringSchema(),
		"effective_subject_type":   enumSchema("user", "service"),
		"effective_subject_id":     stringSchema(),
		"delegation_id":            stringSchema(),
	}
	return closedSchema(properties, []string{
		"tenant_id", "business_domain_id", "application_principal_id",
		"effective_subject_type", "effective_subject_id",
	})
}

func closureManifestOutputSchema() map[string]any {
	return closedSchema(map[string]any{
		"completion_manifest_version": stringSchema(),
		"answer_artifact_ref":         stringSchema(),
		"claims":                      stringArraySchema(),
		"expected_operations":         expectedResourceSchema("operation_id"),
		"expected_receipts":           expectedResourceSchema("receipt_id"),
		"assembler_deadline":          dateTimeSchema(),
		"completion_reason":           stringSchema(),
		"system_partial_reasons":      stringArraySchema(),
	}, []string{"completion_manifest_version", "completion_reason"})
}

func businessRefOutputSchema() map[string]any {
	// ref_type 的取值集合与 bkn-trace 的 sessionvo.BusinessRef 对齐（#587 起它是
	// 一个具名枚举类型）。TestLifecycleSchemaUsesRegisteredIssue541ErrorsAndCoreTypes
	// 会逐项递归比对这两侧，漏一个值就红。
	return closedSchema(map[string]any{
		"ref_type": enumSchema(
			"action_instance", "action_type", "data_resource", "function",
			"knowledge_network", "logic", "metric", "object_instance",
			"object_type", "property", "relation_type",
		),
		"ref_id":             stringSchema(),
		"business_domain_id": stringSchema(),
		"version":            stringSchema(),
		"as_of":              dateTimeSchema(),
		"display_hint":       stringSchema(),
	}, []string{"ref_type", "ref_id", "business_domain_id", "version"})
}

func closedSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type": "object", "properties": properties, "required": required,
		"additionalProperties": false,
	}
}

func stringSchema() map[string]any  { return map[string]any{"type": "string"} }
func integerSchema() map[string]any { return map[string]any{"type": "integer"} }
func booleanSchema() map[string]any { return map[string]any{"type": "boolean"} }
func dateTimeSchema() map[string]any {
	return map[string]any{"type": "string", "format": "date-time"}
}
func stringArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": stringSchema()}
}
func enumSchema(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func expectedResourceSchema(idField string) map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				idField:    map[string]any{"type": "string"},
				"required": map[string]any{"type": "boolean"},
			},
			"required":             []string{idField, "required"},
			"additionalProperties": false,
		},
	}
}

func isBusinessTool(toolKey string) bool {
	_, lifecycle := lifecycleToolNames[toolKey]
	return !lifecycle
}

func offerBKNContext(input json.RawMessage) json.RawMessage {
	var schema map[string]any
	if err := json.Unmarshal(input, &schema); err != nil {
		panic("invalid business tool input schema: " + err.Error())
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
		schema["properties"] = properties
	}
	properties["bkn_context"] = bknContextInputSchema()
	required, _ := schema["required"].([]any)
	for _, value := range required {
		if value == "bkn_context" {
			raw, _ := json.Marshal(schema)
			return raw
		}
	}
	schema["required"] = append(required, "bkn_context")
	raw, err := json.Marshal(schema)
	if err != nil {
		panic("marshal business tool input schema: " + err.Error())
	}
	return raw
}

func bknContextInputSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "BKN Trace managed context. Use only IDs returned by lifecycle tools.",
		"properties": map[string]any{
			"conversation_id":     stringSchema(),
			"interaction_id":      stringSchema(),
			"parent_operation_id": stringSchema(),
			"causation_event_ids": map[string]any{
				"type": "array", "maxItems": 64, "items": stringSchema(),
			},
			"business_refs": map[string]any{
				"type": "array", "maxItems": 64,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ref_type": enumSchema(
							"knowledge_network", "object_type", "object_instance", "property", "relation_type",
							"data_resource", "metric", "logic", "function", "action_type", "action_instance",
						),
						"ref_id": stringSchema(), "version": stringSchema(),
					},
					"required": []string{"ref_type", "ref_id"}, "additionalProperties": false,
				},
			},
		},
		"required":             []string{"conversation_id", "interaction_id"},
		"additionalProperties": false,
	}
}
