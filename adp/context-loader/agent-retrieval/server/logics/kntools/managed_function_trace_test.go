// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kntools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type managedFunctionRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn managedFunctionRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func managedFunctionJSONResponse(status int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: status, Header: make(http.Header),
		Body: io.NopCloser(bytes.NewReader(raw)),
	}
}

func TestRunAndCleanTraceGapsPreservesBusinessResult(t *testing.T) {
	ctx := bkntrace.WithTraceAvailability(context.Background())
	result, err := runAndCleanTraceGaps(ctx, func(context.Context) (map[string]any, error) {
		return map[string]any{
			"status_code": 200,
			"body": map[string]any{
				"stderr": "business stderr\n\n[BKN_TRACE_GAP]{\"partial_reason\":\"trace_call_unrecorded:run_cypher:req_92343e69-c464-49e9-86b8-a3cc2aba92d9\"}\n",
				"result": map[string]any{"value": 30},
			},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	body := result["body"].(map[string]any)
	if body["stderr"] != "business stderr\n" {
		t.Fatalf("stderr = %q", body["stderr"])
	}
	if body["result"].(map[string]any)["value"] != 30 {
		t.Fatalf("business result changed: %#v", body["result"])
	}
}

func TestRunAndCleanTraceGapsPreservesInvalidMarker(t *testing.T) {
	ctx := bkntrace.WithTraceAvailability(context.Background())
	result, err := runAndCleanTraceGaps(ctx, func(context.Context) (map[string]any, error) {
		return map[string]any{"body": map[string]any{
			"stderr": "keep [BKN_TRACE_GAP]{bad}\n",
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result["body"].(map[string]any)["stderr"]; got != "keep [BKN_TRACE_GAP]{bad}\n" {
		t.Fatalf("invalid marker was removed: %q", got)
	}
}

func TestManagedFunctionGuardRecordsBusinessClosureBoundary(t *testing.T) {
	var ensureBody map[string]any
	var finishBody map[string]any
	client := bkntrace.NewLifecycleClient("http://trace.test", &http.Client{
		Transport: managedFunctionRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch {
			case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/interactions/int-1"):
				return managedFunctionJSONResponse(http.StatusOK, bkntrace.Interaction{
					InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active",
					LeaseToken: "lease-1", LeaseEpoch: 1,
				}), nil
			case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/operations:ensure"):
				if err := json.NewDecoder(request.Body).Decode(&ensureBody); err != nil {
					t.Fatal(err)
				}
				return managedFunctionJSONResponse(http.StatusCreated, bkntrace.OperationResult{
					Created: true, Execute: true,
					Operation: bkntrace.Operation{
						OperationID: "op-function-1", ConversationID: "conv-1", InteractionID: "int-1",
						Attempt: 1, AttemptStatus: "pending", CreatedAt: time.Now().UTC(),
					},
					Receipt: bkntrace.Receipt{ReceiptID: "receipt-function-1", ReceiptStatus: "pending"},
				}), nil
			case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/attempts/1:complete"):
				if err := json.NewDecoder(request.Body).Decode(&finishBody); err != nil {
					t.Fatal(err)
				}
				return managedFunctionJSONResponse(http.StatusOK, bkntrace.OperationResult{
					Operation: bkntrace.Operation{OperationID: "op-function-1", Attempt: 1, AttemptStatus: "completed"},
					Receipt:   bkntrace.Receipt{ReceiptID: "receipt-function-1", ReceiptStatus: "completed"},
				}), nil
			default:
				return managedFunctionJSONResponse(http.StatusNotFound, map[string]any{}), nil
			}
		}),
	})
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		RequestID: "req_12345678", ConversationID: "conv-1", InteractionID: "int-1",
		OperationID: "op-execute-tool", Attempt: 1,
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "app-1"},
	})
	guard := &managedFunctionGuard{guard: bkntrace.NewGuard(client), enabled: true}

	result, err := guard.Execute(ctx, ManagedFunctionTraceInput{
		KnowledgeNetworkID: "supply", ToolboxID: "box-1", ToolID: "material_where_used",
		Descriptor: interfaces.ManagedFunctionDescriptor{
			ToolID: "material_where_used", Name: "物料反查产品", Description: "查询使用指定物料的产品", Version: "1.2.3",
		},
		Arguments: map[string]any{
			"material_code": "606-000989", "authorization": "Bearer must-not-persist",
			"accessToken": "must-not-persist", "clientSecret": "must-not-persist",
		},
	}, func(executionContext context.Context) (map[string]any, error) {
		traceContext, _ := common.GetTraceContextFromCtx(executionContext)
		if traceContext.OperationID != "op-function-1" {
			t.Fatalf("function received operation %q", traceContext.OperationID)
		}
		return map[string]any{
			"products": []any{"P-1", "P-2"}, "access_token": "must-not-persist",
			"refreshToken": "must-not-persist", "apiKey": "must-not-persist",
		}, nil
	})

	if err != nil {
		t.Fatalf("managed function trace: %v", err)
	}
	if !reflect.DeepEqual(result["products"], []any{"P-1", "P-2"}) || result["access_token"] != "must-not-persist" {
		t.Fatalf("business result was changed: %#v", result)
	}
	if ensureBody["parent_operation_id"] != "op-execute-tool" ||
		ensureBody["protocol"] != "internal" || ensureBody["source_module"] != managedFunctionSourceModule ||
		ensureBody["tool_name"] != "material_where_used" {
		t.Fatalf("function operation identity = %#v", ensureBody)
	}
	profile := ensureBody["capability_profile"].(map[string]any)
	if profile["evidence_contract"] != "managed_function_execution/v1" ||
		profile["canonical_tool_name"] != "material_where_used" || profile["resolution"] != "matched" {
		t.Fatalf("function capability profile = %#v", profile)
	}
	inputEnvelope := ensureBody["input"].(map[string]any)
	input := inputEnvelope["inline"].(map[string]any)
	if input["function_name"] != "物料反查产品" || input["function_version"] != "1.2.3" {
		t.Fatalf("function business identity missing: %#v", input)
	}
	arguments := input["arguments"].(map[string]any)
	if arguments["material_code"] != "606-000989" || arguments["authorization"] != "[redacted]" ||
		arguments["accessToken"] != "[redacted]" || arguments["clientSecret"] != "[redacted]" {
		t.Fatalf("function arguments not safely recorded: %#v", arguments)
	}
	outputEnvelope := finishBody["output"].(map[string]any)
	output := outputEnvelope["inline"].(map[string]any)
	if !reflect.DeepEqual(output["products"], []any{"P-1", "P-2"}) || output["access_token"] != "[redacted]" ||
		output["refreshToken"] != "[redacted]" || output["apiKey"] != "[redacted]" {
		t.Fatalf("function result not safely recorded: %#v", output)
	}
	refs := finishBody["business_refs"].([]any)
	if len(refs) != 2 {
		t.Fatalf("function refs = %#v", refs)
	}
	functionRef := refs[1].(map[string]any)
	if functionRef["ref_id"] != "function:supply:material_where_used" {
		t.Fatalf("function ref violates the Trace Core canonical contract: %#v", functionRef)
	}
}

func TestManagedFunctionGuardFinalizesExecutionFailures(t *testing.T) {
	tests := []struct {
		name      string
		run       func(context.Context) (map[string]any, error)
		wantError bool
	}{
		{"returned error", func(context.Context) (map[string]any, error) { return nil, errors.New("runtime failed") }, true},
		{"failed result", func(context.Context) (map[string]any, error) { return map[string]any{"status_code": 500}, nil }, false},
		{"panic", func(context.Context) (map[string]any, error) { panic("must-not-persist") }, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var finish map[string]any
			finishes := 0
			client := bkntrace.NewLifecycleClient("http://trace.test", &http.Client{Transport: managedFunctionRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch {
				case r.Method == http.MethodGet:
					return managedFunctionJSONResponse(200, bkntrace.Interaction{InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 1}), nil
				case strings.HasSuffix(r.URL.Path, "/operations:ensure"):
					return managedFunctionJSONResponse(201, bkntrace.OperationResult{Created: true, Execute: true, Operation: bkntrace.Operation{OperationID: "op-function", ConversationID: "conv-1", InteractionID: "int-1", Attempt: 1, AttemptStatus: "pending", CreatedAt: time.Now().UTC()}, Receipt: bkntrace.Receipt{ReceiptID: "receipt-function", ReceiptStatus: "pending"}}), nil
				case strings.HasSuffix(r.URL.Path, "/attempts/1:fail"):
					finishes++
					if err := json.NewDecoder(r.Body).Decode(&finish); err != nil {
						t.Fatal(err)
					}
					return managedFunctionJSONResponse(200, bkntrace.OperationResult{Operation: bkntrace.Operation{OperationID: "op-function", Attempt: 1, AttemptStatus: "failed"}, Receipt: bkntrace.Receipt{ReceiptID: "receipt-function", ReceiptStatus: "failed"}}), nil
				default:
					t.Fatalf("unexpected lifecycle request: %s", r.URL.Path)
					return nil, nil
				}
			})})
			ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{RequestID: "req_12345678", ConversationID: "conv-1", InteractionID: "int-1", OperationID: "op-outer", Attempt: 1})
			ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{AccountID: "user-1", AccountType: interfaces.AccessorTypeUser, TokenInfo: &interfaces.TokenInfo{ClientID: "app-1"}})
			guard := &managedFunctionGuard{guard: bkntrace.NewGuard(client), enabled: true}
			defer func() {
				if v := recover(); v != nil {
					t.Fatalf("panic escaped before recording the function failure: %v", v)
				}
			}()
			_, err := guard.Execute(ctx, ManagedFunctionTraceInput{KnowledgeNetworkID: "supply", ToolboxID: "box", ToolID: "bom"}, test.run)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v", err)
			}
			if finishes != 1 {
				t.Fatalf("failure finishes = %d, want exactly one", finishes)
			}
			refs := finish["business_refs"].([]any)
			if len(refs) != 2 || refs[1].(map[string]any)["ref_id"] != "function:supply:bom" {
				t.Fatalf("failure refs = %#v", refs)
			}
			raw, _ := json.Marshal(finish)
			if bytes.Contains(raw, []byte("must-not-persist")) {
				t.Fatal("panic content leaked into receipt")
			}
			if finish["error"] == nil {
				t.Fatal("failure outcome has no error content")
			}
		})
	}
}

