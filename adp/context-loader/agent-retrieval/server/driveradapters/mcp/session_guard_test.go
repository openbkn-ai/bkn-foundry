// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestSessionGuardMissingConversationFailsClosed(t *testing.T) {
	coreCalls := 0
	downstreamCalls := 0
	guarded := guardBusinessToolCall(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			coreCalls++
			return nil, nil, nil
		},
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			downstreamCalls++
			return mcpsdk.NewToolResultText("unexpected"), nil
		},
	)

	result, err := guarded(context.Background(), mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{
			Name:      "search_schema",
			Arguments: map[string]any{"bkn_context": map[string]any{"interaction_id": "int_1", "operation_key": "op-key-1"}},
		},
	})
	if err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
	if coreCalls != 0 || downstreamCalls != 0 {
		t.Fatalf("fail-closed requires zero calls, got core=%d downstream=%d", coreCalls, downstreamCalls)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected structured lifecycle error, got %#v", result.StructuredContent)
	}
	errValue, ok := structured["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error envelope, got %#v", structured)
	}
	if errValue["code"] != "conversation_required" ||
		errValue["required_action"] != "create_conversation" ||
		errValue["retryable"] != false {
		t.Fatalf("unexpected lifecycle error: %#v", errValue)
	}
}

func TestSessionGuardMissingInteractionFailsClosed(t *testing.T) {
	coreCalls := 0
	downstreamCalls := 0
	guarded := guardBusinessToolCall(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			coreCalls++
			return nil, nil, nil
		},
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			downstreamCalls++
			return mcpsdk.NewToolResultText("unexpected"), nil
		},
	)

	result, err := guarded(context.Background(), mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{
			Name: "search_schema",
			Arguments: map[string]any{"bkn_context": map[string]any{
				"conversation_id": "conv_1",
				"operation_key":   "op-key-1",
			}},
		},
	})
	if err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
	if coreCalls != 0 || downstreamCalls != 0 {
		t.Fatalf("fail-closed requires zero calls, got core=%d downstream=%d", coreCalls, downstreamCalls)
	}
	errValue := result.StructuredContent.(map[string]any)["error"].(map[string]any)
	if errValue["code"] != "interaction_required" || errValue["required_action"] != "start_interaction" {
		t.Fatalf("unexpected lifecycle error: %#v", errValue)
	}
}

func TestSessionGuardCoreRejectionPreventsDownstreamCall(t *testing.T) {
	downstreamCalls := 0
	guarded := guardBusinessToolCall(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return nil, &lifecycleError{
				Code:           "interaction_terminal",
				Message:        "interaction is terminal",
				RequiredAction: "start_interaction",
			}, nil
		},
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			downstreamCalls++
			return mcpsdk.NewToolResultText("unexpected"), nil
		},
	)

	result, err := guarded(context.Background(), mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{
			Name: "search_schema",
			Arguments: map[string]any{"bkn_context": map[string]any{
				"conversation_id": "conv_1",
				"interaction_id":  "int_1",
				"operation_key":   "op-key-1",
			}},
		},
	})
	if err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
	if downstreamCalls != 0 {
		t.Fatalf("core rejection must prevent downstream call, got %d", downstreamCalls)
	}
	errValue := result.StructuredContent.(map[string]any)["error"].(map[string]any)
	if errValue["code"] != "interaction_terminal" {
		t.Fatalf("unexpected lifecycle error: %#v", errValue)
	}
}

