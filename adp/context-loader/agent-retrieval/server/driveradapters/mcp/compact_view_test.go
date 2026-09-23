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
	// bkn_context is the one thing the full profile does rewrite: the model
	// view is the two IDs on both entries. The adapter contract is unchanged,
	// which TestFullInfoDescribesTheWholeAdapterContract holds to.
	if contextFields := bknContextFields(t, tool.InputSchema); !slices.Equal(
		contextFields, []string{"conversation_id", "interaction_id"},
	) {
		t.Fatalf("the full profile's published bkn_context = %v, want the two IDs", contextFields)
	}
}

// The three extension fields are filled in by programs - the PTC stub, the
// SDKs, function-to-function calls - and the server still accepts them. They
// stay described where those integrations read: GET /mcp/info, the REST
// definitions and the developer documentation.
func TestFullInfoDescribesTheWholeAdapterContract(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		info, err := BuildMCPInfoForLocale("http://example.invalid/mcp", locale)
		if err != nil {
			t.Fatalf("%s: build info: %v", locale, err)
		}
		for _, tool := range info.Tools {
			if _, lifecycle := lifecycleToolNames[tool.Name]; lifecycle {
				continue
			}
			fields := bknContextFields(t, tool.InputSchema)
			for _, field := range []string{
				"conversation_id", "interaction_id",
				"parent_operation_id", "causation_event_ids", "business_refs",
			} {
				if !slices.Contains(fields, field) {
					t.Fatalf("%s %s: info lost %s from the adapter contract: %v",
						locale, tool.Name, field, fields)
				}
			}
		}
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

func TestPublishedInputSchema(t *testing.T) {
	got := publishedInputSchema("t", json.RawMessage(`{"type":"object","properties":{"response_format":{"type":"string"},"kn_id":{"type":"string"},"limit":{"type":"integer","maximum":9007199254740993}},"required":["kn_id","response_format"]}`), compactHiddenInputFields)
	properties, required := schemaFields(t, got)
	if slices.Contains(properties, "response_format") || !slices.Equal(required, []string{"kn_id"}) {
		t.Fatalf("compact schema = %s", got)
	}
	if !json.Valid(got) || !strings.Contains(string(got), "9007199254740993") {
		t.Fatalf("a wide integer bound was rewritten: %s", got)
	}
	unchanged := json.RawMessage(`{"type":"object","properties":{"kn_id":{"type":"string"}}}`)
	if string(publishedInputSchema("t", unchanged, compactHiddenInputFields)) != string(unchanged) {
		t.Fatal("a schema without hidden fields should be published byte for byte")
	}
	if string(publishedInputSchema("t", json.RawMessage(`{`), compactHiddenInputFields)) != "{" {
		t.Fatal("an unreadable schema should be published unchanged")
	}
}

// The view runs when a tool is published, not when it is assembled. The
// Capability Profile digests, GET /mcp/info and the PTC stub are all computed
// from the assembled schema; moving the rewrite earlier would change every
// digest, and Trace Core compares those for equality, so replays of existing
// Operations would resolve as schema_digest_mismatch.
func TestTheModelContextViewIsAppliedOnlyOnPublish(t *testing.T) {
	published := listedTools(t, mustServer(newMCPServerForLocale(nil, defaultMCPLocale)))
	for _, tool := range assembledTools(t) {
		if _, lifecycle := lifecycleToolNames[tool.Name]; lifecycle {
			continue
		}
		if fields := bknContextFields(t, tool.RawInputSchema); !slices.Contains(fields, "business_refs") {
			t.Fatalf("%s: the assembled schema lost the adapter contract: %v", tool.Name, fields)
		}
		if len(published[tool.Name].InputSchema) >= len(tool.RawInputSchema) {
			t.Fatalf("%s: the published schema is not shorter than the assembled one", tool.Name)
		}
	}
}

func mustServer(srv *server.MCPServer, _ *toolBuilder) *server.MCPServer {
	return srv
}
