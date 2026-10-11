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
