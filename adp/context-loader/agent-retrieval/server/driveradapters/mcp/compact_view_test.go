// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

type listedTool struct {
	Name         string          `json:"name"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema"`
}

func listedTools(t *testing.T, srv *server.MCPServer) map[string]listedTool {
	t.Helper()
	var result struct {
		Tools []listedTool `json:"tools"`
	}
	resultOf(t, rpc(t, srv, "tools/list", map[string]any{}), &result)
	out := make(map[string]listedTool, len(result.Tools))
	for _, tool := range result.Tools {
		out[tool.Name] = tool
	}
	return out
}

func schemaFields(t *testing.T, raw json.RawMessage) (properties, required []string) {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	for name := range schema.Properties {
		properties = append(properties, name)
	}
	return properties, schema.Required
}

// Results on the compact profile are text, so no business tool may publish
// an output schema or offer response_format there; the lifecycle pair keeps
// its structured contract.
func TestCompactProfilePublishesTextOnlyDefinitions(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for name, tool := range listedTools(t, compactServer(t, locale)) {
			properties, required := schemaFields(t, tool.InputSchema)
			if slices.Contains(properties, "response_format") || slices.Contains(required, "response_format") {
				t.Errorf("%s %s: offers response_format", locale, name)
			}
			_, lifecycle := lifecycleToolNames[name]
			if hasOutput := len(tool.OutputSchema) > 0; hasOutput != lifecycle {
				t.Errorf("%s %s: output schema published = %v, want %v", locale, name, hasOutput, lifecycle)
			}
			if !lifecycle && !slices.Contains(required, "bkn_context") {
				t.Errorf("%s %s: bkn_context is no longer required", locale, name)
			}
		}
	}
}

// /mcp-compact/info must describe the tools exactly as tools/list does.
func TestCompactInfoMatchesTheCompactView(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		listed := listedTools(t, compactServer(t, locale))
		info, err := BuildCompactMCPInfoForLocale(compactEndpointPath, locale)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range info.Tools {
			published, ok := listed[tool.Name]
			if !ok {
				t.Errorf("%s: info lists %s, tools/list does not", locale, tool.Name)
				continue
			}
			if !sameJSON(tool.InputSchema, published.InputSchema) {
				t.Errorf("%s %s: info and tools/list disagree on the input schema", locale, tool.Name)
			}
			if (len(tool.OutputSchema) > 0) != (len(published.OutputSchema) > 0) {
				t.Errorf("%s %s: info and tools/list disagree on the output schema", locale, tool.Name)
			}
		}
	}
}

func TestFullProfileKeepsItsSchemas(t *testing.T) {
	full, _ := newMCPServerForLocale(nil, "zh-CN")
	tool := listedTools(t, full)[toolKeyQueryObjectInstance]
	properties, _ := schemaFields(t, tool.InputSchema)
	if len(tool.OutputSchema) == 0 || !slices.Contains(properties, "response_format") {
		t.Fatal("the full profile lost query_object_instance's output schema or response_format")
	}
	if contextFields := bknContextFields(t, tool.InputSchema); !slices.Contains(contextFields, "business_refs") {
		t.Fatalf("the full profile's bkn_context lost business_refs: %v", contextFields)
	}
}

// The full bkn_context declaration repeated about 2.5K characters on every
// tool; the compact profile publishes only the two IDs every call needs.
func TestCompactProfilePublishesOnlyTheContextIDs(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for name, tool := range listedTools(t, compactServer(t, locale)) {
			if _, lifecycle := lifecycleToolNames[name]; lifecycle {
				continue
			}
			fields := bknContextFields(t, tool.InputSchema)
			slices.Sort(fields)
			if !slices.Equal(fields, []string{"conversation_id", "interaction_id"}) {
				t.Errorf("%s %s: bkn_context publishes %v", locale, name, fields)
			}
		}
	}
}

func bknContextFields(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var schema struct {
		Properties struct {
			BKNContext struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"bkn_context"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	fields := make([]string, 0, len(schema.Properties.BKNContext.Properties))
	for field := range schema.Properties.BKNContext.Properties {
		fields = append(fields, field)
	}
	return fields
}

func TestCompactInputSchema(t *testing.T) {
	got := compactInputSchema("t", json.RawMessage(`{"type":"object","properties":{"response_format":{"type":"string"},"kn_id":{"type":"string"},"limit":{"type":"integer","maximum":9007199254740993}},"required":["kn_id","response_format"]}`))
	properties, required := schemaFields(t, got)
	if slices.Contains(properties, "response_format") || !slices.Equal(required, []string{"kn_id"}) {
		t.Fatalf("compact schema = %s", got)
	}
	if !json.Valid(got) || !strings.Contains(string(got), "9007199254740993") {
		t.Fatalf("a wide integer bound was rewritten: %s", got)
	}
	unchanged := json.RawMessage(`{"type":"object","properties":{"kn_id":{"type":"string"}}}`)
	if string(compactInputSchema("t", unchanged)) != string(unchanged) {
		t.Fatal("a schema without hidden fields should be published byte for byte")
	}
	if string(compactInputSchema("t", json.RawMessage(`{`))) != "{" {
		t.Fatal("an unreadable schema should be published unchanged")
	}
}
