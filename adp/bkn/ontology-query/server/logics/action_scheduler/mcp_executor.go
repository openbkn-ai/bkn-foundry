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

	// params are assembled by the caller through buildExecutionParams + buildMCPParameters and are used directly here.
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

// buildMCPParameters merges MCP tool call parameters.
//
// The MCP tool input schema is declared by the tool itself; get_action_info exposes input_schema directly as
// dynamic_params. The action type does not need to declare parameters one by one, so undeclared dynamic_params must also pass through.
// Otherwise the MCP tool receives empty parameters. Parameters declared by the action type, including const/property/input mapping results, have higher precedence.
// When names collide, declared values override pass-through values. MCP does not distinguish header/query/path/body; all parameters are flattened.
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
