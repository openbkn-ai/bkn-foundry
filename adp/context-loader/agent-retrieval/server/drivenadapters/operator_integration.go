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
	"net/url"
	"strconv"
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
	// Caller-facing resource reads and calls use Execution Factory's public
	// authorization face when an original bearer token is available. Internal
	// callers use the caller-scoped private face, where trusted account headers
	// drive the same authorization checks.
	getToolDetailURI          = "/v1/tool-box/%s/tool/%s"
	getToolDetailInternalURI  = "/internal-v1/caller/tool-box/%s/tool/%s"
	getMCPToolListURI         = "/v1/mcp/proxy/%s/tools"
	getMCPToolListInternalURI = "/internal-v1/caller/mcp/proxy/%s/tools"
	callMCPToolURI            = "/v1/mcp/proxy/%s/tool/call"
	callMCPToolInternalURI    = "/internal-v1/caller/mcp/proxy/%s/tool/call"
	callMCPToolManagedURI     = "/internal-v1/mcp/proxy/%s/tool/call"
)

const (
	headerBKNCallerID     = "x-bkn-caller-id"
	headerBKNCallerType   = "x-bkn-caller-type"
	headerBKNKnowledgeID  = "x-bkn-kn-id"
	headerBKNChildType    = "x-bkn-child-type"
	headerBKNChildID      = "x-bkn-child-id"
	headerBKNProxyVersion = "x-bkn-proxy-version"
	headerBKNTargetType   = "x-bkn-target-type"
	headerBKNTargetID     = "x-bkn-target-id"
	headerBKNOperation    = "x-bkn-operation"
)

func managedProxyHeaders(ctx context.Context, proxy *interfaces.KNProxyExecution) (map[string]string, error) {
	caller, ok := common.GetAccountAuthContextFromCtx(ctx)
	if !ok || caller == nil || proxy == nil || proxy.Mapping == nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	mapping, binding := proxy.Mapping, proxy.Binding
	if caller.AccountID == "" || mapping.ProxyAccountID == "" || mapping.ProxyAccountType != "app" ||
		mapping.Version <= 0 || mapping.KNID != binding.KNID || binding.ChildType != interfaces.KNProxyChildTypeCapability ||
		binding.ChildID == "" || binding.TargetID == "" || binding.Operation != interfaces.KNProxyOperationExecute {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	header := common.GetHeaderForChildOperation(ctx, "operator.capability.proxy.execute", 1)
	header[string(interfaces.HeaderXAccountID)] = mapping.ProxyAccountID
	header[string(interfaces.HeaderXAccountType)] = mapping.ProxyAccountType
	header[headerBKNCallerID] = caller.AccountID
	header[headerBKNCallerType] = string(caller.AccountType)
	header[headerBKNKnowledgeID] = binding.KNID
	header[headerBKNChildType] = binding.ChildType
	header[headerBKNChildID] = binding.ChildID
	header[headerBKNProxyVersion] = strconv.FormatInt(mapping.Version, 10)
	header[headerBKNTargetType] = binding.TargetType
	header[headerBKNTargetID] = binding.TargetID
	header[headerBKNOperation] = binding.Operation
	header[rest.ContentTypeKey] = rest.ContentTypeJSON
	return header, nil
}

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
	uri := fmt.Sprintf(capabilityURI(ctx, getToolDetailURI, getToolDetailInternalURI), req.BoxID, req.ToolID)
	url := fmt.Sprintf("%s%s", o.baseURL, uri)

	// Request logging is intentionally performed before the downstream call.
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#GetToolDetail] URL: %s", url)

	header, err := o.capabilityAuthorizationHeader(ctx, "operator.tool.get")
	if err != nil {
		return nil, err
	}

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
	uri := fmt.Sprintf(capabilityURI(ctx, getMCPToolListURI, getMCPToolListInternalURI), req.McpID)
	url := fmt.Sprintf("%s%s", o.baseURL, uri)

	// Request logging is intentionally performed before the downstream call.
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#GetMCPToolDetail] URL: %s", url)

	header, err := o.capabilityAuthorizationHeader(ctx, "operator.mcp_tool.get")
	if err != nil {
		return nil, err
	}
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
	uri := fmt.Sprintf(capabilityURI(ctx, callMCPToolURI, callMCPToolInternalURI), req.McpID)
	url := fmt.Sprintf("%s%s", o.baseURL, uri)

	// Request logging is intentionally performed before the downstream call.
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#CallMCPTool] URL: %s, Tool: %s", url, req.ToolName)

	header, err := o.capabilityAuthorizationHeader(ctx, "operator.mcp_tool.call")
	if err != nil {
		return nil, err
	}

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

func (o *operatorIntegrationClient) CallMCPToolAsProxy(ctx context.Context,
	req *interfaces.CallMCPToolRequest, proxy *interfaces.KNProxyExecution) (map[string]interface{}, error) {
	if req == nil || proxy == nil || proxy.Binding.TargetType != interfaces.KNProxyTargetTypeMCP || proxy.Binding.TargetID != req.McpID {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusForbidden,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	header, err := managedProxyHeaders(ctx, proxy)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s%s", o.baseURL,
		fmt.Sprintf(callMCPToolManagedURI, url.PathEscape(strings.TrimSpace(req.McpID))))
	reqBody := map[string]interface{}{"tool_name": req.ToolName, "parameters": req.Parameters}
	code, body, err := o.httpClient.PostBytes(ctx, url, header, reqBody)
	if err != nil {
		return nil, skillUpstreamError(ctx, code, "MCPToolCallRequestFailed", err)
	}
	var result map[string]interface{}
	if err := unmarshalPrecise(body, &result); err != nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway,
			infraErr.LocalizedDetail(ctx, "MCPToolCallResponseInvalid"))
	}
	return result, nil
}

