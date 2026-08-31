package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	t.Setenv("CONFIG_PROFILE", "../../infra/config")

	var mu sync.Mutex
	ensureCalls := 0
	finishCalls := 0
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
			if body.OperationKey == "" || body.ToolName != "run_sql" ||
				body.Input["mode"] != "inline" || inline["sql"] != "DELETE FROM forbidden" {
				t.Errorf("invalid ensure body: %#v", body)
			}
			parts := strings.Split(r.URL.Path, "/")
			conversationID, interactionID := parts[len(parts)-4], parts[len(parts)-2]
			operationID := "op-" + body.OperationKey
			mu.Lock()
			ensureCalls++
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
			operationID := strings.TrimSuffix(pathTail(r.URL.Path), ":fail")
			operationID = strings.TrimSuffix(strings.TrimSuffix(r.URL.Path, "/attempts/1:fail"), "/")
			operationID = pathTail(operationID)
			mu.Lock()
			finishCalls++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Operation: bkntrace.Operation{
					OperationID: operationID, Attempt: 1, AttemptStatus: "failed",
				},
				Receipt: bkntrace.Receipt{
					ReceiptID: body.ReceiptID, OperationID: operationID,
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
	handler := NewMCPHandlerWithLifecycle(bkntrace.NewLifecycleClient(core.URL, core.Client()))
	mcpHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sessionID := r.Header.Get("Mcp-Session-Id"); sessionID != "" {
			sessionMu.Lock()
			transportSessions[sessionID] = true
			sessionMu.Unlock()
		}
		ctx := trustedMCPIntegrationContext(r.Context(), requestSequence.Add(1))
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer mcpHTTP.Close()

	ctx := context.Background()
	first := newInitializedMCPClient(t, ctx, mcpHTTP.URL+endpointPath)
	assertLifecycleToolDiscovery(t, ctx, first)
	callInvalidSQLRound(t, ctx, first, "conv-a", "int-a", "round-a-1")
	callInvalidSQLRound(t, ctx, first, "conv-b", "int-b", "round-b-1")
	if err := first.Close(); err != nil {
		t.Fatalf("close first MCP transport: %v", err)
	}

	second := newInitializedMCPClient(t, ctx, mcpHTTP.URL+endpointPath)
	callInvalidSQLRound(t, ctx, second, "conv-a", "int-a", "round-a-2")
	if err := second.Close(); err != nil {
		t.Fatalf("close reconnected MCP transport: %v", err)
	}

	mu.Lock()
	gotEnsure, gotFinish := ensureCalls, finishCalls
	mu.Unlock()
	if gotEnsure != 3 || gotFinish != 3 {
		t.Fatalf("three rounds must each ensure and finish: ensure=%d finish=%d", gotEnsure, gotFinish)
	}
	sessionMu.Lock()
	sessionCount := len(transportSessions)
	sessionMu.Unlock()
	if sessionCount < 2 {
		t.Fatalf("expected transport reconnect with distinct sessions, got %d: %#v",
			sessionCount, transportSessions)
	}
}

func newInitializedMCPClient(
	t *testing.T,
	ctx context.Context,
	endpoint string,
) *mcpclient.Client {
	t.Helper()
	client, err := mcpclient.NewStreamableHttpClient(endpoint)
	if err != nil {
		t.Fatalf("create MCP client: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start MCP client: %v", err)
	}
	_, err = client.Initialize(ctx, mcpsdk.InitializeRequest{Params: mcpsdk.InitializeParams{
		ProtocolVersion: mcpsdk.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcpsdk.Implementation{Name: "context-lifecycle-test", Version: "1.0"},
	}})
	if err != nil {
		t.Fatalf("initialize MCP client: %v", err)
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

func callInvalidSQLRound(
	t *testing.T,
	ctx context.Context,
	client *mcpclient.Client,
	conversationID, interactionID, operationKey string,
) {
	t.Helper()
	result, err := client.CallTool(ctx, mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{
		Name: "run_sql",
		Arguments: map[string]any{
			"sql": "DELETE FROM forbidden",
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
		t.Fatalf("read-only validation must remain a business error: %#v", result)
	}
	structured, _ := result.StructuredContent.(map[string]any)
	if _, ok := structured["bkn_receipt"]; !ok {
		t.Fatalf("tools/call %s omitted durable receipt: %#v", operationKey, structured)
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
