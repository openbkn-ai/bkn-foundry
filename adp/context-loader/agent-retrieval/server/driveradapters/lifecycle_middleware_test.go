// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// TestRESTCapabilityRoutesDoNotRequireManagedContext pins the split between the
// two surfaces this middleware covers.
//
// A managed Interaction records one agent turn. The /kn/ routes are the capability
// layer - Studio answering a click, a CLI operator, one service asking another -
// and minting a conversation and an interaction for each of those produced
// single-operation records that documented nothing. They pass through now.
//
// A tool call proxied over HTTP is an agent calling a tool by another name, so it
// keeps the requirement even though it arrives on the same transport.
func TestRESTCapabilityRoutesDoNotRequireManagedContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		path      string
		wantCalls int
	}{
		{"/api/agent-retrieval/v1/kn/execute_action", 1},
		{"/api/agent-retrieval/v1/kn/run_sql", 1},
		{"/api/agent-retrieval/internal-v1/kn/search_schema", 1},
		{"/api/agent-retrieval/internal-v1/mcp/proxy/mcp-1/tools/tool-1/call", 0},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			downstreamCalls := 0
			router := gin.New()
			router.Use(middlewareLifecycle(bkntrace.NewLifecycleClient("", nil)))
			router.POST("/*path", func(c *gin.Context) {
				downstreamCalls++
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(`{"query":"q"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if downstreamCalls != tc.wantCalls {
				t.Fatalf("%s: downstream calls = %d, want %d (status %d, body %s)",
					tc.path, downstreamCalls, tc.wantCalls, response.Code, response.Body)
			}
		})
	}
}

// TestRESTContextOptionalityBoundary pins where "ad hoc" ends.
//
// The rule is one line - state an id and the call is managed, state none and it
// is ad hoc - so an empty bkn_context is the same as no bkn_context and passes
// through. A partial one does not: a caller passing one id and not the other is
// wiring the context up and got it wrong, and half-attaching the call would be
// worse than refusing it.
//
// The second case also guards against making the context inert rather than
// optional: a stated session must still be validated and recorded.
func TestRESTContextOptionalityBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name      string
		body      string
		wantCalls int
		wantCode  string
	}{
		{"empty context is ad hoc", `{"sql":"select 1","bkn_context":{}}`, 1, ""},
		{"partial context is refused", `{"sql":"select 1","bkn_context":{"conversation_id":"c1"}}`, 0, "interaction_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			downstreamCalls := 0
			router := gin.New()
			router.Use(middlewareLifecycle(bkntrace.NewLifecycleClient("", nil)))
			router.POST("/*path", func(c *gin.Context) {
				downstreamCalls++
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(
				http.MethodPost,
				"/api/agent-retrieval/v1/kn/run_sql",
				bytes.NewBufferString(tc.body),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if downstreamCalls != tc.wantCalls {
				t.Fatalf("downstream calls = %d, want %d (status %d, body %s)",
					downstreamCalls, tc.wantCalls, response.Code, response.Body)
			}
			if tc.wantCode == "" {
				return
			}
			var envelope struct {
				Error bkntrace.APIError `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("invalid error envelope: %v body=%s", err, response.Body.String())
			}
			if envelope.Error.Code != tc.wantCode {
				t.Fatalf("error code = %q, want %q", envelope.Error.Code, tc.wantCode)
			}
		})
	}
}

func TestLifecycleMiddlewareExemptsCapabilityDiscovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middlewareLifecycle(bkntrace.NewLifecycleClient("", nil)))
	router.GET("/api/agent-retrieval/v1/mcp/info", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/api/agent-retrieval/v1/mcp/info", http.NoBody),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("capability discovery unexpectedly required business context: %d %s", response.Code, response.Body)
	}
}

type countingMCPProxyHandler struct{ calls int }

func (h *countingMCPProxyHandler) CallMCPTool(c *gin.Context) {
	h.calls++
	c.Status(http.StatusNoContent)
}

type countingQueryToolsHandler struct {
	stubKnQueryToolsHandler
	calls int
}

func (h *countingQueryToolsHandler) RunSQL(c *gin.Context) {
	h.calls++
	c.Status(http.StatusNoContent)
}

func TestRegisteredProxyRouteCannotBypassLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	queryTools := &countingQueryToolsHandler{}
	publicEngine := gin.New()
	public := &restPublicHandler{
		Hydra:                          stubPublicHydra{},
		KnLogicPropertyResolverHandler: stubLogicPropertyResolverHandler{},
		KnActionRecallHandler:          stubActionRecallHandler{},
		KnQueryObjectInstanceHandler:   stubQueryObjectInstanceHandler{},
		KnQuerySubgraphHandler:         stubQuerySubgraphHandler{},
		KnSearchHandler:                stubKnSearchHandler{},
		KnFindSkillsHandler:            stubKnFindSkillsHandler{},
		KnQueryToolsHandler:            queryTools,
		KnSkillsHandler:                stubKnSkillsHandler{},
		LifecycleClient:                bkntrace.NewLifecycleClient("", nil),
		Logger:                         logger.DefaultLogger(),
	}
	public.RegisterRouter(publicEngine.Group("/api/agent-retrieval/v1"))
	publicRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/agent-retrieval/v1/kn/run_sql",
		bytes.NewBufferString(`{"sql":"select 1"}`),
	)
	publicRequest.Header.Set("Authorization", "Bearer token")
	publicRequest.Header.Set("Content-Type", "application/json")
	publicResponse := httptest.NewRecorder()
	publicEngine.ServeHTTP(publicResponse, publicRequest)
	// The capability route reaches its handler now; only the proxied tool call
	// below still has to be refused.
	if queryTools.calls != 1 {
		t.Fatalf("registered capability route did not reach its handler: calls=%d status=%d body=%s",
			queryTools.calls, publicResponse.Code, publicResponse.Body)
	}

	proxy := &countingMCPProxyHandler{}
	privateEngine := gin.New()
	private := &restPrivateHandler{
		KnLogicPropertyResolverHandler: stubLogicPropertyResolverHandler{},
		KnActionRecallHandler:          stubActionRecallHandler{},
		KnQueryObjectInstanceHandler:   stubQueryObjectInstanceHandler{},
		KnQuerySubgraphHandler:         stubQuerySubgraphHandler{},
		KnSearchHandler:                stubKnSearchHandler{},
		KnFindSkillsHandler:            stubKnFindSkillsHandler{},
		KnQueryToolsHandler:            &countingQueryToolsHandler{},
		KnSkillsHandler:                stubKnSkillsHandler{},
		MCPProxyHandler:                proxy,
		LifecycleClient:                bkntrace.NewLifecycleClient("", nil),
		Logger:                         logger.DefaultLogger(),
	}
	private.RegisterRouter(privateEngine.Group("/api/agent-retrieval/internal-v1"))
	privateRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/agent-retrieval/internal-v1/mcp/proxy/mcp-1/tools/tool-1/call",
		bytes.NewBufferString(`{"arg":"unsafe"}`),
	)
	privateRequest.Header.Set("Content-Type", "application/json")
	privateResponse := httptest.NewRecorder()
	privateEngine.ServeHTTP(privateResponse, privateRequest)
	if proxy.calls != 0 || privateResponse.Code != http.StatusBadRequest {
		t.Fatalf("mcpproxy bypassed lifecycle: calls=%d status=%d body=%s",
			proxy.calls, privateResponse.Code, privateResponse.Body)
	}
}

