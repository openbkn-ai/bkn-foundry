// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestLifecycleSuccessTextContainsStructuredIdentifiers(t *testing.T) {
	target := &bkntrace.Conversation{
		ConversationID:          "conv-real-1",
		ExternalConversationKey: "cursor-chat-1",
		Status:                  "active",
	}

	result, err := lifecycleCallResult(target, nil, nil)
	if err != nil || result.IsError {
		t.Fatalf("lifecycle result failed: result=%#v err=%v", result, err)
	}
	text, ok := mcpsdk.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("lifecycle fallback is not text: %#v", result.Content)
	}
	var fallback map[string]any
	if err := json.Unmarshal([]byte(text.Text), &fallback); err != nil {
		t.Fatalf("lifecycle fallback is not JSON: %q: %v", text.Text, err)
	}
	if fallback["conversation_id"] != "conv-real-1" {
		t.Fatalf("fallback omitted authoritative conversation_id: %#v", fallback)
	}
}

func TestStartInteractionWithoutConversationEnsuresManagedConversationFirst(t *testing.T) {
	var calls []string
	var ensureBody map[string]any
	var startBody map[string]any
	client := &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/agent-observability/v1/conversations:ensure-current":
			if err := json.NewDecoder(r.Body).Decode(&ensureBody); err != nil {
				t.Fatalf("decode ensure body: %v", err)
			}
			return lifecycleAdapterJSONResponse(http.StatusCreated, bkntrace.Conversation{
				ConversationID: "conv-created-1", Status: "active",
			}), nil
		case "/api/agent-observability/v1/conversations/conv-created-1/interactions":
			if err := json.NewDecoder(r.Body).Decode(&startBody); err != nil {
				t.Fatalf("decode start body: %v", err)
			}
			return lifecycleAdapterJSONResponse(http.StatusOK, bkntrace.Interaction{
				InteractionID: "int-created-1", ConversationID: "conv-created-1",
				ExecutionStatus: "active", EvidenceStatus: "pending",
			}), nil
		default:
			return lifecycleAdapterJSONResponse(http.StatusNotFound, map[string]any{
				"error": map[string]any{"code": "not_found", "message": "not found"},
			}), nil
		}
	})}
	t.Setenv("BKN_TRACE_EVIDENCE_INGEST_URL", "")

	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		RequestID: "req_cursor_first_turn_0001", TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "cursor-app"},
	})
	result, err := handleLifecycleTool(
		bkntrace.NewLifecycleClient("http://bkn-trace.test", client),
		"bkn_start_interaction",
	)(ctx, mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: map[string]any{
		"question": "查询多层 BOM", "agent_name": "供应链分析助手",
	}}})
	if err != nil || result.IsError {
		t.Fatalf("first start failed: result=%#v err=%v", result, err)
	}
	if len(calls) < 2 || calls[0] != "POST /api/agent-observability/v1/conversations:ensure-current" ||
		calls[1] != "POST /api/agent-observability/v1/conversations/conv-created-1/interactions" {
		t.Fatalf("first start call order = %#v", calls)
	}
	if key, _ := ensureBody["external_conversation_key"].(string); key == "" || strings.Contains(key, "查询多层 BOM") {
		t.Fatalf("server-managed external key must be opaque: %#v", ensureBody)
	}
	if startBody["request_hash"] != strings.TrimPrefix(hashBytes([]byte("查询多层 BOM")), "sha256:") {
		t.Fatalf("start request omitted question fingerprint: %#v", startBody)
	}
	if startBody["agent_name"] != "供应链分析助手" {
		t.Fatalf("start request omitted agent display declaration: %#v", startBody)
	}
	structured := result.StructuredContent.(map[string]any)
	if structured["conversation_id"] != "conv-created-1" || structured["interaction_id"] != "int-created-1" {
		t.Fatalf("first start omitted authoritative IDs: %#v", structured)
	}
}

type lifecycleAdapterRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn lifecycleAdapterRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func lifecycleAdapterJSONResponse(status int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(raw)),
	}
}

