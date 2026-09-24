// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
)

// receiptMetaKey carries a managed call's receipt in the result's _meta on
// the compact profile, where results have no structuredContent. A client
// reads it from there; a host that forwards _meta does not hand it to the
// model as answer text.
const receiptMetaKey = "openbkn.ai/receipt"

// compactResultMiddleware turns business results into text on the compact
// profile. It is the outermost middleware, so it sees the result after the
// lifecycle guard has completed the Operation and attached the receipt:
// Trace and completion keep the full structured result, and only what is
// sent to the client changes. The lifecycle pair keeps its structured
// results.
func compactResultMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := next(ctx, req)
			if err != nil || result == nil {
				return result, err
			}
			if _, lifecycle := lifecycleToolNames[req.Params.Name]; lifecycle {
				return result, nil
			}
			return projectCompactResult(result), nil
		}
	}
}

// projectCompactResult drops structuredContent and moves the receipt into
// _meta. The text a handler rendered stays as it is: handlers render their
// whole payload, and the receipt was attached after rendering, so the text
// is the payload without the receipt.
//
// An error result is projected only when its structured part is nothing but
// the receipt; any other error structure is left untouched, because losing
// recovery information is worse than sending it twice.
func projectCompactResult(result *mcp.CallToolResult) *mcp.CallToolResult {
	if result.StructuredContent == nil {
		return result
	}
	payload, isObject := structuredContentAsMap(result.StructuredContent)
	receipt, hasReceipt := payload["bkn_receipt"]
	if result.IsError && !(isObject && hasReceipt && onlyReceipt(payload)) {
		return result
	}
	projected := *result
	projected.StructuredContent = nil
	if !hasText(result) {
		// Nothing rendered the payload: render it here rather than send a
		// result with no content.
		body := any(result.StructuredContent)
		if isObject {
			delete(payload, "bkn_receipt")
			body = payload
		}
		_, text, err := rest.MarshalResponse(rest.FormatTOON, body)
		if err != nil {
			return result
		}
		projected.Content = []mcp.Content{mcp.NewTextContent(string(text))}
	}
	if hasReceipt {
		meta := &mcp.Meta{AdditionalFields: map[string]any{}}
		if result.Meta != nil {
			meta.ProgressToken = result.Meta.ProgressToken
			for key, value := range result.Meta.AdditionalFields {
				meta.AdditionalFields[key] = value
			}
		}
		meta.AdditionalFields[receiptMetaKey] = receipt
		projected.Meta = meta
	}
	return &projected
}

// onlyReceipt reports whether a structured payload carries nothing but the
// receipt: the wrapper the guard builds around a text-only result.
func onlyReceipt(payload map[string]any) bool {
	for key, value := range payload {
		switch {
		case key == "bkn_receipt":
		case key == "result" && value == nil:
		default:
			return false
		}
	}
	return true
}

func hasText(result *mcp.CallToolResult) bool {
	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok && text.Text != "" {
			return true
		}
	}
	return false
}