func TestLifecycleMiddlewareFinalizesRESTAndReturnsDurableReceipt(t *testing.T) {
	var mu sync.Mutex
	var finishActions []string
	var operationKeys []string
	var finishedBusinessRefs [][]bkntrace.BusinessRef
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agent-observability/v1/interactions/int-1":
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active",
				LeaseToken: "lease-1", LeaseEpoch: 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			var body struct {
				OperationKey string         `json:"operation_key"`
				Protocol     string         `json:"protocol"`
				SourceModule string         `json:"source_module"`
				Input        map[string]any `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !strings.HasPrefix(body.OperationKey, "http:") {
				t.Errorf("REST operation key must be server-derived: %#v", body)
			}
			if body.Protocol != "sdk" || body.SourceModule != "context-loader" {
				t.Errorf("REST ensure lost producer identity: %#v", body)
			}
			inline, _ := body.Input["inline"].(map[string]any)
			if body.Input["mode"] != "inline" || inline["query"] != "value" || inline["kn_id"] != "kn-demo" {
				t.Errorf("REST ensure lost real request input: %#v", body)
			}
			mu.Lock()
			operationKeys = append(operationKeys, body.OperationKey)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Created: true,
				Execute: true,
				Operation: bkntrace.Operation{
					OperationID: "op-rest-1", ConversationID: "conv-1", InteractionID: "int-1",
					Attempt: 1, AttemptStatus: "pending",
				},
				Receipt: bkntrace.Receipt{ReceiptID: "receipt-rest-1", ReceiptStatus: "pending"},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/attempts/1:"):
			var body struct {
				ReceiptID    string                   `json:"receipt_id"`
				Output       bkntrace.PayloadEnvelope `json:"output"`
				Error        bkntrace.PayloadEnvelope `json:"error"`
				RequestID    string                   `json:"request_id"`
				TraceID      string                   `json:"trace_id"`
				BusinessRefs []bkntrace.BusinessRef   `json:"business_refs"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.ReceiptID != "receipt-rest-1" || (body.Output.Mode == "" && body.Error.Mode == "") ||
				body.RequestID != "req_rest_lifecycle_0001" || len(body.TraceID) != 32 {
				t.Errorf("invalid REST finish body: %#v", body)
			}
			action := pathAction(r.URL.Path)
			mu.Lock()
			finishActions = append(finishActions, action)
			finishedBusinessRefs = append(finishedBusinessRefs, body.BusinessRefs)
			mu.Unlock()
			status := "completed"
			if action == "fail" {
				status = "failed"
			}
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Operation: bkntrace.Operation{OperationID: "op-rest-1", Attempt: 1, AttemptStatus: status},
				Receipt:   bkntrace.Receipt{ReceiptID: "receipt-rest-1", ReceiptStatus: status},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()

	for _, test := range []struct {
		name       string
		status     int
		wantAction string
	}{
		{"success", http.StatusOK, "complete"},
		{"handler error", http.StatusBadRequest, "fail"},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(trustedLifecycleHTTPContext())
			router.Use(middlewareLifecycle(bkntrace.NewLifecycleClient(core.URL, core.Client())))
			router.POST("/kn/execute_action", func(c *gin.Context) {
				traceContext, _ := common.GetTraceContextFromCtx(c.Request.Context())
				if traceContext.OperationID != "op-rest-1" ||
					c.GetHeader(common.HeaderBKNOperationID) != "" {
					t.Fatalf("REST operation identity was not confined to trusted context: %#v", traceContext)
				}
				if common.GetHeaderFromCtx(c.Request.Context())[common.HeaderBKNOperationID] != "op-rest-1" {
					t.Fatalf("outbound trusted header omitted operation ID")
				}
				raw, _ := io.ReadAll(c.Request.Body)
				if bytes.Contains(raw, []byte("bkn_context")) {
					t.Fatalf("caller lifecycle context leaked into downstream body: %s", raw)
				}
				c.JSON(test.status, gin.H{"answer": "ok"})
			})
			request := httptest.NewRequest(http.MethodPost, "/kn/execute_action", bytes.NewBufferString(`{
				"query":"value",
				"kn_id":"kn-demo",
				"bkn_context":{"conversation_id":"conv-1","interaction_id":"int-1","business_refs":[{"ref_type":"object_type","ref_id":"object:kn-demo:order"}]}
			}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["answer"] != "ok" {
				t.Fatalf("downstream REST response was not preserved: %v body=%s", err, response.Body)
			}
			if response.Header().Get(common.HeaderBKNReceiptID) != "receipt-rest-1" {
				t.Fatalf("durable receipt header missing: %#v", response.Header())
			}
			mu.Lock()
			gotAction := finishActions[len(finishActions)-1]
			gotOperationKey := operationKeys[len(operationKeys)-1]
			gotBusinessRefs := finishedBusinessRefs[len(finishedBusinessRefs)-1]
			mu.Unlock()
			if gotAction != test.wantAction {
				t.Fatalf("finish action = %q, want %q", gotAction, test.wantAction)
			}
			if gotOperationKey == "" || len(gotBusinessRefs) != 1 || gotBusinessRefs[0].RefID != "object:kn-demo:order" {
				t.Fatalf("derived operation or declared refs not preserved: key=%q refs=%#v", gotOperationKey, gotBusinessRefs)
			}
		})
	}
}

func TestLifecycleMiddlewarePendingReplaySkipsOperatorSideEffect(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/interactions/int-1"):
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active",
				LeaseToken: "lease-1", LeaseEpoch: 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Created: false,
				Operation: bkntrace.Operation{
					OperationID: "op-pending", Attempt: 1, AttemptStatus: "pending",
				},
				Receipt: bkntrace.Receipt{ReceiptID: "receipt-pending", ReceiptStatus: "pending"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()

	downstreamCalls := 0
	router := gin.New()
	router.Use(trustedLifecycleHTTPContext())
	router.Use(middlewareLifecycle(bkntrace.NewLifecycleClient(core.URL, core.Client())))
	router.POST("/kn/run_sql", func(c *gin.Context) {
		downstreamCalls++
		c.JSON(http.StatusOK, gin.H{"unsafe": true})
	})
	request := httptest.NewRequest(http.MethodPost, "/kn/run_sql", bytes.NewBufferString(`{
		"sql":"select side_effect()",
		"bkn_context":{"conversation_id":"conv-1","interaction_id":"int-1"}
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if downstreamCalls != 0 {
		t.Fatalf("pending replay repeated operator side effect: %d", downstreamCalls)
	}
	var body map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	errorValue := body["error"].(map[string]any)
	if response.Code != http.StatusConflict ||
		errorValue["code"] != "receipt_pending" ||
		errorValue["required_action"] != "poll_receipt" {
		t.Fatalf("unexpected pending replay response: status=%d body=%#v", response.Code, body)
	}
}

func TestLifecycleMiddlewarePreservesBusinessResponseWhenTerminalTraceWriteFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/interactions/int-1"):
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active",
				LeaseToken: "lease-1", LeaseEpoch: 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Created: true, Execute: true,
				Operation: bkntrace.Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "pending"},
				Receipt:   bkntrace.Receipt{ReceiptID: "receipt-1", OperationID: "op-1", ReceiptStatus: "pending"},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/attempts/1:"):
			http.Error(w, "trace store unavailable", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()

	router := gin.New()
	router.Use(trustedLifecycleHTTPContext())
	router.Use(middlewareLifecycle(bkntrace.NewLifecycleClient(core.URL, core.Client())))
	router.POST("/kn/run_sql", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"rows": []any{map[string]any{"material_code": "101-000015"}}})
	})
	request := httptest.NewRequest(http.MethodPost, "/kn/run_sql", bytes.NewBufferString(`{
		"resource_id":"resource-1",
		"sql":"SELECT material_code FROM material WHERE material_code = '101-000015'",
		"bkn_context":{"conversation_id":"conv-1","interaction_id":"int-1"}
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("business response is invalid JSON: %v body=%s", err, response.Body.String())
	}
	rows, _ := body["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("business response changed after Trace failure: %#v", body)
	}
}

func TestLifecycleMiddlewareRejectsUnsupportedContextAndInvalidBusinessRefs(t *testing.T) {
	gintest := []struct {
		name string
		body string
		code string
	}{
		{
			name: "caller supplied operation key",
			body: `{"bkn_context":{"conversation_id":"conv-1","interaction_id":"int-1","operation_key":"caller-defined"}}`,
			code: "invalid_business_context",
		},
		{
			name: "cross knowledge network business ref",
			body: `{"kn_id":"kn-demo","bkn_context":{"conversation_id":"conv-1","interaction_id":"int-1","business_refs":[{"ref_type":"object_type","ref_id":"object:other-kn:order"}]}}`,
			code: "invalid_business_ref",
		},
	}
	for _, test := range gintest {
		t.Run(test.name, func(t *testing.T) {
			downstreamCalls := 0
			router := gin.New()
			router.Use(trustedLifecycleHTTPContext())
			router.Use(middlewareLifecycle(inProcessLifecycleClient(t)))
			router.POST("/kn/search_schema", func(c *gin.Context) {
				downstreamCalls++
				c.JSON(http.StatusOK, gin.H{"unsafe": true})
			})
			request := httptest.NewRequest(http.MethodPost, "/kn/search_schema", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			var envelope struct {
				Error bkntrace.APIError `json:"error"`
			}
			_ = json.Unmarshal(response.Body.Bytes(), &envelope)
			if response.Code != http.StatusBadRequest || downstreamCalls != 0 || envelope.Error.Code != test.code {
				t.Fatalf("invalid context reached downstream: status=%d calls=%d error=%#v", response.Code, downstreamCalls, envelope.Error)
			}
		})
	}
}

func TestManagedHTTPOperationKeyUsesInvocationHintForStableRetry(t *testing.T) {
	context := bkntrace.BusinessContext{ConversationID: "conv-1", InteractionID: "int-1"}
	inputHash := "sha256:input"
	build := func(invocationID string) string {
		request := httptest.NewRequest(http.MethodPost, "/kn/search_schema", http.NoBody)
		request.Header.Set(common.HeaderBKNClientInvocationID, invocationID)
		ctx := common.SetTraceContextToCtx(request.Context(), common.TraceContext{RequestID: "req_rest_lifecycle_0001"})
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = request.WithContext(ctx)
		key, apiErr := managedHTTPOperationKey(c, "search_schema", context, inputHash)
		if apiErr != nil {
			t.Fatalf("derive operation key: %#v", apiErr)
		}
		return key
	}
	if first, second := build("call-1"), build("call-1"); first != second {
		t.Fatalf("retry changed operation key: first=%q second=%q", first, second)
	}
	if first, second := build("call-1"), build("call-2"); first == second {
		t.Fatalf("distinct invocation hints reused operation key: %q", first)
	}
}

func TestLifecycleMiddlewareFinalizesPanicsAndLetsRecoveryReturn500(t *testing.T) {
	var finishCalls atomic.Int32
	var finishRetryable bool
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/interactions/int-1"):
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active",
				LeaseToken: "lease-1", LeaseEpoch: 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Created:   true,
				Execute:   true,
				Operation: bkntrace.Operation{OperationID: "op-panic", Attempt: 1, AttemptStatus: "pending"},
				Receipt:   bkntrace.Receipt{ReceiptID: "receipt-panic", ReceiptStatus: "pending"},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attempts/1:fail"):
			finishCalls.Add(1)
			var body struct {
				Retryable bool `json:"retryable"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			finishRetryable = body.Retryable
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Operation: bkntrace.Operation{OperationID: "op-panic", Attempt: 1, AttemptStatus: "failed"},
				Receipt:   bkntrace.Receipt{ReceiptID: "receipt-panic", ReceiptStatus: "failed"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(trustedLifecycleHTTPContext())
	router.Use(middlewareLifecycle(bkntrace.NewLifecycleClient(core.URL, core.Client())))
	router.POST("/kn/execute_action", func(*gin.Context) { panic("downstream panic") })
	request := httptest.NewRequest(http.MethodPost, "/kn/execute_action", bytes.NewBufferString(`{
		"bkn_context":{"conversation_id":"conv-1","interaction_id":"int-1"}
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError || finishCalls.Load() != 1 {
		t.Fatalf("panic must return 500 after one failed receipt: status=%d finish_calls=%d", response.Code, finishCalls.Load())
	}
	if finishRetryable {
		t.Fatal("deterministic panic must not be marked retryable")
	}
}

func TestLifecycleHTTPStatusPreservesProtocolSemantics(t *testing.T) {
	tests := map[string]int{
		"conversation_required":       http.StatusBadRequest,
		"conversation_not_found":      http.StatusNotFound,
		"resource_not_disclosed":      http.StatusNotFound,
		"conversation_owner_mismatch": http.StatusForbidden,
		"permission_denied":           http.StatusForbidden,
		"feature_not_installed":       http.StatusNotImplemented,
		"trace_core_unavailable":      http.StatusServiceUnavailable,
		"evidence_capture_denied":     http.StatusForbidden,
		"evidence_capture_failed":     http.StatusBadGateway,
		"receipt_pending":             http.StatusConflict,
		// A licence gap is hidden, not refused. This code was never covered
		// here, which is how it sat in the same arm as permission_denied.
		"capability_not_licensed": http.StatusNotFound,
	}
	for code, want := range tests {
		if got := lifecycleHTTPStatus(code); got != want {
			t.Errorf("lifecycleHTTPStatus(%q) = %d, want %d", code, got, want)
		}
	}

	// The distinction the two-binary contract rests on: an entitlement gap must
	// not be reported the way an authorization gap is. Asserting the two codes
	// separately above would still pass if someone merged the arms again, so
	// assert the inequality itself.
	if lifecycleHTTPStatus("capability_not_licensed") == lifecycleHTTPStatus("permission_denied") {
		t.Error("缺证书与缺权限返回了同一个状态码——档位边界会被当成授权边界识别出来")
	}
}

func TestLifecycleUnavailableErrorDistinguishesMissingConfigurationFromOutage(t *testing.T) {
	missing := lifecycleUnavailableError(nil)
	if missing.Code != "feature_not_installed" || missing.Retryable {
		t.Fatalf("missing Core configuration returned %#v", missing)
	}

	outage := lifecycleUnavailableError(bkntrace.NewLifecycleClient("http://trace-core", nil))
	if outage.Code != "trace_core_unavailable" || !outage.Retryable || outage.RequiredAction != "retry_later" {
		t.Fatalf("configured Core outage returned %#v", outage)
	}
}

func trustedLifecycleHTTPContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := common.SetTraceContextToCtx(c.Request.Context(), common.TraceContext{
			RequestID: "req_rest_lifecycle_0001", TenantID: "tenant-1", BusinessDomain: "domain-1",
		})
		ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
			AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
			TokenInfo: &interfaces.TokenInfo{ClientID: "client-1"},
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func pathAction(value string) string {
	if strings.HasSuffix(value, ":fail") {
		return "fail"
	}
	return "complete"
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func inProcessLifecycleClient(t testing.TB) *bkntrace.LifecycleClient {
	t.Helper()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var value any
		status := http.StatusOK
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/interactions/int-route"):
			value = bkntrace.Interaction{
				InteractionID: "int-route", ConversationID: "conv-route",
				ExecutionStatus: "active", LeaseToken: "lease-route", LeaseEpoch: 1,
			}
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/operations:ensure"):
			status = http.StatusCreated
			value = bkntrace.OperationResult{
				Created: true,
				Execute: true,
				Operation: bkntrace.Operation{
					OperationID: "op-route", ConversationID: "conv-route",
					InteractionID: "int-route", Attempt: 1, AttemptStatus: "pending",
				},
				Receipt: bkntrace.Receipt{ReceiptID: "receipt-route", ReceiptStatus: "pending"},
			}
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/attempts/1:complete"):
			value = bkntrace.OperationResult{
				Operation: bkntrace.Operation{OperationID: "op-route", Attempt: 1, AttemptStatus: "completed"},
				Receipt:   bkntrace.Receipt{ReceiptID: "receipt-route", ReceiptStatus: "completed"},
			}
		default:
			status = http.StatusNotFound
			value = map[string]any{"error": bkntrace.APIError{Code: "operation_required"}}
		}
		raw, _ := json.Marshal(value)
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(raw)),
			Request:    request,
		}, nil
	})}
	return bkntrace.NewLifecycleClient("http://core.test", client)
}

func setRouteLifecycleHeaders(request *http.Request) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(common.HeaderBKNRequestID, "req_route_lifecycle_0001")
	request.Header.Set(common.HeaderTenantID, "tenant-1")
	request.Header.Set(common.HeaderBusinessDomain, "domain-1")
	request.Header.Set(common.HeaderTraceparent, "00-4b3d59daeff5bfbb23d46c47a5051ec9-00f067aa0ba902b7-01")
}