func TestManagedFunctionGuardPreservesBusinessResultWhenFinalizationFails(t *testing.T) {
	client := bkntrace.NewLifecycleClient("http://trace.test", &http.Client{
		Transport: managedFunctionRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch {
			case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/interactions/int-1"):
				return managedFunctionJSONResponse(http.StatusOK, bkntrace.Interaction{
					InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active",
					LeaseToken: "lease-1", LeaseEpoch: 1,
				}), nil
			case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/operations:ensure"):
				return managedFunctionJSONResponse(http.StatusCreated, bkntrace.OperationResult{
					Created: true, Execute: true,
					Operation: bkntrace.Operation{
						OperationID: "op-function-1", ConversationID: "conv-1", InteractionID: "int-1",
						Attempt: 1, AttemptStatus: "pending", CreatedAt: time.Now().UTC(),
					},
					Receipt: bkntrace.Receipt{ReceiptID: "receipt-function-1", ReceiptStatus: "pending"},
				}), nil
			case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/attempts/1:complete"):
				return managedFunctionJSONResponse(http.StatusBadRequest, map[string]any{
					"error": map[string]any{
						"code": "trace_finalization_failed", "message": "trace core unavailable",
						"retryable": false,
					},
				}), nil
			default:
				return managedFunctionJSONResponse(http.StatusNotFound, map[string]any{}), nil
			}
		}),
	})
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		RequestID: "req_12345678", ConversationID: "conv-1", InteractionID: "int-1",
		OperationID: "op-execute-tool", Attempt: 1,
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "app-1"},
	})
	guard := &managedFunctionGuard{guard: bkntrace.NewGuard(client), enabled: true}

	result, err := guard.Execute(ctx, ManagedFunctionTraceInput{
		KnowledgeNetworkID: "supply", ToolboxID: "box-1", ToolID: "material_where_used",
		Descriptor: interfaces.ManagedFunctionDescriptor{ToolID: "material_where_used", Name: "物料反查产品"},
		Arguments:  map[string]any{"material_code": "606-000989"},
	}, func(context.Context) (map[string]any, error) {
		return map[string]any{"products": []any{"P-1", "P-2"}}, nil
	})

	if err != nil {
		t.Fatalf("successful function was changed into an error: %v", err)
	}
	if !reflect.DeepEqual(result["products"], []any{"P-1", "P-2"}) {
		t.Fatalf("business result was lost after trace finalization failure: %#v", result)
	}
}

