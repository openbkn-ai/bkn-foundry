// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kntools

import (
	"bytes"
	"context"
	"encoding/json"
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
	if functionRef["ref_id"] != "function:supply:box-1:material_where_used" {
		t.Fatalf("function ref is not toolbox-scoped: %#v", functionRef)
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
