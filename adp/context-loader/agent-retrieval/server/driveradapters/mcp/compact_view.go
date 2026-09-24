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
	tool.RawInputSchema = publishedInputSchema(tool.Name, tool.RawInputSchema, compactHiddenInputFields)
	return tool
}

// modelContextToolView is what every entry publishes of bkn_context: the two
// IDs the caller copies from bkn_start_interaction. The adapter contract is
// unchanged - the guard reads the arguments, not this schema - and the full
// contract is still described at GET /mcp/info, in the REST definitions and in
// the developer documentation.
func modelContextToolView(tool mcp.Tool) mcp.Tool {
	if _, lifecycle := lifecycleToolNames[tool.Name]; lifecycle {
		return tool
	}
	tool.RawInputSchema = publishedInputSchema(tool.Name, tool.RawInputSchema, nil)
	return tool
}

// publishedInputSchema narrows an input schema to what the model is asked to
// fill in: bkn_context becomes the two IDs, and the named fields are dropped
// from the schema and from its required list. A schema that cannot be read is
// published unchanged: a slightly wider definition is better than a missing
// tool.
func publishedInputSchema(name string, input json.RawMessage, hidden []string) json.RawMessage {
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
		properties["bkn_context"] = modelBKNContextSchema()
		changed = true
	}
	for _, field := range hidden {
		if _, present := properties[field]; present {
			delete(properties, field)
			changed = true
		}
	}
	if required, ok := schema["required"].([]any); ok {
		kept := make([]any, 0, len(required))
		for _, field := range required {
			if field, isString := field.(string); isString && slices.Contains(hidden, field) {
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

// modelBKNContextSchema is the bkn_context every entry publishes: the two IDs
// every call needs and nothing else.
//
// The full declaration repeats about 2.4K bytes of business_refs, causation
// and parent-operation documentation on every business tool - 58% of all
// published input schemas on the full entry, and half of the compact entry's
// definitions - while agents almost never send those fields, and a model that
// does has been seen inventing their contents. They are still accepted: the guard reads bkn_context from the
// arguments, not from this schema, so a host that declares business refs keeps
// working. additionalProperties is left open for the same reason.
func modelBKNContextSchema() map[string]any {
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
