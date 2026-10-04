package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// This is a local protocol integration against a strict fake Core. The deployed
// Core + Context Loader three-round end-to-end scenario is tracked by #545.
func TestMCPProtocolLifecycleThreeRoundsAcrossConversationsAndReconnect(t *testing.T) {
	versions := []string{"2025-11-25"}
	for _, version := range mcpsdk.ValidProtocolVersions {
		if version == "2026-07-28" {
			versions = append(versions, version)
		}
	}
	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			testMCPProtocolLifecycleThreeRounds(t, version)
		})
	}
}

func testMCPProtocolLifecycleThreeRounds(t *testing.T, protocolVersion string) {
	t.Helper()
	t.Setenv("CONFIG_PROFILE", "../../infra/config")

	var mu sync.Mutex
	ensureCalls := 0
	finishCalls := 0
	var ensuredScopes [][2]string
	operationScopes := map[string][2]string{}
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, want := range map[string]string{
			"X-BKN-Application-Principal-ID": "client-1",
			"X-BKN-Effective-Subject-Type":   "user",
			"X-BKN-Effective-Subject-ID":     "user-1",
		} {
			if got := r.Header.Get(name); got != want {
				t.Errorf("Core trusted header %s=%q, want %q", name, got, want)
			}
		}
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/interactions/"):
			interactionID := pathTail(r.URL.Path)
			conversationID := "conv-a"
			if interactionID == "int-b" {
				conversationID = "conv-b"
			} else if interactionID != "int-a" {
				t.Errorf("unexpected interaction lookup %q", interactionID)
			}
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: interactionID, ConversationID: conversationID,
				ExecutionStatus: "active", LeaseToken: "lease-" + interactionID, LeaseEpoch: 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			var body struct {
				OperationKey string         `json:"operation_key"`
				ToolName     string         `json:"tool_name"`
				Input        map[string]any `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			inline, _ := body.Input["inline"].(map[string]any)
			if body.OperationKey == "" || body.ToolName != "run_cypher" ||
				body.Input["mode"] != "inline" || inline["query"] != "MATCH (n:Order) WITH n RETURN n" {
				t.Errorf("invalid ensure body: %#v", body)
			}
			parts := strings.Split(r.URL.Path, "/")
			conversationID, interactionID := parts[len(parts)-4], parts[len(parts)-2]
			operationID := "op-" + body.OperationKey
			mu.Lock()
			ensureCalls++
			ensuredScopes = append(ensuredScopes, [2]string{conversationID, interactionID})
			operationScopes[operationID] = [2]string{conversationID, interactionID}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Created: true,
				Execute: true,
				Operation: bkntrace.Operation{
					OperationID: operationID, ConversationID: conversationID,
					InteractionID: interactionID, OperationKey: body.OperationKey,
					ToolName: body.ToolName, Attempt: 1, AttemptStatus: "pending",
				},
				Receipt: bkntrace.Receipt{
					ReceiptID: "receipt-" + body.OperationKey, OperationID: operationID,
					ConversationID: conversationID, InteractionID: interactionID,
					Attempt: 1, ReceiptStatus: "pending",
				},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attempts/1:fail"):
			var body struct {
				ReceiptID string                   `json:"receipt_id"`
				Error     bkntrace.PayloadEnvelope `json:"error"`
				RequestID string                   `json:"request_id"`
				TraceID   string                   `json:"trace_id"`
				Retryable bool                     `json:"retryable"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.ReceiptID == "" || body.Error.Mode != "inline" || len(body.Error.Inline) == 0 || body.RequestID == "" ||
				len(body.TraceID) != 32 || body.Retryable {
				t.Errorf("invalid business IsError finish body: %#v", body)
			}
			var failure struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(body.Error.Inline, &failure) != nil || failure.Message != "local fixture refused query" {
				t.Errorf("finish must retain the deterministic business refusal: %s", body.Error.Inline)
			}
			operationID := pathTail(strings.TrimSuffix(r.URL.Path, "/attempts/1:fail"))
			mu.Lock()
			finishCalls++
			scope, registered := operationScopes[operationID]
			mu.Unlock()
			if !registered {
				t.Errorf("finish used an unregistered operation %q", operationID)
			}
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Operation: bkntrace.Operation{
					OperationID: operationID, ConversationID: scope[0], InteractionID: scope[1],
					Attempt: 1, AttemptStatus: "failed",
				},
				Receipt: bkntrace.Receipt{
					ReceiptID: body.ReceiptID, OperationID: operationID,
					ConversationID: scope[0], InteractionID: scope[1],
					Attempt: 1, ReceiptStatus: "failed",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()

	var requestSequence atomic.Uint64
	var sessionMu sync.Mutex
	transportSessions := map[string]bool{}
	responseSessions := map[string]bool{}
	srv, _ := newMCPServer(bkntrace.NewLifecycleClient(core.URL, core.Client()))
	registered := srv.GetTool(toolKeyRunCypher)
	if registered == nil {
		t.Fatal("assembled MCP server omitted run_cypher")
	}
	// Exercise the real HTTP server and lifecycle middleware while keeping the
	// business refusal deterministic and independent of any external backend.
	var businessCalls atomic.Uint64
	srv.AddTool(registered.Tool, func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		businessCalls.Add(1)
		correlation, ok := common.GetTraceContextFromCtx(ctx)
		scope, _ := req.GetArguments()["bkn_context"].(map[string]any)
		if !ok || correlation.OperationID == "" || correlation.ConversationID != scope["conversation_id"] ||
			correlation.InteractionID != scope["interaction_id"] {
			t.Errorf("business refusal bypassed authoritative lifecycle correlation: %#v", correlation)
		}
		return mcpsdk.NewToolResultError("local fixture refused query"), nil
	})
	handler := newMCPStreamableHTTPHandler(srv, endpointPath)
	mcpHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sessionID := r.Header.Get("Mcp-Session-Id"); sessionID != "" {
			sessionMu.Lock()
			transportSessions[sessionID] = true
			sessionMu.Unlock()
		}
		ctx := trustedMCPIntegrationContext(r.Context(), requestSequence.Add(1))
		handler.ServeHTTP(w, r.WithContext(ctx))
		if sessionID := w.Header().Get("Mcp-Session-Id"); sessionID != "" {
			sessionMu.Lock()
			responseSessions[sessionID] = true
			sessionMu.Unlock()
		}
	}))
	defer mcpHTTP.Close()

	ctx := context.Background()
	first := newInitializedMCPClient(t, ctx, mcpHTTP.URL+endpointPath, protocolVersion)
	assertLifecycleToolDiscovery(t, ctx, first)
	callRefusedQueryRound(t, ctx, first, "conv-a", "int-a", "round-a-1")
	callRefusedQueryRound(t, ctx, first, "conv-b", "int-b", "round-b-1")
	if err := first.Close(); err != nil {
		t.Fatalf("close first MCP transport: %v", err)
	}

	second := newInitializedMCPClient(t, ctx, mcpHTTP.URL+endpointPath, protocolVersion)
	callRefusedQueryRound(t, ctx, second, "conv-a", "int-a", "round-a-2")
	if err := second.Close(); err != nil {
		t.Fatalf("close reconnected MCP transport: %v", err)
	}

	mu.Lock()
	gotEnsure, gotFinish := ensureCalls, finishCalls
	gotScopes := append([][2]string(nil), ensuredScopes...)
	mu.Unlock()
	if gotEnsure != 3 || gotFinish != 3 {
		t.Fatalf("three rounds must each ensure and finish: ensure=%d finish=%d", gotEnsure, gotFinish)
	}
	if got := businessCalls.Load(); got != 3 {
		t.Fatalf("three guarded rounds must execute the refusal fixture: got %d", got)
	}
	wantScopes := [][2]string{{"conv-a", "int-a"}, {"conv-b", "int-b"}, {"conv-a", "int-a"}}
	if !reflect.DeepEqual(gotScopes, wantScopes) {
		t.Fatalf("reconnect must preserve authoritative conversation/interaction scopes: got=%#v want=%#v", gotScopes, wantScopes)
	}
	sessionMu.Lock()
	sessionCount := len(transportSessions)
	responseSessionCount := len(responseSessions)
	sessionMu.Unlock()
	t.Logf("protocol=%s guarded_rounds=%d ensure=%d finish=%d request_sessions=%d response_sessions=%d",
		protocolVersion, businessCalls.Load(), gotEnsure, gotFinish, sessionCount, responseSessionCount)
	if protocolVersion == "2026-07-28" {
		if sessionCount != 0 || responseSessionCount != 0 {
			t.Fatalf("modern protocol must not carry or mint sessions: request=%#v response=%#v", transportSessions, responseSessions)
		}
	} else if sessionCount < 2 || responseSessionCount < 2 {
		t.Fatalf("expected transport reconnect with distinct sessions, got %d: %#v",
			sessionCount, transportSessions)
	}
}