func TestSessionGuardDistinguishesUninstalledAndUnavailableCore(t *testing.T) {
	tests := []struct {
		name          string
		ensure        ensureOperationFunc
		wantCode      string
		wantAction    string
		wantRetryable bool
	}{
		{
			name:     "not configured",
			ensure:   ensureOperationAdapter(bkntrace.NewLifecycleClient("", nil)),
			wantCode: "feature_not_installed", wantAction: "install_enterprise_implementation",
		},
		{
			name: "runtime unavailable",
			ensure: func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
				return nil, nil, errors.New("connection refused")
			},
			wantCode: "feature_not_installed", wantAction: "install_enterprise_implementation",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			downstreamCalls := 0
			result, err := guardBusinessToolCall(
				test.ensure,
				func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
					downstreamCalls++
					return mcpsdk.NewToolResultText("unexpected"), nil
				},
			)(trustedSessionGuardContext(), validBusinessToolRequest())
			if err != nil {
				t.Fatalf("guard returned protocol error: %v", err)
			}
			if downstreamCalls != 0 {
				t.Fatalf("Core failure must keep downstream at zero")
			}
			value := result.StructuredContent.(map[string]any)["error"].(map[string]any)
			if value["code"] != test.wantCode || value["required_action"] != test.wantAction ||
				value["retryable"] != test.wantRetryable {
				t.Fatalf("wrong Core availability semantics: %#v", value)
			}
		})
	}
}

func trustedSessionGuardContext() context.Context {
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	return common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "client-1"},
	})
}

func TestSessionGuardLostResponseReplayReturnsExistingReceiptWithoutDownstream(t *testing.T) {
	downstreamCalls := 0
	guarded := guardBusinessToolCall(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Operation: map[string]any{"operation_id": "op-1", "attempt_status": "completed"},
				Receipt: map[string]any{
					"receipt_id": "receipt-1", "receipt_status": "completed",
				},
			}, nil, nil
		},
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			downstreamCalls++
			return mcpsdk.NewToolResultText("unexpected"), nil
		},
	)

	result, err := guarded(context.Background(), validBusinessToolRequest())
	if err != nil {
		t.Fatalf("replay returned protocol error: %v", err)
	}
	if downstreamCalls != 0 {
		t.Fatalf("completed operation replay must not call downstream, got %d", downstreamCalls)
	}
	structured := result.StructuredContent.(map[string]any)
	receipt := structured["receipt"].(map[string]any)
	if receipt["receipt_id"] != "receipt-1" {
		t.Fatalf("replay did not return durable receipt: %#v", structured)
	}
}

func TestSessionGuardFailedResponseReplayReturnsExistingReceiptWithoutDownstream(t *testing.T) {
	downstreamCalls := 0
	guarded := guardBusinessToolCall(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Operation: map[string]any{"operation_id": "op-1", "attempt_status": "failed"},
				Receipt: map[string]any{
					"receipt_id": "receipt-1", "receipt_status": "failed",
				},
			}, nil, nil
		},
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			downstreamCalls++
			return mcpsdk.NewToolResultText("unexpected"), nil
		},
	)

	result, err := guarded(context.Background(), validBusinessToolRequest())
	if err != nil {
		t.Fatalf("replay returned protocol error: %v", err)
	}
	if downstreamCalls != 0 {
		t.Fatalf("failed operation replay must not call downstream, got %d", downstreamCalls)
	}
	receipt := result.StructuredContent.(map[string]any)["receipt"].(map[string]any)
	if receipt["receipt_status"] != "failed" {
		t.Fatalf("replay did not return failed durable receipt: %#v", result.StructuredContent)
	}
}

func TestSessionGuardPendingReplayReturnsReceiptPendingWithoutDownstream(t *testing.T) {
	downstreamCalls := 0
	guarded := guardBusinessToolCall(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Created:   false,
				Operation: map[string]any{"operation_id": "op-1", "attempt_status": "pending"},
				Receipt: map[string]any{
					"receipt_id": "receipt-1", "receipt_status": "pending",
				},
			}, nil, nil
		},
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			downstreamCalls++
			return mcpsdk.NewToolResultText("unexpected"), nil
		},
	)

	result, err := guarded(context.Background(), validBusinessToolRequest())
	if err != nil {
		t.Fatalf("pending replay returned protocol error: %v", err)
	}
	if downstreamCalls != 0 {
		t.Fatalf("pending replay must not call downstream, got %d", downstreamCalls)
	}
	structured := result.StructuredContent.(map[string]any)
	errorValue := structured["error"].(map[string]any)
	if errorValue["code"] != "receipt_pending" ||
		errorValue["required_action"] != "poll_receipt" ||
		structured["receipt"].(map[string]any)["receipt_id"] != "receipt-1" {
		t.Fatalf("unexpected pending replay result: %#v", structured)
	}
}

