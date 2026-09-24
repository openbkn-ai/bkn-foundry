// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const testBKNContext = `{"conversation_id":"conv-1","interaction_id":"int-1"}`

// executorHarness runs the executor over the real catalogue with recording
// fakes for the target handlers and the lifecycle guard.
type executorHarness struct {
	executor *nativeExecutor
	intents  []operationIntent
	calls    []mcpsdk.CallToolRequest
	reply    *mcpsdk.CallToolResult
}

func newExecutorHarness(t *testing.T) *executorHarness {
	t.Helper()
	h := &executorHarness{reply: mcpsdk.NewToolResultText("target ran")}
	catalog := catalogForLocale(t, "zh-CN")
	for name, target := range catalog.targets {
		target.handler = func(_ context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			h.calls = append(h.calls, req)
			// A handler builds a new result per call; the executor may annotate it.
			reply := *h.reply
			return &reply, nil
		}
		catalog.targets[name] = target
	}
	ensure := func(_ context.Context, intent operationIntent) (*operationResult, *lifecycleError, error) {
		h.intents = append(h.intents, intent)
		return &operationResult{Execute: true}, nil, nil
	}
	h.executor = &nativeExecutor{
		catalog: catalog,
		wrap: func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
			return guardBusinessToolCallWithCompletion(ensure, nil, nil, next)
		},
	}
	return h
}

func (h *executorHarness) call(t *testing.T, arguments string) *mcpsdk.CallToolResult {
	t.Helper()
	req := mcpsdk.CallToolRequest{}
	req.Params.Name = toolKeyExecuteNativeTool
	req.Params.RawArguments = json.RawMessage(arguments)
	if err := json.Unmarshal(req.Params.RawArguments, &req.Params.Arguments); err != nil {
		t.Fatal(err)
	}
	result, err := h.executor.handle(context.Background(), req)
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	return result
}

func resultText(result *mcpsdk.CallToolResult) string {
	var sb strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(mcpsdk.TextContent); ok {
			sb.WriteString(text.Text)
		}
	}
	return sb.String()
}

func refusalOf(t *testing.T, result *mcpsdk.CallToolResult) map[string]any {
	t.Helper()
	if !result.IsError {
		t.Fatalf("want a refusal, got %s", resultText(result))
	}
	var refusal map[string]any
	if err := json.Unmarshal([]byte(resultText(result)), &refusal); err != nil {
		t.Fatalf("refusal is not JSON: %s", resultText(result))
	}
	return refusal
}

