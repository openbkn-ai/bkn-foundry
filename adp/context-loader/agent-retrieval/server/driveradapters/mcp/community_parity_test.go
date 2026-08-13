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
	// 追踪生命周期工具（bkntrace 适配层直接挂到 server 上，不走 toolBuilder）
	"bkn_finish_interaction",
	"bkn_start_interaction",
	// 知识网络工具
	"describe_resource",
	"execute_action",
	"find_skills",
	"get_action_execution",
	"get_action_info",
	"get_kn_detail",
	"get_logic_properties_values",
	"get_object_types",
	"get_relation_types",
	"get_skill_content",
	"list_action_executions",
	"list_knowledge_networks",
	"list_resources",
	"list_skills",
	"query_instance_subgraph",
	"query_metric",
	"query_object_instance",
	"read_skill_file",
	"run_sql",
	"search_instance",
	"search_schema",
	// execute_skill 默认不装配（MCP_EXECUTE_SKILL_ENABLED），所以不在这份基线里，
	// 由 TestExecuteSkillOnlyAppearsWhenEnabled 单独盯。
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
	srv, _ := newMCPServer(nil)
	for _, key := range communityTools {
		if _, isLifecycle := lifecycleToolNames[key]; isLifecycle {
			// 生命周期工具由 bkntrace 适配层直接注册，schema 也来自那边，
			// 不经过插座的装饰路径——这条断言只管走 toolBuilder 的那些。
			continue
		}
		tool := srv.GetTool(key)
		if tool == nil {
			t.Fatalf("tool %q missing from the assembled server", key)
		}
		want, _ := bundle.ToolSchemas(key)
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
	// tools/list 的顺序来自 map 遍历，本身不确定，所以那一侧只能比集合；
	// /mcp/info 的顺序是按 key 稳定排出来的，逐位比才有意义——上面那段循环
	// 已经做了。这里补的是集合相等，两个方向都要。
	assembled := map[string]bool{}
	for _, name := range assembledNames(t) {
		assembled[name] = true
	}
	if len(assembled) != len(info.Tools) {
		t.Fatalf("/mcp/info 有 %d 个工具，tools/list 有 %d 个", len(info.Tools), len(assembled))
	}
	for _, tool := range info.Tools {
		if !assembled[tool.Name] {
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
	locale := loadMCPLocaleBundle(mcpLocaleFromEnv())
	for _, tool := range info.Tools {
		want, _ := tryLoadToolSchemas(locale, tool.Name)
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
	srv, b := newMCPServer(nil)

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
