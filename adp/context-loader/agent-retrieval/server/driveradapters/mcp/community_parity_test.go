// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

// TestMain points the config loader at the checked-in defaults. Assembling the
// tool surface constructs the services behind the tools, and those read the
// configuration on the way up; without this the loader panics on the /sysvol
// paths it expects inside a container.
func TestMain(m *testing.M) {
	if os.Getenv("CONFIG_PROFILE") == "" {
		os.Setenv("CONFIG_PROFILE", "../../infra/config")
	}
	os.Exit(m.Run())
}

// communityTools is the tool surface a community binary advertises, written out
// rather than derived, so this test fails when the surface changes instead of
// agreeing with whatever the code now does.
//
// This is the baseline the enterprise socket must not disturb. Adding a tool to
// core means adding it here in the same change; a diff that touches only the
// socket and moves this list is the bug this file exists to catch.
var communityTools = []string{
	"describe_resource",
	"execute_action",
	"find_skills",
	"get_action_execution",
	"get_action_info",
	"get_kn_detail",
	"get_logic_properties_values",
	"get_object_types",
	"get_relation_types",
	"list_action_executions",
	"list_knowledge_networks",
	"list_resources",
	"query_instance_subgraph",
	"query_object_instance",
	"run_sql",
	"search_schema",
}

// noExtensions puts the process in the state a community binary is always in:
// nothing registered, no licence.
func noExtensions(t *testing.T) {
	t.Helper()
	mcptool.ResetForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(func() { mcptool.ResetForTest(entitlement.FixedGate(licverify.EditionCommunity)) })
}

func TestCommunityToolsListUnchanged(t *testing.T) {
	noExtensions(t)

	got := assembledNames(t)
	if len(got) != len(communityTools) {
		t.Fatalf("tools/list has %d tools, want %d: %v", len(got), len(communityTools), got)
	}
	for i := range communityTools {
		if got[i] != communityTools[i] {
			t.Fatalf("tools/list = %v, want %v", got, communityTools)
		}
	}
}

func TestCommunityToolSchemasUnchanged(t *testing.T) {
	noExtensions(t)

	// With nothing registered the decorator path is the identity, byte for
	// byte, or the community binary is shipping a schema the socket touched.
	bundle := loadMCPLocaleBundle(mcpLocaleFromEnv())
	srv, _ := newMCPServer()
	for _, key := range communityTools {
		tool := srv.GetTool(key)
		if tool == nil {
			t.Fatalf("tool %q missing from the assembled server", key)
		}
		want, _ := bundle.ToolSchemas(key)
		if key == toolKeyGetObjectTypes || key == toolKeyGetRelationTypes {
			// These two still read the embedded schemas directly; see
			// toolBuilder.addEmbedded.
			want, _ = loadToolSchemas(key)
		}
		if string(tool.Tool.RawInputSchema) != string(want) {
			t.Fatalf("tool %q input schema was modified with no extension registered", key)
		}
	}
}

func TestCommunityMCPInfoUnchanged(t *testing.T) {
	noExtensions(t)

	info, err := BuildMCPInfo("https://example.invalid/api/agent-retrieval/v1/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	if info.ToolCount != len(communityTools) {
		t.Fatalf("tool_count = %d, want %d", info.ToolCount, len(communityTools))
	}
	for i, want := range communityTools {
		if info.Tools[i].Name != want {
			t.Fatalf("/mcp/info tool %d = %q, want %q", i, info.Tools[i].Name, want)
		}
	}

	// /mcp/info and tools/list have to agree: the endpoint exists so an
	// integrator can read the surface without a handshake.
	assembled := assembledNames(t)
	for i, name := range assembled {
		if info.Tools[i].Name != name {
			t.Fatalf("/mcp/info and tools/list disagree at %d: %q vs %q", i, info.Tools[i].Name, name)
		}
	}
}

func TestCommunityMCPInfoSchemasComeFromEmbeddedFiles(t *testing.T) {
	noExtensions(t)

	info, err := BuildMCPInfo("https://example.invalid/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	for _, tool := range info.Tools {
		want, _ := tryLoadToolSchemas(tool.Name)
		if len(want) == 0 {
			continue
		}
		if string(tool.InputSchema) != string(want) {
			t.Fatalf("tool %q input schema in /mcp/info was modified with no extension registered", tool.Name)
		}
	}
}

// assembledNames returns the sorted names a client would see from tools/list.
func assembledNames(t *testing.T) []string {
	t.Helper()
	srv, b := newMCPServer()

	all := make([]mcp.Tool, 0, len(srv.ListTools()))
	for _, st := range srv.ListTools() {
		all = append(all, st.Tool)
	}
	visible := b.filter(context.Background(), all)

	names := make([]string, 0, len(visible))
	for _, tool := range visible {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}
