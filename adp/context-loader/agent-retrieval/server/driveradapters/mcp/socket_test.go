// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

// These tests stand in for the enterprise code line: core cannot import ee, so
// the socket is exercised with fakes registered from here. What they assert is
// the wiring — that a registered tool reaches tools/list and /mcp/info when the
// licence covers it, disappears when it does not, and that the schema patch
// follows the same rule.

// mutableGate is a licence a test can revoke mid-run.
type mutableGate struct{ ed licverify.Edition }

func (g *mutableGate) Snapshot() entitlement.Snapshot {
	return entitlement.Snapshot{
		Licensed: g.ed != licverify.EditionCommunity,
		Edition:  g.ed,
	}
}

func extraTool(key, name string) mcptool.ExtraTool {
	return mcptool.ExtraTool{
		Capability: "context_probe",
		MinEdition: licverify.EditionEnterprise,
		Key:        key,
		Name:       name,
		Desc:       "enterprise probe",
		Input:      json.RawMessage(`{"type":"object","properties":{"depth":{"type":"integer"}}}`),
		Output:     json.RawMessage(`{"type":"object"}`),
		Handle: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("probe result"), nil
		},
	}
}

// patchSearchSchema adds a property the way an enterprise decorator adds a paid
// parameter.
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

func searchSchemaDecorator() mcptool.Decorator {
	return mcptool.Decorator{
		Capability: "context_probe",
		MinEdition: licverify.EditionEnterprise,
		PatchInput: patchSearchSchema,
		After: func(_ context.Context, _ mcp.CallToolRequest, res *mcp.CallToolResult) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("core+enterprise"), nil
		},
	}
}

func withSocket(t *testing.T, gate entitlement.Gate) {
	t.Helper()
	mcptool.ResetForTest(gate)
	t.Cleanup(func() { mcptool.ResetForTest(entitlement.FixedGate(licverify.EditionCommunity)) })
}

func listVisible(t *testing.T) map[string]mcp.Tool {
	t.Helper()
	srv, b := newMCPServer()
	all := make([]mcp.Tool, 0)
	for _, st := range srv.ListTools() {
		all = append(all, st.Tool)
	}
	out := map[string]mcp.Tool{}
	for _, tool := range b.filter(context.Background(), all) {
		out[tool.Name] = tool
	}
	return out
}

func TestEnterpriseToolIsInvisibleWithoutTheLicence(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionCommunity))
	mcptool.Register(extraTool("probe_context", "probe_context"))

	// Registration is unconditional, so the tool IS assembled…
	if len(mcptool.Extras()) != 1 {
		t.Fatal("the tool should be registered regardless of the licence")
	}
	// …but an under-licensed binary must look exactly like a community one.
	if _, seen := listVisible(t)["probe_context"]; seen {
		t.Fatal("an unlicensed enterprise tool must not appear in tools/list")
	}

	info, err := BuildMCPInfo("https://example.invalid/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	if info.ToolCount != len(communityTools) {
		t.Fatalf("tool_count = %d, want the community count %d", info.ToolCount, len(communityTools))
	}
}

func TestEnterpriseToolAppearsWithTheLicence(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionEnterprise))
	mcptool.Register(extraTool("probe_context", "probe_context"))

	if _, seen := listVisible(t)["probe_context"]; !seen {
		t.Fatal("a licensed enterprise tool should be listed")
	}

	info, err := BuildMCPInfo("https://example.invalid/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	if info.ToolCount != len(communityTools)+1 {
		t.Fatalf("tool_count = %d, want community+1", info.ToolCount)
	}
	var names []string
	for _, tool := range info.Tools {
		names = append(names, tool.Name)
	}
	if !sortedStrings(names) {
		t.Fatalf("/mcp/info tools are out of order: %v", names)
	}
}

func TestIndustryLicenceInheritsEnterpriseTools(t *testing.T) {
	// The ladder is ordered: paying more must never mean seeing less.
	withSocket(t, entitlement.FixedGate(licverify.EditionIndustry))
	mcptool.Register(extraTool("probe_context", "probe_context"))

	if _, seen := listVisible(t)["probe_context"]; !seen {
		t.Fatal("an industry licence must cover enterprise capabilities")
	}
}

