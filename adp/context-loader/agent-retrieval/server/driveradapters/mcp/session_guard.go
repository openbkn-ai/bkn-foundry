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
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/bytedance/sonic"
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
			return receiptTerminalToolError(status, ensured.Operation, ensured.Receipt), nil
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
		result, err, failure := callBusinessTool(ctx, req, next)
		if err != nil {
			failure = &operationFailure{
				Code: "downstream_error", Message: err.Error(), Stage: "tool_execution",
			}
			result = mcpsdk.NewToolResultError(err.Error())
			ctx = context.WithValue(ctx, downstreamRetryableKey{}, true)
			err = nil
		}
		if failure != nil {
			ctx = context.WithValue(ctx, operationFailureKey{}, *failure)
		}
		if result == nil || complete == nil {
			return result, err
		}
		completed, lifecycleErr, err := complete(ctx, ensured, result)
		if err != nil {
			logger.DefaultLogger().WithContext(ctx).Errorf(
				"[BKN Trace] failed to finalize MCP tool %q: %v", req.Params.Name, err,
			)
			return result, nil
		}
		if lifecycleErr != nil {
			logger.DefaultLogger().WithContext(ctx).Errorf(
				"[BKN Trace] failed to finalize MCP tool %q: %s", req.Params.Name, lifecycleErr.Code,
			)
			return result, nil
		}
		if completed != nil {
			attachReceipt(result, completed.Receipt)
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
		payload, _ := sonic.Marshal(struct {
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
	payload, _ := sonic.Marshal(struct {
		ConversationID string
		InteractionID  string
		ToolName       string
		RequestID      string
		Input          map[string]any
	}{conversationID, interactionID, req.Params.Name, requestID, arguments})
	sum := sha256.Sum256(payload)
	return "mcp:" + hex.EncodeToString(sum[:16])
}

func parseBusinessRefs(value any, currentKnID string) ([]bkntrace.BusinessRef, *lifecycleError) {
	refs, apiErr := bkntrace.ParseBusinessRefs(value, currentKnID)
	if apiErr == nil {
		return refs, nil
	}
	return nil, &lifecycleError{
		Code: apiErr.Code, Message: apiErr.Message, RequiredAction: apiErr.RequiredAction,
	}
}

func callBusinessTool(
	ctx context.Context,
	req mcpsdk.CallToolRequest,
	next func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error),
) (result *mcpsdk.CallToolResult, err error, failure *operationFailure) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.DefaultLogger().WithContext(ctx).Errorf(
				"[BKN Trace] MCP business tool %q panicked: %v\n%s",
				req.Params.Name, recovered, debug.Stack(),
			)
			result = mcpsdk.NewToolResultError("business tool execution failed")
			err = nil
			failure = &operationFailure{
				Code: "handler_panic", Message: fmt.Sprint(recovered), Stage: "tool_execution",
			}
		}
	}()
	result, err = next(ctx, req)
	return result, err, failure
}

type operationFailureKey struct{}

type operationFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Stage   string `json:"stage"`
	Result  any    `json:"result,omitempty"`
}

func lifecycleAvailabilityError(err error) lifecycleError {
	// Keep this mapping aligned with the REST twin in lifecycle_middleware.go
	// and the Core lifecycle error registry.
	if errors.Is(err, bkntrace.ErrFeatureNotInstalled) {
		return lifecycleError{
			Code: "feature_not_installed", Message: "BKN Trace Core is not configured",
		}
	}
	var coreErr *bkntrace.CoreHTTPError
	if errors.As(err, &coreErr) {
		message := coreErr.Message
		if message == "" {
			message = "BKN Trace evidence could not be recorded"
		}
		// A missing ingest credential is a deployment defect, not a transient
		// Core outage. Returning retry_later here made a caller start another
		// interaction after Core had already accepted the first one, leaving
		// empty active interactions behind until their leases expired.
		if coreErr.Code == "INGEST_AUTH_NOT_CONFIGURED" {
			return lifecycleError{
				Code: "evidence_capture_failed", Message: message,
				RequiredAction: "contact_platform_operator",
			}
		}
		switch coreErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return lifecycleError{
				Code: "evidence_capture_denied", Message: message,
				RequiredAction: "request_authorization",
			}
		default:
			if coreErr.Retryable() {
				return lifecycleError{
					Code: "trace_core_unavailable", Message: "BKN Trace Core is temporarily unavailable",
					Retryable: true, RequiredAction: "retry_later",
				}
			}
			return lifecycleError{
				Code: "evidence_capture_failed", Message: message,
				RequiredAction: "contact_platform_operator",
			}
		}
	}
	return lifecycleError{
		Code: "trace_core_unavailable", Message: "BKN Trace Core is temporarily unavailable",
		Retryable: true, RequiredAction: "retry_later",
	}
}

