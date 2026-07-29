// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
)

// These tests stand in for the enterprise code line: core cannot import ee, so
// the socket is exercised with fakes registered from here. What they assert is
// the wiring — that a registered tool reaches tools/list and /mcp/info, that a
// decorator's schema patch reaches both, and that the license still decides at
// call time.

// mutableGate is a license that a test can revoke mid-run.
type mutableGate struct{ on bool }

func (g *mutableGate) Enabled(extension.Feature) bool { return g.on }

func extraTool(key, name string) mcptool.ExtraTool {
	return mcptool.ExtraTool{
		Feature: extension.FeatureContextProbe,
		Key:     key,
		Name:    name,
		Desc:    "enterprise probe",
		Input:   json.RawMessage(`{"type":"object","properties":{"depth":{"type":"integer"}}}`),
		Output:  json.RawMessage(`{"type":"object"}`),
		Handle: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("probe result"), nil
		},
	}
}

// patchSearchSchema adds a property to search_schema's input schema, the way an
// enterprise decorator adds a paid parameter.
func patchSearchSchema(in json.RawMessage) json.RawMessage {
	var schema map[string]any
	if err := json.Unmarshal(in, &schema); err != nil {
		return in
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		schema["properties"] = props
	}
	props["probe_depth"] = map[string]any{"type": "integer"}
	out, err := json.Marshal(schema)
	if err != nil {
		return in
	}
	return out
}

func withEnterpriseSocket(t *testing.T, gate extension.Gate) {
	t.Helper()
	mcptool.ResetForTest(gate)
	t.Cleanup(func() {
		mcptool.ResetForTest(extension.GateFunc(func(extension.Feature) bool { return false }))
	})
}

func TestEnterpriseToolAppearsInToolsListAndInfo(t *testing.T) {
	withEnterpriseSocket(t, extension.GateFunc(func(extension.Feature) bool { return true }))
	mcptool.Register(extraTool("probe_context", "probe_context"))

	srv := newMCPServer()
	if srv.GetTool("probe_context") == nil {
		t.Fatal("enterprise tool missing from tools/list")
	}
	if len(srv.ListTools()) != len(communityTools)+1 {
		t.Fatalf("tools/list has %d tools, want the community set plus one", len(srv.ListTools()))
	}

	info, err := BuildMCPInfo("https://example.invalid/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	if info.ToolCount != len(communityTools)+1 {
		t.Fatalf("tool_count = %d, want %d", info.ToolCount, len(communityTools)+1)
	}
	var found bool
	names := make([]string, 0, len(info.Tools))
	for _, tool := range info.Tools {
		names = append(names, tool.Name)
		if tool.Name == "probe_context" {
			found = true
			if string(tool.InputSchema) != string(extraTool("probe_context", "probe_context").Input) {
				t.Fatal("enterprise tool's own schema should be advertised verbatim")
			}
		}
	}
	if !found {
		t.Fatalf("/mcp/info does not list the enterprise tool: %v", names)
	}
	// The catalogue is ordered by tool key, and the enterprise tool takes part
	// in that ordering rather than being appended at the end.
	if !sort.StringsAreSorted(names) {
		t.Fatalf("/mcp/info tools are out of order: %v", names)
	}
}

func TestDecoratorPatchesSchemaEverywhereItIsAdvertised(t *testing.T) {
	withEnterpriseSocket(t, extension.GateFunc(func(extension.Feature) bool { return true }))
	mcptool.Decorate(toolKeySearchSchema, mcptool.Decorator{
		Feature:    extension.FeatureContextProbe,
		PatchInput: patchSearchSchema,
		After: func(_ context.Context, _ mcp.CallToolRequest, res *mcp.CallToolResult) (*mcp.CallToolResult, error) {
			return res, nil
		},
	})

	tool := newMCPServer().GetTool(toolKeySearchSchema)
	if tool == nil {
		t.Fatal("search_schema missing")
	}
	if !strings.Contains(string(tool.Tool.RawInputSchema), "probe_depth") {
		t.Fatal("tools/list does not carry the decorator's added parameter")
	}

	// /mcp/info reads the embedded schemas, so it needs the same patch applied
	// or the two descriptions of the same tool disagree.
	info, err := BuildMCPInfo("https://example.invalid/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	for _, entry := range info.Tools {
		if entry.Name != toolKeySearchSchema {
			continue
		}
		if !strings.Contains(string(entry.InputSchema), "probe_depth") {
			t.Fatal("/mcp/info does not carry the decorator's added parameter")
		}
		return
	}
	t.Fatal("search_schema missing from /mcp/info")
}

func TestEnterpriseToolStopsServingWhenLicenseLapses(t *testing.T) {
	gate := &mutableGate{on: true}
	withEnterpriseSocket(t, gate)
	mcptool.Register(extraTool("probe_context", "probe_context"))

	tool := newMCPServer().GetTool("probe_context")
	if tool == nil {
		t.Fatal("enterprise tool missing")
	}

	res, err := tool.Handler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("licensed call failed: %v", err)
	}
	if res.IsError {
		t.Fatal("a licensed enterprise tool should serve")
	}

	// The license dies while the process keeps running. tools/list was fixed at
	// freeze time, so the tool stays listed and the call is what fails.
	gate.on = false
	res, err = tool.Handler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("refusal should come back as an error result, not a transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("an unlicensed enterprise tool call must fail")
	}
}

func TestEnterpriseToolCannotShadowACoreTool(t *testing.T) {
	withEnterpriseSocket(t, extension.GateFunc(func(extension.Feature) bool { return true }))
	// mcp-go would replace the core tool of the same name without a word, and
	// search_schema would vanish from tools/list. Assembly has to stop instead.
	mcptool.Register(extraTool("probe_context", toolKeySearchSchema))

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("assembling a tool name that collides with a core tool must panic")
		}
		if msg, _ := got.(string); !strings.Contains(msg, toolKeySearchSchema) {
			t.Fatalf("panic should name the contested tool, got %q", msg)
		}
	}()
	newMCPServer()
}
