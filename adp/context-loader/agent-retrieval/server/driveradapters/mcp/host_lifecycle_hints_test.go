// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type clientInfoSession struct {
	info mcpsdk.Implementation
}

func (session *clientInfoSession) Initialize()                                            {}
func (session *clientInfoSession) Initialized() bool                                      { return true }
func (session *clientInfoSession) NotificationChannel() chan<- mcpsdk.JSONRPCNotification { return nil }
func (session *clientInfoSession) SessionID() string                                      { return "session-client-info" }
func (session *clientInfoSession) GetClientInfo() mcpsdk.Implementation                   { return session.info }
func (session *clientInfoSession) SetClientInfo(info mcpsdk.Implementation)               { session.info = info }
func (session *clientInfoSession) GetClientCapabilities() mcpsdk.ClientCapabilities {
	return mcpsdk.ClientCapabilities{}
}
func (session *clientInfoSession) SetClientCapabilities(mcpsdk.ClientCapabilities) {}

func TestWithMCPClientApplicationUsesInitializeClientInfo(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0")
	ctx := mcpServer.WithContext(context.Background(), &clientInfoSession{
		info: mcpsdk.Implementation{Name: "Cursor", Version: "1.2.3"},
	})

	ctx = withMCPClientApplication(ctx)
	if value, ok := common.GetApplicationDisplayNameFromCtx(ctx); !ok || value != "Cursor" {
		t.Fatalf("application display name = %q, %v; want Cursor", value, ok)
	}
}

func TestHostLifecycleHintsUseNamespacedMetaAndHeaderPrecedence(t *testing.T) {
	request := mcpsdk.CallToolRequest{
		Header: http.Header{
			hostConversationKeyHeader: []string{"header-thread"},
			clientInvocationIDHeader:  []string{"header-invocation"},
			"Mcp-Session-Id":          []string{"transport-session-must-be-ignored"},
		},
		Params: mcpsdk.CallToolParams{
			Arguments: map[string]any{
				"host_conversation_key": "forged-argument-thread",
				"client_invocation_id":  "forged-argument-invocation",
			},
			Meta: &mcpsdk.Meta{AdditionalFields: map[string]any{
				hostConversationKeyMeta: "meta-thread",
				clientInvocationIDMeta:  "meta-invocation",
			}},
		},
	}

	hints, err := hostLifecycleHintsFromRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if hints.HostConversationKey != "header-thread" || hints.ClientInvocationID != "header-invocation" {
		t.Fatalf("host hints = %#v", hints)
	}
}

func TestHostLifecycleHintsRejectInvalidValues(t *testing.T) {
	request := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{
		Meta: &mcpsdk.Meta{AdditionalFields: map[string]any{
			hostConversationKeyMeta: map[string]any{"not": "a string"},
		}},
	}}
	if _, err := hostLifecycleHintsFromRequest(request); err == nil {
		t.Fatal("object-valued host hint must be rejected")
	}
}

func TestStartInteractionRetryUsesStableHostHintsAcrossTransportSessions(t *testing.T) {
	var ensureKeys []string
	var startKeys []string
	client := &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/agent-observability/v1/conversations:ensure-current":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			ensureKeys = append(ensureKeys, stringValue(body["external_conversation_key"]))
			return lifecycleAdapterJSONResponse(http.StatusOK, bkntrace.Conversation{
				ConversationID: "conv-stable-1", Status: "active",
			}), nil
		case "/api/agent-observability/v1/conversations/conv-stable-1/interactions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			startKeys = append(startKeys, stringValue(body["idempotency_key"]))
			return lifecycleAdapterJSONResponse(http.StatusOK, bkntrace.Interaction{
				InteractionID: "int-stable-1", ConversationID: "conv-stable-1",
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
		RequestID: "req-host-retry-1", TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "cursor-app"},
	})
	handler := handleLifecycleTool(
		bkntrace.NewLifecycleClient("http://bkn-trace.test", client),
		"bkn_start_interaction",
	)

	for _, transportSession := range []string{"transport-a", "transport-b"} {
		request := mcpsdk.CallToolRequest{
			Header: http.Header{"Mcp-Session-Id": []string{transportSession}},
			Params: mcpsdk.CallToolParams{
				Arguments: map[string]any{"question": "查询库存"},
				Meta: &mcpsdk.Meta{AdditionalFields: map[string]any{
					hostConversationKeyMeta: "cursor-chat-1",
					clientInvocationIDMeta:  "cursor-call-1",
				}},
			},
		}
		result, err := handler(ctx, request)
		if err != nil || result.IsError {
			t.Fatalf("retry failed: result=%#v err=%v", result, err)
		}
		structured := result.StructuredContent.(map[string]any)
		if structured["conversation_id"] != "conv-stable-1" || structured["interaction_id"] != "int-stable-1" {
			t.Fatalf("retry returned different IDs: %#v", structured)
		}
	}
	if len(ensureKeys) != 2 || ensureKeys[0] == "" || ensureKeys[0] != ensureKeys[1] {
		t.Fatalf("host conversation mapping is not stable: %#v", ensureKeys)
	}
	if len(startKeys) != 2 || startKeys[0] == "" || startKeys[0] != startKeys[1] {
		t.Fatalf("start retry idempotency is not stable: %#v", startKeys)
	}
}

func TestStartInteractionRejectsConversationThatConflictsWithHostMapping(t *testing.T) {
	startCalls := 0
	client := &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/agent-observability/v1/conversations:ensure-current":
			return lifecycleAdapterJSONResponse(http.StatusOK, bkntrace.Conversation{
				ConversationID: "conv-host-owned", Status: "active",
			}), nil
		case "/api/agent-observability/v1/conversations/conv-model-supplied/interactions":
			startCalls++
			return lifecycleAdapterJSONResponse(http.StatusOK, bkntrace.Interaction{}), nil
		default:
			return lifecycleAdapterJSONResponse(http.StatusNotFound, map[string]any{
				"error": map[string]any{"code": "not_found", "message": "not found"},
			}), nil
		}
	})}
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		RequestID: "req-host-conflict-1", TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "cursor-app"},
	})
	result, err := handleLifecycleTool(
		bkntrace.NewLifecycleClient("http://bkn-trace.test", client),
		"bkn_start_interaction",
	)(ctx, mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{
		Arguments: map[string]any{
			"conversation_id": "conv-model-supplied",
			"question":        "查询库存",
		},
		Meta: &mcpsdk.Meta{AdditionalFields: map[string]any{
			hostConversationKeyMeta: "cursor-chat-1",
		}},
	}})
	if err != nil || !result.IsError {
		t.Fatalf("host conflict must be a tool error: result=%#v err=%v", result, err)
	}
	if startCalls != 0 {
		t.Fatalf("host conflict executed start %d times", startCalls)
	}
	errorValue := lifecycleErrorFromResult(t, result)
	if errorValue["code"] != "conversation_context_conflict" {
		t.Fatalf("host conflict error = %#v", errorValue)
	}
}
