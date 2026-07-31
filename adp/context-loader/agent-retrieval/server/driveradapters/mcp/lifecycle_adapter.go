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
	"net/http"
	"net/url"
	"sort"
	"strings"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

var lifecycleToolNames = map[string]struct{}{
	"bkn_create_conversation":  {},
	"bkn_resume_conversation":  {},
	"bkn_start_interaction":    {},
	"bkn_complete_interaction": {},
	"bkn_fail_interaction":     {},
	"bkn_cancel_interaction":   {},
	"bkn_handoff_interaction":  {},
	"bkn_close_conversation":   {},
	"bkn_get_operation":        {},
	"bkn_retry_operation":      {},
	"bkn_get_receipt":          {},
}

func lifecycleToolMiddleware(client *bkntrace.LifecycleClient) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			if _, lifecycle := lifecycleToolNames[req.Params.Name]; lifecycle {
				return next(ctx, req)
			}
			return guardBusinessToolCallWithCompletion(
				ensureOperationAdapter(client),
				completeOperationAdapter(client),
				next,
			)(ctx, req)
		}
	}
}

func ensureOperationAdapter(client *bkntrace.LifecycleClient) ensureOperationFunc {
	return func(ctx context.Context, intent operationIntent) (*operationResult, *lifecycleError, error) {
		lifecycleContext, state, _, apiErr, err := bkntrace.NewGuard(client).Begin(ctx, bkntrace.GuardIntent{
			Context: bkntrace.BusinessContext{
				ConversationID: intent.Context.ConversationID, InteractionID: intent.Context.InteractionID,
				OperationKey: intent.Context.OperationKey, ParentOperationID: intent.Context.ParentOperationID,
				CausationEventIDs: intent.Context.CausationEventIDs,
			},
			ToolName:            intent.ToolName,
			NormalizedInputHash: normalizedBusinessInputHash(intent.Input),
		})
		if apiErr != nil {
			value := lifecycleError(*apiErr)
			return nil, &value, nil
		}
		if err != nil {
			return nil, nil, err
		}
		return &operationResult{
			Operation: state.Result.Operation, Receipt: state.Result.Receipt, Created: state.Result.Created,
			Execute:          state.Result.Execute || state.Result.Created,
			LifecycleContext: lifecycleContext,
		}, nil, nil
	}
}

func completeOperationAdapter(client *bkntrace.LifecycleClient) completeOperationFunc {
	return func(
		ctx context.Context,
		ensured *operationResult,
		downstream *mcpsdk.CallToolResult,
	) (*operationResult, *lifecycleError, error) {
		operation, ok := ensured.Operation.(bkntrace.Operation)
		if !ok {
			return nil, nil, nil
		}
		receipt, ok := ensured.Receipt.(bkntrace.Receipt)
		if !ok {
			return nil, nil, nil
		}
		raw, err := json.Marshal(downstream.StructuredContent)
		if err != nil {
			return nil, nil, err
		}
		state := bkntrace.GuardState{Result: bkntrace.OperationResult{
			Operation: operation,
			Receipt:   receipt,
		}}
		retryable, _ := ctx.Value(downstreamRetryableKey{}).(bool)
		result, apiErr, err := bkntrace.NewGuard(client).Finish(
			ctx, state, hashBytes(raw), downstream.IsError, retryable,
		)
		if apiErr != nil {
			value := lifecycleError(*apiErr)
			return &operationResult{
				Operation: result.Operation,
				Receipt:   result.Receipt,
			}, &value, nil
		}
		if err != nil {
			return nil, nil, err
		}
		return &operationResult{Operation: result.Operation, Receipt: result.Receipt}, nil, nil
	}
}

func normalizedBusinessInputHash(input map[string]any) string {
	normalized := make(map[string]any, len(input))
	for key, value := range input {
		if key != "bkn_context" {
			normalized[key] = value
		}
	}
	if rawContext, ok := input["bkn_context"].(map[string]any); ok {
		causationIDs := stringSliceValue(rawContext["causation_event_ids"])
		sort.Strings(causationIDs)
		normalized["bkn_causality"] = map[string]any{
			"parent_operation_id": stringValue(rawContext["parent_operation_id"]),
			"causation_event_ids": causationIDs,
		}
	}
	raw, _ := json.Marshal(normalized)
	return hashBytes(raw)
}

func hashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func registerLifecycleTools(mcpServer *server.MCPServer, client *bkntrace.LifecycleClient) {
	for name := range lifecycleToolNames {
		input, output := loadToolSchemas(name)
		title, description := loadToolMeta(name)
		mcpServer.AddTool(
			newToolWithSchemas(title, description, input, output),
			handleLifecycleTool(client, name),
		)
	}
}

func handleLifecycleTool(
	client *bkntrace.LifecycleClient,
	name string,
) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := req.GetArguments()
		operationID := stringValue(args["operation_id"])
		switch name {
		case "bkn_get_operation":
			target, apiErr, err := client.GetOperation(ctx, operationID)
			return lifecycleCallResult(target, apiErr, err)
		case "bkn_retry_operation":
			target, apiErr, err := client.StartOperationAttempt(ctx, operationID)
			return lifecycleCallResult(target, apiErr, err)
		case "bkn_get_receipt":
			target, apiErr, err := client.GetReceipt(ctx, stringValue(args["receipt_id"]))
			return lifecycleCallResult(target, apiErr, err)
		}
		method, path, body := lifecycleRequest(name, args)
		var target any
		switch name {
		case "bkn_create_conversation", "bkn_resume_conversation", "bkn_close_conversation":
			target = &bkntrace.Conversation{}
		default:
			target = &bkntrace.Interaction{}
		}
		apiErr, err := client.Call(ctx, method, path, body, target)
		if err != nil {
			return lifecycleToolError(lifecycleAvailabilityError(err)), nil
		}
		if apiErr != nil {
			return lifecycleToolError(lifecycleError(*apiErr)), nil
		}
		return mcpsdk.NewToolResultStructured(target, "managed lifecycle state updated"), nil
	}
}

func lifecycleCallResult(target any, apiErr *bkntrace.APIError, err error) (*mcpsdk.CallToolResult, error) {
	if err != nil {
		return lifecycleToolError(lifecycleAvailabilityError(err)), nil
	}
	if apiErr != nil {
		return lifecycleToolError(lifecycleError(*apiErr)), nil
	}
	return mcpsdk.NewToolResultStructured(target, "managed lifecycle state updated"), nil
}

func lifecycleRequest(name string, args map[string]any) (string, string, map[string]any) {
	switch name {
	case "bkn_create_conversation":
		return http.MethodPost, "/conversations:ensure-current", copyArgs(args,
			"external_conversation_key", "idempotency_key", "one_shot")
	case "bkn_resume_conversation":
		return http.MethodPost, "/conversations:resume-by-id", copyArgs(args, "conversation_id")
	case "bkn_start_interaction":
		conversationID := url.PathEscape(stringValue(args["conversation_id"]))
		return http.MethodPost, "/conversations/" + conversationID + "/interactions",
			copyArgs(args, "idempotency_key", "lease_seconds")
	case "bkn_close_conversation":
		conversationID := url.PathEscape(stringValue(args["conversation_id"]))
		return http.MethodPost, "/conversations/" + conversationID + "/close",
			copyArgs(args, "idempotency_key")
	default:
		interactionID := url.PathEscape(stringValue(args["interaction_id"]))
		action := strings.TrimPrefix(strings.TrimSuffix(name, "_interaction"), "bkn_")
		return http.MethodPost, "/interactions/" + interactionID + "/" + action,
			copyArgs(args, "terminal_idempotency_key", "lease_token", "lease_epoch",
				"completion_manifest_version", "answer_artifact_ref", "claims",
				"expected_operations", "expected_receipts", "assembler_deadline", "completion_reason")
	}
}

func copyArgs(args map[string]any, keys ...string) map[string]any {
	result := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := args[key]; ok {
			result[key] = value
		}
	}
	return result
}