func TestManagedFunctionIdentityIncludesToolboxScope(t *testing.T) {
	trace := common.TraceContext{OperationID: "op-execute", Attempt: 1}
	left := managedFunctionOperationKey(trace, ManagedFunctionTraceInput{
		KnowledgeNetworkID: "supply", ToolboxID: "box-a", ToolID: "same-tool",
	})
	right := managedFunctionOperationKey(trace, ManagedFunctionTraceInput{
		KnowledgeNetworkID: "supply", ToolboxID: "box-b", ToolID: "same-tool",
	})
	if left == right {
		t.Fatalf("toolbox-scoped functions share operation key %q", left)
	}
}

func TestManagedFunctionResultFailedUsesExecutionStatusCode(t *testing.T) {
	tests := []struct {
		name   string
		result map[string]any
		failed bool
	}{
		{name: "success", result: map[string]any{"status_code": float64(200)}, failed: false},
		{name: "runtime failure number", result: map[string]any{"status_code": float64(500)}, failed: true},
		{name: "runtime failure string", result: map[string]any{"status_code": "503"}, failed: true},
		{name: "sandbox exit failure", result: map[string]any{
			"status_code": float64(200), "body": map[string]any{"exit_code": float64(1)},
		}, failed: true},
		{name: "business payload without envelope", result: map[string]any{"products": []any{"P-1"}}, failed: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := managedFunctionResultFailed(test.result); got != test.failed {
				t.Fatalf("managedFunctionResultFailed(%#v) = %t, want %t", test.result, got, test.failed)
			}
		})
	}
}

func TestManagedFunctionFailurePayloadKeepsOnlySafeExecutionFacts(t *testing.T) {
	payload := managedFunctionFailurePayload(map[string]any{
		"status_code": float64(200),
		"body": map[string]any{
			"exit_code": float64(1), "stderr": "sensitive runtime detail",
		},
	})
	if payload["status_code"] != 200 || payload["exit_code"] != 1 || payload["code"] != "managed_function_failed" {
		t.Fatalf("safe failure payload = %#v", payload)
	}
	if _, exists := payload["stderr"]; exists {
		t.Fatalf("failure payload exposed stderr: %#v", payload)
	}
}
