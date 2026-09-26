// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// A retired tool has to be gone from every model-facing surface at once. It is
// withheld in one place (toolBuilder.filter) and left out of two others that do
// not go through it (/mcp/info and the gateway catalogue), so a surface that
// still offers one would be a surface someone forgot — which is exactly the
// failure this file is here to name.

func fullServer(t *testing.T, locale string) *server.MCPServer {
	t.Helper()
	srv, _ := newMCPServerForLocale(nil, locale)
	return srv
}

func TestRetiredToolsAreListedByNoProfile(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for name, srv := range map[string]*server.MCPServer{
			"full":    fullServer(t, locale),
			"compact": compactServer(t, locale),
		} {
			listed := listedToolNames(t, srv)
			for key := range retiredTools {
				if slices.Contains(listed, key) {
					t.Errorf("%s %s: tools/list still offers %s", locale, name, key)
				}
			}
		}
	}
}

// The refusal must be the one an unknown tool gets. Anything else — an
// INTERNAL_ERROR from a middleware, a business refusal with a receipt — tells a
// caller the tool is there and something else went wrong.
func TestRetiredToolCallsAreRefusedLikeUnknownOnes(t *testing.T) {
	srv := fullServer(t, "zh-CN")
	unknown, ok := rpc(t, srv, "tools/call", map[string]any{
		"name": "no_such_tool", "arguments": map[string]any{},
	}).(mcpsdk.JSONRPCError)
	if !ok {
		t.Fatal("unknown tool: expected a JSON-RPC error")
	}
	for key := range retiredTools {
		t.Run(key, func(t *testing.T) {
			got := rpc(t, srv, "tools/call", map[string]any{"name": key, "arguments": map[string]any{}})
			refused, ok := got.(mcpsdk.JSONRPCError)
			if !ok {
				t.Fatalf("expected a JSON-RPC error, got %#v", got)
			}
			if refused.Error.Code != unknown.Error.Code {
				t.Fatalf("code = %d, want the unknown-tool code %d", refused.Error.Code, unknown.Error.Code)
			}
			if want := strings.Replace(unknown.Error.Message, "no_such_tool", key, 1); refused.Error.Message != want {
				t.Fatalf("message = %q, want %q", refused.Error.Message, want)
			}
		})
	}
}

// The gateway is the compact profile's route to everything it does not publish,
// so a retired tool reachable through it would be retired in name only.
func TestRetiredToolsAreNotGatewayTargets(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		_, b := newMCPServerForLocale(nil, locale)
		catalog := newNativeCatalog(b)
		for key := range retiredTools {
			if slices.Contains(catalog.order, key) {
				t.Errorf("%s: %s is a gateway target", locale, key)
			}
			if _, _, ok := catalog.lookup(context.Background(), key); ok {
				t.Errorf("%s: %s resolves through the gateway", locale, key)
			}
		}
		// Naming one outright must not surface it either: search reads the
		// catalogue, and a name that used to rank first now matches nothing.
		for key := range retiredTools {
			result := catalog.search(context.Background(), "用 "+key+" 查一下", searchDefaultLimit)
			for _, candidate := range result.Candidates {
				if candidate.Name == key {
					t.Errorf("%s: search returned %s", locale, key)
				}
			}
		}
	}
}

// /mcp/info answers "what can I call without a handshake", and the sandbox
// toolkit is rendered from that same list — a retired tool left in it would put
// a function in the shipped stub that every call refuses.
func TestRetiredToolsAreAbsentFromInfoAndTheSandboxToolkit(t *testing.T) {
	info, err := BuildMCPInfo("https://example.invalid/mcp")
	if err != nil {
		t.Fatalf("BuildMCPInfo: %v", err)
	}
	for _, tool := range info.Tools {
		if _, gone := retiredTools[tool.Name]; gone {
			t.Errorf("/mcp/info still describes %s", tool.Name)
		}
	}
	toolkit, err := BuildPTCToolkit(ImageBuildEndpoint, ImageSandboxPort)
	if err != nil {
		t.Fatalf("BuildPTCToolkit: %v", err)
	}
	for key := range retiredTools {
		if strings.Contains(toolkit.Stub, "def "+key+"(") {
			t.Errorf("the sandbox stub still defines %s", key)
		}
		if strings.Contains(toolkit.Digest, key+"(") {
			t.Errorf("the run_code digest still advertises %s", key)
		}
	}
}

// The tools stay assembled: the capability manifest keeps describing them, a
// decorator registered against one still lands, and publishing them again is a
// line removed from retiredTools rather than a handler rebuilt.
func TestRetiredToolsRemainAssembled(t *testing.T) {
	_, b := newMCPServerForLocale(nil, "zh-CN")
	assembled := map[string]bool{}
	for _, p := range b.pending {
		assembled[p.tool.Name] = true
	}
	for key := range retiredTools {
		if !assembled[key] {
			t.Errorf("%s is no longer assembled: retirement is a filter, not a deletion", key)
		}
	}
	manifest := mustLoadCapabilityManifest()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for key := range retiredTools {
		if !strings.Contains(string(raw), key) {
			t.Errorf("the capability manifest dropped %s", key)
		}
	}
}
