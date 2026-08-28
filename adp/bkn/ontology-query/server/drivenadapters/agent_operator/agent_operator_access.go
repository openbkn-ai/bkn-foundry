// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package agent_operator

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"ontology-query/common"
	"ontology-query/interfaces"
)

var (
	aoAccessOnce sync.Once
	aoAccess     interfaces.AgentOperatorAccess
)

type agentOperatorAccess struct {
	appSetting       *common.AppSetting
	agentOperatorUrl string
	httpClient       rest.HTTPClient
}

type integrationError struct {
	Code        string      `json:"code"`        // Error code
	Description string      `json:"description"` // Error description
	Detail      interface{} `json:"detail"`      // Error details
	Solution    interface{} `json:"solution"`    // Suggested resolution
	Link        interface{} `json:"link"`        // Error link
}

type executionResult struct {
	StatusCode int            `json:"status_code"`
	Headers    map[string]any `json:"headers"`
	Body       any            `json:"body"`
	Error      string         `json:"error"`
	DurationMs int            `json:"duration_ms"`
}

func NewAgentOperatorAccess(appSetting *common.AppSetting) interfaces.AgentOperatorAccess {
	aoAccessOnce.Do(func() {
		aoAccess = &agentOperatorAccess{
			appSetting:       appSetting,
			agentOperatorUrl: appSetting.AgentOperatorUrl,
			httpClient:       common.NewHTTPClient(),
		}
	})

	return aoAccess
}

// ExecuteTool executes a tool via tool-box API
// API: POST /tool-box/{box_id}/proxy/{tool_id}
func (aoa *agentOperatorAccess) ExecuteTool(ctx context.Context, boxID string,
	toolID string, execRequest interfaces.ToolExecutionRequest) (any, error) {

	var (
		respCode int
		result   []byte
		err      error
	)

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	// http://{host}:{port}/api/agent-operator-integration/internal-v1/tool-box/{box_id}/proxy/{tool_id}
	url := fmt.Sprintf("%s/%s/proxy/%s", aoa.appSetting.ToolBoxUrl, boxID, toolID)

	start := time.Now().UnixMilli()
	respCode, result, err = aoa.httpClient.PostNoUnmarshal(ctx, url, headers, execRequest)
	logger.Debugf("post [%s] with headers[%v] finished, request is [%v] response code is [%d], error is [%v], 耗时: %dms",
		url, headers, execRequest, respCode, err, time.Now().UnixMilli()-start)

	toolResult := executionResult{}

	if err != nil {
		logger.Errorf("Tool execution request failed: %v", err)
		return toolResult, fmt.Errorf("tool execution request failed: %v", err)
	}

	if respCode != http.StatusOK {
		var opError integrationError
		if err = sonic.Unmarshal(result, &opError); err != nil {
			logger.Errorf("unmarshal ToolError failed: %v", err)
			return toolResult, err
		}
		httpErr := &rest.HTTPError{HTTPCode: respCode,
			BaseError: rest.BaseError{
				ErrorCode:    opError.Code,
				Description:  opError.Description,
				ErrorDetails: opError.Detail,
			}}
		logger.Errorf("Tool execution failed: %v", httpErr.Error())
		return toolResult, fmt.Errorf("execute tool %s/%s return error %v", boxID, toolID, httpErr.Error())
	}

	if result == nil {
		return toolResult, fmt.Errorf("execute tool %s/%s return null", boxID, toolID)
	}

	if err := common.UnmarshalPreciseJSON(result, &toolResult); err != nil {
		logger.Errorf("Unmarshal tool execution result failed, %s", err)
		return toolResult, err
	}

	// status_code is considered successful only when it is between 100 and 300.
	if http.StatusContinue <= toolResult.StatusCode &&
		toolResult.StatusCode < http.StatusMultipleChoices {
		return toolResult.Body, nil
	} else {
		resByte, err := sonic.Marshal(toolResult)
		if err != nil {
			logger.Errorf("marshal tool result failed: %v", err)
			return toolResult, err
		}
		return nil, fmt.Errorf("execute tool failed: %v", string(resByte))
	}
}

