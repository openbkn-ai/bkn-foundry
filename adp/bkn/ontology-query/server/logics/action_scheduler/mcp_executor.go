// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"ontology-query/interfaces"
)

const mcpExecutionTimeoutSeconds int64 = 60

// ExecuteMCP executes an MCP-based action through agent-operator-integration
// API: POST /mcp/proxy/{mcp_id}/tool/call
func ExecuteMCP(ctx context.Context, aoAccess interfaces.AgentOperatorAccess, actionType *interfaces.ActionType, params map[string]any) (any, error) {
	source := actionType.ActionSource

	// Validate MCP configuration
	if source.McpID == "" {
		return nil, fmt.Errorf("MCP execution requires mcp_id")
	}

	toolName := source.ToolName
	if toolName == "" {
		toolName = source.ToolID
	}

	// params 由调用方经 buildExecutionParams + buildMCPParameters 组装好，此处直接使用
	mcpRequest := interfaces.MCPExecutionRequest{
		McpID:      source.McpID,
		ToolName:   toolName,
		Parameters: params,
		Timeout:    mcpExecutionTimeoutSeconds,
	}

	mcpID := source.McpID

	logger.Debugf("Executing MCP: mcp_id=%s, tool_name=%s, params=%+v", mcpID, toolName, params)

	// Execute through agent-operator-integration MCP endpoint
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(mcpRequest.Timeout)*time.Second)
	defer cancel()

	result, err := aoAccess.ExecuteMCP(execCtx, mcpID, toolName, mcpRequest)
	if err != nil {
		logger.Errorf("MCP execution failed: %v", err)
		return nil, fmt.Errorf("MCP execution failed: %w", err)
	}

	logger.Debugf("MCP execution completed successfully")
	return result, nil
}

// buildMCPParameters 合并 MCP 工具的调用参数。
//
// MCP 工具的入参 schema 由工具自身声明（get_action_info 直接把 input_schema 暴露为
// dynamic_params），行动类不必逐项声明 parameters；因此未声明的 dynamic_params 也要透传，
// 否则 MCP 工具会收到空参数。行动类声明过的参数（const/property/input 映射结果）优先级更高，
// 同名时覆盖直通值。MCP 不区分 header/query/path/body，全部平铺。
func buildMCPParameters(params map[string]any, dynamicParams map[string]any) map[string]any {
	merged := make(map[string]any, len(params)+len(dynamicParams))
	for k, v := range dynamicParams {
		merged[k] = v
	}
	for k, v := range params {
		merged[k] = v
	}
	return merged
}
