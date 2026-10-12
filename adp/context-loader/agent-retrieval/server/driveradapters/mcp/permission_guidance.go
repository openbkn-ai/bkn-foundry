// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"errors"

	"github.com/bytedance/sonic"
	"github.com/mark3labs/mcp-go/mcp"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// permissionGuidanceKey is where guidance sits in both a refused call's error
// envelope and a successful result, so a client reads it from one place.
const permissionGuidanceKey = "permission_guidance"

// toolErrorWithPermissionGuidance returns the usual text error, extended with
// guidance when the refusal was a permission shortfall. The guidance travels
// in the error text rather than structuredContent: MCP tool errors have no
// structured contract here, and run_code's stub only reads the text.
func toolErrorWithPermissionGuidance(err error, guidance *interfaces.PermissionGuidance) *mcp.CallToolResult {
	if guidance == nil {
		return mcp.NewToolResultError(err.Error())
	}
	return toolErrorWithPermissionGuidanceValue(err, guidance)
}

func toolErrorWithPermissionGuidances(err error, guidance []*interfaces.PermissionGuidance) *mcp.CallToolResult {
	if len(guidance) == 0 {
		return mcp.NewToolResultError(err.Error())
	}
	return toolErrorWithPermissionGuidanceValue(err, guidance)
}

func toolErrorWithPermissionGuidanceValue(err error, guidance any) *mcp.CallToolResult {
	var httpErr *infraErr.HTTPError
	if guidance == nil || !errors.As(err, &httpErr) {
		return mcp.NewToolResultError(err.Error())
	}
	envelope := map[string]any{}
	if sonic.UnmarshalString(httpErr.Error(), &envelope) != nil {
		return mcp.NewToolResultError(err.Error())
	}
	envelope[permissionGuidanceKey] = guidance
	text, marshalErr := sonic.ConfigStd.MarshalToString(envelope)
	if marshalErr != nil {
		return mcp.NewToolResultError(err.Error())
	}
	return mcp.NewToolResultError(text)
}

func cypherPermissionImpactsFromError(err error) []interfaces.ObjectPermissionImpact {
	var httpErr *infraErr.HTTPError
	if !errors.As(err, &httpErr) || len(httpErr.Metadata) == 0 {
		return nil
	}
	raw, ok := httpErr.Metadata["permission_impacts"]
	if !ok {
		return nil
	}
	encoded, marshalErr := sonic.ConfigStd.Marshal(raw)
	if marshalErr != nil {
		return nil
	}
	var impacts []interfaces.ObjectPermissionImpact
	if sonic.Unmarshal(encoded, &impacts) != nil {
		return nil
	}
	return impacts
}
