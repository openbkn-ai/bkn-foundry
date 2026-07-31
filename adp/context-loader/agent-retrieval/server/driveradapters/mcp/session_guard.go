// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

type bknContext struct {
	ConversationID    string   `json:"conversation_id"`
	InteractionID     string   `json:"interaction_id"`
	OperationKey      string   `json:"operation_key"`
	ParentOperationID string   `json:"parent_operation_id,omitempty"`
	CausationEventIDs []string `json:"causation_event_ids,omitempty"`
}

type operationIntent struct {
	Context  bknContext
	ToolName string
	Input    map[string]any
}

type operationResult struct {
	Operation        any             `json:"operation"`
	Receipt          any             `json:"receipt"`
	Created          bool            `json:"created"`
	LifecycleContext context.Context `json:"-"`
}

type lifecycleError struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	CurrentStatus  string `json:"current_status,omitempty"`
	Retryable      bool   `json:"retryable"`
	RequiredAction string `json:"required_action,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	RetryAfterMS   int    `json:"retry_after_ms"`
}

type downstreamRetryableKey struct{}

type ensureOperationFunc func(context.Context, operationIntent) (*operationResult, *lifecycleError, error)
type completeOperationFunc func(
	context.Context,
	*operationResult,
	*mcpsdk.CallToolResult,
) (*operationResult, *lifecycleError, error)

func guardBusinessToolCall(
	ensure ensureOperationFunc,
	next func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error),
) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return guardBusinessToolCallWithCompletion(ensure, nil, next)
}

func guardBusinessToolCallWithCompletion(
	ensure ensureOperationFunc,
	complete completeOperationFunc,
	next func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error),
) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		arguments := req.GetArguments()
		rawContext, _ := arguments["bkn_context"].(map[string]any)
		conversationID, _ := rawContext["conversation_id"].(string)
		if conversationID == "" {
			return lifecycleToolError(lifecycleError{
				Code:           "conversation_required",
				Message:        "conversation_id is required",
				RequiredAction: "create_conversation",
			}), nil
		}
		interactionID, _ := rawContext["interaction_id"].(string)
		if interactionID == "" {
			return lifecycleToolError(lifecycleError{
				Code:           "interaction_required",
				Message:        "interaction_id is required",
				RequiredAction: "start_interaction",
			}), nil
		}
		operationKey, _ := rawContext["operation_key"].(string)
		if operationKey == "" {
			return lifecycleToolError(lifecycleError{
				Code:           "operation_required",
				Message:        "operation_key is required",
				RequiredAction: "ensure_operation",
			}), nil
		}
		if ensure == nil {
			return nil, fmt.Errorf("lifecycle operation client is not configured")
		}
		intent := operationIntent{
			Context: bknContext{
				ConversationID:    conversationID,
				InteractionID:     interactionID,
				OperationKey:      operationKey,
				ParentOperationID: stringValue(rawContext["parent_operation_id"]),
				CausationEventIDs: stringSliceValue(rawContext["causation_event_ids"]),
			},
			ToolName: req.Params.Name,
			Input:    arguments,
		}
		ensured, lifecycleErr, err := ensure(ctx, intent)
		if err != nil {
			return lifecycleToolError(lifecycleAvailabilityError(err)), nil
		} else if lifecycleErr != nil {
			return lifecycleToolError(*lifecycleErr), nil
		}
		status := ""
		if ensured != nil {
			status = receiptStatus(ensured.Receipt)
		}
		if status == "completed" || status == "failed" {
			replay := map[string]any{"operation": ensured.Operation, "receipt": ensured.Receipt}
			result := mcpsdk.NewToolResultStructured(replay, "operation is terminal; durable receipt reused")
			result.IsError = status == "failed"
			return result, nil
		}
		if ensured != nil && !ensured.Created && status == "pending" {
			return receiptPendingToolError(ensured.Receipt), nil
		}
		if ensured != nil {
			if ensured.LifecycleContext != nil {
				ctx = ensured.LifecycleContext
			}
			operationID, attempt := operationIdentity(ensured.Operation)
			traceContext, _ := common.GetTraceContextFromCtx(ctx)
			traceContext.ConversationID = conversationID
			traceContext.InteractionID = interactionID
			traceContext.OperationID = operationID
			traceContext.Attempt = attempt
			ctx = common.SetTraceContextToCtx(ctx, traceContext)
		}
		result, err := next(ctx, req)
		if err != nil {
			result = mcpsdk.NewToolResultError(err.Error())
			ctx = context.WithValue(ctx, downstreamRetryableKey{}, true)
			err = nil
		}
		if result == nil || complete == nil {
			return result, err
		}
		completed, lifecycleErr, err := complete(ctx, ensured, result)
		if err != nil {
			return lifecycleToolError(lifecycleAvailabilityError(err)), nil
		}
		if lifecycleErr != nil {
			errorResult := lifecycleToolError(*lifecycleErr)
			if completed != nil && lifecycleErr.Code == "receipt_pending" {
				structured := errorResult.StructuredContent.(map[string]any)
				structured["receipt"] = completed.Receipt
			}
			return errorResult, nil
		}
		if completed != nil {
			if structured, ok := result.StructuredContent.(map[string]any); ok {
				structured["bkn_receipt"] = completed.Receipt
			} else {
				result.StructuredContent = map[string]any{
					"result": result.StructuredContent, "bkn_receipt": completed.Receipt,
				}
			}
		}
		return result, nil
	}
}

func lifecycleAvailabilityError(err error) lifecycleError {
	message := "BKN Trace Core is unavailable"
	if errors.Is(err, bkntrace.ErrFeatureNotInstalled) {
		message = "BKN Trace Core is not configured"
	}
	return lifecycleError{
		Code: "feature_not_installed", Message: message,
		RequiredAction: "install_enterprise_implementation",
	}
}

func operationIdentity(operation any) (string, int) {
	raw, err := json.Marshal(operation)
	if err != nil {
		return "", 0
	}
	var value struct {
		OperationID string `json:"operation_id"`
		Attempt     int    `json:"attempt"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", 0
	}
	return value.OperationID, value.Attempt
}

func receiptPendingToolError(receipt any) *mcpsdk.CallToolResult {
	errorValue := lifecycleError{
		Code: "receipt_pending", Message: "operation receipt is still pending",
		Retryable: true, RequiredAction: "poll_receipt",
	}
	result := lifecycleToolError(errorValue)
	structured := result.StructuredContent.(map[string]any)
	structured["receipt"] = receipt
	return result
}

func receiptStatus(receipt any) string {
	if value, ok := receipt.(map[string]any); ok {
		status, _ := value["receipt_status"].(string)
		return status
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return ""
	}
	var value struct {
		Status string `json:"receipt_status"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.Status
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func stringSliceValue(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func lifecycleToolError(value lifecycleError) *mcpsdk.CallToolResult {
	errorValue := map[string]any{
		"code":            value.Code,
		"message":         value.Message,
		"retryable":       value.Retryable,
		"required_action": value.RequiredAction,
		"retry_after_ms":  value.RetryAfterMS,
	}
	if value.CurrentStatus != "" {
		errorValue["current_status"] = value.CurrentStatus
	}
	if value.RequestID != "" {
		errorValue["request_id"] = value.RequestID
	}
	envelope := map[string]any{"error": errorValue}
	result := mcpsdk.NewToolResultStructured(envelope, fmt.Sprintf("%s: %s", value.Code, value.Message))
	result.IsError = true
	return result
}
