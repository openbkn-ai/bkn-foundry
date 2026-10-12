// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"github.com/mark3labs/mcp-go/server"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

func TestBusinessToolContinuesWithoutManagedContext(t *testing.T) {
	calls := 0
	expected := mcpsdk.NewToolResultText("actual business value")
	handler := guardBusinessToolCall(nil, func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		calls++
		return expected, nil
	})
	result, err := handler(context.Background(), mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Name: "get_kn_detail", Arguments: map[string]any{"kn_id": "kn-1"}}})
	if err != nil || calls != 1 || result != expected || result.IsError {
		t.Fatalf("business prevented by missing Trace context: calls=%d result=%#v err=%v", calls, result, err)
	}
}

func TestStartCoreOutageLetsAgentContinueWithoutFakeIDs(t *testing.T) {
	client := bkntrace.NewLifecycleClient("http://trace.test", &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, syscall.ECONNREFUSED })})
	result, err := handleLifecycleTool(client, toolKeyStartInteraction)(startArtifactTestContext(), mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Name: toolKeyStartInteraction, Arguments: map[string]any{"conversation_mode": "new", "question": "business question", "agent_name": "test-agent"}}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("start outage prevents continuation: result=%#v err=%v", result, err)
	}
	value, ok := result.StructuredContent.(map[string]any)
	if !ok || value["trace_recorded"] != false || value["required_action"] != "continue_without_bkn_context" {
		t.Fatalf("start did not disclose unrecorded continuation: %#v", result)
	}
	if _, exists := value["interaction_id"]; exists {
		t.Fatalf("fake interaction id: %#v", value)
	}
}

func TestFinishWithoutRegisteredStartSkipsCore(t *testing.T) {
	calls := 0
	client := bkntrace.NewLifecycleClient("http://trace.test", &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, syscall.ECONNREFUSED })})
	result, err := handleLifecycleTool(client, toolKeyFinishInteraction)(context.Background(), mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Name: toolKeyFinishInteraction, Arguments: map[string]any{"outcome": "completed", "answer": "actual answer"}}})
	if err != nil || result == nil || result.IsError || calls != 0 {
		t.Fatalf("finish without IDs blocked/called Core: calls=%d result=%#v err=%v", calls, result, err)
	}
}

func TestBusinessToolContinuesOnTraceTransportFailure(t *testing.T) {
	calls, finishes := 0, 0
	expected := mcpsdk.NewToolResultText("actual business value")
	handler := guardBusinessToolCallWithCompletion(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return nil, nil, syscall.ECONNREFUSED
		},
		func(context.Context, *operationResult, *mcpsdk.CallToolResult) (*operationResult, *lifecycleError, error) {
			finishes++
			return nil, nil, nil
		}, nil,
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			calls++
			return expected, nil
		},
	)
	result, err := handler(context.Background(), businessToolRequest("session-1", "conv-1", "int-1", "invocation-1"))
	if err != nil || calls != 1 || finishes != 0 || result != expected || result.IsError {
		t.Fatalf("Trace transport blocked/re-finalized business: calls=%d finishes=%d result=%#v err=%v", calls, finishes, result, err)
	}
}

func TestTraceDomainRefusalStillPreventsBusiness(t *testing.T) {
	calls := 0
	handler := guardBusinessToolCall(func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
		return nil, &lifecycleError{Code: "permission_denied", Message: "foreign owner"}, nil
	}, func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		calls++
		return mcpsdk.NewToolResultText("must not run"), nil
	})
	result, err := handler(context.Background(), businessToolRequest("session-1", "conv-1", "int-1", "invocation-1"))
	if err != nil || calls != 0 || result == nil || !result.IsError {
		t.Fatalf("domain refusal swallowed: calls=%d result=%#v err=%v", calls, result, err)
	}
}

func TestUnmanagedPTCWorkdirNeverSharesFallback(t *testing.T) {
	first, second := ptcWorkdirRelative(nil), ptcWorkdirRelative(nil)
	if first == "shared" || first == second {
		t.Fatalf("missing Core IDs share a sandbox: first=%q second=%q", first, second)
	}
}

