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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestLifecycleMiddlewareFinalizesRealAdapterFailures(t *testing.T) {
	type finishAttemptBody struct {
		ReceiptID   string `json:"receipt_id"`
		PayloadHash string `json:"payload_hash"`
		RequestID   string `json:"request_id"`
		TraceID     string `json:"trace_id"`
		Retryable   bool   `json:"retryable"`
	}
	var mu sync.Mutex
	failPaths := []string{}
	finishBodies := []finishAttemptBody{}
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agent-observability/v1/interactions/int-1":
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1",
				ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Created: true,
				Operation: bkntrace.Operation{
					OperationID: "op-1", ConversationID: "conv-1", InteractionID: "int-1",
					Attempt: 1, AttemptStatus: "pending",
				},
				Receipt: bkntrace.Receipt{ReceiptID: "receipt-1", ReceiptStatus: "pending"},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attempts/1:fail"):
			var body finishAttemptBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode finish body: %v", err)
				http.Error(w, "invalid finish body", http.StatusBadRequest)
				return
			}
			mu.Lock()
			failPaths = append(failPaths, r.URL.Path)
			finishBodies = append(finishBodies, body)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Operation: bkntrace.Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "failed"},
				Receipt:   bkntrace.Receipt{ReceiptID: "receipt-1", ReceiptStatus: "failed"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()

	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		RequestID: "req_lifecycle_adapter_0001", TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "client-1"},
	})
	client := bkntrace.NewLifecycleClient(core.URL, core.Client())

	tests := []struct {
		name          string
		wantRetryable bool
		next          func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error)
	}{
		{
			name: "MCP IsError",
			next: func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				result := mcpsdk.NewToolResultError("invalid business input")
				return result, nil
			},
		},
		{
			name: "Go error", wantRetryable: true,
			next: func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return nil, errors.New("downstream unavailable")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := lifecycleToolMiddleware(client)(test.next)
			result, err := handler(ctx, businessToolRequest("ignored-session", "conv-1", "int-1", test.name))
			if err != nil {
				t.Fatalf("failure escaped as protocol error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("downstream failure was not preserved: %#v", result)
			}
			structured := result.StructuredContent.(map[string]any)
			receipt, ok := structured["bkn_receipt"].(bkntrace.Receipt)
			if !ok || receipt.ReceiptStatus != "failed" {
				t.Fatalf("real adapter did not return failed receipt: %#v", structured)
			}
		})
	}
	if len(failPaths) != len(tests) {
		t.Fatalf("expected %d Core fail attempts, got %d: %#v", len(tests), len(failPaths), failPaths)
	}
	for index, body := range finishBodies {
		if body.RequestID != "req_lifecycle_adapter_0001" {
			t.Fatalf("finish request_id mismatch: %#v", body)
		}
		if len(body.TraceID) != 32 || body.TraceID == strings.Repeat("0", 32) {
			t.Fatalf("finish trace_id must be generated when the caller has no OTel span: %#v", body)
		}
		if body.ReceiptID != "receipt-1" || body.PayloadHash == "" {
			t.Fatalf("finish receipt contract mismatch: %#v", body)
		}
		if body.Retryable != tests[index].wantRetryable {
			t.Fatalf("%s retryable=%t, want %t", tests[index].name, body.Retryable, tests[index].wantRetryable)
		}
	}
}

func TestNormalizedInputHashIncludesCanonicalCausationMetadata(t *testing.T) {
	base := map[string]any{
		"query": "select *",
		"bkn_context": map[string]any{
			"conversation_id":     "conv-1",
			"interaction_id":      "int-1",
			"operation_key":       "op-key-1",
			"parent_operation_id": "parent-1",
			"causation_event_ids": []any{"event-b", "event-a"},
		},
	}
	reordered := map[string]any{
		"bkn_context": map[string]any{
			"operation_key":       "different-key",
			"interaction_id":      "different-interaction",
			"conversation_id":     "different-conversation",
			"parent_operation_id": "parent-1",
			"causation_event_ids": []any{"event-a", "event-b"},
		},
		"query": "select *",
	}
	changedParent := map[string]any{
		"query": "select *",
		"bkn_context": map[string]any{
			"parent_operation_id": "parent-2",
			"causation_event_ids": []any{"event-a", "event-b"},
		},
	}

	if normalizedBusinessInputHash(base) != normalizedBusinessInputHash(reordered) {
		t.Fatal("hash must sort causation_event_ids and ignore lifecycle identity keys")
	}
	if normalizedBusinessInputHash(base) == normalizedBusinessInputHash(changedParent) {
		t.Fatal("hash must change when parent_operation_id changes")
	}
}

