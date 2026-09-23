// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

// fakeTraceCore answers the lifecycle calls a managed tool makes. It records
// the tool name of every Operation it is asked to ensure, and the business
// refs of every receipt it is asked to finish.
func fakeTraceCore(t *testing.T) (*bkntrace.LifecycleClient, func() []string, func() [][]bkntrace.BusinessRef) {
	t.Helper()
	var mu sync.Mutex
	var ensured []string
	var finished [][]bkntrace.BusinessRef
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/interactions/"):
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: pathTail(r.URL.Path), ConversationID: "conv-1",
				ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			var body struct {
				OperationKey string `json:"operation_key"`
				ToolName     string `json:"tool_name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			ensured = append(ensured, body.ToolName)
			mu.Unlock()
			operationID := "op-" + body.OperationKey
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Created: true, Execute: true,
				Operation: bkntrace.Operation{
					OperationID: operationID, ConversationID: "conv-1", InteractionID: "int-1",
					OperationKey: body.OperationKey, ToolName: body.ToolName, Attempt: 1, AttemptStatus: "pending",
				},
				Receipt: bkntrace.Receipt{
					ReceiptID: "receipt-" + body.OperationKey, OperationID: operationID,
					ConversationID: "conv-1", InteractionID: "int-1", Attempt: 1, ReceiptStatus: "pending",
				},
			})
		case r.Method == http.MethodPost && (strings.HasSuffix(r.URL.Path, ":complete") || strings.HasSuffix(r.URL.Path, ":fail")):
			var body struct {
				BusinessRefs []bkntrace.BusinessRef `json:"business_refs"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			finished = append(finished, body.BusinessRefs)
			mu.Unlock()
			status := "completed"
			if strings.HasSuffix(r.URL.Path, ":fail") {
				status = "failed"
			}
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Operation: bkntrace.Operation{OperationID: "op-done", Attempt: 1, AttemptStatus: status},
				Receipt: bkntrace.Receipt{
					ReceiptID: "receipt-done", OperationID: "op-done", Attempt: 1,
					ReceiptStatus: status, EvidenceDurability: "durable",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(core.Close)
	return bkntrace.NewLifecycleClient(core.URL, core.Client()), func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), ensured...)
		}, func() [][]bkntrace.BusinessRef {
			mu.Lock()
			defer mu.Unlock()
			return append([][]bkntrace.BusinessRef(nil), finished...)
		}
}

func callTrustedTool(t *testing.T, srv *server.MCPServer, name string, arguments map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": arguments},
	})
	if err != nil {
		t.Fatal(err)
	}
	var result mcpsdk.CallToolResult
	resultOf(t, srv.HandleMessage(trustedMCPIntegrationContext(context.Background(), 1), req), &result)
	return &result
}

// Search, describe and execute through the real compact server and its real
// middleware stack: search and describe are each recorded under their own
// name, and execute is recorded once, under the target's.
func TestCompactGatewayRunsEndToEnd(t *testing.T) {
	t.Setenv("CONFIG_PROFILE", "../../infra/config")
	client, ensured, _ := fakeTraceCore(t)
	srv, _ := newMCPServerForProfile(client, "zh-CN", defaultPTCServicePort, compactProfile)
	bknContext := map[string]any{"conversation_id": "conv-1", "interaction_id": "int-1"}

	found := callTrustedTool(t, srv, toolKeySearchNativeTools, map[string]any{"query": "看订单对象类有哪些字段", "bkn_context": bknContext})
	var search gatewaySearchResult
	if err := json.Unmarshal([]byte(resultText(found)), &search); err != nil || found.IsError {
		t.Fatalf("search: %s", resultText(found))
	}
	if len(search.Candidates) == 0 || search.Candidates[0].Name != toolKeyGetObjectTypes {
		t.Fatalf("search candidates = %v", candidateNames(search))
	}

	described := callTrustedTool(t, srv, toolKeyDescribeNativeTool, map[string]any{"name": toolKeyGetObjectTypes, "bkn_context": bknContext})
	var description gatewayDescription
	if err := json.Unmarshal([]byte(resultText(described)), &description); err != nil || described.IsError {
		t.Fatalf("describe: %s", resultText(described))
	}

	callTrustedTool(t, srv, toolKeyExecuteNativeTool, map[string]any{
		"name": toolKeyGetObjectTypes, "arguments": description.CallTemplate.Arguments, "bkn_context": bknContext,
	})
	want := []string{toolKeySearchNativeTools, toolKeyDescribeNativeTool, toolKeyGetObjectTypes}
	if got := ensured(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Operations ensured for %v, want %v", got, want)
	}
}