func TestLifecycleToolsRejectInvalidArgumentsBeforeCallingCore(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		args        map[string]any
		wantMessage string
	}{
		{
			name: "start without agent name", toolName: "bkn_start_interaction",
			args:        map[string]any{"question": "查询库存"},
			wantMessage: "bkn_start_interaction expects top-level question and agent_name, plus optional conversation_id",
		},
		{
			name: "start with empty agent name", toolName: "bkn_start_interaction",
			args:        map[string]any{"question": "查询库存", "agent_name": ""},
			wantMessage: "bkn_start_interaction expects top-level question and agent_name, plus optional conversation_id",
		},
		{
			name: "start with unsupported lease seconds", toolName: "bkn_start_interaction",
			args: map[string]any{
				"question": "查询库存", "agent_name": "供应链分析助手", "lease_seconds": 600,
			},
			wantMessage: "bkn_start_interaction received unsupported field(s): lease_seconds. Remove them and retry",
		},
		{
			name: "finish with lifecycle IDs nested in bkn context", toolName: "bkn_finish_interaction",
			args: map[string]any{
				"bkn_context": map[string]any{
					"conversation_id": "conv-1", "interaction_id": "int-1",
				},
				"outcome": "completed", "answer": "库存充足",
			},
			wantMessage: "bkn_finish_interaction requires interaction_id as a top-level field; remove bkn_context and retry",
		},
		{
			name: "finish with unsupported outcome", toolName: "bkn_finish_interaction",
			args:        map[string]any{"interaction_id": "int-1", "outcome": "abandoned"},
			wantMessage: "bkn_finish_interaction outcome must be one of: completed, failed, cancelled, handed_off",
		},
		{
			name: "finish with empty interaction id", toolName: "bkn_finish_interaction",
			args:        map[string]any{"interaction_id": "", "outcome": "failed"},
			wantMessage: "bkn_finish_interaction expects top-level interaction_id and outcome, plus answer for completed or optional reason otherwise",
		},
		{
			name: "finish with caller-owned claims", toolName: "bkn_finish_interaction",
			args: map[string]any{
				"interaction_id": "int-1", "outcome": "completed", "answer": "库存充足",
				"claims": []any{map[string]any{"claim_id": "caller-owned"}},
			},
			wantMessage: "bkn_finish_interaction received unsupported field(s): claims. Remove them and retry",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coreCalls := 0
			client := &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(*http.Request) (*http.Response, error) {
				coreCalls++
				return lifecycleAdapterJSONResponse(http.StatusInternalServerError, map[string]any{}), nil
			})}
			ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
				RequestID: "req-invalid-lifecycle-arguments", TenantID: "tenant-1", BusinessDomain: "domain-1",
			})
			ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
				AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
			})

			result, err := handleLifecycleTool(
				bkntrace.NewLifecycleClient("http://bkn-trace.test", client), test.toolName,
			)(ctx, mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: test.args}})
			if err != nil {
				t.Fatalf("invalid lifecycle arguments returned handler error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("invalid lifecycle arguments unexpectedly succeeded: %#v", result)
			}
			if coreCalls != 0 {
				t.Fatalf("invalid lifecycle arguments reached Core %d times", coreCalls)
			}
			textContent, ok := mcpsdk.AsTextContent(result.Content[0])
			if !ok {
				t.Fatalf("invalid lifecycle error is not text: %#v", result.Content)
			}
			var envelope map[string]any
			if err := json.Unmarshal([]byte(textContent.Text), &envelope); err != nil {
				t.Fatalf("decode invalid lifecycle error: %v", err)
			}
			errorValue := envelope["error"].(map[string]any)
			if errorValue["code"] != "invalid_params" ||
				errorValue["required_action"] != "correct_tool_arguments" ||
				errorValue["message"] != test.wantMessage {
				t.Fatalf("invalid lifecycle error lacks correction guidance: %#v", errorValue)
			}
		})
	}
}

