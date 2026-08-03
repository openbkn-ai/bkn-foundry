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
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

type fakeClientSession struct{ id string }

func (s fakeClientSession) Initialize()                                            {}
func (s fakeClientSession) Initialized() bool                                      { return true }
func (s fakeClientSession) NotificationChannel() chan<- mcpsdk.JSONRPCNotification { return nil }
func (s fakeClientSession) SessionID() string                                      { return s.id }

type fakeCore struct {
	server            *httptest.Server
	mu                sync.Mutex
	conversationCalls int
	interactionKeys   []string
}

func newFakeCore(t *testing.T) *fakeCore {
	t.Helper()
	core := &fakeCore{}
	core.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		core.mu.Lock()
		defer core.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/conversations:ensure-current"):
			var body struct {
				ExternalConversationKey string `json:"external_conversation_key"`
				IdempotencyKey          string `json:"idempotency_key"`
				OneShot                 bool   `json:"one_shot"`
			}
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": bkntrace.APIError{
					Code: "conversation_required", Message: "request body does not match the lifecycle contract",
					RequiredAction: "create_conversation",
				}})
				return
			}
			core.conversationCalls++
			_ = json.NewEncoder(w).Encode(bkntrace.Conversation{
				ConversationID:          "conv-for-" + body.ExternalConversationKey,
				ExternalConversationKey: body.ExternalConversationKey,
				Status:                  "active",
			})
		case strings.HasSuffix(r.URL.Path, "/interactions"):
			var body struct {
				IdempotencyKey string `json:"idempotency_key"`
				LeaseSeconds   int64  `json:"lease_seconds"`
			}
			// Core decodes with DisallowUnknownFields and its start contract has
			// exactly these two fields. A lenient fake here let a body Core would
			// reject pass every unit test and fail on the first real call.
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": bkntrace.APIError{
					Code: "interaction_required", Message: "request body does not match the lifecycle contract",
					RequiredAction: "start_interaction",
				}})
				return
			}
			core.interactionKeys = append(core.interactionKeys, body.IdempotencyKey)
			// Mirrors Core: a replayed start key resolves to the interaction that
			// key already owns and answers 200, without consulting its lease. Core
			// therefore never reports staleness here, and a rebuild that reuses the
			// key gets the dead interaction straight back.
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID:  "int-for-" + body.IdempotencyKey,
				ConversationID: "conv-active", ExecutionStatus: "active",
				LeaseToken: "lease-1", LeaseEpoch: 1,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(core.server.Close)
	return core
}

func (c *fakeCore) client() *bkntrace.LifecycleClient {
	return bkntrace.NewLifecycleClient(c.server.URL, c.server.Client())
}

func contextWithSession(sessionID string) context.Context {
	server := mcpserver.NewMCPServer("test", "0")
	return server.WithContext(trustedSessionGuardContext(), fakeClientSession{id: sessionID})
}

func contextlessBusinessRequest() mcpsdk.CallToolRequest {
	return mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{
			Name:      "search_schema",
			Arguments: map[string]any{"kn_id": "kn-1", "query": "orders"},
		},
	}
}

func TestMissingBusinessContextFallsBackToTheMCPSession(t *testing.T) {
	core := newFakeCore(t)
	var seen []bknContext
	guarded := guardBusinessToolCallWithCompletion(
		func(_ context.Context, intent operationIntent) (*operationResult, *lifecycleError, error) {
			seen = append(seen, intent.Context)
			return &operationResult{Created: true, Execute: true}, nil, nil
		},
		nil, core.client(),
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(map[string]any{"ok": true}, `{"ok":true}`), nil
		},
	)

	result, err := guarded(contextWithSession("session-a"), contextlessBusinessRequest())
	if err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
	if result.IsError {
		t.Fatalf("a client with no bkn_context must still be served: %#v", result.StructuredContent)
	}
	if len(seen) != 1 {
		t.Fatalf("expected exactly one ensured operation, got %d", len(seen))
	}
	if seen[0].ConversationID != "conv-for-mcp:session-a" {
		t.Fatalf("conversation is not bound to the MCP session: %#v", seen[0])
	}
	if seen[0].InteractionID == "" || seen[0].OperationKey == "" {
		t.Fatalf("fallback left the lifecycle context incomplete: %#v", seen[0])
	}
}