// The gateway tools exist only on the compact profile, and the full profile's
// tools/list is unchanged by them.
func TestOnlyTheCompactProfileHasTheGateway(t *testing.T) {
	full, _ := newMCPServerForLocale(nil, "zh-CN")
	for _, name := range listedToolNames(t, full) {
		if _, gateway := gatewayTools[name]; gateway {
			t.Errorf("the full profile publishes %s", name)
		}
	}
	listed := listedToolNames(t, compactServer(t, "zh-CN"))
	for name := range gatewayTools {
		found := false
		for _, got := range listed {
			found = found || got == name
		}
		if !found {
			t.Errorf("the compact profile does not publish %s", name)
		}
	}
}

// search and describe are recorded under their own names, so Trace must
// recognise them; the executor never is, so it needs no entry.
func TestGatewayToolsResolveTheirCapabilityProfiles(t *testing.T) {
	for _, name := range []string{toolKeySearchNativeTools, toolKeyDescribeNativeTool} {
		var profile CapabilityProfile
		if err := json.Unmarshal(capabilityProfileJSON(name), &profile); err != nil {
			t.Fatal(err)
		}
		if profile.Resolution != capabilityResolutionMatched || profile.ExecutionRole != "discovery" {
			t.Errorf("%s: resolution %q (%s), role %q", name, profile.Resolution, profile.Reason, profile.ExecutionRole)
		}
	}
}

// Derivation must run on the target, not on the executor. The executor call
// carries no kn_id of its own, so if the guard derived refs from the outer
// arguments the receipt would record none - and the compact entry's receipts
// would be poorer than the full entry's for the same work.
func TestGatewayDerivesTheTargetsBusinessRefs(t *testing.T) {
	t.Setenv("CONFIG_PROFILE", "../../infra/config")
	bknContext := map[string]any{"conversation_id": "conv-1", "interaction_id": "int-1"}
	arguments := map[string]any{"kn_id": "kn-1", "ids": []any{"ot-1"}}

	gatewayClient, _, gatewayFinished := fakeTraceCore(t)
	gateway, _ := newMCPServerForProfile(gatewayClient, "zh-CN", defaultPTCServicePort, compactProfile)
	callTrustedTool(t, gateway, toolKeyExecuteNativeTool, map[string]any{
		"name": toolKeyGetObjectTypes, "arguments": arguments, "bkn_context": bknContext,
	})

	directClient, _, directFinished := fakeTraceCore(t)
	direct, _ := newMCPServerForProfile(directClient, "zh-CN", defaultPTCServicePort, fullProfile)
	callTrustedTool(t, direct, toolKeyGetObjectTypes, mergedArguments(arguments, bknContext))

	throughGateway, directly := lastFinishedRefs(t, gatewayFinished()), lastFinishedRefs(t, directFinished())
	if len(directly) == 0 {
		t.Fatalf("the direct call derived no refs, so this test proves nothing")
	}
	if !reflect.DeepEqual(throughGateway, directly) {
		t.Fatalf("refs through the gateway = %#v, directly = %#v", throughGateway, directly)
	}
}

func mergedArguments(arguments, bknContext map[string]any) map[string]any {
	merged := map[string]any{"bkn_context": bknContext}
	for key, value := range arguments {
		merged[key] = value
	}
	return merged
}

func lastFinishedRefs(t *testing.T, finished [][]bkntrace.BusinessRef) []bkntrace.BusinessRef {
	t.Helper()
	if len(finished) == 0 {
		t.Fatalf("no Operation was finished")
	}
	return finished[len(finished)-1]
}