func TestSessionGuardDoesNotInferExecutionFromCreated(t *testing.T) {
	downstreamCalls := 0
	guarded := guardBusinessToolCall(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Created: true,
				Execute: false,
				Operation: map[string]any{
					"operation_id": "op-legacy", "attempt_status": "pending",
				},
				Receipt: map[string]any{
					"receipt_id": "receipt-legacy", "receipt_status": "pending",
				},
			}, nil, nil
		},
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			downstreamCalls++
			return mcpsdk.NewToolResultText("unexpected"), nil
		},
	)

	result, err := guarded(context.Background(), validBusinessToolRequest())
	if err != nil {
		t.Fatalf("missing execute returned protocol error: %v", err)
	}
	if downstreamCalls != 0 {
		t.Fatalf("created without execute must fail closed, downstream calls=%d", downstreamCalls)
	}
	errorValue := result.StructuredContent.(map[string]any)["error"].(map[string]any)
	if errorValue["code"] != "receipt_pending" {
		t.Fatalf("missing execute returned wrong lifecycle error: %#v", result.StructuredContent)
	}
}

func TestSessionGuardExecutesPreparedRetryAttempt(t *testing.T) {
	downstreamCalls := 0
	guarded := guardBusinessToolCall(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Created: false,
				Execute: true,
				Operation: map[string]any{
					"operation_id": "op-1", "attempt": 2, "attempt_status": "pending",
				},
				Receipt: map[string]any{
					"receipt_id": "receipt-2", "receipt_status": "pending",
				},
			}, nil, nil
		},
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			downstreamCalls++
			return mcpsdk.NewToolResultText("retried"), nil
		},
	)

	result, err := guarded(context.Background(), validBusinessToolRequest())
	if err != nil || result == nil || result.IsError {
		t.Fatalf("prepared retry attempt did not execute: result=%#v err=%v", result, err)
	}
	if downstreamCalls != 1 {
		t.Fatalf("prepared retry attempt executed %d times, want 1", downstreamCalls)
	}
}

func TestSessionGuardFinishPendingPreservesStableReceipt(t *testing.T) {
	guarded := guardBusinessToolCallWithCompletion(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Created:   true,
				Execute:   true,
				Operation: map[string]any{"operation_id": "op-1", "attempt": float64(1)},
				Receipt:   map[string]any{"receipt_id": "receipt-1", "receipt_status": "pending"},
			}, nil, nil
		},
		func(context.Context, *operationResult, *mcpsdk.CallToolResult) (*operationResult, *lifecycleError, error) {
			return &operationResult{
					Operation: map[string]any{"operation_id": "op-1", "attempt": float64(1)},
					Receipt:   map[string]any{"receipt_id": "receipt-1", "receipt_status": "pending"},
				}, &lifecycleError{
					Code: "receipt_pending", Message: "finalization not confirmed",
					Retryable: true, RequiredAction: "poll_receipt",
				}, nil
		},
		nil,
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(map[string]any{"answer": "ok"}, `{"answer":"ok"}`), nil
		},
	)

	result, err := guarded(context.Background(), validBusinessToolRequest())
	if err != nil {
		t.Fatalf("finish pending escaped as protocol error: %v", err)
	}
	structured := result.StructuredContent.(map[string]any)
	errorValue := structured["error"].(map[string]any)
	receipt := structured["receipt"].(map[string]any)
	if errorValue["code"] != "receipt_pending" ||
		errorValue["required_action"] != "poll_receipt" ||
		receipt["receipt_id"] != "receipt-1" {
		t.Fatalf("finish pending lost stable receipt: %#v", structured)
	}
}