func TestStartInteractionCreatesCorrelationAndUsesCoreCreatedAtForQuestionEvidence(t *testing.T) {
	createdAt := time.Date(2026, 8, 3, 6, 30, 0, 123000000, time.UTC)
	var artifact map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent-observability/v1/conversations/conv-1/interactions":
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1", Ordinal: 1,
				ExecutionStatus: "active", EvidenceStatus: "pending",
				LeaseToken: "lease-1", LeaseEpoch: 1, CreatedAt: createdAt,
			})
		case "/api/agent-observability/v1/evidence/artifacts":
			if err := json.NewDecoder(r.Body).Decode(&artifact); err != nil {
				t.Fatalf("decode artifact: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"created":true}`))
		case "/api/agent-observability/v1/evidence/events":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"accepted":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	t.Setenv("BKN_TRACE_EVIDENCE_INGEST_URL", backend.URL+"/api/agent-observability/v1/evidence/events")
	t.Setenv("BKN_TRACE_EVIDENCE_INGEST_TOKEN", "ingest-token")

	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		RequestID: "req_cursor_native_0001", TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	if traceContext.ObservedAtProvided {
		t.Fatal("test must represent a third-party MCP request without an internal observed-at header")
	}
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "cursor-app"},
	})

	result, err := handleLifecycleTool(
		bkntrace.NewLifecycleClient(backend.URL, backend.Client()),
		"bkn_start_interaction",
	)(ctx, mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: map[string]any{
		"conversation_id": "conv-1", "question": "查询 BOM", "agent_name": "供应链分析助手",
	}}})
	if err != nil || result.IsError {
		t.Fatalf("start interaction failed: result=%#v err=%v", result, err)
	}
	if got := artifact["observed_at"]; got != createdAt.Format(time.RFC3339Nano) {
		t.Fatalf("question artifact observed_at=%v, want Core created_at %s", got, createdAt.Format(time.RFC3339Nano))
	}
	if got, _ := artifact["trace_id"].(string); len(got) != 32 {
		t.Fatalf("question artifact trace_id=%q, want server-created W3C trace ID", got)
	}
}

func TestFinishInteractionUsesCoreUpdatedAtForServerOwnedResultEvidence(t *testing.T) {
	updatedAt := time.Date(2026, 8, 3, 6, 32, 0, 789000000, time.UTC)
	var artifact map[string]any
	var finishBody map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agent-observability/v1/interactions/int-1":
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1",
				ExecutionStatus: "active", EvidenceStatus: "assembling",
				LeaseToken: "lease-1", LeaseEpoch: 1, UpdatedAt: updatedAt,
			})
		case r.URL.Path == "/api/agent-observability/v1/evidence/artifacts":
			if err := json.NewDecoder(r.Body).Decode(&artifact); err != nil {
				t.Fatalf("decode artifact: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"created":true}`))
		case r.URL.Path == "/api/agent-observability/v1/evidence/events":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"accepted":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent-observability/v1/interactions/int-1/finish":
			if err := json.NewDecoder(r.Body).Decode(&finishBody); err != nil {
				t.Fatalf("decode finish body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1",
				ExecutionStatus: "completed", EvidenceStatus: "complete", UpdatedAt: updatedAt,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	t.Setenv("BKN_TRACE_EVIDENCE_INGEST_URL", backend.URL+"/api/agent-observability/v1/evidence/events")
	t.Setenv("BKN_TRACE_EVIDENCE_INGEST_TOKEN", "ingest-token")

	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{2}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	ctx = common.SetTraceContextToCtx(ctx, common.TraceContext{
		RequestID: "req_cursor_native_0002", TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "cursor-app"},
	})

	result, err := handleLifecycleTool(
		bkntrace.NewLifecycleClient(backend.URL, backend.Client()),
		"bkn_finish_interaction",
	)(ctx, mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: map[string]any{
		"interaction_id": "int-1", "outcome": "completed",
		"answer": "BOM 查询完成",
	}}})
	if err != nil || result.IsError {
		t.Fatalf("complete interaction failed: result=%#v err=%v", result, err)
	}
	if got := artifact["observed_at"]; got != updatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("result artifact observed_at=%v, want Core updated_at %s", got, updatedAt.Format(time.RFC3339Nano))
	}
}

