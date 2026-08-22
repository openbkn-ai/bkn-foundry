// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/bytedance/sonic"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

var lifecycleToolNames = map[string]struct{}{
	"bkn_start_interaction":  {},
	"bkn_finish_interaction": {},
}

func lifecycleToolMiddleware(client *bkntrace.LifecycleClient) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			ctx = withMCPClientApplication(ctx)
			if _, lifecycle := lifecycleToolNames[req.Params.Name]; lifecycle {
				return next(ctx, req)
			}
			return guardBusinessToolCallWithCompletion(
				ensureOperationAdapter(client),
				completeOperationAdapter(client),
				client,
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
				CausationEventIDs: intent.Context.CausationEventIDs, BusinessRefs: intent.Context.BusinessRefs,
			},
			ToolName:     intent.ToolName,
			Protocol:     "mcp",
			SourceModule: "context-loader",
			Input:        normalizedBusinessInput(intent.Input),
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
			Execute:          state.Result.Execute,
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
		payload := any(downstream)
		if downstream.IsError {
			failure, ok := ctx.Value(operationFailureKey{}).(operationFailure)
			if !ok {
				failure = operationFailure{
					Code: "tool_error", Message: toolResultErrorMessage(downstream), Stage: "tool_execution",
				}
			}
			failure.Result = downstream
			payload = failure
		}
		raw, err := sonic.Marshal(payload)
		if err != nil {
			return nil, nil, err
		}
		state := bkntrace.GuardState{Result: bkntrace.OperationResult{
			Operation: operation,
			Receipt:   receipt,
		}}
		retryable, _ := ctx.Value(downstreamRetryableKey{}).(bool)
		result, apiErr, err := bkntrace.NewGuard(client).Finish(
			ctx, state, raw, downstream.IsError, retryable,
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

func toolResultErrorMessage(result *mcpsdk.CallToolResult) string {
	if structured, ok := result.StructuredContent.(map[string]any); ok {
		for _, key := range []string{"message", "error"} {
			if message, ok := structured[key].(string); ok && message != "" {
				return message
			}
		}
	}
	for _, content := range result.Content {
		if text, ok := mcpsdk.AsTextContent(content); ok && text.Text != "" {
			return text.Text
		}
	}
	return "tool returned an error"
}

func normalizedBusinessInput(input map[string]any) json.RawMessage {
	normalized := make(map[string]any, len(input))
	for key, value := range input {
		if key != "bkn_context" {
			normalized[key] = value
		}
	}
	raw, _ := sonic.ConfigStd.Marshal(normalized)
	return raw
}

func hashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// registerLifecycleTools hangs the tracing lifecycle tools straight off the
// server rather than putting them through toolBuilder, but it takes the same
// locale bundle: a client sees one catalogue, and half of it arriving in
// another language is the kind of defect only a non-default deployment shows.
func registerLifecycleTools(
	mcpServer *server.MCPServer,
	client *bkntrace.LifecycleClient,
	locale *mcpLocaleBundle,
) {
	for name := range lifecycleToolNames {
		input, output := locale.ToolSchemas(name)
		mcpServer.AddTool(
			newToolWithSchemas(locale.ToolMeta(name), input, output),
			handleLifecycleTool(client, name),
		)
	}
}

func handleLifecycleTool(
	client *bkntrace.LifecycleClient,
	name string,
) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var err error
		ctx, err = bkntrace.EnsureTraceCorrelation(ctx)
		if err != nil {
			return lifecycleToolError(lifecycleAvailabilityError(err)), nil
		}
		hints, hintErr := hostLifecycleHintsFromRequest(req)
		if hintErr != nil {
			return lifecycleToolError(lifecycleError{
				Code: "lifecycle_hint_invalid", Message: hintErr.Error(),
				RequiredAction: "fix_host_lifecycle_hint",
			}), nil
		}
		args := req.GetArguments()
		if validationErr := validateLifecycleArguments(name, args); validationErr != nil {
			return lifecycleToolError(*validationErr), nil
		}
		ensureLifecycleIdempotency(ctx, name, args, hints)
		if name == "bkn_start_interaction" {
			args["request_hash"] = strings.TrimPrefix(hashBytes([]byte(stringValue(args["question"]))), "sha256:")
		}
		if name == "bkn_start_interaction" &&
			(startConversationMode(args) == "new" || hints.HostConversationKey != "") {
			conversation, result := ensureManagedConversation(ctx, client, hints)
			if result != nil {
				return result, nil
			}
			if supplied := stringValue(args["conversation_id"]); supplied != "" &&
				supplied != conversation.ConversationID {
				return lifecycleToolError(lifecycleError{
					Code:           "conversation_context_conflict",
					Message:        "conversation_id does not match the host conversation mapping",
					RequiredAction: "use_authoritative_conversation",
				}), nil
			}
			args["conversation_id"] = conversation.ConversationID
		}
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
		if name == "bkn_finish_interaction" && stringValue(args["outcome"]) == "completed" {
			if stringValue(args["answer"]) == "" {
				return lifecycleToolError(lifecycleError{
					Code: "closure_manifest_invalid", Message: "completed outcome requires answer",
					RequiredAction: "provide_answer",
				}), nil
			}
			interactionID := stringValue(args["interaction_id"])
			var current bkntrace.Interaction
			apiErr, err := client.Call(
				ctx, http.MethodGet, "/interactions/"+url.PathEscape(interactionID), nil, &current,
			)
			if err != nil {
				return lifecycleToolError(lifecycleAvailabilityError(err)), nil
			}
			if apiErr != nil {
				return lifecycleToolError(lifecycleError(*apiErr)), nil
			}
			if current.ExecutionStatus != "active" && current.ExecutionStatus != "completed" {
				return lifecycleToolError(lifecycleError{
					Code: "terminal_conflict", Message: "another terminal outcome already won",
					CurrentStatus: current.ExecutionStatus, RequiredAction: "reuse_authoritative_interaction",
				}), nil
			}
			if current.ExecutionStatus == "completed" && current.ClosureManifest != nil &&
				current.ClosureManifest.AnswerArtifactRef != "" {
				args["answer_artifact_ref"] = current.ClosureManifest.AnswerArtifactRef
			} else {
				ctx = common.SetAuthoritativeObservedAtIfMissing(ctx, current.UpdatedAt)
				artifactRef, err := bkntrace.RecordInteractionArtifact(
					ctx, current.ConversationID, current.InteractionID,
					bkntrace.InteractionArtifactResult, args["answer"],
				)
				if err != nil {
					return lifecycleToolError(lifecycleAvailabilityError(err)), nil
				}
				args["answer_artifact_ref"] = artifactRef
			}
		}
		method, path, body := lifecycleRequest(name, args)
		target := &bkntrace.Interaction{}
		apiErr, err := client.Call(ctx, method, path, body, target)
		if err != nil {
			return lifecycleToolError(lifecycleAvailabilityError(err)), nil
		}
		if apiErr != nil {
			return lifecycleToolError(lifecycleError(*apiErr)), nil
		}
		if name == "bkn_start_interaction" {
			ctx = common.SetAuthoritativeObservedAtIfMissing(ctx, target.CreatedAt)
			if _, err := bkntrace.RecordInteractionArtifact(
				ctx, target.ConversationID, target.InteractionID,
				bkntrace.InteractionArtifactQuestion, args["question"],
			); err != nil {
				return lifecycleToolError(lifecycleAvailabilityError(err)), nil
			}
		}
		return lifecycleSuccessResult(agentLifecycleView(name, target))
	}
}