func TestDecoratorSchemaFollowsTheLicence(t *testing.T) {
	gate := &mutableGate{ed: licverify.EditionCommunity}
	withSocket(t, gate)
	mcptool.Decorate(toolKeySearchSchema, searchSchemaDecorator())

	// Unlicensed: the paid parameter must not be advertised, or the community
	// parity promise is broken by a schema rather than by a tool.
	if s := string(listVisible(t)[toolKeySearchSchema].RawInputSchema); strings.Contains(s, "probe_depth") {
		t.Fatal("an unlicensed binary advertised the paid parameter")
	}
	info, _ := BuildMCPInfo("https://example.invalid/mcp")
	for _, tool := range info.Tools {
		if tool.Name == toolKeySearchSchema && strings.Contains(string(tool.InputSchema), "probe_depth") {
			t.Fatal("/mcp/info advertised the paid parameter while unlicensed")
		}
	}

	// Licensed: it appears in both places, without a restart.
	gate.ed = licverify.EditionEnterprise
	if s := string(listVisible(t)[toolKeySearchSchema].RawInputSchema); !strings.Contains(s, "probe_depth") {
		t.Fatal("tools/list does not carry the decorator's added parameter")
	}
	info, _ = BuildMCPInfo("https://example.invalid/mcp")
	found := false
	for _, tool := range info.Tools {
		if tool.Name == toolKeySearchSchema {
			found = strings.Contains(string(tool.InputSchema), "probe_depth")
		}
	}
	if !found {
		t.Fatal("/mcp/info does not carry the decorator's added parameter")
	}
}

func TestEnterpriseToolCallIsRefusedAsUnknown(t *testing.T) {
	gate := &mutableGate{ed: licverify.EditionEnterprise}
	withSocket(t, gate)
	tool := extraTool("probe_context", "probe_context")
	mcptool.Register(tool)

	h := mcptool.Gated(tool)
	res, err := h(context.Background(), mcp.CallToolRequest{})
	if err != nil || res == nil {
		t.Fatalf("licensed call failed: %v", err)
	}

	// The licence lapses under a running process. The catalogue stops showing
	// the tool, and a client that calls it anyway is answered the way an
	// unknown tool is answered — a community binary genuinely does not have it.
	gate.ed = licverify.EditionCommunity
	res, err = h(context.Background(), mcp.CallToolRequest{})
	if err == nil {
		t.Fatal("an unlicensed enterprise tool call must fail")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("refusal should read like an unknown tool, got %q", err)
	}
	if strings.Contains(err.Error(), "licen") || strings.Contains(err.Error(), "edition") {
		t.Fatalf("the refusal must not advertise that a paid surface exists: %q", err)
	}
}

func TestDecoratorStopsProcessingWhenTheLicenceLapses(t *testing.T) {
	gate := &mutableGate{ed: licverify.EditionEnterprise}
	withSocket(t, gate)
	mcptool.Decorate(toolKeySearchSchema, searchSchemaDecorator())

	d, ok := mcptool.DecoratorFor(toolKeySearchSchema)
	if !ok {
		t.Fatal("decorator should be registered")
	}
	core := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("core"), nil
	}
	h := d.Wrap(core)

	if got := textOf(h(context.Background(), mcp.CallToolRequest{})); got != "core+enterprise" {
		t.Fatalf("licensed call = %q, want the enterprise-processed result", got)
	}

	// A decorator degrades silently rather than failing the tool: core's result
	// is complete and useful on its own, and taking a community capability down
	// because the paid layer went away would be worse than losing the layer.
	gate.ed = licverify.EditionCommunity
	if got := textOf(h(context.Background(), mcp.CallToolRequest{})); got != "core" {
		t.Fatalf("unlicensed call = %q, want core's untouched result", got)
	}
}

func TestDecoratorSkipsFailedCoreResults(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionEnterprise))
	mcptool.Decorate(toolKeySearchSchema, searchSchemaDecorator())
	d, _ := mcptool.DecoratorFor(toolKeySearchSchema)

	// mcp-go reports tool-level failures in the result's IsError flag, not in
	// err. Appending enterprise content to a failed result yields a response
	// that is both isError and full of enterprise output — a client reading the
	// last block treats the failure as success. Found by calling the real
	// thing; no unit test would have thought of it.
	failing := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("backend unreachable"), nil
	}
	res, err := d.Wrap(failing)(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("the failure flag must survive")
	}
	if strings.Contains(textOf(res, err), "enterprise") {
		t.Fatal("enterprise content was appended to a failed result")
	}
}

func TestEnterpriseToolCannotShadowACoreTool(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionEnterprise))
	// mcp-go's AddTool replaces a same-named tool without a word, so search_schema
	// would vanish from tools/list. Assembly has to stop instead.
	mcptool.Register(extraTool("probe_context", toolKeySearchSchema))

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("a tool name that collides with a core tool must panic at assembly")
		}
		if msg, _ := got.(string); !strings.Contains(msg, toolKeySearchSchema) {
			t.Fatalf("panic should name the contested tool, got %q", msg)
		}
	}()
	newMCPServer()
}

func textOf(res *mcp.CallToolResult, _ error) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}