func TestFinishInteractionRetryReusesCommittedResultArtifact(t *testing.T) {
	evidenceCalls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agent-observability/v1/interactions/int-1":
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1",
				ExecutionStatus: "completed", EvidenceStatus: "complete",
				LeaseToken: "lease-1", LeaseEpoch: 1,
				ClosureManifest: &bkntrace.ClosureManifest{
					Version: "3.0.0", AnswerArtifactRef: "artifact:result-existing",
				},
			})
		case strings.HasPrefix(r.URL.Path, "/api/agent-observability/v1/evidence/"):
			evidenceCalls++
			http.Error(w, "must not rewrite committed evidence", http.StatusConflict)
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent-observability/v1/interactions/int-1/finish":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode terminal retry: %v", err)
			}
			if body["answer_artifact_ref"] != "artifact:result-existing" {
				t.Fatalf("terminal retry did not reuse committed artifact: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1",
				ExecutionStatus: "completed", EvidenceStatus: "complete",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	t.Setenv("BKN_TRACE_EVIDENCE_INGEST_URL", backend.URL+"/api/agent-observability/v1/evidence/events")

	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		RequestID: "req_cursor_retry_0001", TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})
	result, err := handleLifecycleTool(
		bkntrace.NewLifecycleClient(backend.URL, backend.Client()),
		"bkn_finish_interaction",
	)(ctx, mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: map[string]any{
		"interaction_id": "int-1", "outcome": "completed",
		"answer": "BOM 查询完成",
	}}})
	if err != nil || result.IsError {
		t.Fatalf("terminal retry failed: result=%#v err=%v", result, err)
	}
	if evidenceCalls != 0 {
		t.Fatalf("terminal retry rewrote evidence %d times", evidenceCalls)
	}
}

