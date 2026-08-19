// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

// GetResponseFormatFromRequest parses response_format from the arguments of MCP CallToolRequest. If not passed, it defaults to toon.
func GetResponseFormatFromRequest(req mcp.CallToolRequest) (rest.ResponseFormat, error) {
	s := req.GetString("response_format", "toon")
	return rest.ParseResponseFormat(s)
}

// BuildMCPToolResult uniformly constructs the MCP Tool return result based on response_format (the text is JSON or TOON, and structuredContent is still the original object)
func BuildMCPToolResult(resp interface{}, format rest.ResponseFormat) (*mcp.CallToolResult, error) {
	var textContent string
	if format == rest.FormatTOON {
		_, bodyBytes, err := rest.MarshalResponse(rest.FormatTOON, resp)
		if err != nil {
			return nil, err
		}
		textContent = string(bodyBytes)
	} else {
		textContent = utils.ObjectToJSON(resp)
	}
	return mcp.NewToolResultStructured(resp, textContent), nil
}
