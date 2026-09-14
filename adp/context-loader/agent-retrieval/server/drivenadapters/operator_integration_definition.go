// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/bytedance/sonic"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// classifyToolReadError keeps Execution Factory's answer to a tool definition read.
//
// A refusal, a missing tool or a bad request is the caller's answer, so its status and the
// downstream code travel on: a 403 used to reach the caller as a 502 "dependency unavailable",
// which reads as an outage and hides the grant that is missing (#1548). Only a transport failure
// or a 5xx is a dependency failure. The downstream details can hold internal addresses and stay in
// the server log.
func classifyToolReadError(ctx context.Context, code int, err error, detailKey string) error {
	if code >= http.StatusBadRequest && code < http.StatusInternalServerError {
		if downstreamCode, description := executionFactoryError(err); downstreamCode != "" && description != "" {
			return infraErr.DefaultHTTPError(ctx, code, downstreamCode+": "+description)
		}
		return infraErr.DefaultHTTPError(ctx, code, infraErr.LocalizedDetail(ctx, detailKey))
	}
	return infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, infraErr.LocalizedDetail(ctx, detailKey))
}

// GetToolDefinitionAsProxy reads the invocation contract of the tool an action type is bound to,
// as the knowledge network's managed proxy.
//
// The caller has already been checked for view on the action type and BKN has confirmed the box is
// that action type's current published target; Execution Factory rechecks the proxy's grant on the
// box and that the tool lives in it. What comes back is the name, description and schemas only.
func (o *operatorIntegrationClient) GetToolDefinitionAsProxy(ctx context.Context,
	req *interfaces.GetToolDetailRequest, proxy *interfaces.KNProxyExecution) (*interfaces.GetToolDetailResponse, error) {
	if req == nil || proxy == nil || proxy.Binding.TargetType != interfaces.KNProxyTargetTypeToolBox ||
		proxy.Binding.TargetID != req.BoxID || strings.TrimSpace(req.ToolID) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusForbidden,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	header, err := managedProxyHeadersFor(ctx, proxy, interfaces.KNProxyChildTypeActionType,
		"operator.action.proxy.definition")
	if err != nil {
		return nil, err
	}
	fullURL := o.baseURL + fmt.Sprintf(getToolDefinitionManagedURI,
		url.PathEscape(strings.TrimSpace(req.BoxID)), url.PathEscape(strings.TrimSpace(req.ToolID)))
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#GetToolDefinitionAsProxy] URL: %s", fullURL)

	code, body, err := o.httpClient.GetBytes(ctx, fullURL, nil, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetToolDefinitionAsProxy] Request failed, err: %v", err)
		return nil, classifyToolReadError(ctx, code, err, "ToolDetailRequestFailed")
	}
	var definition struct {
		ToolID      string         `json:"tool_id"`
		Name        string         `json:"name"`
		Description string         `json:"description"`
		APISpec     map[string]any `json:"api_spec"`
	}
	if err = sonic.Unmarshal(body, &definition); err != nil || definition.ToolID != req.ToolID {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetToolDefinitionAsProxy] Invalid response for tool %s, err: %v",
			req.ToolID, err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "ToolDetailResponseInvalid"))
	}
	return &interfaces.GetToolDetailResponse{
		ToolID:      definition.ToolID,
		Name:        definition.Name,
		Description: definition.Description,
		Metadata:    interfaces.ToolMetadata{APISpec: definition.APISpec},
	}, nil
}

// GetMCPToolDefinitionAsProxy reads the input contract of the MCP tool an action type is bound to,
// as the knowledge network's managed proxy. Only the named tool comes back, never the server's
// whole listing.
func (o *operatorIntegrationClient) GetMCPToolDefinitionAsProxy(ctx context.Context,
	req *interfaces.GetMCPToolDetailRequest, proxy *interfaces.KNProxyExecution) (*interfaces.GetMCPToolDetailResponse, error) {
	if req == nil || proxy == nil || proxy.Binding.TargetType != interfaces.KNProxyTargetTypeMCP ||
		proxy.Binding.TargetID != req.McpID || strings.TrimSpace(req.ToolName) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusForbidden,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	header, err := managedProxyHeadersFor(ctx, proxy, interfaces.KNProxyChildTypeActionType,
		"operator.action.proxy.definition")
	if err != nil {
		return nil, err
	}
	fullURL := o.baseURL + fmt.Sprintf(getMCPToolDefinitionManagedURI, url.PathEscape(strings.TrimSpace(req.McpID)))
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#GetMCPToolDefinitionAsProxy] URL: %s, Tool: %s",
		fullURL, req.ToolName)

	code, body, err := o.httpClient.GetBytes(ctx, fullURL, url.Values{"tool_name": {req.ToolName}}, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetMCPToolDefinitionAsProxy] Request failed, err: %v", err)
		return nil, classifyToolReadError(ctx, code, err, "MCPToolListRequestFailed")
	}
	var definition struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"input_schema"`
	}
	if err = sonic.Unmarshal(body, &definition); err != nil || definition.Name != req.ToolName {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetMCPToolDefinitionAsProxy] Invalid response for tool %s, err: %v",
			req.ToolName, err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "MCPToolListResponseInvalid"))
	}
	return &interfaces.GetMCPToolDetailResponse{
		Name:        definition.Name,
		Description: definition.Description,
		InputSchema: definition.InputSchema,
	}, nil
}