func TestLifecycleOperationToolsUseExplicitQueryAndRetryPaths(t *testing.T) {
	var paths []string
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/agent-observability/v1/operations/op-1":
			_ = json.NewEncoder(w).Encode(bkntrace.Operation{
				OperationID: "op-1", InteractionID: "int-1",
				Attempt: 1, AttemptStatus: "failed", Retryable: true,
			})
		case "/api/agent-observability/v1/interactions/int-1":
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ExecutionStatus: "active",
				LeaseToken: "lease-1", LeaseEpoch: 1,
			})
		case "/api/agent-observability/v1/operations/op-1/attempts":
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Operation: bkntrace.Operation{OperationID: "op-1", Attempt: 2, AttemptStatus: "pending"},
				Receipt:   bkntrace.Receipt{ReceiptID: "receipt-2", Attempt: 2, ReceiptStatus: "pending"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()
	client := bkntrace.NewLifecycleClient(core.URL, core.Client())
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		RequestID: "req_lifecycle_adapter_0002", TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "client-1"},
	})

	for _, name := range []string{"bkn_get_operation", "bkn_retry_operation"} {
		result, err := handleLifecycleTool(client, name)(ctx, mcpsdk.CallToolRequest{
			Params: mcpsdk.CallToolParams{Arguments: map[string]any{"operation_id": "op-1"}},
		})
		if err != nil || result.IsError {
			t.Fatalf("%s failed: result=%#v err=%v", name, result, err)
		}
	}
	want := []string{
		"GET /api/agent-observability/v1/operations/op-1",
		"GET /api/agent-observability/v1/operations/op-1",
		"GET /api/agent-observability/v1/interactions/int-1",
		"POST /api/agent-observability/v1/operations/op-1/attempts",
	}
	if len(paths) != len(want) {
		t.Fatalf("unexpected operation tool paths: %#v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("operation tool path %d = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestFinalizeOperationRecoversPendingReceiptWithoutReexecutingDownstream(t *testing.T) {
	var paths []string
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/agent-observability/v1/operations/op-finalize":
			_ = json.NewEncoder(w).Encode(bkntrace.Operation{
				OperationID: "op-finalize", InteractionID: "int-1",
				Attempt: 1, AttemptStatus: "pending",
			})
		case "/api/agent-observability/v1/receipts/receipt-finalize":
			_ = json.NewEncoder(w).Encode(bkntrace.Receipt{
				ReceiptID: "receipt-finalize", OperationID: "op-finalize",
				Attempt: 1, ReceiptStatus: "pending",
			})
		case "/api/agent-observability/v1/operations/op-finalize/attempts/1:complete":
			var body struct {
				ReceiptID   string `json:"receipt_id"`
				PayloadHash string `json:"payload_hash"`
				RequestID   string `json:"request_id"`
				TraceID     string `json:"trace_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.ReceiptID != "receipt-finalize" || body.PayloadHash != "sha256:durable-output" ||
				body.RequestID == "" || len(body.TraceID) != 32 {
				t.Errorf("invalid explicit finalize body: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Operation: bkntrace.Operation{
					OperationID: "op-finalize", Attempt: 1, AttemptStatus: "completed",
				},
				Receipt: bkntrace.Receipt{
					ReceiptID: "receipt-finalize", OperationID: "op-finalize",
					Attempt: 1, ReceiptStatus: "completed",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()

	ctx := trustedLifecycleAdapterContext()
	result, err := handleLifecycleTool(
		bkntrace.NewLifecycleClient(core.URL, core.Client()),
		"bkn_finalize_operation",
	)(ctx, mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{
		Name: "bkn_finalize_operation",
		Arguments: map[string]any{
			"operation_id": "op-finalize", "receipt_id": "receipt-finalize",
			"payload_hash": "sha256:durable-output", "outcome": "complete",
		},
	}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("explicit finalize failed: result=%#v err=%v", result, err)
	}
	want := []string{
		"GET /api/agent-observability/v1/operations/op-finalize",
		"GET /api/agent-observability/v1/receipts/receipt-finalize",
		"POST /api/agent-observability/v1/operations/op-finalize/attempts/1:complete",
	}
	if len(paths) != len(want) {
		t.Fatalf("explicit finalize calls = %#v, want %#v", paths, want)
	}
	for index := range want {
		if paths[index] != want[index] {
			t.Fatalf("explicit finalize call %d = %q, want %q", index, paths[index], want[index])
		}
	}
}

func trustedLifecycleAdapterContext() context.Context {
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		RequestID: "req_explicit_finalize_0001", TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	traceID := trace.TraceID{0x4b, 0x3d, 0x59, 0xda, 0xef, 0xf5, 0xbf, 0xbb, 0x23, 0xd4, 0x6c, 0x47, 0xa5, 0x05, 0x1e, 0xc9}
	spanID := trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7}
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))
	return common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "client-1"},
	})
}

func TestLifecycleReceiptToolUsesAuthoritativePollPath(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet ||
			r.URL.Path != "/api/agent-observability/v1/receipts/receipt-1" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(bkntrace.Receipt{
			ReceiptID: "receipt-1", ReceiptStatus: "completed",
		})
	}))
	defer core.Close()
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})
	result, err := handleLifecycleTool(
		bkntrace.NewLifecycleClient(core.URL, core.Client()),
		"bkn_get_receipt",
	)(ctx, mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{Arguments: map[string]any{"receipt_id": "receipt-1"}},
	})
	if err != nil || result.IsError {
		t.Fatalf("receipt poll failed: result=%#v err=%v", result, err)
	}
	receipt, ok := result.StructuredContent.(bkntrace.Receipt)
	if !ok || receipt.ReceiptID != "receipt-1" {
		t.Fatalf("receipt poll returned wrong type: %#v", result.StructuredContent)
	}
}
