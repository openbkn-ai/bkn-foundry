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

	// The half of the enterprise tools has nothing to do with locale: both sides use t.Input that comes with ee, and each passes it once.
	// offerBKNContext. This assertion must be true in any locale, so it is not specific to the environment.
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
	// Pin locale. The core tools on the tools/list side will go through the locale overlay.
	// (schema_descriptions.json of locale.go), the /mcp/info side always reads the embedded copy,
	// So "both sides are literally equal" only holds true in the default locale where the overlay is empty. If not nailed,
	// mcpLocaleFromEnv can read LANG - it is not available on CI runner, so it is green today, on the development machine.
	// LANG=en_US.UTF-8 is the norm and will be red on a core tool that has nothing to do with this change.
	//
	// "/mcp/info is blind to the locale" itself is a gap that existed before this PR (even the Description.
	// Take the default tools_meta.json, and the external documentation says that it will be localized with the deployment language), it is worth opening follow-up separately;
	// Let’s first write down its boundaries clearly, lest this comment become the only place where it is remembered in the repo.
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

	// An unauthorized enterprise tool must look exactly like a non-existent tool to the outside world——.
	// The text and error code are the same. Otherwise, the caller can guess the names one by one and combine the "community version binary" and.
	// "Unauthorized Enterprise Edition Binaries" are distinguished, and the indistinguishability between the two is precisely the beginning of assemble.go.
	// Written self-commitment.
	unknown := errorText(t, callTool(t, "probe_context_x", map[string]any{}))
	refused := errorText(t, callTool(t, "probe_context", map[string]any{}))
	if want := strings.Replace(unknown, "probe_context_x", "probe_context", 1); refused != want {
		t.Fatalf("未授权的企业工具与未知工具的文本不一致：\n未知  : %s\n未授权: %s", want, refused)
	}

	// The error code has been inconsistent: Unknown tool goes to INVALID_PARAMS inside handleToolCall.
	// (-32602), and the error returned by the middleware is always packaged as INTERNAL_ERROR (-32603).
	// Starting from mcp-go v0.55.0, ToolFilterFunc is also executed when tools/call, and the tools are filtered out.
	// The handleToolCall uses the same format string and the same sentinel to reject it directly. Both sides.
	// Hence the alignment.
	//
	// This guarantee is hung on server.WithToolFilter(b.filter) in app.go: filter now.
	// Both list visibility and call boundaries. Whoever picks it will make it red.
	unknownCode := callTool(t, "probe_context_x", map[string]any{}).(mcpsdk.JSONRPCError).Error.Code
	refusedCode := callTool(t, "probe_context", map[string]any{}).(mcpsdk.JSONRPCError).Error.Code
	if unknownCode != refusedCode {
		t.Fatalf("错误码把付费工具的存在泄露出去了：未知 %d，未授权 %d", unknownCode, refusedCode)
	}
}

func TestEnterpriseToolCannotShadowACoreToolKey(t *testing.T) {
	withSocket(t, entitlement.FixedGate(licverify.EditionEnterprise))
	// Name does not collide, Key does. /mcp/info is sorted by key. Two entries sharing a key will be displayed twice.
	// Changing positions between processes - the second duplication check of claimName is for this, and no use case has gone through it before.
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
