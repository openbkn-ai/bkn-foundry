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
	// A call with no bkn_context now falls back to the MCP session, so the guard
	// answers about the Core it needs rather than the conversation the client
	// omitted. Either wording proves the gate handed the call on.
	if !strings.Contains(got, "conversation") && !strings.Contains(got, "BKN Trace Core") {
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

	// 企业工具那一半与 locale 无关：两侧用的都是 ee 自带的 t.Input，各自过一次
	// requireBKNContext。这条断言在任何 locale 下都必须成立，所以不钉环境。
	listed := listVisible(t)
	info, err := BuildMCPInfo("https://example.invalid/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	for _, extra := range mcptool.Extras() {
		tool, ok := listed[extra.Name]
		if !ok {
			t.Fatalf("tools/list 没有企业工具 %q", extra.Name)
		}
		var fromInfo json.RawMessage
		for _, e := range info.Tools {
			if e.Name == extra.Name {
				fromInfo = e.InputSchema
			}
		}
		if string(fromInfo) != string(tool.RawInputSchema) {
			t.Fatalf("企业工具 %q 的 input schema 两侧不一致：\n/mcp/info : %s\ntools/list: %s",
				extra.Name, fromInfo, tool.RawInputSchema)
		}
	}
}

func TestInfoAndToolsListAgreeOnEveryToolInTheDefaultLocale(t *testing.T) {
	// 钉 locale。tools/list 那侧的 core 工具会过 locale 覆盖层
	// （locale.go 的 schema_descriptions.json），/mcp/info 那侧永远读内嵌那份，
	// 所以「两侧逐字相等」只在覆盖层为空的默认 locale 下成立。不钉的话，
	// mcpLocaleFromEnv 会读 LANG——CI runner 上没有所以今天绿，开发机上
	// LANG=en_US.UTF-8 是常态，会红在一处与本改动无关的 core 工具上。
	//
	// 「/mcp/info 对 locale 失明」本身是本 PR 之前就有的缺口（连 Description 都
	// 取默认 tools_meta.json，而对外文档说随部署语言本地化），值得单开 follow-up；
	// 这里先把它的边界写清楚，免得这条注释成为仓里唯一记得它的地方。
	t.Setenv("MCP_LOCALE", "zh-CN")
	withSocket(t, entitlement.FixedGate(licverify.EditionEnterprise))
	mcptool.Register(extraTool("probe_context", "probe_context"))
	mcptool.Decorate(toolKeySearchSchema, searchSchemaDecorator())

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

func TestRefusalIsIndistinguishableFromAnUnknownTool(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionCommunity))
	mcptool.Register(extraTool("probe_context", "probe_context"))

	// 一个未授权的企业工具，对外必须和一个根本不存在的工具长得一模一样——
	// 文本和错误码都一样。否则调用方逐个猜名字就能把「社区版二进制」和
	// 「未授权的企业版二进制」区分开，而两者不可区分正是 assemble.go 开头
	// 写下的自我承诺。
	unknown := errorText(t, callTool(t, "probe_context_x", map[string]any{}))
	refused := errorText(t, callTool(t, "probe_context", map[string]any{}))
	if want := strings.Replace(unknown, "probe_context_x", "probe_context", 1); refused != want {
		t.Fatalf("未授权的企业工具与未知工具的文本不一致：\n未知  : %s\n未授权: %s", want, refused)
	}

	// 错误码曾经不一致：未知工具走 handleToolCall 内部的 INVALID_PARAMS
	// (-32602)，而中间件返回的 error 一律被包成 INTERNAL_ERROR(-32603)。
	// mcp-go v0.55.0 起 ToolFilterFunc 在 tools/call 时也执行，被过滤掉的工具
	// 由 handleToolCall 用同一条 format string、同一个 sentinel 直接拒掉，两侧
	// 因此对齐。
	//
	// 这条保证挂在 app.go 的 server.WithToolFilter(b.filter) 上：filter 现在
	// 同时是列表可见性和调用边界。谁把它摘了，这里就会红。
	unknownCode := callTool(t, "probe_context_x", map[string]any{}).(mcpsdk.JSONRPCError).Error.Code
	refusedCode := callTool(t, "probe_context", map[string]any{}).(mcpsdk.JSONRPCError).Error.Code
	if unknownCode != refusedCode {
		t.Fatalf("错误码把付费工具的存在泄露出去了：未知 %d，未授权 %d", unknownCode, refusedCode)
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