func newInitializedMCPClient(
	t *testing.T,
	ctx context.Context,
	endpoint, protocolVersion string,
) *mcpclient.Client {
	t.Helper()
	client, err := mcpclient.NewStreamableHttpClient(endpoint)
	if err != nil {
		t.Fatalf("create MCP client: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start MCP client: %v", err)
	}
	initialized, err := client.Initialize(ctx, mcpsdk.InitializeRequest{Params: mcpsdk.InitializeParams{
		ProtocolVersion: protocolVersion,
		ClientInfo:      mcpsdk.Implementation{Name: "context-lifecycle-test", Version: "1.0"},
	}})
	if err != nil {
		t.Fatalf("initialize MCP client: %v", err)
	}
	if initialized.ProtocolVersion != protocolVersion {
		t.Fatalf("negotiated protocol=%q, want %q", initialized.ProtocolVersion, protocolVersion)
	}
	return client
}

func assertLifecycleToolDiscovery(t *testing.T, ctx context.Context, client *mcpclient.Client) {
	t.Helper()
	result, err := client.ListTools(ctx, mcpsdk.ListToolsRequest{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	found := map[string]bool{}
	for _, tool := range result.Tools {
		found[tool.Name] = true
	}
	for name := range lifecycleToolNames {
		if !found[name] {
			t.Fatalf("tools/list omitted lifecycle tool %s", name)
		}
	}
}

func callRefusedQueryRound(
	t *testing.T,
	ctx context.Context,
	client *mcpclient.Client,
	conversationID, interactionID, operationKey string,
) {
	t.Helper()
	result, err := client.CallTool(ctx, mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{
		Name: "run_cypher",
		Arguments: map[string]any{
			"kn_id": "kn-001",
			// The local handler returns a deterministic business refusal; this
			// lifecycle test does not depend on the compiler's supported subset.
			"query": "MATCH (n:Order) WITH n RETURN n",
			"bkn_context": map[string]any{
				"conversation_id": conversationID,
				"interaction_id":  interactionID,
			},
		},
	}})
	if err != nil {
		t.Fatalf("tools/call %s: %v", operationKey, err)
	}
	if !result.IsError {
		t.Fatalf("query refusal must remain a business error: %#v", result)
	}
	structured, _ := result.StructuredContent.(map[string]any)
	receipt, ok := structured["bkn_receipt"].(map[string]any)
	if !ok || receipt["receipt_status"] != "failed" {
		t.Fatalf("tools/call %s lost the durable failed receipt: %#v", operationKey, structured)
	}
}

func trustedMCPIntegrationContext(parent context.Context, sequence uint64) context.Context {
	ctx := common.SetTraceContextToCtx(parent, common.TraceContext{
		RequestID: fmt.Sprintf("req_mcp_integration_%04d", sequence)})
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

func pathTail(value string) string {
	value = strings.TrimSuffix(value, "/")
	if index := strings.LastIndexByte(value, '/'); index >= 0 {
		return value[index+1:]
	}
	return value
}
