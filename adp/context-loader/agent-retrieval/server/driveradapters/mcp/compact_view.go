// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"bytes"
	"encoding/json"
	"log"
	"slices"

	"github.com/mark3labs/mcp-go/mcp"
)

// compactHiddenInputFields are the input fields the compact profile does not
// publish. response_format asks the model for an encoding choice unrelated to
// the answer; the deployment decides the encoding instead.
var compactHiddenInputFields = []string{"response_format"}

// compactToolView is how the compact profile publishes a tool. Results there
// are text, so a business tool publishes no output schema: once a server
// publishes one it must return matching structuredContent. The lifecycle pair
// keeps its contract, because a client must read the conversation and
// interaction IDs from it reliably.
//
// Only the published definition changes. Handlers, Trace capability profiles
// and the full profile keep the original schemas.
func compactToolView(tool mcp.Tool) mcp.Tool {
	if _, lifecycle := lifecycleToolNames[tool.Name]; lifecycle {
		return tool
	}
	tool.RawOutputSchema = nil
	tool.OutputSchema = mcp.ToolOutputSchema{}
	tool.RawInputSchema = compactInputSchema(tool.Name, tool.RawInputSchema)
	return tool
}

// compactInputSchema drops the hidden fields from an input schema and from
// its required list. A schema that cannot be read is published unchanged:
// a slightly wider definition is better than a missing tool.
func compactInputSchema(name string, input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return input
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil {
		log.Printf("WARN: compact view of %s: cannot read input schema, publishing it unchanged: %v", name, err)
		return input
	}
	properties, _ := schema["properties"].(map[string]any)
	changed := false
	if _, present := properties["bkn_context"]; present {
		properties["bkn_context"] = compactBKNContextSchema()
		changed = true
	}
	for _, field := range compactHiddenInputFields {
		if _, present := properties[field]; present {
			delete(properties, field)
			changed = true
		}
	}
	if required, ok := schema["required"].([]any); ok {
		kept := make([]any, 0, len(required))
		for _, field := range required {
			if field, isString := field.(string); isString && slices.Contains(compactHiddenInputFields, field) {
				changed = true
				continue
			}
			kept = append(kept, field)
		}
		schema["required"] = kept
	}
	if !changed {
		return input
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		log.Printf("WARN: compact view of %s: cannot write input schema, publishing it unchanged: %v", name, err)
		return input
	}
	return raw
}

// compactBKNContextSchema is the bkn_context the compact profile publishes: the
// two IDs every call needs and nothing else.
//
// The full declaration repeats about 2.5K characters of business_refs,
// causation and parent-operation documentation on every tool, which on this
// entry was half of all tool definitions, while agents almost never send those
// fields. They are still accepted: the guard reads bkn_context from the
// arguments, not from this schema, so a host that declares business refs keeps
// working. additionalProperties is left open for the same reason.
func compactBKNContextSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "BKN Trace managed context. Copy both IDs from bkn_start_interaction.",
		"properties": map[string]any{
			"conversation_id": describedStringSchema("conversation_id returned by bkn_start_interaction."),
			"interaction_id":  describedStringSchema("interaction_id returned by bkn_start_interaction."),
		},
		"required": []string{"conversation_id", "interaction_id"},
	}
}

// compactInfoView applies compactToolView to a /mcp-compact/info entry.
func compactInfoView(info MCPToolInfo) MCPToolInfo {
	view := compactToolView(mcp.Tool{Name: info.Name, RawInputSchema: info.InputSchema, RawOutputSchema: info.OutputSchema})
	info.InputSchema, info.OutputSchema = view.RawInputSchema, view.RawOutputSchema
	return info
}