func TestFallbackReusesOneConversationAcrossCallsOnTheSameSession(t *testing.T) {
	core := newFakeCore(t)
	var seen []bknContext
	guarded := guardBusinessToolCallWithCompletion(
		func(_ context.Context, intent operationIntent) (*operationResult, *lifecycleError, error) {
			seen = append(seen, intent.Context)
			return &operationResult{Created: true, Execute: true}, nil, nil
		},
		nil, core.client(),
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(map[string]any{"ok": true}, `{"ok":true}`), nil
		},
	)

	ctx := contextWithSession("session-a")
	for range 3 {
		if _, err := guarded(ctx, contextlessBusinessRequest()); err != nil {
			t.Fatalf("guard returned protocol error: %v", err)
		}
	}
	other := contextWithSession("session-b")
	if _, err := guarded(other, contextlessBusinessRequest()); err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}

	if len(seen) != 4 {
		t.Fatalf("expected four ensured operations, got %d", len(seen))
	}
	for _, entry := range seen[:3] {
		if entry.ConversationID != seen[0].ConversationID || entry.InteractionID != seen[0].InteractionID {
			t.Fatalf("one session must map to one conversation and interaction: %#v", seen[:3])
		}
	}
	if seen[3].ConversationID == seen[0].ConversationID {
		t.Fatalf("a different MCP session must not share a conversation: %#v", seen)
	}
	// Identical calls on one session share an operation key, which is what makes
	// a retried call idempotent instead of a second execution.
	if seen[0].OperationKey != seen[1].OperationKey {
		t.Fatalf("identical calls produced different operation keys: %q vs %q", seen[0].OperationKey, seen[1].OperationKey)
	}
}

func TestFallbackGivesDifferentCallsDistinctOperationKeys(t *testing.T) {
	core := newFakeCore(t)
	var seen []bknContext
	guarded := guardBusinessToolCallWithCompletion(
		func(_ context.Context, intent operationIntent) (*operationResult, *lifecycleError, error) {
			seen = append(seen, intent.Context)
			return &operationResult{Created: true, Execute: true}, nil, nil
		},
		nil, core.client(),
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(map[string]any{"ok": true}, `{"ok":true}`), nil
		},
	)

	ctx := contextWithSession("session-a")
	first := contextlessBusinessRequest()
	second := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{
		Name: "search_schema", Arguments: map[string]any{"kn_id": "kn-1", "query": "shipments"},
	}}
	for _, request := range []mcpsdk.CallToolRequest{first, second} {
		if _, err := guarded(ctx, request); err != nil {
			t.Fatalf("guard returned protocol error: %v", err)
		}
	}
	if seen[0].OperationKey == seen[1].OperationKey {
		t.Fatalf("two different calls collapsed onto one operation key: %q", seen[0].OperationKey)
	}
}

func TestExplicitBusinessContextIsNeverReplacedByTheSession(t *testing.T) {
	core := newFakeCore(t)
	var seen []bknContext
	guarded := guardBusinessToolCallWithCompletion(
		func(_ context.Context, intent operationIntent) (*operationResult, *lifecycleError, error) {
			seen = append(seen, intent.Context)
			return &operationResult{Created: true, Execute: true}, nil, nil
		},
		nil, core.client(),
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(map[string]any{"ok": true}, `{"ok":true}`), nil
		},
	)

	if _, err := guarded(contextWithSession("session-a"), validBusinessToolRequest()); err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
	if seen[0].ConversationID != "conv_1" || seen[0].OperationKey != "op-key-1" {
		t.Fatalf("caller-owned context was overwritten by the session: %#v", seen[0])
	}
	if core.conversationCalls != 0 {
		t.Fatalf("an explicit context must not open a fallback conversation")
	}
}

func TestPartialBusinessContextStillFailsInsteadOfBeingCompleted(t *testing.T) {
	core := newFakeCore(t)
	guarded := guardBusinessToolCallWithCompletion(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			t.Fatal("a half-filled context must never reach the lifecycle client")
			return nil, nil, nil
		},
		nil, core.client(),
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(map[string]any{"ok": true}, `{"ok":true}`), nil
		},
	)

	for _, testCase := range []struct {
		name      string
		arguments map[string]any
		wantCode  string
	}{
		{"conversation only", map[string]any{"conversation_id": "conv_1"}, "interaction_required"},
		{"missing operation key", map[string]any{
			"conversation_id": "conv_1", "interaction_id": "int_1",
		}, "operation_required"},
		{"interaction only", map[string]any{"interaction_id": "int_1"}, "conversation_required"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{
				Name:      "search_schema",
				Arguments: map[string]any{"bkn_context": testCase.arguments},
			}}
			result, err := guarded(contextWithSession("session-a"), request)
			if err != nil {
				t.Fatalf("guard returned protocol error: %v", err)
			}
			if !result.IsError {
				t.Fatal("a half-filled context must not be silently completed")
			}
			envelope := result.StructuredContent.(map[string]any)["error"].(map[string]any)
			if envelope["code"] != testCase.wantCode {
				t.Fatalf("error code = %v, want %s", envelope["code"], testCase.wantCode)
			}
		})
	}
}

