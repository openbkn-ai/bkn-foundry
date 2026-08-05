// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package mcp

import (
	"encoding/json"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

func errorEnvelopeFromResult(t *testing.T, result *mcpsdk.CallToolResult) map[string]any {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("expected MCP tool error, got %#v", result)
	}
	if result.StructuredContent != nil {
		t.Fatalf("error result must not carry success structured content: %#v", result.StructuredContent)
	}
	if len(result.Content) == 0 {
		t.Fatal("error result omitted text fallback")
	}
	textContent, ok := mcpsdk.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("error result is not text: %#v", result.Content)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &envelope); err != nil {
		t.Fatalf("error result is not JSON: %q: %v", textContent.Text, err)
	}
	return envelope
}

func lifecycleErrorFromResult(t *testing.T, result *mcpsdk.CallToolResult) map[string]any {
	t.Helper()
	envelope := errorEnvelopeFromResult(t, result)
	errorValue, ok := envelope["error"].(map[string]any)
	if !ok {
		t.Fatalf("error envelope is missing error object: %#v", envelope)
	}
	return errorValue
}