// The target is governed once, under its own name, with the network taken
// from the target's arguments; the executor itself leaves no Operation.
func TestExecutorGuardsTheTargetOnceUnderItsOwnName(t *testing.T) {
	h := newExecutorHarness(t)
	result := h.call(t, `{"name":"get_object_types","arguments":{"kn_id":"kn_demo","ids":["ot_order"]},"bkn_context":`+testBKNContext+`}`)
	if result.IsError || resultText(result) != "target ran" {
		t.Fatalf("result = %+v", result)
	}
	if len(h.intents) != 1 || h.intents[0].ToolName != toolKeyGetObjectTypes || h.intents[0].KnowledgeNetworkID != "kn_demo" {
		t.Fatalf("guard intents = %+v, want one for get_object_types on kn_demo", h.intents)
	}
	if len(h.calls) != 1 || h.calls[0].Params.Name != toolKeyGetObjectTypes {
		t.Fatalf("target calls = %+v", h.calls)
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(h.calls[0].Params.RawArguments, &inner); err != nil {
		t.Fatal(err)
	}
	if !sameJSON(inner["bkn_context"], json.RawMessage(testBKNContext)) || string(inner["kn_id"]) != `"kn_demo"` {
		t.Fatalf("inner arguments = %s", h.calls[0].Params.RawArguments)
	}
}

func TestExecutorPassesWideIntegersThroughUnrounded(t *testing.T) {
	h := newExecutorHarness(t)
	h.call(t, `{"name":"query_instance_subgraph","bkn_context":`+testBKNContext+`,"arguments":{"kn_id":"kn_demo","relation_type_paths":[{
		"object_types":[{"id":"ot_order","condition":{"operation":"==","field":"order_no","value_from":"const","value":9007199254740993},"limit":10},
		                {"id":"ot_item","condition":{"operation":"and","sub_conditions":[]},"limit":10}],
		"relation_types":[{"relation_type_id":"rt_order_item"}]}]}}`)
	if len(h.calls) != 1 {
		t.Fatal("target did not run")
	}
	if !strings.Contains(string(h.calls[0].Params.RawArguments), "9007199254740993") {
		t.Fatalf("wide integer was rewritten: %s", h.calls[0].Params.RawArguments)
	}
}

// A failed validation names the fields and hands back the schema, and nothing
// reaches the guard or the target.
func TestExecutorRefusesInvalidArgumentsWithTheSchema(t *testing.T) {
	h := newExecutorHarness(t)
	refusal := refusalOf(t, h.call(t, `{"name":"get_action_info","arguments":{"kn_id":"kn_demo"},"bkn_context":`+testBKNContext+`}`))
	if refusal["error"] != refusalInvalidArguments || refusal["arguments_schema"] == nil {
		t.Fatalf("refusal = %v", refusal)
	}
	violations, _ := refusal["violations"].([]any)
	if len(violations) == 0 || !strings.Contains(resultText(h.call(t, `{"name":"get_action_info","arguments":{"kn_id":"kn_demo"},"bkn_context":`+testBKNContext+`}`)), "at_id") {
		t.Fatalf("violations = %v, want the missing at_id named", violations)
	}
	if len(h.intents) != 0 || len(h.calls) != 0 {
		t.Fatalf("an invalid call reached the guard (%d) or the target (%d)", len(h.intents), len(h.calls))
	}
}

// describe_native_tool refuses a schema over maxExecutableSchemaChars; a failed
// call must not hand back more than that. The violations still name the field.
func TestInvalidArgumentsOmitAnOversizedSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","description":"` + strings.Repeat("x", maxExecutableSchemaChars) +
		`","properties":{"kn_id":{"type":"string"}},"required":["kn_id"],"additionalProperties":false}`)
	err := validateTargetArguments("big_target", schema, map[string]json.RawMessage{})
	var refusal *invalidArguments
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want an invalid-arguments refusal", err)
	}
	if refusal.ArgumentsSchema != nil {
		t.Fatalf("an oversized schema came back (%d bytes)", len(refusal.ArgumentsSchema))
	}
	raw, _ := json.Marshal(refusal)
	if len(refusal.Violations) == 0 || !strings.Contains(string(raw), "kn_id") || strings.Contains(string(raw), "arguments_schema") {
		t.Fatalf("refusal = %s, want the missing kn_id named and no schema", raw)
	}
}

func TestExecutorNormalizesArgumentsByFixedRules(t *testing.T) {
	h := newExecutorHarness(t)
	asString := h.call(t, `{"name":"get_object_types","arguments":"{\"kn_id\":\"kn_demo\",\"ids\":[\"ot_order\"]}","bkn_context":`+testBKNContext+`}`)
	if asString.IsError || asString.Meta == nil || asString.Meta.AdditionalFields[normalizedArgumentsMetaKey] == nil {
		t.Fatalf("string arguments: %+v, want run and echoed", asString)
	}
	sameContext := h.call(t, `{"name":"get_object_types","arguments":{"kn_id":"kn_demo","ids":["ot_order"],"bkn_context":{"interaction_id":"int-1","conversation_id":"conv-1"}},"bkn_context":`+testBKNContext+`}`)
	if sameContext.IsError || sameContext.Meta == nil || sameContext.Meta.AdditionalFields[normalizedArgumentsMetaKey] == nil {
		t.Fatalf("equal inner bkn_context: %+v, want dropped and echoed", sameContext)
	}
	if len(h.calls) != 2 {
		t.Fatalf("target ran %d times, want 2", len(h.calls))
	}
	plain := h.call(t, `{"name":"get_object_types","arguments":{"kn_id":"kn_demo","ids":["ot_order"]},"bkn_context":`+testBKNContext+`}`)
	if plain.Meta != nil && plain.Meta.AdditionalFields[normalizedArgumentsMetaKey] != nil {
		t.Fatal("arguments sent as-is were echoed as rewritten")
	}
	for arguments, code := range map[string]string{
		`{"kn_id":"kn_demo","ids":["ot_order"],"bkn_context":{"conversation_id":"conv-2","interaction_id":"int-1"}}`: refusalBKNContextMismatch,
		`{"kn_id":"kn_demo","ids":["ot_order"],"response_format":"json"}`:                                            refusalResponseFormatInArgs,
		`"not json"`: refusalInvalidArguments,
		`[1,2]`:      refusalInvalidArguments,
	} {
		refusal := refusalOf(t, h.call(t, `{"name":"get_object_types","arguments":`+arguments+`,"bkn_context":`+testBKNContext+`}`))
		if refusal["error"] != code {
			t.Errorf("arguments %s: refusal %v, want %s", arguments, refusal, code)
		}
	}
}