func TestFallbackRebuildsTheSessionWhenCoreReportsItStale(t *testing.T) {
	// Core never reports staleness while starting an interaction: a replayed
	// start key resolves to the interaction it already owns, lease or no lease,
	// and answers 200. The death only shows up one step later, when the
	// operation is ensured — which is why the rebuild has to wrap that call.
	for _, staleCode := range []string{"interaction_terminal", "operation_required"} {
		t.Run(staleCode, func(t *testing.T) {
			core := newFakeCore(t)
			var seen []bknContext
			guarded := guardBusinessToolCallWithCompletion(
				func(_ context.Context, intent operationIntent) (*operationResult, *lifecycleError, error) {
					seen = append(seen, intent.Context)
					if len(seen) == 1 {
						return nil, &lifecycleError{
							Code: staleCode, Message: "session went stale between calls",
							RequiredAction: "start_interaction",
						}, nil
					}
					return &operationResult{Created: true, Execute: true}, nil, nil
				},
				nil, core.client(),
				func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
					return mcpsdk.NewToolResultStructured(map[string]any{"ok": true}, `{"ok":true}`), nil
				},
			)

			result, err := guarded(contextWithSession("session-a"), contextlessBusinessRequest())
			if err != nil {
				t.Fatalf("guard returned protocol error: %v", err)
			}
			if result.IsError {
				t.Fatalf("a stale auto session must be rebuilt, not surfaced: %#v", result.StructuredContent)
			}
			if len(core.interactionKeys) != 2 || core.interactionKeys[0] == core.interactionKeys[1] {
				t.Fatalf("rebuild must use a different start key, keys = %v", core.interactionKeys)
			}
			// Salted with the dead interaction, so concurrent callers that saw the
			// same death converge on one replacement instead of racing.
			if !strings.Contains(core.interactionKeys[1], seen[0].InteractionID) {
				t.Fatalf("replacement key is not derived from the dead interaction: %v", core.interactionKeys)
			}
			if len(seen) != 2 || seen[1].InteractionID == seen[0].InteractionID {
				t.Fatalf("rebuilt session reused the dead interaction: %#v", seen)
			}
		})
	}
}

func TestExplicitContextIsNeverRebuiltOnStaleErrors(t *testing.T) {
	// A caller that owns its conversation owns the recovery too. Silently moving
	// its work onto a service-invented interaction would attribute that work to a
	// turn the caller never started.
	core := newFakeCore(t)
	attempts := 0
	guarded := guardBusinessToolCallWithCompletion(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			attempts++
			return nil, &lifecycleError{
				Code: "interaction_terminal", Message: "interaction is not active",
				RequiredAction: "start_interaction",
			}, nil
		},
		nil, core.client(),
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(map[string]any{"ok": true}, `{"ok":true}`), nil
		},
	)

	result, err := guarded(contextWithSession("session-a"), validBusinessToolRequest())
	if err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("a caller-owned terminal interaction must surface, not be papered over")
	}
	if attempts != 1 {
		t.Fatalf("ensure ran %d times; an explicit context must not be retried", attempts)
	}
	if core.conversationCalls != 0 {
		t.Fatal("an explicit context must never open a fallback conversation")
	}
}

func TestResolvedContextReachesTheDownstreamTraceContext(t *testing.T) {
	// The guard used to write the caller's raw arguments back onto the trace
	// context. Under the fallback those are empty, so the evidence events the
	// business tool emits carried no interaction id — and Core drops such events
	// in bulk with only a warning, silently emptying the very trail this fallback
	// exists to produce.
	core := newFakeCore(t)
	var resolved bknContext
	var downstream common.TraceContext
	guarded := guardBusinessToolCallWithCompletion(
		func(ctx context.Context, intent operationIntent) (*operationResult, *lifecycleError, error) {
			resolved = intent.Context
			return &operationResult{
				Created: true, Execute: true, LifecycleContext: ctx,
				Operation: map[string]any{"operation_id": "op-1", "attempt": float64(1)},
			}, nil, nil
		},
		nil, core.client(),
		func(ctx context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			downstream, _ = common.GetTraceContextFromCtx(ctx)
			return mcpsdk.NewToolResultStructured(map[string]any{"ok": true}, `{"ok":true}`), nil
		},
	)

	if _, err := guarded(contextWithSession("session-a"), contextlessBusinessRequest()); err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
	if resolved.ConversationID == "" || resolved.InteractionID == "" {
		t.Fatalf("fallback did not resolve a usable context: %#v", resolved)
	}
	if downstream.ConversationID != resolved.ConversationID {
		t.Fatalf("downstream conversation = %q, want %q", downstream.ConversationID, resolved.ConversationID)
	}
	if downstream.InteractionID != resolved.InteractionID {
		t.Fatalf("downstream interaction = %q, want %q", downstream.InteractionID, resolved.InteractionID)
	}
}

func TestFallbackWithoutAnMCPSessionKeepsTheOriginalError(t *testing.T) {
	core := newFakeCore(t)
	guarded := guardBusinessToolCallWithCompletion(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			t.Fatal("a sessionless call must not reach the lifecycle client")
			return nil, nil, nil
		},
		nil, core.client(),
		func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultStructured(map[string]any{"ok": true}, `{"ok":true}`), nil
		},
	)

	result, err := guarded(trustedSessionGuardContext(), contextlessBusinessRequest())
	if err != nil {
		t.Fatalf("guard returned protocol error: %v", err)
	}
	envelope := result.StructuredContent.(map[string]any)["error"].(map[string]any)
	if envelope["code"] != "conversation_required" {
		t.Fatalf("without a session the guard must still demand a conversation, got %v", envelope["code"])
	}
}