func TestMissingTraceAdapterStillRejectsConflictingDeclaredReference(t *testing.T) {
	calls := 0
	handler := guardBusinessToolCall(nil, func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		calls++
		return mcpsdk.NewToolResultText("must not run"), nil
	})
	req := businessToolRequest("session-1", "conv-1", "int-1", "invocation-1")
	args := req.Params.Arguments.(map[string]any)
	args["kn_id"] = "kn-current"
	args["bkn_context"].(map[string]any)["business_refs"] = []any{map[string]any{"ref_type": "knowledge_network", "ref_id": "kn-foreign"}}
	result, err := handler(context.Background(), req)
	if err != nil || result == nil || !result.IsError || calls != 0 {
		t.Fatalf("missing Trace adapter swallowed declared reference conflict: calls=%d result=%#v err=%v", calls, result, err)
	}
}

func TestUnmanagedPTCScopeUsesTrustedSessionAndOwnerIsolation(t *testing.T) {
	srv := server.NewMCPServer("scope-test", "1")
	scope := func(owner, client string, req mcpsdk.CallToolRequest, withSession bool) string {
		ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{AccountID: owner, AccountType: interfaces.AccessorTypeUser, TokenInfo: &interfaces.TokenInfo{ClientID: client}})
		if withSession {
			ctx = srv.WithContext(ctx, &clientInfoSession{})
		}
		value, err := ptcWorkspaceScope(ctx, req, nil)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	code := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Name: "run_code"}}
	shell := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Name: "run_shell"}}
	first := scope("user-a", "app-a", code, true)
	if scope("user-a", "app-a", shell, true) != first {
		t.Fatal("run_code/run_shell lost actual session workspace continuity")
	}
	if scope("user-b", "app-a", code, true) == first || scope("user-a", "app-b", code, true) == first {
		t.Fatal("different owner/app shared trusted session workspace")
	}
	host := code
	host.Header = http.Header{hostConversationKeyHeader: []string{"host-one"}}
	if scope("user-a", "app-a", host, false) == scope("user-b", "app-a", host, false) {
		t.Fatal("different owner shared host workspace")
	}
	rawHeader := code
	rawHeader.Header = http.Header{"Mcp-Session-Id": []string{"untrusted"}}
	a, b := scope("user-a", "app-a", rawHeader, false), scope("user-a", "app-a", rawHeader, false)
	if a == b || !strings.HasPrefix(a, "adhoc-") {
		t.Fatal("sessionless or caller header shared namespace")
	}
}
func TestBusinessCancellationAndTraceOnlyTimeoutHaveDifferentOutcomes(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		t.Run(map[bool]string{true: "caller_cancelled", false: "trace_only_deadline"}[cancelled], func(t *testing.T) {
			calls, traceCalls := 0, 0
			handler := guardBusinessToolCall(func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
				traceCalls++
				return nil, nil, context.DeadlineExceeded
			}, func(ctx context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				calls++
				if ctx.Err() != nil {
					t.Fatal("Trace deadline reached business context")
				}
				return mcpsdk.NewToolResultText("actual"), nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				cancel()
			}
			result, err := handler(ctx, businessToolRequest("s", "conv-1", "int-1", "call-1"))
			if cancelled {
				if calls != 0 || traceCalls != 0 || !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled caller executed: calls=%d trace=%d err=%v", calls, traceCalls, err)
				}
			} else if err != nil || calls != 1 || result == nil || result.IsError {
				t.Fatalf("Trace-only deadline blocked business: %#v %v", result, err)
			}
		})
	}
}
func TestExplicitDomainDenialsCannotUseInfrastructureFallback(t *testing.T) {
	for _, code := range []string{"permission_denied", "resource_not_disclosed", "lease_invalid", "lease_conflict", "terminal_conflict", "attempt_conflict", "operation_required", "business_ref_invalid"} {
		t.Run(code, func(t *testing.T) {
			calls := 0
			handler := guardBusinessToolCall(func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
				return nil, &lifecycleError{HTTPStatus: 503, Code: code, Retryable: true}, nil
			}, func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				calls++
				return mcpsdk.NewToolResultText("must not run"), nil
			})
			result, err := handler(context.Background(), businessToolRequest("s", "conv-1", "int-1", "call-1"))
			if err != nil || calls != 0 || result == nil || !result.IsError {
				t.Fatalf("domain error swallowed: %s calls=%d result=%#v err=%v", code, calls, result, err)
			}
		})
	}
}