func TestSessionGuardPropagatesStableOperationIDToExecuteAction(t *testing.T) {
	guarded := guardBusinessToolCall(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Created:   true,
				Execute:   true,
				Operation: map[string]any{"operation_id": "op-stable-1", "attempt": float64(1)},
				Receipt:   map[string]any{"receipt_id": "receipt-1", "receipt_status": "pending"},
			}, nil, nil
		},
		func(ctx context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			traceContext, _ := common.GetTraceContextFromCtx(ctx)
			if traceContext.OperationID != "op-stable-1" || traceContext.Attempt != 1 {
				t.Fatalf("execute_action did not receive stable operation identity: %#v", traceContext)
			}
			headers := common.GetHeaderFromCtx(ctx)
			if headers[common.HeaderBKNOperationID] != "op-stable-1" {
				t.Fatalf("execute_action outbound headers omitted operation ID: %#v", headers)
			}
			return mcpsdk.NewToolResultText("ok"), nil
		},
	)
	request := validBusinessToolRequest()
	request.Params.Name = "execute_action"

	if _, err := guarded(context.Background(), request); err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
}

func TestSessionGuardCompletesAttemptAndReturnsDurableReceipt(t *testing.T) {
	completeCalls := 0
	guarded := guardBusinessToolCallWithCompletion(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Created:   true,
				Execute:   true,
				Operation: map[string]any{"operation_id": "op-1", "attempt": float64(1)},
				Receipt:   map[string]any{"receipt_id": "receipt-1", "receipt_status": "pending"},
			}, nil, nil
		},
		func(_ context.Context, ensured *operationResult, downstream *mcpsdk.CallToolResult) (*operationResult, *lifecycleError, error) {
			completeCalls++
			if downstream.StructuredContent.(map[string]any)["answer"] != "ok" {
				t.Fatalf("completion did not receive downstream result: %#v", downstream.StructuredContent)
			}
			return &operationResult{
				Operation: ensured.Operation,
				Receipt:   map[string]any{"receipt_id": "receipt-1", "receipt_status": "completed"},
			}, nil, nil
		},
		nil,
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(map[string]any{"answer": "ok"}, `{"answer":"ok"}`), nil
		},
	)

	result, err := guarded(context.Background(), validBusinessToolRequest())
	if err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
	if completeCalls != 1 {
		t.Fatalf("expected one completion call, got %d", completeCalls)
	}
	structured := result.StructuredContent.(map[string]any)
	receipt := structured["bkn_receipt"].(map[string]any)
	if receipt["receipt_status"] != "completed" {
		t.Fatalf("durable receipt missing from result: %#v", structured)
	}
}

func TestSessionGuardPersistsFailedAttemptAndReturnsReceipt(t *testing.T) {
	finishCalls := 0
	guarded := guardBusinessToolCallWithCompletion(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Created:   true,
				Execute:   true,
				Operation: map[string]any{"operation_id": "op-1", "attempt": float64(1)},
				Receipt:   map[string]any{"receipt_id": "receipt-1", "receipt_status": "pending"},
			}, nil, nil
		},
		func(_ context.Context, ensured *operationResult, downstream *mcpsdk.CallToolResult) (*operationResult, *lifecycleError, error) {
			finishCalls++
			if !downstream.IsError {
				t.Fatal("failed downstream result was not preserved for attempt finalization")
			}
			return &operationResult{
				Operation: ensured.Operation,
				Receipt:   map[string]any{"receipt_id": "receipt-1", "receipt_status": "failed"},
			}, nil, nil
		},
		nil,
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			result := mcpsdk.NewToolResultStructured(map[string]any{"error": "bad input"}, "bad input")
			result.IsError = true
			return result, nil
		},
	)

	result, err := guarded(context.Background(), validBusinessToolRequest())
	if err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
	if finishCalls != 1 {
		t.Fatalf("expected failed attempt finalization, got %d calls", finishCalls)
	}
	if !result.IsError {
		t.Fatal("business error must remain an MCP tool error")
	}
	receipt := result.StructuredContent.(map[string]any)["bkn_receipt"].(map[string]any)
	if receipt["receipt_status"] != "failed" {
		t.Fatalf("failed durable receipt missing: %#v", result.StructuredContent)
	}
}