func TestLifecycleMiddlewareFinalizesRealAdapterFailures(t *testing.T) {
	type finishAttemptBody struct {
		ReceiptID string                   `json:"receipt_id"`
		Error     bkntrace.PayloadEnvelope `json:"error"`
		RequestID string                   `json:"request_id"`
		TraceID   string                   `json:"trace_id"`
		Retryable bool                     `json:"retryable"`
	}
	var mu sync.Mutex
	failPaths := []string{}
	finishBodies := []finishAttemptBody{}
	ensureInputs := []bkntrace.PayloadEnvelope{}
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agent-observability/v1/interactions/int-1":
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1",
				ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			var body struct {
				Protocol     string                   `json:"protocol"`
				SourceModule string                   `json:"source_module"`
				Input        bkntrace.PayloadEnvelope `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode ensure body: %v", err)
				http.Error(w, "invalid ensure body", http.StatusBadRequest)
				return
			}
			if body.Protocol != "mcp" || body.SourceModule != "context-loader" {
				t.Errorf("MCP ensure lost producer identity: %#v", body)
			}
			ensureInputs = append(ensureInputs, body.Input)
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Created: true,
				Execute: true,
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
		wantCode      string
		wantMessage   string
		toolName      string
		input         map[string]any
		next          func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error)
	}{
		{
			name: "validation failure", wantCode: "tool_error", wantMessage: "kn_id and ot_id are required",
			toolName: "query_object_instance", input: map[string]any{"kn_id": "supplychain_hd0202", "condition": map[string]any{"operation": "and"}},
			next: func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				result := mcpsdk.NewToolResultError("kn_id and ot_id are required")
				return result, nil
			},
		},
		{
			name: "policy rejection", wantCode: "tool_error", wantMessage: "run_sql is read-only",
			toolName: "run_sql", input: map[string]any{"sql": "DELETE FROM {{.purchase_order_resource}} WHERE status = 'closed'"},
			next: func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return mcpsdk.NewToolResultError("run_sql is read-only"), nil
			},
		},
		{
			name: "downstream failure", wantRetryable: true,
			wantCode: "downstream_error", wantMessage: "downstream unavailable",
			toolName: "search_schema", input: map[string]any{"kn_id": "supplychain_hd0202", "query": "采购订单"},
			next: func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return nil, errors.New("downstream unavailable")
			},
		},
		{
			name: "panic", wantCode: "handler_panic", wantMessage: "deterministic business defect",
			toolName: "run_sql", input: map[string]any{"sql": "SELECT * FROM {{.purchase_order_resource}} LIMIT 1"},
			next: func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				panic("deterministic business defect")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := lifecycleToolMiddleware(client)(test.next)
			request := businessToolRequest("ignored-session", "conv-1", "int-1", test.name)
			request.Params.Name = test.toolName
			arguments := make(map[string]any, len(test.input)+1)
			for key, value := range test.input {
				arguments[key] = value
			}
			arguments["bkn_context"] = map[string]any{
				"conversation_id": "conv-1", "interaction_id": "int-1", "operation_key": test.name,
			}
			request.Params.Arguments = arguments
			result, err := handler(ctx, request)
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
	if len(ensureInputs) != len(tests) {
		t.Fatalf("expected %d captured failure inputs, got %d", len(tests), len(ensureInputs))
	}
	for index, body := range finishBodies {
		gotInputJSON := ensureInputs[index].Inline
		wantInputJSON, _ := json.Marshal(tests[index].input)
		if ensureInputs[index].Mode != "inline" || !bytes.Equal(gotInputJSON, wantInputJSON) {
			t.Fatalf("%s input=%s, want %s", tests[index].name, gotInputJSON, wantInputJSON)
		}
		if body.RequestID != "req_lifecycle_adapter_0001" {
			t.Fatalf("finish request_id mismatch: %#v", body)
		}
		if len(body.TraceID) != 32 || body.TraceID == strings.Repeat("0", 32) {
			t.Fatalf("finish trace_id must be generated when the caller has no OTel span: %#v", body)
		}
		if body.ReceiptID != "receipt-1" || body.Error.Mode != "inline" || len(body.Error.Inline) == 0 {
			t.Fatalf("finish receipt contract mismatch: %#v", body)
		}
		var failure struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Stage   string `json:"stage"`
		}
		if err := json.Unmarshal(body.Error.Inline, &failure); err != nil ||
			failure.Code != tests[index].wantCode || failure.Message != tests[index].wantMessage ||
			failure.Stage != "tool_execution" {
			t.Fatalf("%s error fact=%#v err=%v", tests[index].name, failure, err)
		}
		if body.Retryable != tests[index].wantRetryable {
			t.Fatalf("%s retryable=%t, want %t", tests[index].name, body.Retryable, tests[index].wantRetryable)
		}
	}
}

func TestNormalizedBusinessInputPreservesOnlyRealToolArguments(t *testing.T) {
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
	var got map[string]any
	if err := json.Unmarshal(normalizedBusinessInput(base), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]any{"query": "select *"}) {
		t.Fatalf("stored MCP input=%#v, want only actual tool arguments", got)
	}
}

func TestManagedCommunityToolsSubmitRealInputAndTerminalPayload(t *testing.T) {
	ensureCalls := 0
	finishCalls := 0
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/interactions/int-1"):
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1",
				ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			var body struct {
				ToolName string         `json:"tool_name"`
				Input    map[string]any `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			inline, _ := body.Input["inline"].(map[string]any)
			if body.Input["mode"] != "inline" || inline["probe"] != body.ToolName {
				t.Errorf("%s input=%#v, want original tool arguments", body.ToolName, body.Input)
			}
			ensureCalls++
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Created: true, Execute: true,
				Operation: bkntrace.Operation{
					OperationID: "op-1", ConversationID: "conv-1", InteractionID: "int-1",
					ToolName: body.ToolName, Attempt: 1, AttemptStatus: "pending",
				},
				Receipt: bkntrace.Receipt{ReceiptID: "receipt-1", ReceiptStatus: "pending"},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attempts/1:complete"):
			var body struct {
				Output bkntrace.PayloadEnvelope `json:"output"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Output.Mode != "inline" || len(body.Output.Inline) == 0 {
				t.Error("completed tool call omitted the real MCP result")
			}
			finishCalls++
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Operation: bkntrace.Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"},
				Receipt:   bkntrace.Receipt{ReceiptID: "receipt-1", ReceiptStatus: "completed"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()

	ctx := trustedMCPIntegrationContext(context.Background(), 77)
	handler := lifecycleToolMiddleware(bkntrace.NewLifecycleClient(core.URL, core.Client()))(
		func(_ context.Context, request mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(
				map[string]any{"tool": request.Params.Name, "ok": true}, `{"ok":true}`,
			), nil
		},
	)
	wantCalls := 0
	for _, toolName := range communityTools {
		if _, lifecycle := lifecycleToolNames[toolName]; lifecycle {
			continue
		}
		wantCalls++
		request := businessToolRequest("session-1", "conv-1", "int-1", "call-"+toolName)
		request.Params.Name = toolName
		request.Params.Arguments.(map[string]any)["probe"] = toolName
		result, err := handler(ctx, request)
		if err != nil || result == nil || result.IsError {
			t.Fatalf("%s lifecycle failed: result=%#v err=%v", toolName, result, err)
		}
	}
	if ensureCalls != wantCalls || finishCalls != wantCalls {
		t.Fatalf("managed tool coverage: ensure=%d finish=%d want=%d", ensureCalls, finishCalls, wantCalls)
	}
}

func TestManagedBusinessToolsPreservePreciseArgumentsAndResults(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string]any
		output map[string]any
	}{
		{
			name: "search_schema",
			input: map[string]any{
				"kn_id": "supplychain_hd0202", "query": "采购订单 供应商",
				"search_scope": map[string]any{
					"concept_groups":       []any{"procurement"},
					"include_object_types": true, "include_relation_types": true,
					"include_action_types": false, "include_metric_types": false,
				},
				"max_concepts": 8, "schema_brief": false, "include_columns": true,
			},
			output: map[string]any{
				"object_types":   []any{map[string]any{"id": "purchase_order"}},
				"relation_types": []any{map[string]any{"id": "ordered_from_supplier"}},
				"action_types":   []any{}, "metric_types": []any{},
			},
		},
		{
			name: "query_object_instance",
			input: map[string]any{
				"kn_id": "supplychain_hd0202", "ot_id": "purchase_order",
				"condition": map[string]any{
					"operation": "and",
					"sub_conditions": []any{
						map[string]any{"field": "material_number", "operation": "==", "value_from": "const", "value": "101-000015"},
						map[string]any{"field": "status", "operation": "in", "value_from": "const", "value": []any{"open", "released"}},
					},
				},
				"properties": []any{"purchase_order_number", "supplier_name"}, "limit": 20,
			},
			output: map[string]any{
				"datas":       []any{map[string]any{"purchase_order_number": "PO-240801", "supplier_name": "华东供应商"}},
				"total_count": 1,
			},
		},
		{
			name: "run_sql",
			input: map[string]any{
				"sql":           "SELECT purchase_order_number, supplier_name FROM {{.purchase_order_resource}} WHERE material_number = '101-000015' ORDER BY created_at DESC LIMIT 20",
				"query_timeout": 60,
			},
			output: map[string]any{
				"columns":     []any{map[string]any{"name": "purchase_order_number", "type": "string"}, map[string]any{"name": "supplier_name", "type": "string"}},
				"entries":     []any{map[string]any{"purchase_order_number": "PO-240801", "supplier_name": "华东供应商"}},
				"total_count": 1,
			},
		},
	}

	var inputs []bkntrace.PayloadEnvelope
	var outputs []bkntrace.PayloadEnvelope
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/interactions/int-1"):
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1",
				ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			var body struct {
				ToolName string                   `json:"tool_name"`
				Input    bkntrace.PayloadEnvelope `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode ensure: %v", err)
			}
			inputs = append(inputs, body.Input)
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Created: true, Execute: true,
				Operation: bkntrace.Operation{
					OperationID: "op-1", ConversationID: "conv-1", InteractionID: "int-1",
					ToolName: body.ToolName, Attempt: 1, AttemptStatus: "pending",
				},
				Receipt: bkntrace.Receipt{ReceiptID: "receipt-1", ReceiptStatus: "pending"},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attempts/1:complete"):
			var body struct {
				Output bkntrace.PayloadEnvelope `json:"output"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode completion: %v", err)
			}
			outputs = append(outputs, body.Output)
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Operation: bkntrace.Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"},
				Receipt:   bkntrace.Receipt{ReceiptID: "receipt-1", ReceiptStatus: "completed"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()

	results := make(map[string]*mcpsdk.CallToolResult, len(tests))
	wantOutputs := make(map[string][]byte, len(tests))
	for _, test := range tests {
		results[test.name] = mcpsdk.NewToolResultStructured(test.output, `{"ok":true}`)
		wantOutputs[test.name], _ = json.Marshal(results[test.name])
	}
	handler := lifecycleToolMiddleware(bkntrace.NewLifecycleClient(core.URL, core.Client()))(
		func(_ context.Context, request mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return results[request.Params.Name], nil
		},
	)
	for index, test := range tests {
		request := businessToolRequest("session-1", "conv-1", "int-1", "precise-"+test.name)
		request.Params.Name = test.name
		args := make(map[string]any, len(test.input)+1)
		for key, value := range test.input {
			args[key] = value
		}
		args["bkn_context"] = map[string]any{
			"conversation_id": "conv-1", "interaction_id": "int-1", "operation_key": "precise-" + test.name,
		}
		request.Params.Arguments = args
		result, err := handler(trustedMCPIntegrationContext(context.Background(), uint64(index+100)), request)
		if err != nil || result == nil || result.IsError {
			t.Fatalf("%s lifecycle failed: result=%#v err=%v", test.name, result, err)
		}
	}
	if len(inputs) != len(tests) || len(outputs) != len(tests) {
		t.Fatalf("captured inputs=%d outputs=%d, want %d", len(inputs), len(outputs), len(tests))
	}
	for index, test := range tests {
		if inputs[index].Mode != "inline" {
			t.Fatalf("%s input mode=%q", test.name, inputs[index].Mode)
		}
		var gotInput map[string]any
		if err := json.Unmarshal(inputs[index].Inline, &gotInput); err != nil {
			t.Fatalf("%s input decode failed: %v", test.name, err)
		}
		gotInputJSON, _ := json.Marshal(gotInput)
		wantInputJSON, _ := json.Marshal(test.input)
		if !bytes.Equal(gotInputJSON, wantInputJSON) {
			t.Fatalf("%s input=%s, want %s", test.name, gotInputJSON, wantInputJSON)
		}
		wantOutput := wantOutputs[test.name]
		var gotOutputValue, wantOutputValue any
		gotErr := json.Unmarshal(outputs[index].Inline, &gotOutputValue)
		wantErr := json.Unmarshal(wantOutput, &wantOutputValue)
		if outputs[index].Mode != "inline" || gotErr != nil || wantErr != nil || !reflect.DeepEqual(gotOutputValue, wantOutputValue) {
			t.Fatalf("%s output=%s, want %s; gotErr=%v wantErr=%v", test.name, outputs[index].Inline, wantOutput, gotErr, wantErr)
		}
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

func TestLifecycleToolRegistryDoesNotExposeCallerControlledFinalize(t *testing.T) {
	if _, exposed := lifecycleToolNames["bkn_finalize_operation"]; exposed {
		t.Fatal("third-party callers must not be allowed to finalize platform execution receipts")
	}
}