// ExecuteMCP executes an MCP-based action through agent-operator-integration
// API: POST /mcp/proxy/{mcp_id}/tool/call
func (aoa *agentOperatorAccess) ExecuteMCP(ctx context.Context, mcpID string,
	toolName string, execRequest interfaces.MCPExecutionRequest) (any, error) {

	var (
		respCode int
		result   []byte
		err      error
	)

	// Get account info from context for user_id header
	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}

	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	// http://{host}:{port}/api/agent-operator-integration/internal-v1/mcp/proxy/{mcp_id}/tool/call
	url := fmt.Sprintf("%s/proxy/%s/tool/call", aoa.appSetting.MCPUrl, mcpID)

	start := time.Now().UnixMilli()
	respCode, result, err = aoa.httpClient.PostNoUnmarshal(ctx, url, headers, execRequest)
	logger.Debugf("post [%s] with headers[%v] finished, request is [%v] response code is [%d], error is [%v], 耗时: %dms",
		url, headers, execRequest, respCode, err, time.Now().UnixMilli()-start)

	mcpResult := mcpCallToolResult{}

	if err != nil {
		logger.Errorf("MCP execution request failed: %v", err)
		return mcpResult, fmt.Errorf("MCP execution request failed: %v", err)
	}

	if respCode != http.StatusOK {
		var opError integrationError
		if err = sonic.Unmarshal(result, &opError); err != nil {
			logger.Errorf("unmarshal integration error failed: %v\n", err)
			return mcpResult, err
		}
		httpErr := &rest.HTTPError{HTTPCode: respCode,
			BaseError: rest.BaseError{
				ErrorCode:    opError.Code,
				Description:  opError.Description,
				ErrorDetails: opError.Detail,
			}}
		logger.Errorf("MCP execution failed: %v", httpErr.Error())
		return mcpResult, fmt.Errorf("execute MCP %s return error %v", mcpID, httpErr.Error())
	}

	if result == nil {
		return mcpResult, fmt.Errorf("execute MCP %s return null", mcpID)
	}

	if err := common.UnmarshalPreciseJSON(result, &mcpResult); err != nil {
		logger.Errorf("Unmarshal MCP execution result failed, %s", err)
		return mcpResult, err
	}

	// The MCP protocol uses is_error to express tool-level failures; there is no HTTP status_code.
	if mcpResult.IsError {
		return nil, fmt.Errorf("execute MCP failed: %v", mcpResult.normalize())
	}

	return mcpResult.normalize(), nil
}

// mcpCallToolResult matches the response structure of the execution-factory MCP proxy (POST /mcp/proxy/{mcp_id}/tool/call):
// {"content":[{"type":"text","text":"..."}],"is_error":false}
// It differs from the tool-box proxy executionResult{status_code, body} and must not be mixed with it.
type mcpCallToolResult struct {
	Content []map[string]any `json:"content"`
	IsError bool             `json:"is_error"`
}

// normalize compacts MCP content blocks into a result that is easier for downstream consumers to handle.
//
// The result is always a JSON object: execution records are stored in the OpenSearch results.result field, which is mapped as object.
// Tool executions return an HTTP body object; returning a bare scalar triggers mapper_parsing_exception and causes
// the whole execution record to fail to write. Likewise, the same field name must keep the same type across executions, so keys are split by content shape:
// - All text blocks whose concatenated content is a JSON object: return that object directly.
// - All text blocks whose concatenated content is a JSON array: {"items": [...]}
// - Other all-text blocks, including plain text or JSON scalars: {"text": "..."}
// - Blocks containing non-text content, such as images or resources: {"content": [...]}
func (r mcpCallToolResult) normalize() map[string]any {
	texts := make([]string, 0, len(r.Content))
	for _, item := range r.Content {
		text, ok := item["text"].(string)
		if !ok || item["type"] != "text" {
			return map[string]any{"content": r.Content}
		}
		texts = append(texts, text)
	}

	if len(texts) == 0 {
		return map[string]any{"content": r.Content}
	}

	joined := strings.Join(texts, "\n")
	var parsed any
	if err := common.UnmarshalPreciseJSON([]byte(joined), &parsed); err == nil {
		switch v := parsed.(type) {
		case map[string]any:
			return v
		case []any:
			return map[string]any{"items": v}
		}
	}

	return map[string]any{"text": joined}
}