func TestSessionGuardConvertsDownstreamGoErrorToFailedReceipt(t *testing.T) {
	finishCalls := 0
	guarded := guardBusinessToolCallWithCompletion(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Created:   true,
				Execute:   true,
				Operation: map[string]any{"operation_id": "op-1", "attempt": float64(1)},
				Receipt:   map[string]any{"receipt_id": "receipt-1", "receipt_status": "pending"},
			}, nil, nil
		},
		func(_ context.Context, ensured *operationResult, downstream *mcpsdk.CallToolResult) (*operationResult, *lifecycleError, error) {
			finishCalls++
			if !downstream.IsError {
				t.Fatal("Go error must be represented as failed tool result for finalization")
			}
			return &operationResult{
				Operation: ensured.Operation,
				Receipt:   map[string]any{"receipt_id": "receipt-1", "receipt_status": "failed"},
			}, nil, nil
		},
		nil,
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return nil, errors.New("downstream unavailable")
		},
	)

	result, err := guarded(context.Background(), validBusinessToolRequest())
	if err != nil {
		t.Fatalf("business Go error must not escape as protocol error: %v", err)
	}
	if finishCalls != 1 || !result.IsError {
		t.Fatalf("Go error left operation pending: finish_calls=%d result=%#v", finishCalls, result)
	}
	receipt := result.StructuredContent.(map[string]any)["bkn_receipt"].(map[string]any)
	if receipt["receipt_status"] != "failed" {
		t.Fatalf("failed durable receipt missing: %#v", result.StructuredContent)
	}
}

func TestSessionGuardConvertsDownstreamPanicToFailedReceipt(t *testing.T) {
	finishCalls := 0
	guarded := guardBusinessToolCallWithCompletion(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Created:   true,
				Execute:   true,
				Operation: map[string]any{"operation_id": "op-1", "attempt": float64(1)},
				Receipt:   map[string]any{"receipt_id": "receipt-1", "receipt_status": "pending"},
			}, nil, nil
		},
		func(_ context.Context, ensured *operationResult, downstream *mcpsdk.CallToolResult) (*operationResult, *lifecycleError, error) {
			finishCalls++
			if !downstream.IsError {
				t.Fatal("panic must be represented as failed tool result for finalization")
			}
			return &operationResult{
				Operation: ensured.Operation,
				Receipt:   map[string]any{"receipt_id": "receipt-1", "receipt_status": "failed"},
			}, nil, nil
		},
		nil,
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			panic("sensitive downstream detail")
		},
	)

	result, err := guarded(context.Background(), validBusinessToolRequest())
	if err != nil {
		t.Fatalf("business panic must not escape as protocol error: %v", err)
	}
	if finishCalls != 1 || !result.IsError {
		t.Fatalf("panic left operation pending: finish_calls=%d result=%#v", finishCalls, result)
	}
	text, ok := mcpsdk.AsTextContent(result.Content[0])
	if !ok || strings.Contains(text.Text, "sensitive") {
		t.Fatalf("panic detail leaked to caller: %#v", result.Content)
	}
	receipt := result.StructuredContent.(map[string]any)["bkn_receipt"].(map[string]any)
	if receipt["receipt_status"] != "failed" {
		t.Fatalf("failed receipt missing after panic: %#v", result.StructuredContent)
	}
}