// mcpServerDetailURI reads one MCP Server, including its publication state.
const mcpServerDetailURI = "/internal-v1/mcp/%s"

// toolBoxDetailURI reads one tool box, including its publication state.
const toolBoxDetailURI = "/internal-v1/tool-box/%s"

// toolBoxToolsURI lists the tools of one box; status=enabled narrows to the callable ones.
const toolBoxToolsURI = "/internal-v1/tool-box/%s/tools/list"

// ToolBoxLifecycle reads whether the box is published and which tools are enabled.
//
// Two internal reads: the box for its state, the tools listing narrowed to enabled. Neither needs
// a caller token, so this answers on the internal face where the caller-visible listing cannot,
// and it fails closed — a box or a listing that cannot be read yields unpublished / no tools.
//
// The listing is asked with all=true, which the execution factory answers in one page with no
// limit or offset (dbaccess/tool.go applies paging only when all is unset). So the enabled set is
// complete from a single request, and EnabledKnown is always true from this adapter. An earlier
// version walked page/page_size here; the server ignores both under all=true, so that walk made
// five identical full queries and then wrongly reported a large box's set as a prefix.
func (o *operatorIntegrationClient) ToolBoxLifecycle(ctx context.Context, boxID string) (*interfaces.ToolBoxLifecycle, error) {
	out := &interfaces.ToolBoxLifecycle{EnabledTools: map[string]struct{}{}, EnabledKnown: true}
	if strings.TrimSpace(boxID) == "" {
		return out, nil
	}
	header := common.GetHeaderForChildOperation(ctx, "operator.tool_box.get", 1)

	code, body, err := o.httpClient.Get(ctx, o.baseURL+fmt.Sprintf(toolBoxDetailURI, boxID), nil, header)
	if err != nil || code != http.StatusOK {
		o.logger.WithContext(ctx).Warnf("[OperatorIntegration#ToolBoxLifecycle] box_id=%s unreadable: code=%d err=%v",
			boxID, code, err)
		return out, nil
	}
	var box struct {
		Status string `json:"status"`
	}
	if err = sonic.Unmarshal(utils.ObjectToByte(body), &box); err != nil {
		o.logger.WithContext(ctx).Warnf("[OperatorIntegration#ToolBoxLifecycle] unmarshal box failed: %v", err)
		return out, nil
	}
	out.Published = box.Status == mcpServerStatusPublished
	if !out.Published {
		return out, nil
	}

	query := url.Values{"all": {"true"}, "status": {"enabled"}}
	code, body, err = o.httpClient.Get(ctx, o.baseURL+fmt.Sprintf(toolBoxToolsURI, boxID), query, header)
	if err != nil || code != http.StatusOK {
		o.logger.WithContext(ctx).Warnf("[OperatorIntegration#ToolBoxLifecycle] box_id=%s tools unreadable: code=%d err=%v",
			boxID, code, err)
		return &interfaces.ToolBoxLifecycle{EnabledTools: map[string]struct{}{}}, nil
	}
	var listed struct {
		Tools []struct {
			ToolID string `json:"tool_id"`
		} `json:"tools"`
	}
	if err = sonic.Unmarshal(utils.ObjectToByte(body), &listed); err != nil {
		o.logger.WithContext(ctx).Warnf("[OperatorIntegration#ToolBoxLifecycle] unmarshal tools failed: %v", err)
		return &interfaces.ToolBoxLifecycle{EnabledTools: map[string]struct{}{}}, nil
	}
	for _, tool := range listed.Tools {
		if id := strings.TrimSpace(tool.ToolID); id != "" {
			out.EnabledTools[id] = struct{}{}
		}
	}
	return out, nil
}

// MCPServerIsUsable reports whether the MCP Server's tools are callable: it is published, or it is
// editing, which the execution factory serves from its release (bkn-foundry#1478).
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
	return payload.BaseInfo.Status == mcpServerStatusPublished || payload.BaseInfo.Status == mcpServerStatusEditing, nil
}

// An MCP Server's tools are callable in two states: published, and editing — a published server
// with a draft beside it, still served from its release. The tool box's lifecycle reuses the first.
const (
	mcpServerStatusPublished = "published"
	mcpServerStatusEditing   = "editing"
)