func TestCallerCancellationAfterSuccessfulEnsureDoesNotStartBusiness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls, finishes := 0, 0
	handler := guardBusinessToolCallWithCompletion(func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
		cancel()
		return &operationResult{Execute: true, LifecycleContext: context.Background(), Operation: map[string]any{"operation_id": "op-actual", "attempt": 1}, Receipt: map[string]any{"receipt_status": "pending"}}, nil, nil
	}, func(context.Context, *operationResult, *mcpsdk.CallToolResult) (*operationResult, *lifecycleError, error) {
		finishes++
		return nil, nil, nil
	}, nil, func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		calls++
		return mcpsdk.NewToolResultText("must not start"), nil
	})
	_, err := handler(ctx, businessToolRequest("s", "conv-1", "int-1", "call"))
	if calls != 0 || finishes != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller executed after actual registration: business=%d finish=%d err=%v", calls, finishes, err)
	}
}

func TestFencedLeaseRefreshFailureNeverStartsUnmanagedMCPBusiness(t *testing.T) {
	for _, failure := range []string{"network", "http_503", "retry_network", "retry_503"} {
		t.Run(failure, func(t *testing.T) {
			gets, posts, calls := 0, 0, 0
			client := bkntrace.NewLifecycleClient("http://trace.test", &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					gets++
					if gets > 1 && failure == "network" {
						return nil, syscall.ECONNREFUSED
					}
					if gets > 1 && failure == "http_503" {
						return lifecycleAdapterJSONResponse(503, map[string]any{"error": bkntrace.APIError{Code: "trace_core_unavailable"}}), nil
					}
					return lifecycleAdapterJSONResponse(200, bkntrace.Interaction{ConversationID: "conv-1", InteractionID: "int-1", ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 1, LeaseExpiresAt: time.Now().Add(time.Minute)}), nil
				}
				posts++
				if posts > 1 && failure == "retry_network" {
					return nil, syscall.ECONNREFUSED
				}
				if posts > 1 && failure == "retry_503" {
					return lifecycleAdapterJSONResponse(503, map[string]any{"error": bkntrace.APIError{Code: "trace_core_unavailable"}}), nil
				}
				return lifecycleAdapterJSONResponse(409, map[string]any{"error": bkntrace.APIError{Code: "terminal_conflict", Message: "stale interaction lease was fenced"}}), nil
			})})
			ctx := startArtifactTestContext()
			var interaction bkntrace.Interaction
			apiErr, err := client.Call(ctx, http.MethodGet, "/interactions/int-1", nil, &interaction)
			if apiErr != nil || err != nil {
				t.Fatalf("seed lease: %v %v", apiErr, err)
			}
			handler := guardBusinessToolCall(ensureOperationAdapter(client), func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				calls++
				return mcpsdk.NewToolResultText("must not run"), nil
			})
			result, err := handler(ctx, businessToolRequest("session-1", "conv-1", "int-1", "invocation-1"))
			if err != nil || calls != 0 || result == nil || !result.IsError {
				t.Fatalf("fencing bypassed: calls=%d result=%#v err=%v", calls, result, err)
			}
			if !strings.Contains(fmt.Sprint(result.StructuredContent)+fmt.Sprint(result.Content), "terminal_conflict") {
				t.Fatalf("lost rejection: %#v", result)
			}
		})
	}
}
