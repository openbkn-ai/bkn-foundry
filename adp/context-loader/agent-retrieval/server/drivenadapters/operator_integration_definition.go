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

// The schema keys of a tool's API spec that get_action_info builds a tool from: its parameters
// and request body, the response it reads the output schema from, and the components they refer to.
var toolDefinitionSpecKeys = []string{"parameters", "request_body", "responses", "components"}

// GetToolDetailAs reads one tool as account rather than as the account in the context.
//
// It uses the caller-scoped route the direct read uses on the internal face, so Execution Factory
// authorizes account exactly as it would a caller — view, public access or execute on the tool
// box — and no bearer token travels. The answer is trimmed to the tool's identity, prose and schemas:
// source code, service address, path and authoring metadata never leave this adapter.
func (o *operatorIntegrationClient) GetToolDetailAs(ctx context.Context, account interfaces.AccountIdentity,
	req *interfaces.GetToolDetailRequest) (*interfaces.GetToolDetailResponse, error) {
	header, err := o.accountHeader(ctx, account, "operator.tool.get")
	if err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.BoxID) == "" || strings.TrimSpace(req.ToolID) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusForbidden,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	fullURL := o.baseURL + fmt.Sprintf(getToolDetailInternalURI,
		url.PathEscape(strings.TrimSpace(req.BoxID)), url.PathEscape(strings.TrimSpace(req.ToolID)))
	detail, err := o.readToolDetail(ctx, "GetToolDetailAs", fullURL, header)
	if err != nil {
		return nil, err
	}
	definition := &interfaces.GetToolDetailResponse{
		ToolID:      detail.ToolID,
		Name:        detail.Name,
		Description: detail.Description,
	}
	if spec := detail.Metadata.APISpec; spec != nil {
		definition.Metadata.APISpec = make(map[string]any, len(toolDefinitionSpecKeys))
		for _, key := range toolDefinitionSpecKeys {
			if value, ok := spec[key]; ok {
				definition.Metadata.APISpec[key] = value
			}
		}
	}
	return definition, nil
}

// GetMCPToolDetailAs reads one MCP tool as account rather than as the account in the context, over
// the same caller-scoped route as the direct read. Only the named tool's name, description and
// input schema come back; the rest of the server's listing never leaves this adapter.
func (o *operatorIntegrationClient) GetMCPToolDetailAs(ctx context.Context, account interfaces.AccountIdentity,
	req *interfaces.GetMCPToolDetailRequest) (*interfaces.GetMCPToolDetailResponse, error) {
	header, err := o.accountHeader(ctx, account, "operator.mcp_tool.get")
	if err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.McpID) == "" || strings.TrimSpace(req.ToolName) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusForbidden,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	fullURL := o.baseURL + fmt.Sprintf(getMCPToolListInternalURI, url.PathEscape(strings.TrimSpace(req.McpID)))
	tool, err := o.readMCPToolDetail(ctx, "GetMCPToolDetailAs", fullURL, header, req.ToolName)
	if err != nil {
		return nil, err
	}
	return &interfaces.GetMCPToolDetailResponse{
		Name:        tool.Name,
		Description: tool.Description,
		InputSchema: tool.InputSchema,
	}, nil
}

// accountHeader carries account, and only account, as the principal of the request. An incomplete
// account fails before any request is made: there is no falling back to the context's account.
func (o *operatorIntegrationClient) accountHeader(ctx context.Context, account interfaces.AccountIdentity,
	operationName string) (map[string]string, error) {
	if strings.TrimSpace(account.ID) == "" || strings.TrimSpace(string(account.Type)) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	header := o.skillHeader(ctx, operationName)
	header[string(interfaces.HeaderXAccountID)] = account.ID
	header[string(interfaces.HeaderXAccountType)] = string(account.Type)
	delete(header, "Authorization")
	return header, nil
}