func TestExecutorRefusesWhatItCannotRun(t *testing.T) {
	h := newExecutorHarness(t)
	for name, code := range map[string]string{
		toolKeySearchSchema:      refusalPublishedDirectly,
		"no_such_tool":           refusalUnknownTool,
		toolKeyExecuteNativeTool: refusalPublishedDirectly,
	} {
		refusal := refusalOf(t, h.call(t, `{"name":"`+name+`","arguments":{},"bkn_context":`+testBKNContext+`}`))
		if refusal["error"] != code {
			t.Errorf("%s: refusal %v, want %s", name, refusal, code)
		}
	}
	if len(h.intents) != 0 || len(h.calls) != 0 {
		t.Fatal("a refused call reached the guard or a target")
	}
}

// A model that found a mounted function with search_capabilities tends to call
// it by that name. The refusal must name the call that runs it, not send the
// model back to search_native_tools, where the function will never appear.
func TestExecutorRefusalPointsMountedFunctionsAtExecuteTool(t *testing.T) {
	h := newExecutorHarness(t)
	refusal := refusalOf(t, h.call(t, `{"name":"bf2abe46-d177-4eec-a945-2514488a1a8f","arguments":{"product":"382-000005"},"bkn_context":`+testBKNContext+`}`))
	message, _ := refusal["message"].(string)
	if refusal["error"] != refusalUnknownTool {
		t.Fatalf("refusal = %v, want unknown_tool", refusal)
	}
	for _, want := range []string{`"execute_tool"`, `"toolbox_id"`, `"tool_id"`, `"get_skill_content"`} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q does not mention %s", message, want)
		}
	}
}

func TestExecutorPassesTheTargetErrorThrough(t *testing.T) {
	h := newExecutorHarness(t)
	h.reply = mcpsdk.NewToolResultError("ot_missing not found")
	result := h.call(t, `{"name":"get_object_types","arguments":{"kn_id":"kn_demo","ids":["ot_missing"]},"bkn_context":`+testBKNContext+`}`)
	if !result.IsError || resultText(result) != "ot_missing not found" {
		t.Fatalf("result = %+v, want the target's error unchanged", result)
	}
}

// The production executor wraps the target in the server's own guard: a call
// without bkn_context is stopped there, before the target runs.
func TestExecutorAppliesTheServerGuardToTheTarget(t *testing.T) {
	catalog := catalogForLocale(t, "zh-CN")
	ran := false
	target := catalog.targets[toolKeyGetObjectTypes]
	target.handler = func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		ran = true
		return mcpsdk.NewToolResultText("ran"), nil
	}
	catalog.targets[toolKeyGetObjectTypes] = target
	executor := newNativeExecutor(catalog, nil)
	req := mcpsdk.CallToolRequest{}
	req.Params.Name = toolKeyExecuteNativeTool
	req.Params.RawArguments = json.RawMessage(`{"name":"get_object_types","arguments":{"kn_id":"kn_demo","ids":["ot_order"]}}`)
	result, err := executor.handle(context.Background(), req)
	if err != nil || ran || !result.IsError || !strings.Contains(resultText(result), "conversation_required") {
		t.Fatalf("result=%v err=%v ran=%v, want conversation_required before the target", resultText(result), err, ran)
	}
}

// At the server level the executor passes the guard untouched, while any other
// business tool is still guarded there.
func TestServerGuardLeavesTheExecutorToItself(t *testing.T) {
	passed := ""
	next := func(_ context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		passed = req.Params.Name
		return mcpsdk.NewToolResultText("ok"), nil
	}
	guarded := lifecycleToolMiddleware(nil)(next)
	for _, name := range []string{toolKeyExecuteNativeTool, toolKeyGetObjectTypes} {
		passed = ""
		req := mcpsdk.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = map[string]any{}
		result, err := guarded(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if name == toolKeyExecuteNativeTool && passed != name {
			t.Errorf("the executor was guarded at the server level: %s", resultText(result))
		}
		if name == toolKeyGetObjectTypes && (passed != "" || !strings.Contains(resultText(result), "conversation_required")) {
			t.Errorf("get_object_types passed the server guard without bkn_context")
		}
	}
}
