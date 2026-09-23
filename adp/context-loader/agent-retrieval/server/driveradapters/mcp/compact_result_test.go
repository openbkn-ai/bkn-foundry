// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

var testReceipt = map[string]any{"receipt_status": "completed", "evidence_durability": "durable"}

func receiptInMeta(result *mcpsdk.CallToolResult) any {
	if result.Meta == nil {
		return nil
	}
	return result.Meta.AdditionalFields[receiptMetaKey]
}

func TestProjectionKeepsTheTextAndMovesTheReceipt(t *testing.T) {
	payload := map[string]any{"object_types": []any{"ot_order"}, "bkn_receipt": testReceipt}
	result := mcpsdk.NewToolResultStructured(payload, "object_types[1]: ot_order")
	got := projectCompactResult(result)
	if got.StructuredContent != nil || resultText(got) != "object_types[1]: ot_order" {
		t.Fatalf("projected = %+v, want the handler's text and no structuredContent", got)
	}
	if receiptInMeta(got) == nil {
		t.Fatal("the receipt did not move to _meta")
	}
	if result.StructuredContent == nil {
		t.Fatal("the projection modified the result the guard returned")
	}
}

func TestProjectionOfATextOnlyResult(t *testing.T) {
	result := mcpsdk.NewToolResultText(`{"candidates":[]}`)
	result.StructuredContent = map[string]any{"result": nil, "bkn_receipt": testReceipt}
	result.Meta = &mcpsdk.Meta{AdditionalFields: map[string]any{normalizedArgumentsMetaKey: "kept"}}
	got := projectCompactResult(result)
	if got.StructuredContent != nil || resultText(got) != `{"candidates":[]}` || receiptInMeta(got) == nil {
		t.Fatalf("projected = %+v", got)
	}
	if got.Meta.AdditionalFields[normalizedArgumentsMetaKey] != "kept" {
		t.Fatal("existing _meta fields were dropped")
	}
}

func TestProjectionOfErrors(t *testing.T) {
	receiptOnly := mcpsdk.NewToolResultError("ot_missing not found")
	receiptOnly.StructuredContent = map[string]any{"result": nil, "bkn_receipt": testReceipt}
	got := projectCompactResult(receiptOnly)
	if !got.IsError || got.StructuredContent != nil || receiptInMeta(got) == nil || resultText(got) != "ot_missing not found" {
		t.Fatalf("receipt-only error projected to %+v", got)
	}
	structured := mcpsdk.NewToolResultError("replayed")
	structured.StructuredContent = map[string]any{"error": map[string]any{"code": "receipt_terminal"}, "bkn_receipt": testReceipt}
	if projectCompactResult(structured) != structured {
		t.Fatal("an error with its own structure must be left untouched")
	}
	plain := mcpsdk.NewToolResultError("boom")
	if projectCompactResult(plain) != plain {
		t.Fatal("an error without structuredContent must be left untouched")
	}
}

func TestProjectionRendersAPayloadWithoutText(t *testing.T) {
	result := &mcpsdk.CallToolResult{StructuredContent: map[string]any{"total_count": 3, "bkn_receipt": testReceipt}}
	got := projectCompactResult(result)
	text := resultText(got)
	if got.StructuredContent != nil || !strings.Contains(text, "total_count") || strings.Contains(text, "bkn_receipt") {
		t.Fatalf("rendered text = %q", text)
	}
}

func TestProjectionLeavesTheLifecyclePairAlone(t *testing.T) {
	structured := mcpsdk.NewToolResultStructured(map[string]any{"conversation_id": "c", "interaction_id": "i"}, "started")
	next := func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) { return structured, nil }
	req := mcpsdk.CallToolRequest{}
	req.Params.Name = "bkn_start_interaction"
	got, err := compactResultMiddleware()(next)(context.Background(), req)
	if err != nil || got.StructuredContent == nil {
		t.Fatalf("lifecycle result = %+v, %v; want it structured", got, err)
	}
	if fullProfile.textResults {
		t.Fatal("the full profile must keep structured results")
	}
}

// Through the real compact server: the wire result of a managed call has no
// structuredContent, and the receipt the guard attached is in _meta.
func TestCompactResultsGoOutAsTextWithTheReceiptInMeta(t *testing.T) {
	t.Setenv("CONFIG_PROFILE", "../../infra/config")
	client, _, _ := fakeTraceCore(t)
	srv, _ := newMCPServerForProfile(client, "zh-CN", defaultPTCServicePort, compactProfile)
	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": toolKeySearchNativeTools, "arguments": map[string]any{
			"query":       "看订单对象类有哪些字段",
			"bkn_context": map[string]any{"conversation_id": "conv-1", "interaction_id": "int-1"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Content           []map[string]any `json:"content"`
		StructuredContent any              `json:"structuredContent"`
		Meta              map[string]any   `json:"_meta"`
	}
	resultOf(t, srv.HandleMessage(trustedMCPIntegrationContext(context.Background(), 1), req), &wire)
	if wire.StructuredContent != nil {
		t.Fatalf("structuredContent reached the client: %v", wire.StructuredContent)
	}
	receipt, _ := wire.Meta[receiptMetaKey].(map[string]any)
	if receipt["receipt_status"] != "completed" {
		t.Fatalf("_meta = %v, want the completed receipt", wire.Meta)
	}
	if len(wire.Content) != 1 || !strings.Contains(wire.Content[0]["text"].(string), toolKeyGetObjectTypes) {
		t.Fatalf("content = %v", wire.Content)
	}
}
