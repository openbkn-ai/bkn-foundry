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
)

//go:embed schemas/*.json schemas/locales/*/*.json schemas/locales/*/*.txt
var schemasFS embed.FS

// ToolMeta defines tool metadata (name, description).
type ToolMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// loadToolMeta loads tool metadata (name, description) from schemas/tools_meta.json.
func loadToolMeta(toolKey string) (name, description string) {
	data, err := schemasFS.ReadFile("schemas/tools_meta.json")
	if err != nil {
		panic("cannot read tools_meta.json: " + err.Error())
	}
	var meta map[string]ToolMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		panic("invalid tools_meta.json: " + err.Error())
	}
	t, ok := meta[toolKey]
	if !ok {
		panic("tool meta not found: " + toolKey)
	}
	return t.Name, t.Description
}

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
		wrapper.InputSchema = requireBKNContext(wrapper.InputSchema)
	}
	return wrapper.InputSchema, wrapper.OutputSchema
}

func lifecycleToolSchemas(toolKey string) (json.RawMessage, json.RawMessage, bool) {
	if _, ok := lifecycleToolNames[toolKey]; !ok {
		return nil, nil, false
	}
	properties := map[string]any{}
	required := []string{}
	addString := func(name string, isRequired bool) {
		properties[name] = map[string]any{"type": "string"}
		if isRequired {
			required = append(required, name)
		}
	}
	switch toolKey {
	case "bkn_create_conversation":
		addString("external_conversation_key", true)
		addString("idempotency_key", false)
		properties["one_shot"] = map[string]any{"type": "boolean", "default": false}
	case "bkn_resume_conversation":
		addString("conversation_id", true)
	case "bkn_start_interaction":
		addString("conversation_id", true)
		addString("idempotency_key", true)
		properties["lease_seconds"] = map[string]any{"type": "integer", "minimum": 1}
	case "bkn_close_conversation":
		addString("conversation_id", true)
		addString("idempotency_key", false)
	case "bkn_get_operation", "bkn_retry_operation":
		addString("operation_id", true)
	case "bkn_get_receipt":
		addString("receipt_id", true)
	case "bkn_finalize_operation":
		addString("operation_id", true)
		addString("receipt_id", true)
		addString("payload_hash", true)
		properties["outcome"] = enumSchema("complete", "fail")
		properties["retryable"] = booleanSchema()
		required = append(required, "outcome")
	default:
		addString("interaction_id", true)
		addString("terminal_idempotency_key", true)
		addString("lease_token", true)
		properties["lease_epoch"] = map[string]any{"type": "integer", "minimum": 1}
		addString("completion_manifest_version", true)
		addString("completion_reason", true)
		addString("answer_artifact_ref", false)
		properties["claims"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		properties["expected_operations"] = expectedResourceSchema("operation_id")
		properties["expected_receipts"] = expectedResourceSchema("receipt_id")
		properties["assembler_deadline"] = map[string]any{"type": "string", "format": "date-time"}
		required = append(required, "lease_epoch")
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
	case "bkn_create_conversation", "bkn_resume_conversation", "bkn_close_conversation":
		return conversationOutputSchema()
	case "bkn_start_interaction", "bkn_complete_interaction", "bkn_fail_interaction",
		"bkn_cancel_interaction", "bkn_handoff_interaction":
		return interactionOutputSchema()
	case "bkn_get_operation":
		return operationOutputSchema()
	case "bkn_retry_operation", "bkn_finalize_operation":
		return closedSchema(
			map[string]any{
				"operation": operationOutputSchema(),
				"receipt":   receiptOutputSchema(),
				"created":   map[string]any{"type": "boolean"},
			},
			[]string{"operation", "receipt", "created"},
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
		"operation_id":          stringSchema(),
		"conversation_id":       stringSchema(),
		"interaction_id":        stringSchema(),
		"operation_key":         stringSchema(),
		"tool_name":             stringSchema(),
		"normalized_input_hash": stringSchema(),
		"parent_operation_id":   stringSchema(),
		"causation_event_ids":   stringArraySchema(),
		"attempt":               integerSchema(),
		"attempt_status":        enumSchema("pending", "completed", "failed"),
		"retryable":             booleanSchema(),
		"row_version":           integerSchema(),
		"created_at":            dateTimeSchema(),
		"updated_at":            dateTimeSchema(),
	}
	return closedSchema(properties, []string{
		"operation_id", "conversation_id", "interaction_id", "operation_key", "tool_name",
		"normalized_input_hash", "attempt", "attempt_status", "retryable", "row_version",
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
		"normalized_input_hash":  stringSchema(),
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
		"payload_hash":    stringSchema(),
	}
	return closedSchema(properties, []string{
		"receipt_id", "schema_version", "owner", "conversation_id", "interaction_id",
		"operation_id", "attempt", "operation_key", "tool_name", "normalized_input_hash",
		"receipt_status", "evidence_durability", "required", "request_id", "trace_id",
		"causation_event_ids", "observed_evidence_refs", "business_refs", "artifact_refs",
		"partial_reasons", "row_version", "issued_at", "payload_hash",
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
	return closedSchema(map[string]any{
		"ref_type":           stringSchema(),
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
	switch toolKey {
	case toolKeySearchSchema, toolKeyQueryObjectInstance, toolKeyQueryInstanceSubgraph,
		toolKeyGetLogicPropertiesValues, toolKeyGetActionInfo, toolKeyExecuteAction,
		toolKeyGetActionExecution, toolKeyListActionExecutions, toolKeyFindSkills,
		toolKeyListKnowledgeNetworks, toolKeyGetKnDetail, toolKeyGetObjectTypes,
		toolKeyGetRelationTypes, toolKeyRunSQL, toolKeyListResources, toolKeyDescribeResource:
		return true
	default:
		return false
	}
}

func requireBKNContext(input json.RawMessage) json.RawMessage {
	var schema map[string]any
	if err := json.Unmarshal(input, &schema); err != nil {
		panic("invalid business tool input schema: " + err.Error())
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
		schema["properties"] = properties
	}
	properties["bkn_context"] = map[string]any{
		"type":        "object",
		"description": "BKN Trace 3.0 managed lifecycle context for this logical tool call.",
		"properties": map[string]any{
			"conversation_id": map[string]any{"type": "string"},
			"interaction_id":  map[string]any{"type": "string"},
			"operation_key":   map[string]any{"type": "string"},
			"parent_operation_id": map[string]any{
				"type": "string",
			},
			"causation_event_ids": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"},
			},
		},
		"required":             []string{"conversation_id", "interaction_id", "operation_key"},
		"additionalProperties": false,
	}
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