func operationIdentity(operation any) (string, int) {
	raw, err := sonic.Marshal(operation)
	if err != nil {
		return "", 0
	}
	var value struct {
		OperationID string `json:"operation_id"`
		Attempt     int    `json:"attempt"`
	}
	if err := sonic.Unmarshal(raw, &value); err != nil {
		return "", 0
	}
	return value.OperationID, value.Attempt
}

func receiptPendingToolError(receipt any) *mcpsdk.CallToolResult {
	errorValue := lifecycleError{
		Code: "receipt_pending", Message: "operation receipt is still pending",
		Retryable: true, RequiredAction: "poll_receipt",
	}
	return lifecycleToolErrorWithDetails(errorValue, map[string]any{"receipt": receipt})
}

// receiptTerminalToolError replays a terminal operation without re-executing the tool.
//
// A lost response leaves nothing but the durable receipt: the tool did not run again, so there is no
// payload in the shape its output schema declares. Returning that receipt as a successful structured
// result made every schema-validating host reject the call, and handed an agent a receipt where it
// had asked for rows. It travels as a lifecycle error instead -- hosts skip output validation on
// error results, and the caller is told plainly that this operation is already terminal.
func receiptTerminalToolError(status string, operation any, receipt any) *mcpsdk.CallToolResult {
	errorValue := lifecycleError{
		Code:           "receipt_terminal",
		Message:        "operation is terminal; durable receipt reused, the tool was not re-executed",
		CurrentStatus:  status,
		RequiredAction: "read_receipt",
	}
	return lifecycleToolErrorWithDetails(errorValue, map[string]any{
		"operation": operation, "receipt": receipt,
	})
}

// attachReceipt hangs the durable receipt on a tool result without reshaping the tool's own payload.
//
// Business handlers hand back their response struct as structured content, so the map assertion used
// to miss and the whole payload got nested under "result". That silently broke the contract every
// tool publishes in its output schema, and search_instance -- the only schema with a required field
// -- failed validation outright on hosts that check it. Marshalling the struct into a map keeps the
// tool's own fields at the top level, which is where both the schema and the agent look for them.
//
// Payloads that are not JSON objects (text-only or errored results) have no shape worth preserving,
// so they keep the nested envelope rather than losing the receipt.
func attachReceipt(result *mcpsdk.CallToolResult, receipt any) {
	if structured, ok := result.StructuredContent.(map[string]any); ok {
		structured["bkn_receipt"] = receipt
		return
	}
	if payload, ok := structuredContentAsMap(result.StructuredContent); ok {
		payload["bkn_receipt"] = receipt
		result.StructuredContent = payload
		return
	}
	result.StructuredContent = map[string]any{
		"result": result.StructuredContent, "bkn_receipt": receipt,
	}
}

// structuredContentAsMap re-reads a structured payload as a JSON object.
//
// The decode keeps wide integers as json.Number rather than rounding them through float64: this
// round-trip sits on the way out to the client, and is the last place the precision the driven
// adapters preserved could still be lost.
func structuredContentAsMap(value any) (map[string]any, bool) {
	if value == nil {
		return nil, false
	}
	raw, err := sonic.ConfigStd.Marshal(value)
	if err != nil {
		return nil, false
	}
	var payload map[string]any
	if err := common.UnmarshalPreciseJSON(raw, &payload); err != nil || payload == nil {
		return nil, false
	}
	return payload, true
}

func receiptStatus(receipt any) string {
	if value, ok := receipt.(map[string]any); ok {
		status, _ := value["receipt_status"].(string)
		return status
	}
	raw, err := sonic.Marshal(receipt)
	if err != nil {
		return ""
	}
	var value struct {
		Status string `json:"receipt_status"`
	}
	_ = sonic.Unmarshal(raw, &value)
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
	return lifecycleToolErrorWithDetails(value, nil)
}

func lifecycleToolErrorWithDetails(value lifecycleError, details map[string]any) *mcpsdk.CallToolResult {
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
	for key, detail := range details {
		envelope[key] = detail
	}
	raw, _ := sonic.Marshal(envelope)
	return mcpsdk.NewToolResultError(string(raw))
}