func startConversationMode(arguments map[string]any) string {
	if mode := stringValue(arguments["conversation_mode"]); mode != "" {
		return mode
	}
	if stringValue(arguments["conversation_id"]) != "" {
		return "continue"
	}
	return "new"
}

func ensureManagedConversation(
	ctx context.Context,
	client *bkntrace.LifecycleClient,
	hints hostLifecycleHints,
) (bkntrace.Conversation, *mcpsdk.CallToolResult) {
	externalKey := ""
	switch {
	case hints.HostConversationKey != "":
		externalKey = opaqueLifecycleKey("mcp-host", hints.HostConversationKey)
	default:
		var err error
		externalKey, err = newManagedConversationKey()
		if err != nil {
			return bkntrace.Conversation{}, lifecycleToolError(lifecycleAvailabilityError(err))
		}
	}
	idempotencyKey := "mcp-conversation:" + strings.TrimPrefix(hashBytes([]byte(externalKey)), "sha256:")[:32]
	conversation, apiErr, err := client.EnsureCurrentConversation(ctx, externalKey, idempotencyKey)
	if err != nil {
		return bkntrace.Conversation{}, lifecycleToolError(lifecycleAvailabilityError(err))
	}
	if apiErr != nil {
		return bkntrace.Conversation{}, lifecycleToolError(lifecycleError(*apiErr))
	}
	return conversation, nil
}

