// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/logger"
)

type bknContext struct {
	ConversationID    string                 `json:"conversation_id"`
	InteractionID     string                 `json:"interaction_id"`
	OperationKey      string                 `json:"operation_key"`
	ParentOperationID string                 `json:"parent_operation_id,omitempty"`
	CausationEventIDs []string               `json:"causation_event_ids,omitempty"`
	BusinessRefs      []bkntrace.BusinessRef `json:"business_refs,omitempty"`
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
	Execute          bool            `json:"execute"`
	LifecycleContext context.Context `json:"-"`
}

type lifecycleError struct {
	Code                 string `json:"code"`
	Message              string `json:"message"`
	CurrentStatus        string `json:"current_status,omitempty"`
	CurrentInteractionID string `json:"current_interaction_id,omitempty"`
	Retryable            bool   `json:"retryable"`
	RequiredAction       string `json:"required_action,omitempty"`
	RequestID            string `json:"request_id,omitempty"`
	RetryAfterMS         int    `json:"retry_after_ms"`
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
	return guardBusinessToolCallWithCompletion(ensure, nil, nil, next)
}

func guardBusinessToolCallWithCompletion(
	ensure ensureOperationFunc,
	complete completeOperationFunc,
	_ *bkntrace.LifecycleClient,
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
				RequiredAction: "bkn_start_interaction",
			}), nil
		}
		interactionID, _ := rawContext["interaction_id"].(string)
		if interactionID == "" {
			return lifecycleToolError(lifecycleError{
				Code:           "interaction_required",
				Message:        "interaction_id is required",
				RequiredAction: "bkn_start_interaction",
			}), nil
		}
		hints, hintErr := hostLifecycleHintsFromRequest(req)
		if hintErr != nil {
			return lifecycleToolError(lifecycleError{
				Code: "lifecycle_hint_invalid", Message: hintErr.Error(),
				RequiredAction: "fix_host_lifecycle_hint",
			}), nil
		}
		operationKey := managedOperationKey(
			ctx, req, conversationID, interactionID, arguments, hints.ClientInvocationID,
		)
		if ensure == nil {
			return nil, fmt.Errorf("lifecycle operation client is not configured")
		}
		businessRefs, validationErr := parseBusinessRefs(
			rawContext["business_refs"],
			getStringArg(req, "kn_id", getKnIDFromHeader(req)),
		)
		if validationErr != nil {
			return lifecycleToolError(*validationErr), nil
		}
		intent := operationIntent{
			Context: bknContext{
				ConversationID:    conversationID,
				InteractionID:     interactionID,
				OperationKey:      operationKey,
				ParentOperationID: stringValue(rawContext["parent_operation_id"]),
				CausationEventIDs: stringSliceValue(rawContext["causation_event_ids"]),
				BusinessRefs:      businessRefs,
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
		if ensured != nil && !ensured.Execute && status == "pending" {
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
		result, err := callBusinessTool(ctx, req, next)
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

func managedOperationKey(
	ctx context.Context,
	req mcpsdk.CallToolRequest,
	conversationID string,
	interactionID string,
	arguments map[string]any,
	clientInvocationID string,
) string {
	if clientInvocationID != "" {
		payload, _ := json.Marshal(struct {
			ConversationID string
			InteractionID  string
			InvocationKey  string
		}{conversationID, interactionID, opaqueLifecycleKey("mcp-invocation", clientInvocationID)})
		sum := sha256.Sum256(payload)
		return "mcp:" + hex.EncodeToString(sum[:16])
	}
	requestID := ""
	if traceContext, ok := common.GetTraceContextFromCtx(ctx); ok {
		requestID = traceContext.RequestID
	}
	payload, _ := json.Marshal(struct {
		ConversationID string
		InteractionID  string
		ToolName       string
		RequestID      string
		Input          map[string]any
	}{conversationID, interactionID, req.Params.Name, requestID, arguments})
	sum := sha256.Sum256(payload)
	return "mcp:" + hex.EncodeToString(sum[:16])
}

var businessRefPrefixes = map[string]string{
	"knowledge_network": "kn",
	"object_type":       "object",
	"object_instance":   "object_instance",
	"property":          "property",
	"relation_type":     "relation",
	"metric":            "metric",
	"logic":             "logic",
	"function":          "function",
	"action_type":       "action_type",
	"action_instance":   "action_instance",
	"data_resource":     "resource",
}

var businessRefMinSegments = map[string]int{
	"knowledge_network": 2,
	"object_type":       3,
	"object_instance":   4,
	"property":          4,
	"relation_type":     3,
	"metric":            3,
	"logic":             3,
	"function":          3,
	"action_type":       3,
	"action_instance":   4,
	"data_resource":     2,
}

func parseBusinessRefs(value any, currentKnID string) ([]bkntrace.BusinessRef, *lifecycleError) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok || len(items) > 64 {
		return nil, invalidBusinessRefError()
	}
	refs := make([]bkntrace.BusinessRef, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		declaration, ok := item.(map[string]any)
		if !ok {
			return nil, invalidBusinessRefError()
		}
		for field := range declaration {
			if field != "ref_type" && field != "ref_id" && field != "version" {
				return nil, invalidBusinessRefError()
			}
		}
		refType := strings.TrimSpace(stringValue(declaration["ref_type"]))
		refID := strings.TrimSpace(stringValue(declaration["ref_id"]))
		prefix, registered := businessRefPrefixes[refType]
		parts := strings.Split(refID, ":")
		if !registered || len(refID) > 512 || len(parts) < businessRefMinSegments[refType] || parts[0] != prefix {
			return nil, invalidBusinessRefError()
		}
		for _, part := range parts {
			if strings.TrimSpace(part) == "" {
				return nil, invalidBusinessRefError()
			}
		}
		if refType != "data_resource" {
			if currentKnID == "" || len(parts) < 2 || parts[1] != currentKnID {
				return nil, invalidBusinessRefError()
			}
		}
		version := strings.TrimSpace(stringValue(declaration["version"]))
		if version == "" {
			version = "unversioned"
		}
		if len(version) > 128 {
			return nil, invalidBusinessRefError()
		}
		key := refType + "\x00" + refID + "\x00" + version
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, bkntrace.BusinessRef{RefType: refType, RefID: refID, Version: version})
	}
	return refs, nil
}

func invalidBusinessRefError() *lifecycleError {
	return &lifecycleError{
		Code:           "invalid_business_ref",
		Message:        "business_refs must use canonical identifiers from the current knowledge network",
		RequiredAction: "correct_business_refs",
	}
}

func callBusinessTool(
	ctx context.Context,
	req mcpsdk.CallToolRequest,
	next func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error),
) (result *mcpsdk.CallToolResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.DefaultLogger().WithContext(ctx).Errorf(
				"[BKN Trace] MCP business tool %q panicked: %v\n%s",
				req.Params.Name, recovered, debug.Stack(),
			)
			result = mcpsdk.NewToolResultError("business tool execution failed")
			err = nil
		}
	}()
	result, err = next(ctx, req)
	return result, err
}

func lifecycleAvailabilityError(err error) lifecycleError {
	message := "BKN Trace Core is unavailable"
	if errors.Is(err, bkntrace.ErrFeatureNotInstalled) {
		message = "BKN Trace Core is not configured"
	}
	// See the HTTP twin in driveradapters/lifecycle_middleware.go: the caller is
	// told the dependency is unavailable, not which product to buy.
	return lifecycleError{Code: "feature_not_installed", Message: message}
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
	if value.CurrentInteractionID != "" {
		errorValue["current_interaction_id"] = value.CurrentInteractionID
	}
	if value.RequestID != "" {
		errorValue["request_id"] = value.RequestID
	}
	envelope := map[string]any{"error": errorValue}
	raw, _ := json.Marshal(envelope)
	result := mcpsdk.NewToolResultStructured(envelope, string(raw))
	result.IsError = true
	return result
}
