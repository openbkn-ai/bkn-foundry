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

	"github.com/mark3labs/mcp-go/mcp"
)

var testContextArgument = map[string]any{"conversation_id": "conv-1", "interaction_id": "int-1"}

// A misspelt or hidden argument on the compact profile is refused before the
// guard, naming what is allowed; nothing reaches Trace.
func TestCompactProfileRefusesUnknownArguments(t *testing.T) {
	t.Setenv("CONFIG_PROFILE", "../../infra/config")
	client, ensured, _ := fakeTraceCore(t)
	srv, _ := newMCPServerForProfile(client, "zh-CN", defaultPTCServicePort, compactProfile)
	for _, arguments := range []map[string]any{
		{"kn_id": "kn_demo", "ot_id": "ot_order", "limt": 5, "bkn_context": testContextArgument},
		{"kn_id": "kn_demo", "ot_id": "ot_order", "response_format": "json", "bkn_context": testContextArgument},
	} {
		result := callTrustedTool(t, srv, toolKeyQueryObjectInstance, arguments)
		refusal := refusalOf(t, result)
		allowed, _ := refusal["allowed"].([]any)
		if refusal["error"] != refusalUnknownArguments || len(allowed) == 0 || !slices.Contains(allowed, any("bkn_context")) {
			t.Fatalf("refusal = %v", refusal)
		}
	}
	if got := ensured(); len(got) != 0 {
		t.Fatalf("refused calls reached Trace: %v", got)
	}
}

// Declared arguments pass the check and reach the guard.
func TestCompactProfileLetsDeclaredArgumentsThrough(t *testing.T) {
	t.Setenv("CONFIG_PROFILE", "../../infra/config")
	client, ensured, _ := fakeTraceCore(t)
	srv, _ := newMCPServerForProfile(client, "zh-CN", defaultPTCServicePort, compactProfile)
	result := callTrustedTool(t, srv, toolKeySearchNativeTools, map[string]any{"query": "子图", "limit": 2, "bkn_context": testContextArgument})
	if result.IsError {
		t.Fatalf("declared arguments were refused: %s", resultText(result))
	}
	if got := ensured(); !slices.Equal(got, []string{toolKeySearchNativeTools}) {
		t.Fatalf("Operations = %v", got)
	}
}

func TestCompactProfileIgnoresLegacyObjectPosition(t *testing.T) {
	input, _ := loadToolSchemas(toolKeyQueryObjectInstance)
	handler := compactArgumentsMiddleware(func(_ context.Context, name string) (mcp.Tool, bool) {
		return mcp.Tool{Name: name, RawInputSchema: input}, true
	})(func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw := string(req.Params.RawArguments)
		if _, present := req.GetArguments()["search_after"]; present || strings.Contains(raw, `"search_after"`) ||
			!strings.Contains(raw, "9007199254740993") || !strings.Contains(raw, `"cursor":"token"`) {
			t.Fatalf("unexpected arguments: %s", raw)
		}
		return mcp.NewToolResultText("continued"), nil
	})
	req := mcp.CallToolRequest{}
	req.Params.Name = toolKeyQueryObjectInstance
	req.Params.RawArguments = json.RawMessage(`{"kn_id":"kn","ot_id":"ot","paging_mode":"cursor","cursor":"token","search_after":null,"condition":{"value":9007199254740993}}`)
	if err := json.Unmarshal(req.Params.RawArguments, &req.Params.Arguments); err != nil {
		t.Fatal(err)
	}
	result, err := handler(t.Context(), req)
	if err != nil || result == nil || result.IsError || resultText(result) != "continued" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

// The full profile keeps its tolerant contract.
func TestFullProfileStillIgnoresUnknownArguments(t *testing.T) {
	full, _ := newMCPServerForLocale(nil, "zh-CN")
	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": toolKeyQueryObjectInstance, "arguments": map[string]any{"limt": 5}},
	})
	raw, err := json.Marshal(full.HandleMessage(context.Background(), req))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, refusalUnknownArguments) || !strings.Contains(text, "conversation_required") {
		t.Fatalf("full profile answered %s", text)
	}
}

func TestExecutorRefusesUnknownTargetArguments(t *testing.T) {
	h := newExecutorHarness(t)
	refusal := refusalOf(t, h.call(t, `{"name":"list_action_executions","arguments":{"kn_id":"kn_demo","offest":20},"bkn_context":`+testBKNContext+`}`))
	unknown, _ := refusal["unknown"].([]any)
	if refusal["error"] != refusalUnknownArguments || !slices.Equal(unknown, []any{"offest"}) {
		t.Fatalf("refusal = %v", refusal)
	}
	if len(h.calls) != 0 || len(h.intents) != 0 {
		t.Fatal("a call with an unknown argument reached the guard or the target")
	}
}

func TestCheckArgumentNames(t *testing.T) {
	schema := json.RawMessage(`{"properties":{"kn_id":{},"limit":{}}}`)
	if err := checkArgumentNames("t", schema, []string{"kn_id", "limit"}); err != nil {
		t.Fatalf("declared names refused: %v", err)
	}
	err := checkArgumentNames("t", schema, []string{"limt", "kn_id", "offest"})
	refusal, ok := err.(*unknownArgumentsRefusal)
	if !ok || !slices.Equal(refusal.Unknown, []string{"limt", "offest"}) || !slices.Equal(refusal.Allowed, []string{"kn_id", "limit"}) {
		t.Fatalf("refusal = %#v", err)
	}
	if checkArgumentNames("t", json.RawMessage(`{"type":"object"}`), []string{"anything"}) != nil {
		t.Fatal("a schema without properties declares nothing to check against")
	}
}
