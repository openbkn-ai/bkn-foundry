// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"ontology-query/interfaces"
)

const toolExecutionTimeoutSeconds int64 = 300

// ExecuteTool executes a tool-based action through tool-box API
// API: POST /tool-box/{box_id}/proxy/{tool_id}
func ExecuteTool(ctx context.Context, aoAccess interfaces.AgentOperatorAccess, actionType *interfaces.ActionType, params map[string]any) (any, error) {
	source := actionType.ActionSource

	// Validate tool configuration
	if source.BoxID == "" || source.ToolID == "" {
		return nil, fmt.Errorf("tool execution requires box_id and tool_id")
	}

	// Build tool execution request using ActionType.Parameters configuration
	execRequest := buildToolExecutionRequest(actionType.Parameters, params)
	execRequest.Timeout = toolExecutionTimeoutSeconds

	logger.Debugf("Executing tool: box_id=%s, tool_id=%s, request=%+v", source.BoxID, source.ToolID, execRequest)

	// Execute through tool-box API
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(execRequest.Timeout)*time.Second)
	defer cancel()

	result, err := aoAccess.ExecuteToolAsProxy(execCtx, source.BoxID, source.ToolID, execRequest)
	if err != nil {
		logger.Errorf("Tool execution failed: %v", err)
		return nil, fmt.Errorf("tool execution failed: %w", err)
	}

	if proxy, ok := interfaces.TrustedProxyContextFromContext(ctx); ok &&
		proxy.Binding.TargetType == interfaces.ProxyTargetTypeFunction {
		if err := functionExitError(result); err != nil {
			logger.Errorf("Function execution failed: %v", err)
			// The sandbox outcome is kept beside the error: its stderr is what explains the failure.
			return result, err
		}
	}

	logger.Debugf("Tool execution completed successfully")
	return result, nil
}

// functionExitError reports a Function that ran but exited non-zero.
//
// Execution Factory's Function runtime answers HTTP 200 whatever the sandbox outcome is and
// carries that outcome in the body, so a successful transport says nothing about the Function
// itself. Only Function targets are inspected: an exit_code field in an OpenAPI Tool's own
// response body belongs to that Tool and keeps its meaning.
func functionExitError(result any) error {
	body, ok := result.(map[string]any)
	if !ok {
		return nil
	}
	exitCode, ok := body["exit_code"]
	if !ok || exitCode == nil {
		return nil
	}
	code := strings.TrimSpace(fmt.Sprint(exitCode))
	if code == "0" || code == "" {
		return nil
	}
	if detail := lastLine(body["stderr"]); detail != "" {
		return fmt.Errorf("function exited with code %s: %s", code, detail)
	}
	return fmt.Errorf("function exited with code %s", code)
}

// lastLine returns the last non-empty line of a stderr value, bounded so a runaway trace cannot
// flood the execution record. A Python traceback ends with the exception itself.
func lastLine(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	const maxLen = 500
	if runes := []rune(line); len(runes) > maxLen {
		line = string(runes[:maxLen]) + "..."
	}
	return line
}

// buildToolExecutionRequest builds ToolExecutionRequest based on ActionType.Parameters configuration
// Note: params already contains processed values from buildExecutionParams
func buildToolExecutionRequest(configParams []interfaces.Parameter, params map[string]any) interfaces.ToolExecutionRequest {
	request := interfaces.ToolExecutionRequest{
		Header: map[string]any{},
		Query:  map[string]any{},
		Body:   map[string]any{},
		Path:   map[string]any{},
	}

	// If no parameters configured, put all params in body (backward compatible)
	if len(configParams) == 0 {
		request.Body = params
		return request
	}

	// Process each configured parameter - get value from params (already processed by buildExecutionParams)
	// and assign to the appropriate location based on Source.
	// Uses getNestedValue/setNestedValue to support dot-separated nested parameter names (e.g. "props.headers").
	for _, param := range configParams {
		value := getNestedValue(params, param.Name)
		if value == nil {
			continue
		}

		switch strings.ToLower(param.Source) {
		case interfaces.PARAMETER_HEADER:
			setNestedValue(request.Header, param.Name, value)
		case interfaces.PARAMETER_QUERY:
			setNestedValue(request.Query, param.Name, value)
		case interfaces.PARAMETER_BODY:
			setNestedValue(request.Body, param.Name, value)
		case interfaces.PARAMETER_PATH:
			setNestedValue(request.Path, param.Name, value)
		default:
			setNestedValue(request.Body, param.Name, value)
		}
	}

	return request
}

// getNestedValue retrieves a value from a map using a dot-separated key for nested access.
func getNestedValue(data map[string]any, key string) any {
	if data == nil {
		return nil
	}

	if strings.Contains(key, ".") {
		parts := strings.Split(key, ".")
		current := data

		for i, part := range parts {
			if i == len(parts)-1 {
				return current[part]
			}
			if next, ok := current[part].(map[string]any); ok {
				current = next
			} else {
				return nil
			}
		}
	}

	return data[key]
}

// setNestedValue sets a value in a map using a dot-separated key, creating intermediate maps as needed.
func setNestedValue(target map[string]any, key string, value any) {
	if value == nil {
		return
	}

	if strings.Contains(key, ".") {
		parts := strings.Split(key, ".")
		current := target

		for i, part := range parts {
			if i == len(parts)-1 {
				current[part] = value
				return
			}
			if _, exists := current[part]; !exists {
				current[part] = make(map[string]any)
			}
			if next, ok := current[part].(map[string]any); ok {
				current = next
			} else {
				current[part] = make(map[string]any)
				current = current[part].(map[string]any)
			}
		}
		return
	}

	target[key] = value
}
