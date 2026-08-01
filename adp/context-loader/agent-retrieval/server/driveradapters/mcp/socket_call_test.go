// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

// The tests in socket_test.go call mcptool.Gated directly, which is why they
// never noticed that this service wraps every tool in a lifecycle middleware.
// A tool handler is the innermost layer: whatever the service installs around
// it answers first. These go through the real tools/call path instead.

// callTool drives one tools/call through the assembled server — middlewares
// included — and returns the JSON-RPC reply.
func callTool(t *testing.T, name string, args map[string]any) mcpsdk.JSONRPCMessage {
	t.Helper()
	srv, _ := newMCPServer(nil)

	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return srv.HandleMessage(context.Background(), req)
}

func errorText(t *testing.T, msg mcpsdk.JSONRPCMessage) string {
	t.Helper()
	if e, ok := msg.(mcpsdk.JSONRPCError); ok {
		return e.Error.Message
	}
	raw, _ := json.Marshal(msg)
	return string(raw)
}

func TestUnlicensedEnterpriseToolCallLooksLikeAnUnknownTool(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionCommunity))
	mcptool.Register(extraTool("probe_context", "probe_context"))

	got := errorText(t, callTool(t, "probe_context", map[string]any{}))

	// The point of the whole exercise: a community binary does not have this
	// tool, and an under-licensed enterprise binary must be indistinguishable.
	if !strings.Contains(got, "not found") {
		t.Fatalf("reply = %q, want it to read like an unknown tool", got)
	}
	// The failure this test was written for. Without a licence gate ahead of the
	// lifecycle middleware, the guard answers first and hands back a structured
	// "conversation_required / required_action" — which no community binary
	// would ever produce for a name it has never heard of.
	for _, leak := range []string{"conversation_required", "required_action", "interaction", "licen", "edition"} {
		if strings.Contains(strings.ToLower(got), leak) {
			t.Fatalf("reply leaks that a paid surface exists (%q): %s", leak, got)
		}
	}
}

func TestLicensedEnterpriseToolCallReachesTheLifecycleGuard(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionEnterprise))
	mcptool.Register(extraTool("probe_context", "probe_context"))

	got := errorText(t, callTool(t, "probe_context", map[string]any{}))

	// Licensed, so the gate lets it through and the service's own guard takes
	// over — an enterprise tool is a business tool by this service's definition
	// and has to satisfy the same lifecycle contract. What matters here is that
	// the answer is now *different* from the unlicensed one: the gate is not
	// swallowing calls it should pass on.
	if strings.Contains(got, "not found") {
		t.Fatalf("a licensed tool must not be refused as unknown: %s", got)
	}
	if !strings.Contains(got, "conversation") {
		t.Fatalf("expected the lifecycle guard to answer, got %s", got)
	}
}

func TestEnterpriseToolAdvertisesTheContextTheGuardDemands(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionEnterprise))
	mcptool.Register(extraTool("probe_context", "probe_context"))

	// ee supplies its own schema and has no reason to know this service wraps
	// business tools in a lifecycle guard. If the advertised schema omitted
	// bkn_context, a caller following it would be rejected before the tool ever
	// ran — the tool would be licensed and unusable.
	tool, ok := listVisible(t)["probe_context"]
	if !ok {
		t.Fatal("a licensed enterprise tool should be listed")
	}
	if !strings.Contains(string(tool.RawInputSchema), "bkn_context") {
		t.Fatalf("enterprise tool schema omits what the guard demands: %s", tool.RawInputSchema)
	}
}

func TestCoreToolCallIsUnaffectedByTheGate(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionCommunity))

	// The gate consults a snapshot of enterprise tools only; a core tool must
	// take exactly the path it took before the socket existed.
	got := errorText(t, callTool(t, toolKeyRunSQL, map[string]any{}))
	if strings.Contains(got, "not found") {
		t.Fatalf("a core tool must not be refused as unknown: %s", got)
	}
}

func TestInfoAndToolsListAgreeOnEnterpriseSchemas(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionEnterprise))
	mcptool.Register(extraTool("probe_context", "probe_context"))
	mcptool.Decorate(toolKeySearchSchema, searchSchemaDecorator())

	// 两侧是两份实现：tools/list 走 toolBuilder，/mcp/info 遍历 tools_meta。
	// 合并成一份的代价过高（BuildMCPInfo 每请求调用、拿不到 builder），所以改用
	// 一条把它们钉在一起的用例——有扩展且已授权时，每个工具的 input schema 必须
	// 逐字相等。少了这条，两边只要有一侧漏施加什么，就会长期无声分叉。
	listed := listVisible(t)
	info, err := BuildMCPInfo("https://example.invalid/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	if len(info.Tools) != len(listed) {
		t.Fatalf("/mcp/info 有 %d 个工具，tools/list 有 %d 个", len(info.Tools), len(listed))
	}
	for _, entry := range info.Tools {
		tool, ok := listed[entry.Name]
		if !ok {
			t.Fatalf("/mcp/info advertises %q, which tools/list does not have", entry.Name)
		}
		if string(entry.InputSchema) != string(tool.RawInputSchema) {
			t.Fatalf("工具 %q 的 input schema 两侧不一致：\n/mcp/info : %s\ntools/list: %s",
				entry.Name, entry.InputSchema, tool.RawInputSchema)
		}
	}
}

func TestEnterpriseToolCannotShadowACoreToolKey(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionEnterprise))
	// Name 不撞、Key 撞。/mcp/info 按 key 排序，两个条目共用一个 key 会在两次
	// 进程之间换位置——claimName 的第二道查重就是为这个，之前没有用例走过它。
	shadow := extraTool(toolKeyRunSQL, "ee_run_sql")
	mcptool.Register(shadow)

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("撞 key 必须在装配期 panic")
		}
		if msg, _ := got.(string); !strings.Contains(msg, "tool key") {
			t.Fatalf("panic should name the contested key, got %q", msg)
		}
	}()
	newMCPServer(nil)
}