func opaqueLifecycleKey(prefix string, value string) string {
	return prefix + ":" + strings.TrimPrefix(hashBytes([]byte(value)), "sha256:")
}

func newManagedConversationKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "mcp:" + hex.EncodeToString(raw), nil
}

func agentLifecycleView(name string, target any) any {
	switch value := target.(type) {
	case *bkntrace.Interaction:
		result := map[string]any{
			"interaction_id":   value.InteractionID,
			"conversation_id":  value.ConversationID,
			"execution_status": value.ExecutionStatus,
		}
		if name == "bkn_finish_interaction" {
			result["evidence_status"] = value.EvidenceStatus
		}
		return result
	default:
		return target
	}
}

func ensureLifecycleIdempotency(
	ctx context.Context,
	name string,
	args map[string]any,
	hints hostLifecycleHints,
) {
	if args == nil {
		return
	}
	if name == "bkn_start_interaction" && hints.ClientInvocationID != "" {
		args["idempotency_key"] = opaqueLifecycleKey("mcp-invocation", hints.ClientInvocationID)
		return
	}
	requestID := ""
	if name == "bkn_start_interaction" {
		if traceContext, ok := common.GetTraceContextFromCtx(ctx); ok {
			requestID = traceContext.RequestID
		}
	}
	stableArgs := make(map[string]any, len(args))
	for key, value := range args {
		if key != "idempotency_key" {
			stableArgs[key] = value
		}
	}
	raw, _ := sonic.ConfigStd.Marshal(struct {
		Name      string
		RequestID string
		Args      map[string]any
	}{name, requestID, stableArgs})
	args["idempotency_key"] = "mcp:" + strings.TrimPrefix(hashBytes(raw), "sha256:")[:32]
}

func lifecycleCallResult(target any, apiErr *bkntrace.APIError, err error) (*mcpsdk.CallToolResult, error) {
	if err != nil {
		return lifecycleToolError(lifecycleAvailabilityError(err)), nil
	}
	if apiErr != nil {
		return lifecycleToolError(lifecycleError(*apiErr)), nil
	}
	return lifecycleSuccessResult(target)
}

func lifecycleSuccessResult(target any) (*mcpsdk.CallToolResult, error) {
	raw, err := sonic.Marshal(target)
	if err != nil {
		return nil, err
	}
	return mcpsdk.NewToolResultStructured(target, string(raw)), nil
}

func lifecycleRequest(name string, args map[string]any) (string, string, map[string]any) {
	switch name {
	case "bkn_start_interaction":
		conversationID := url.PathEscape(stringValue(args["conversation_id"]))
		return http.MethodPost, "/conversations/" + conversationID + "/interactions",
			copyArgs(args, "idempotency_key", "request_hash", "agent_name")
	case "bkn_finish_interaction":
		interactionID := url.PathEscape(stringValue(args["interaction_id"]))
		return http.MethodPost, "/interactions/" + interactionID + "/finish",
			copyArgs(args, "outcome", "idempotency_key", "answer_artifact_ref", "reason")
	default:
		return "", "", nil
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
