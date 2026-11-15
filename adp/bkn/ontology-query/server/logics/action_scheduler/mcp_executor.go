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

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
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

	// params are assembled by the caller through buildExecutionParams and are used directly here.
	mcpRequest := interfaces.MCPExecutionRequest{
		McpID:      source.McpID,
		ToolName:   toolName,
		Parameters: params,
		Timeout:    mcpExecutionTimeoutSeconds,
	}

	mcpID := source.McpID

	logger.Debugf("Executing MCP: mcp_id=%s, tool_name=%s, parameter_count=%d", mcpID, toolName, len(params))

	// Execute through agent-operator-integration MCP endpoint
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(mcpRequest.Timeout)*time.Second)
	defer cancel()

	var (
		result any
		err    error
	)
	if interfaces.CallerIdentityExecutionFromContext(ctx) {
		result, err = aoAccess.ExecuteMCP(execCtx, mcpID, toolName, mcpRequest)
	} else {
		result, err = aoAccess.ExecuteMCPAsProxy(execCtx, mcpID, toolName, mcpRequest)
	}
	if err != nil {
		logger.Errorf("MCP execution failed: %v", err)
		return nil, fmt.Errorf("MCP execution failed: %w", err)
	}

	logger.Debugf("MCP execution completed successfully")
	return result, nil
}
