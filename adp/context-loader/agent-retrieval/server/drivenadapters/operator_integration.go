// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

type operatorIntegrationClient struct {
	logger     interfaces.Logger
	baseURL    string
	httpClient interfaces.HTTPClient
}

var (
	operatorIntegrationOnce sync.Once
	operatorIntegration     interfaces.DrivenOperatorIntegration
)

const (
	// https://{host}:{port}/api/agent-operator-integration/internal-v1/tool-box/:box_id/tool/:tool_id
	getToolDetailURI = "/internal-v1/tool-box/%s/tool/%s"
	// https://{host}:{port}/api/agent-operator-integration/internal-v1/mcp/proxy/:mcp_id/tools
	getMCPToolListURI = "/internal-v1/mcp/proxy/%s/tools"
	// https://{host}:{port}/api/agent-operator-integration/internal-v1/mcp/proxy/:mcp_id/tool/call
	callMCPToolURI = "/internal-v1/mcp/proxy/%s/tool/call"
)

// NewOperatorIntegrationClient creates an OperatorIntegration client.
func NewOperatorIntegrationClient() interfaces.DrivenOperatorIntegration {
	operatorIntegrationOnce.Do(func() {
		configLoader := config.NewConfigLoader()
		operatorIntegration = &operatorIntegrationClient{
			logger:     configLoader.GetLogger(),
			baseURL:    configLoader.OperatorIntegration.BuildURL("/api/agent-operator-integration"),
			httpClient: rest.NewHTTPClient(),
		}
	})
	return operatorIntegration
}

// GetToolDetail retrieves tool details.
func (o *operatorIntegrationClient) GetToolDetail(ctx context.Context, req *interfaces.GetToolDetailRequest) (resp *interfaces.GetToolDetailResponse, err error) {
	uri := fmt.Sprintf(getToolDetailURI, req.BoxID, req.ToolID)
	url := fmt.Sprintf("%s%s", o.baseURL, uri)

	// Request logging is intentionally performed before the downstream call.
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#GetToolDetail] URL: %s", url)

	header := common.GetHeaderForChildOperation(ctx, "operator.tool.get", 1)

	_, respBody, err := o.httpClient.Get(ctx, url, nil, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetToolDetail] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "ToolDetailRequestFailed"))
	}

	resp = &interfaces.GetToolDetailResponse{}
	resultByt := utils.ObjectToByte(respBody)
	err = sonic.Unmarshal(resultByt, resp)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetToolDetail] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "ToolDetailResponseInvalid"))
		return nil, err
	}

	// Response logging is intentionally performed after a successful decode.
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#GetToolDetail] Tool: %s, Name: %s", resp.ToolID, resp.Name)

	return resp, nil
}

// GetMCPToolDetail retrieves MCP tool details.
func (o *operatorIntegrationClient) GetMCPToolDetail(ctx context.Context, req *interfaces.GetMCPToolDetailRequest) (*interfaces.GetMCPToolDetailResponse, error) {
	uri := fmt.Sprintf(getMCPToolListURI, req.McpID)
	url := fmt.Sprintf("%s%s", o.baseURL, uri)

	// Request logging is intentionally performed before the downstream call.
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#GetMCPToolDetail] URL: %s", url)

	header := common.GetHeaderForChildOperation(ctx, "operator.mcp_tool.get", 1)
	_, respBody, err := o.httpClient.Get(ctx, url, nil, header)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetMCPToolDetail] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "MCPToolListRequestFailed"))
	}

	var listResp struct {
		Tools []interfaces.GetMCPToolDetailResponse `json:"tools"`
	}

	resultByt := utils.ObjectToByte(respBody)
	err = sonic.Unmarshal(resultByt, &listResp)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#GetMCPToolDetail] Unmarshal failed, body: %s, err: %v", string(resultByt), err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "MCPToolListResponseInvalid"))
	}

	for _, tool := range listResp.Tools {
		if tool.Name == req.ToolName {
			// Response logging is intentionally performed after a tool is found.
			o.logger.WithContext(ctx).Debugf("[OperatorIntegration#GetMCPToolDetail] Found Tool: %s", tool.Name)
			return &tool, nil
		}
	}

	return nil, infraErr.DefaultHTTPError(ctx, http.StatusNotFound,
		infraErr.LocalizedDetail(ctx, "MCPToolNotFound"))
}

// CallMCPTool calls an MCP tool.
func (o *operatorIntegrationClient) CallMCPTool(ctx context.Context, req *interfaces.CallMCPToolRequest) (map[string]interface{}, error) {
	uri := fmt.Sprintf(callMCPToolURI, req.McpID)
	url := fmt.Sprintf("%s%s", o.baseURL, uri)

	// Request logging is intentionally performed before the downstream call.
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#CallMCPTool] URL: %s, Tool: %s", url, req.ToolName)

	header := common.GetHeaderForChildOperation(ctx, "operator.mcp_tool.call", 1)

	// Build the request body.
	reqBody := map[string]interface{}{
		"tool_name":  req.ToolName,
		"parameters": req.Parameters,
	}

	// PostBytes, not Post: a tool's output is arbitrary business data — a SQL tool
	// hands back whatever the customer's columns hold — and Post's interface{} hop
	// would round every integer past float64's mantissa. See
	// openbkn-ai/bkn-studio#464.
	_, respBody, err := o.httpClient.PostBytes(ctx, url, header, reqBody)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#CallMCPTool] Request failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "MCPToolCallRequestFailed"))
	}

	var result map[string]interface{}
	err = unmarshalPrecise(respBody, &result)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#CallMCPTool] Unmarshal failed, body: %s, err: %v", string(respBody), err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "MCPToolCallResponseInvalid"))
	}

	return result, nil
}

// mcpServerDetailURI reads one MCP Server, including its publication state.
const mcpServerDetailURI = "/internal-v1/mcp/%s"

// MCPServerIsUsable reports whether the MCP Server is published.
//
// A server that cannot be read is reported as unusable rather than assumed fine: this gates a
// call that runs, and the safe direction when the answer is unknown is to refuse.
func (o *operatorIntegrationClient) MCPServerIsUsable(ctx context.Context, mcpID string) (bool, error) {
	if strings.TrimSpace(mcpID) == "" {
		return false, nil
	}
	fullURL := o.baseURL + fmt.Sprintf(mcpServerDetailURI, mcpID)
	header := common.GetHeaderForChildOperation(ctx, "operator.mcp_server.get", 1)

	code, body, err := o.httpClient.Get(ctx, fullURL, nil, header)
	if err != nil || code != http.StatusOK {
		o.logger.WithContext(ctx).Warnf("[OperatorIntegration#MCPServerIsUsable] mcp_id=%s unreadable: code=%d err=%v",
			mcpID, code, err)
		return false, nil
	}

	var payload struct {
		BaseInfo struct {
			Status string `json:"status"`
		} `json:"base_info"`
	}
	if err = sonic.Unmarshal(utils.ObjectToByte(body), &payload); err != nil {
		o.logger.WithContext(ctx).Warnf("[OperatorIntegration#MCPServerIsUsable] unmarshal failed: %v", err)
		return false, nil
	}
	return payload.BaseInfo.Status == mcpServerStatusPublished, nil
}

// mcpServerStatusPublished is the one state in which an MCP Server's tools are callable.
const mcpServerStatusPublished = "published"
