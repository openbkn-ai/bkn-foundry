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
