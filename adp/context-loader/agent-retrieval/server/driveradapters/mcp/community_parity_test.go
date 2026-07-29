// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
)

// TestMain points the config loader at the checked-in defaults. Assembling the
// tool surface constructs the services behind the tools, and those read the
// configuration on the way up; without this the loader panics on the absent
// /sysvol paths it expects in a container.
func TestMain(m *testing.M) {
	if os.Getenv("CONFIG_PROFILE") == "" {
		os.Setenv("CONFIG_PROFILE", "../../infra/config")
	}
	os.Exit(m.Run())
}

// communityTools is the tool surface a community binary advertises, written out
// rather than derived, so that this test fails when the surface changes instead
// of agreeing with whatever the code now does.
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
// nothing registered, nothing licensed.
func noExtensions(t *testing.T) {
	t.Helper()
	mcptool.ResetForTest(extension.GateFunc(func(extension.Feature) bool { return false }))
	t.Cleanup(func() {
		mcptool.ResetForTest(extension.GateFunc(func(extension.Feature) bool { return false }))
	})
}

func TestCommunityToolsListUnchanged(t *testing.T) {
	noExtensions(t)

	got := make([]string, 0)
	for name := range newMCPServer().ListTools() {
		got = append(got, name)
	}
	sort.Strings(got)

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

	// The decorator path runs for every tool; with nothing registered it has to
	// be the identity, byte for byte, or the community binary is shipping a
	// schema the socket touched.
	bundle := loadMCPLocaleBundle(mcpLocaleFromEnv())
	srv := newMCPServer()
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
	if len(info.Tools) != len(communityTools) {
		t.Fatalf("/mcp/info lists %d tools, want %d", len(info.Tools), len(communityTools))
	}
	for i, want := range communityTools {
		if info.Tools[i].Name != want {
			t.Fatalf("/mcp/info tool %d = %q, want %q", i, info.Tools[i].Name, want)
		}
	}

	// /mcp/info and tools/list have to agree; the whole point of the endpoint is
	// to describe the surface without a handshake.
	assembled := newMCPServer().ListTools()
	for _, tool := range info.Tools {
		if _, ok := assembled[tool.Name]; !ok {
			t.Fatalf("/mcp/info advertises %q, which tools/list does not have", tool.Name)
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
		if !json.Valid(tool.InputSchema) {
			t.Fatalf("tool %q has an invalid input schema", tool.Name)
		}
		if string(tool.InputSchema) != string(want) {
			t.Fatalf("tool %q input schema in /mcp/info was modified with no extension registered", tool.Name)
		}
	}
}
