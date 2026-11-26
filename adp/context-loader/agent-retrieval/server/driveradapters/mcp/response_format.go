// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
)

// GetResponseFormatFromRequest parses response_format from the arguments of MCP CallToolRequest. If not passed, it defaults to toon.
func GetResponseFormatFromRequest(req mcp.CallToolRequest) (rest.ResponseFormat, error) {
	s := req.GetString("response_format", "toon")
	return rest.ParseResponseFormat(s)
}

// BuildMCPToolResult returns the original object with JSON or TOON fallback text.
func BuildMCPToolResult(resp interface{}, format rest.ResponseFormat) (*mcp.CallToolResult, error) {
	text, err := marshalToolResultText(resp, format)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultStructured(resp, string(text)), nil
}

func marshalToolResultText(resp any, format rest.ResponseFormat) ([]byte, error) {
	if format == rest.FormatTOON {
		_, body, err := rest.MarshalResponse(rest.FormatTOON, resp)
		return body, err
	}
	return jsoncodec.Marshal(resp)
}

// toolResultRunes is how many characters the larger of a tool result's two copies
// carries: the text in the requested format, and the structuredContent JSON that
// BuildMCPToolResult puts beside it. A host may read either one, so a size bound on a
// tool's answer has to hold for both.
func toolResultRunes(resp interface{}, format rest.ResponseFormat) (int, error) {
	text, err := marshalToolResultText(resp, format)
	if err != nil {
		return 0, err
	}
	textRunes := utf8.RuneCount(text)
	if resp == nil {
		return textRunes, nil
	}
	// The MCP SDK marshals structuredContent with encoding/json.
	structured, err := json.Marshal(resp)
	if err != nil {
		return 0, err
	}
	return max(textRunes, utf8.RuneCount(structured)), nil
}

// receiptReserveRunes is the room a managed call's additions take besides its business refs:
// the receipt's status, durability, observed evidence refs and partial reasons in
// structuredContent, and the trace availability diagnostic appended as a text item.
const receiptReserveRunes = 2048

// managedToolResultRunes is toolResultRunes plus what the session guard adds to a managed
// call's result after the handler returns: the receipt, whose business refs list one entry per
// object or relation type snapshotted, and the trace diagnostic. A handler bounding its answer
// measures with it, so the result the caller reads stays within the bound and the page the
// trace records is the page delivered (#3413). Outside a managed call it is toolResultRunes.
func managedToolResultRunes(ctx context.Context, resp any, format rest.ResponseFormat,
	snapshotKind, knID string, snapshotIDs []string) (int, error) {
	runes, err := toolResultRunes(resp, format)
	if err != nil || !bkntrace.ReceiptExpected(ctx) {
		return runes, err
	}
	refs, err := json.Marshal(bkntrace.SchemaSnapshotBusinessRefs(ctx, snapshotKind, knID, snapshotIDs))
	if err != nil {
		return 0, err
	}
	return runes + utf8.RuneCount(refs) + receiptReserveRunes, nil
}
