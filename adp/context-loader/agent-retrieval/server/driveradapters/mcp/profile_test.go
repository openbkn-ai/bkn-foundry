// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// rpc sends one JSON-RPC request through the assembled server, filters and
// middlewares included, exactly as the HTTP transport would.
func rpc(t *testing.T, srv *server.MCPServer, method string, params map[string]any) mcpsdk.JSONRPCMessage {
	t.Helper()
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return srv.HandleMessage(context.Background(), req)
}

func resultOf(t *testing.T, msg mcpsdk.JSONRPCMessage, into any) {
	t.Helper()
	resp, ok := msg.(mcpsdk.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected a result, got %#v", msg)
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}

func listedToolNames(t *testing.T, srv *server.MCPServer) []string {
	t.Helper()
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	resultOf(t, rpc(t, srv, "tools/list", map[string]any{}), &result)
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func compactServer(t *testing.T, locale string) *server.MCPServer {
	t.Helper()
	srv, _ := newMCPServerForProfile(nil, locale, defaultPTCServicePort, compactProfile)
	return srv
}

// The list is written out rather than derived, so changing what the profile
// publishes has to change this test too.
func TestCompactProfilePublishesExactlyItsToolList(t *testing.T) {
	want := []string{
		"bkn_finish_interaction", "bkn_start_interaction",
		"describe_native_tool", "execute_native_read_tool",
		"get_kn_detail", "list_knowledge_networks",
		"query_metric", "query_object_instance",
		"search_instance", "search_native_tools", "search_schema",
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			srv := compactServer(t, locale)
			if got := listedToolNames(t, srv); !slices.Equal(got, want) {
				t.Fatalf("tools/list = %v, want %v", got, want)
			}
			// The list must not change from one request to the next.
			if again := listedToolNames(t, srv); !slices.Equal(again, want) {
				t.Fatalf("second tools/list = %v, want %v", again, want)
			}
		})
	}
}

func TestCompactProfileRefusesUnpublishedToolsLikeUnknownOnes(t *testing.T) {
	srv := compactServer(t, "zh-CN")
	unknown := rpc(t, srv, "tools/call", map[string]any{"name": "no_such_tool", "arguments": map[string]any{}})
	unknownErr, ok := unknown.(mcpsdk.JSONRPCError)
	if !ok {
		t.Fatalf("unknown tool: expected a JSON-RPC error, got %#v", unknown)
	}
	for _, hidden := range []string{toolKeyRunSQL, toolKeyGetObjectTypes, toolKeyExecuteAction, "run_code"} {
		t.Run(hidden, func(t *testing.T) {
			got := rpc(t, srv, "tools/call", map[string]any{"name": hidden, "arguments": map[string]any{}})
			refused, ok := got.(mcpsdk.JSONRPCError)
			if !ok {
				t.Fatalf("expected a JSON-RPC error, got %#v", got)
			}
			if refused.Error.Code != unknownErr.Error.Code {
				t.Fatalf("code = %d, want the unknown-tool code %d", refused.Error.Code, unknownErr.Error.Code)
			}
			if want := strings.Replace(unknownErr.Error.Message, "no_such_tool", hidden, 1); refused.Error.Message != want {
				t.Fatalf("message = %q, want %q", refused.Error.Message, want)
			}
		})
	}
}

func TestCompactProfileKeepsTheLifecycleGuard(t *testing.T) {
	srv := compactServer(t, "zh-CN")
	got := rpc(t, srv, "tools/call", map[string]any{
		"name":      toolKeySearchSchema,
		"arguments": map[string]any{"kn_id": "kn-1", "query": "orders"},
	})
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "conversation_required") {
		t.Fatalf("a business tool without bkn_context must be refused as on /mcp, got %s", raw)
	}
}

func TestProfilesServeTheirOwnInstructions(t *testing.T) {
	init := map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "profile-test", "version": "1"},
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			bundle := loadMCPLocaleBundle(locale)
			var compact, full struct {
				Instructions string `json:"instructions"`
			}
			resultOf(t, rpc(t, compactServer(t, locale), "initialize", init), &compact)
			fullSrv, _ := newMCPServerForLocale(nil, locale)
			resultOf(t, rpc(t, fullSrv, "initialize", init), &full)
			if compact.Instructions != bundle.CompactServerInstructions() {
				t.Fatal("compact profile does not serve the compact instructions")
			}
			if full.Instructions != bundle.ServerInstructions() {
				t.Fatal("full profile instructions changed")
			}
		})
	}
}

// The compact instructions may only name tools the compact profile publishes;
// naming any other tool sends the model to a tool it cannot call.
func TestCompactInstructionsNameOnlyPublishedTools(t *testing.T) {
	data, err := schemasFS.ReadFile("schemas/tools_meta.json")
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		text := loadMCPLocaleBundle(locale).CompactServerInstructions()
		for name := range meta {
			if _, published := compactProfile.published[name]; published {
				continue
			}
			pattern := regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(name) + `([^A-Za-z0-9_]|$)`)
			if pattern.MatchString(text) {
				t.Errorf("%s compact instructions name %s, which the profile does not publish", locale, name)
			}
		}
	}
}

func TestCompactInfoAgreesWithToolsList(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			info, err := BuildCompactMCPInfoForLocale("https://example.test/api/agent-retrieval/v1/mcp-compact", locale)
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, 0, len(info.Tools))
			for _, tool := range info.Tools {
				names = append(names, tool.Name)
			}
			slices.Sort(names)
			if want := listedToolNames(t, compactServer(t, locale)); !slices.Equal(names, want) {
				t.Fatalf("info tools = %v, tools/list = %v", names, want)
			}
			if info.ToolCount != len(info.Tools) {
				t.Fatalf("tool_count = %d, want %d", info.ToolCount, len(info.Tools))
			}
			if info.ToolkitVersion != "" {
				t.Fatalf("compact info must not carry a sandbox toolkit version, got %q", info.ToolkitVersion)
			}
		})
	}
}