func validBusinessToolRequest() mcpsdk.CallToolRequest {
	return mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{
			Name: "search_schema",
			Arguments: map[string]any{"bkn_context": map[string]any{
				"conversation_id": "conv_1",
				"interaction_id":  "int_1",
				"operation_key":   "op-key-1",
			}},
		},
	}
}

func TestSessionGuardUsesOnlyExplicitContextAcrossTransportSessions(t *testing.T) {
	var mu sync.Mutex
	seen := []bknContext{}
	guarded := guardBusinessToolCall(
		func(_ context.Context, intent operationIntent) (*operationResult, *lifecycleError, error) {
			mu.Lock()
			seen = append(seen, intent.Context)
			mu.Unlock()
			return &operationResult{}, nil, nil
		},
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(map[string]any{"ok": true}, `{"ok":true}`), nil
		},
	)

	requests := []mcpsdk.CallToolRequest{
		businessToolRequest("transport-a", "conv-a", "int-a", "logical-a"),
		businessToolRequest("transport-a", "conv-b", "int-b", "logical-b"),
		businessToolRequest("transport-reconnected", "conv-a", "int-a", "logical-c"),
	}
	for _, request := range requests {
		if _, err := guarded(context.Background(), request); err != nil {
			t.Fatalf("guard call failed: %v", err)
		}
	}
	if len(seen) != 3 ||
		seen[0].ConversationID != "conv-a" ||
		seen[1].ConversationID != "conv-b" ||
		seen[2].OperationKey != "logical-c" {
		t.Fatalf("guard inferred or mixed transport context: %#v", seen)
	}
}

func TestSessionGuardWithoutCompletionAdapterDoesNotInventFinalReceiptOrAnswer(t *testing.T) {
	guarded := guardBusinessToolCall(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return &operationResult{
				Created:   true,
				Execute:   true,
				Operation: map[string]any{"operation_id": "op-observed", "attempt": float64(1)},
				Receipt:   map[string]any{"receipt_id": "receipt-observed", "receipt_status": "pending"},
			}, nil, nil
		},
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(
				map[string]any{"observed_operation": "op-observed"},
				`{"observed_operation":"op-observed"}`,
			), nil
		},
	)
	result, err := guarded(context.Background(), validBusinessToolRequest())
	if err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
	structured := result.StructuredContent.(map[string]any)
	if structured["observed_operation"] != "op-observed" {
		t.Fatalf("downstream observed result was changed: %#v", structured)
	}
	if _, fabricated := structured["bkn_receipt"]; fabricated {
		t.Fatalf("guard without completion adapter fabricated a terminal receipt: %#v", structured)
	}
	if _, fabricated := structured["answer"]; fabricated {
		t.Fatalf("guard without completion adapter fabricated an answer: %#v", structured)
	}
}

func businessToolRequest(sessionID, conversationID, interactionID, operationKey string) mcpsdk.CallToolRequest {
	return mcpsdk.CallToolRequest{
		Header: http.Header{"Mcp-Session-Id": []string{sessionID}},
		Params: mcpsdk.CallToolParams{
			Name: "search_schema",
			Arguments: map[string]any{"bkn_context": map[string]any{
				"conversation_id": conversationID,
				"interaction_id":  interactionID,
				"operation_key":   operationKey,
			}},
		},
	}
}

func TestLifecycleRequestDropsCallerSuppliedOwnerIdentity(t *testing.T) {
	_, _, body := lifecycleRequest("bkn_create_conversation", map[string]any{
		"external_conversation_key": "external-1",
		"idempotency_key":           "create-1",
		"tenant_id":                 "forged-tenant",
		"business_domain_id":        "forged-domain",
		"application_principal_id":  "forged-app",
		"effective_subject_id":      "forged-user",
	})
	for _, field := range []string{
		"tenant_id", "business_domain_id", "application_principal_id", "effective_subject_id",
	} {
		if _, exists := body[field]; exists {
			t.Fatalf("caller-supplied trusted field %s leaked into Core JSON: %#v", field, body)
		}
	}
}
