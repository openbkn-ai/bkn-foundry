// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package kntools

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestManagedFunctionContinuesOnTraceTransportFailure(t *testing.T) {
	client := bkntrace.NewLifecycleClient("http://trace.test", &http.Client{Transport: managedFunctionRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, syscall.ECONNREFUSED })})
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{RequestID: "rq_audit", ConversationID: "conv-1", InteractionID: "int-1", OperationID: "op-outer", Attempt: 1})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{AccountID: "user", AccountType: interfaces.AccessorTypeUser, TokenInfo: &interfaces.TokenInfo{ClientID: "app"}})
	guard := &managedFunctionGuard{guard: bkntrace.NewGuard(client), enabled: true}
	calls := 0
	actual, err := guard.Execute(ctx, ManagedFunctionTraceInput{KnowledgeNetworkID: "kn-1", ToolID: "function-1"}, func(context.Context) (map[string]any, error) {
		calls++
		return map[string]any{"status_code": 200, "body": "actual business value"}, nil
	})
	if err != nil || calls != 1 || actual["body"] != "actual business value" {
		t.Fatalf("Trace prevented authorized function: calls=%d result=%#v err=%v", calls, actual, err)
	}
}

func TestManagedFunctionCancellationAfterSuccessfulEnsureDoesNotStartBusiness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls, ensures, finishes := 0, 0, 0
	client := bkntrace.NewLifecycleClient("http://trace.test", &http.Client{Transport: managedFunctionRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/interactions/int-1") {
			return managedFunctionJSONResponse(200, bkntrace.Interaction{InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active", LeaseToken: "lease", LeaseEpoch: 1}), nil
		}
		if strings.HasSuffix(r.URL.Path, "/operations:ensure") {
			ensures++
			cancel()
			return managedFunctionJSONResponse(200, bkntrace.OperationResult{Execute: true, Operation: bkntrace.Operation{OperationID: "op-actual", Attempt: 1}, Receipt: bkntrace.Receipt{ReceiptID: "receipt-actual", ReceiptStatus: "pending"}}), nil
		}
		finishes++
		return managedFunctionJSONResponse(200, map[string]any{}), nil
	})})
	ctx = common.SetTraceContextToCtx(ctx, common.TraceContext{RequestID: "req_cancel", ConversationID: "conv-1", InteractionID: "int-1", OperationID: "op-outer", Attempt: 1})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{AccountID: "user", AccountType: interfaces.AccessorTypeUser, TokenInfo: &interfaces.TokenInfo{ClientID: "app"}})
	guard := &managedFunctionGuard{guard: bkntrace.NewGuard(client), enabled: true}
	_, err := guard.Execute(ctx, ManagedFunctionTraceInput{KnowledgeNetworkID: "kn-1", ToolID: "function-1"}, func(context.Context) (map[string]any, error) {
		calls++
		return map[string]any{"body": "must not start"}, nil
	})
	if ensures != 1 || calls != 0 || finishes != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled function caller executed after registration: ensure=%d business=%d finish=%d err=%v", ensures, calls, finishes, err)
	}
}
