// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestGuardBeginUsesCoreCreatedAtForOperationEvidence(t *testing.T) {
	createdAt := time.Date(2026, 8, 3, 6, 31, 0, 456000000, time.UTC)
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/agent-observability/v1/interactions/int-1":
			return lifecycleJSONResponse(http.StatusOK, Interaction{
				InteractionID: "int-1", ConversationID: "conv-1",
				ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 1,
			}), nil
		case "/api/agent-observability/v1/conversations/conv-1/interactions/int-1/operations:ensure":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["protocol"] != "mcp" || body["source_module"] != "context-loader" {
				t.Errorf("guard ensure lost producer identity: %#v", body)
			}
			return lifecycleJSONResponse(http.StatusCreated, OperationResult{
				Created: true, Execute: true,
				Operation: Operation{
					OperationID: "op-1", ConversationID: "conv-1", InteractionID: "int-1",
					Attempt: 1, AttemptStatus: "pending", CreatedAt: createdAt,
				},
				Receipt: Receipt{ReceiptID: "receipt-1", ReceiptStatus: "pending"},
			}), nil
		default:
			return lifecycleJSONResponse(http.StatusNotFound, nil), nil
		}
	})
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		RequestID: "req_cursor_operation_0001", TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})

	lifecycleContext, _, _, apiErr, err := NewGuard(client).Begin(ctx, GuardIntent{
		Context: BusinessContext{
			ConversationID: "conv-1", InteractionID: "int-1", OperationKey: "schema-1",
		},
		ToolName: "search_schema", Protocol: "mcp", SourceModule: "context-loader",
		Input: json.RawMessage(`{"query":"material"}`),
	})
	if err != nil || apiErr != nil {
		t.Fatalf("begin operation failed: api=%#v err=%v", apiErr, err)
	}
	traceContext, _ := common.GetTraceContextFromCtx(lifecycleContext)
	if !traceContext.ObservedAtProvided || traceContext.ObservedAt != createdAt.Format(time.RFC3339Nano) {
		t.Fatalf("operation observed time=%q provided=%t, want Core created_at %s",
			traceContext.ObservedAt, traceContext.ObservedAtProvided, createdAt.Format(time.RFC3339Nano))
	}
}

func TestLifecycleClientEnsureOperationUsesTrustedContext(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		for name, expected := range map[string]string{
			"x-account-id":                   "user-1",
			"x-account-type":                 "user",
			"x-tenant-id":                    "tenant-1",
			"x-business-domain":              "domain-1",
			"X-BKN-Tenant-ID":                "tenant-1",
			"X-Business-Domain-ID":           "domain-1",
			"X-BKN-Application-Principal-ID": "client-1",
			"X-BKN-Effective-Subject-Type":   "user",
			"X-BKN-Effective-Subject-ID":     "user-1",
			"X-BKN-Effective-Subject-Name":   "供应链管理员",
			"X-BKN-Auth-Method":              "api_key",
		} {
			if actual := r.Header.Get(name); actual != expected {
				t.Errorf("%s = %q, want %q", name, actual, expected)
			}
		}
		if got := r.Header.Get("X-BKN-Trace-Query-Token"); got != "" {
			t.Errorf("internal lifecycle request must not carry gateway token, got %q", got)
		}
		switch r.URL.Path {
		case "/api/agent-observability/v1/interactions/int-1":
			_ = json.NewEncoder(w).Encode(Interaction{
				InteractionID: "int-1", ConversationID: "conv-1",
				ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 7,
			})
		case "/api/agent-observability/v1/conversations/conv-1/interactions/int-1/operations:ensure":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["protocol"] != "mcp" || body["source_module"] != "context-loader" {
				t.Errorf("ensure request lost producer identity: %#v", body)
			}
			if body["lease_token"] != "lease-1" || body["lease_epoch"] != float64(7) {
				t.Errorf("ensure request did not reuse authoritative lease: %#v", body)
			}
			if _, forged := body["tenant_id"]; forged {
				t.Errorf("owner identity must not be sent in JSON: %#v", body)
			}
			input, _ := body["input"].(map[string]any)
			inputByteLength, _ := input["byte_length"].(float64)
			if input["mode"] != "inline" || input["media_type"] != "application/json" ||
				inputByteLength <= 0 ||
				!reflect.DeepEqual(input["inline"], map[string]any{
					"query": "物料", "knowledge_network_id": "supply-chain",
				}) {
				t.Errorf("ensure request did not send a bounded real-input envelope: %#v", body)
			}
			if _, legacy := body["normalized_input_hash"]; legacy {
				t.Errorf("ensure request still exposes normalized_input_hash: %#v", body)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(OperationResult{
				Created:   true,
				Execute:   true,
				Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "pending"},
				Receipt:   Receipt{ReceiptID: "receipt-1", ReceiptStatus: "pending"},
			})
		case "/api/agent-observability/v1/operations/op-1/attempts/1:fail":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["evidence_durability"] != "durable" {
				t.Errorf("failed attempt must preserve durable lifecycle evidence: %#v", body)
			}
			errorPayload, _ := body["error"].(map[string]any)
			errorByteLength, _ := errorPayload["byte_length"].(float64)
			if errorPayload["mode"] != "inline" || errorPayload["media_type"] != "application/json" ||
				errorByteLength <= 0 ||
				!reflect.DeepEqual(errorPayload["inline"], map[string]any{
					"code": "ontology_query_failed", "message": "upstream timeout", "stage": "downstream",
				}) {
				t.Errorf("failed attempt did not send a bounded real-error envelope: %#v", body)
			}
			if _, legacy := body["payload_hash"]; legacy {
				t.Errorf("failed attempt still exposes payload_hash: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(OperationResult{
				Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "failed"},
				Receipt:   Receipt{ReceiptID: "receipt-1", ReceiptStatus: "failed"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser, AuthMethod: "api_key",
		TokenInfo: &interfaces.TokenInfo{ClientID: "client-1", VisitorName: "供应链管理员"},
	})
	client := NewLifecycleClient(server.URL, server.Client())
	result, apiErr, err := client.EnsureOperation(ctx, EnsureOperationInput{
		ConversationID: "conv-1", InteractionID: "int-1", OperationKey: "logical-1",
		ToolName: "search_schema", Protocol: "mcp", SourceModule: "context-loader",
		Input: json.RawMessage(`{"query":"物料","knowledge_network_id":"supply-chain"}`),
	})
	if err != nil || apiErr != nil {
		t.Fatalf("ensure operation failed: api=%#v err=%v", apiErr, err)
	}
	if requests != 2 || result.Receipt.ReceiptID != "receipt-1" {
		t.Fatalf("unexpected result: requests=%d result=%#v", requests, result)
	}
	failed, apiErr, err := client.FailAttempt(ctx, FinishAttemptInput{
		OperationID: "op-1", Attempt: 1, ReceiptID: "receipt-1",
		Error: json.RawMessage(`{"code":"ontology_query_failed","message":"upstream timeout","stage":"downstream"}`),
	})
	if err != nil || apiErr != nil || requests != 3 || failed.Receipt.ReceiptStatus != "failed" {
		t.Fatalf("fail attempt did not persist receipt: requests=%d api=%#v err=%v result=%#v", requests, apiErr, err, failed)
	}
}

func TestLifecycleClientUsesBoundedPooledHTTPClientByDefault(t *testing.T) {
	client := NewLifecycleClient("http://core.test", nil)
	if client.client.Timeout != lifecycleClientTimeout {
		t.Fatalf("lifecycle client timeout=%s, want %s", client.client.Timeout, lifecycleClientTimeout)
	}
	transport, ok := client.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("lifecycle client transport=%T, want *http.Transport", client.client.Transport)
	}
	if transport.MaxIdleConns != lifecycleMaxIdleConnections ||
		transport.MaxIdleConnsPerHost != lifecycleMaxIdleConnections ||
		transport.IdleConnTimeout != lifecycleIdleConnectionTTL {
		t.Fatalf("lifecycle connection pool is not bounded for service concurrency: %#v", transport)
	}
}

func TestLifecycleClientDoesNotClaimDurableEvidenceWithoutAck(t *testing.T) {
	var durability string
	client := NewLifecycleClient("http://core.test", &http.Client{
		Transport: lifecycleRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			durability, _ = body["evidence_durability"].(string)
			return lifecycleJSONResponse(http.StatusOK, OperationResult{
				Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"},
				Receipt: Receipt{
					ReceiptID: "receipt-1", ReceiptStatus: "completed",
					EvidenceDurability: durability,
				},
			}), nil
		}),
	})

	_, apiErr, err := client.CompleteAttempt(
		trustedLifecycleTestContext(),
		FinishAttemptInput{
			OperationID: "op-1", Attempt: 1, ReceiptID: "receipt-1",
			Output: json.RawMessage(`{"rows":[]}`), RequestID: "req_01JZVALIDREQUESTID000000011",
			TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		},
	)
	if err != nil || apiErr != nil {
		t.Fatalf("complete attempt failed: api=%#v err=%v", apiErr, err)
	}
	if durability != "pending" {
		t.Fatalf("evidence durability = %q, want pending without durable ACK", durability)
	}
}

func TestLifecycleClientStartsExplicitRetryWithAuthoritativeLease(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/agent-observability/v1/operations/op-1":
			_ = json.NewEncoder(w).Encode(Operation{
				OperationID: "op-1", InteractionID: "int-1",
				Attempt: 1, AttemptStatus: "pending", Retryable: false,
			})
		case "/api/agent-observability/v1/interactions/int-1":
			_ = json.NewEncoder(w).Encode(Interaction{
				InteractionID: "int-1", ExecutionStatus: "active",
				LeaseToken: "lease-current", LeaseEpoch: 9,
			})
		case "/api/agent-observability/v1/operations/op-1/attempts":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["lease_token"] != "lease-current" || body["lease_epoch"] != float64(9) {
				t.Errorf("retry did not use authoritative lease: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(OperationResult{
				Operation: Operation{OperationID: "op-1", Attempt: 2, AttemptStatus: "ready"},
				Receipt:   Receipt{ReceiptID: "receipt-2", Attempt: 2, ReceiptStatus: "pending"},
				Created:   false,
				Execute:   false,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := trustedLifecycleTestContext()
	result, apiErr, err := NewLifecycleClient(server.URL, server.Client()).StartOperationAttempt(ctx, "op-1")
	if err != nil || apiErr != nil {
		t.Fatalf("start retry failed: api=%#v err=%v", apiErr, err)
	}
	if result.Operation.Attempt != 2 || result.Receipt.ReceiptID != "receipt-2" ||
		result.Created || result.Execute {
		t.Fatalf("unexpected retry result: %#v", result)
	}
	want := []string{
		"GET /api/agent-observability/v1/operations/op-1",
		"GET /api/agent-observability/v1/interactions/int-1",
		"POST /api/agent-observability/v1/operations/op-1/attempts",
	}
	if len(paths) != len(want) {
		t.Fatalf("unexpected retry calls: %#v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("retry call %d = %q, want %q", i, paths[i], want[i])
		}
	}
}

func trustedLifecycleTestContext() context.Context {
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		TenantID: "tenant-1", BusinessDomain: "domain-1",
	})
	return common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{ClientID: "client-1"},
	})
}

func TestEnsureFinishCorrelationDerivesStableSyntheticTraceFromRequest(t *testing.T) {
	contextFor := func(requestID string) context.Context {
		return common.SetTraceContextToCtx(context.Background(), common.TraceContext{
			RequestID: requestID,
		})
	}
	first, err := EnsureTraceCorrelation(contextFor("req_01JZVALIDREQUESTID000000021"))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := EnsureTraceCorrelation(contextFor("req_01JZVALIDREQUESTID000000021"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := EnsureTraceCorrelation(contextFor("req_01JZVALIDREQUESTID000000022"))
	if err != nil {
		t.Fatal(err)
	}
	firstSpan := trace.SpanContextFromContext(first)
	replaySpan := trace.SpanContextFromContext(replay)
	otherSpan := trace.SpanContextFromContext(other)
	if firstSpan.TraceID() != replaySpan.TraceID() || firstSpan.SpanID() != replaySpan.SpanID() {
		t.Fatalf("same request produced different synthetic correlation: %s/%s vs %s/%s",
			firstSpan.TraceID(), firstSpan.SpanID(), replaySpan.TraceID(), replaySpan.SpanID())
	}
	if firstSpan.TraceID() == otherSpan.TraceID() {
		t.Fatalf("different requests produced the same synthetic trace ID: %s", firstSpan.TraceID())
	}
}

func TestGuardFinishCreatesCorrelationWhenCallerHasNoSpan(t *testing.T) {
	var requestBody map[string]any
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost ||
			!strings.HasSuffix(request.URL.Path, "/attempts/1:complete") {
			return lifecycleJSONResponse(http.StatusNotFound, nil), nil
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode finish request: %v", err)
		}
		return lifecycleJSONResponse(http.StatusOK, OperationResult{
			Operation: Operation{
				OperationID: "op-1", Attempt: 1, AttemptStatus: "completed",
			},
			Receipt: Receipt{
				ReceiptID: "receipt-1", OperationID: "op-1",
				Attempt: 1, ReceiptStatus: "completed",
			},
		}), nil
	})
	ctx := trustedLifecycleTestContext()
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	traceContext.RequestID = "req_finish_without_span_0001"
	ctx = common.SetTraceContextToCtx(ctx, traceContext)
	ctx = withEvidenceOutcome(ctx)
	outcome := evidenceOutcomeFromContext(ctx)
	outcome.durable = true
	outcome.eventIDs = []string{"evt_durable"}
	outcome.businessRefs = []BusinessRef{{
		RefType: "data_resource", RefID: "resource:forecast_resource",
		BusinessDomainID: "domain-demo", Version: "unversioned",
	}}

	_, apiErr, err := NewGuard(client).Finish(
		ctx, pendingGuardState(), "sha256:result", false, false,
	)
	if err != nil || apiErr != nil {
		t.Fatalf("finish without caller span failed: api=%#v err=%v", apiErr, err)
	}
	if requestBody["request_id"] != "req_finish_without_span_0001" {
		t.Fatalf("finish request lost request correlation: %#v", requestBody)
	}
	traceID, _ := requestBody["trace_id"].(string)
	if len(traceID) != 32 {
		t.Fatalf("finish request did not derive a valid trace ID: %#v", requestBody)
	}
	if requestBody["evidence_durability"] != "durable" {
		t.Fatalf("finish request lost durable evidence ACK: %#v", requestBody)
	}
	refs, _ := requestBody["observed_evidence_refs"].([]any)
	if len(refs) != 1 || refs[0] != "evt_durable" {
		t.Fatalf("finish request lost evidence refs: %#v", requestBody)
	}
	businessRefs, _ := requestBody["business_refs"].([]any)
	if len(businessRefs) != 1 {
		t.Fatalf("finish request lost business refs: %#v", requestBody)
	}
}

func TestGuardFinishTerminalizesReceiptWhenToolEmitsNoEvidenceEvent(t *testing.T) {
	t.Setenv(envEvidenceIngestURL, "http://trace.local/ingest")
	var requestBody map[string]any
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode finish request: %v", err)
		}
		return lifecycleJSONResponse(http.StatusOK, OperationResult{
			Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"},
			Receipt:   Receipt{ReceiptID: "receipt-1", OperationID: "op-1", Attempt: 1, ReceiptStatus: "completed"},
		}), nil
	})
	ctx := trustedLifecycleTestContext()
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	traceContext.RequestID = "req_no_evidence_event_0001"
	ctx, _ = EnsureTraceCorrelation(common.SetTraceContextToCtx(ctx, traceContext))
	ctx = withEvidenceOutcome(ctx)
	if _, apiErr, err := NewGuard(client).Finish(ctx, pendingGuardState(), "sha256:result", false, false); err != nil || apiErr != nil {
		t.Fatalf("finish no-evidence operation: api=%#v err=%v", apiErr, err)
	}
	if requestBody["evidence_durability"] != "durable" {
		t.Fatalf("lifecycle receipt itself must terminalize no-evidence operations: %#v", requestBody)
	}
}

func TestGuardFinishKeepsEvidencePendingWhenIngestIsDisabled(t *testing.T) {
	t.Setenv(envEvidenceIngestURL, "")
	var requestBody map[string]any
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode finish request: %v", err)
		}
		return lifecycleJSONResponse(http.StatusOK, OperationResult{
			Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"},
			Receipt:   Receipt{ReceiptID: "receipt-1", OperationID: "op-1", Attempt: 1, ReceiptStatus: "completed"},
		}), nil
	})
	ctx := trustedLifecycleTestContext()
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	traceContext.RequestID = "req_evidence_disabled_0001"
	ctx, _ = EnsureTraceCorrelation(common.SetTraceContextToCtx(ctx, traceContext))
	ctx = withEvidenceOutcome(ctx)

	if _, apiErr, err := NewGuard(client).Finish(ctx, pendingGuardState(), "sha256:result", false, false); err != nil || apiErr != nil {
		t.Fatalf("finish with evidence disabled: api=%#v err=%v", apiErr, err)
	}
	if requestBody["evidence_durability"] != "pending" {
		t.Fatalf("disabled evidence ingestion must remain pending: %#v", requestBody)
	}
}

func TestGuardFinishMarksAttemptedEvidenceDeliveryFailurePartial(t *testing.T) {
	var requestBody map[string]any
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode finish request: %v", err)
		}
		return lifecycleJSONResponse(http.StatusOK, OperationResult{
			Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"},
			Receipt:   Receipt{ReceiptID: "receipt-1", OperationID: "op-1", Attempt: 1, ReceiptStatus: "completed"},
		}), nil
	})
	ctx := trustedLifecycleTestContext()
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	traceContext.RequestID = "req_evidence_failed_0001"
	ctx, _ = EnsureTraceCorrelation(common.SetTraceContextToCtx(ctx, traceContext))
	ctx = withEvidenceOutcome(ctx)
	recordEvidenceAttempt(ctx)
	if _, apiErr, err := NewGuard(client).Finish(ctx, pendingGuardState(), "sha256:result", false, false); err != nil || apiErr != nil {
		t.Fatalf("finish failed-evidence operation: api=%#v err=%v", apiErr, err)
	}
	if requestBody["evidence_durability"] != "failed" {
		t.Fatalf("attempted evidence failure must become explicit partial evidence: %#v", requestBody)
	}
}

func TestLifecycleValueTypesPreserveCore30RequiredFields(t *testing.T) {
	fixtures := []struct {
		name   string
		target any
		fields []string
	}{
		{"conversation", &Conversation{}, []string{
			"conversation_id", "owner", "external_conversation_key", "generation", "status",
			"one_shot", "row_version", "created_at", "updated_at",
		}},
		{"interaction", &Interaction{}, []string{
			"interaction_id", "conversation_id", "ordinal", "execution_status", "evidence_status",
			"lease_token", "lease_epoch", "lease_version", "lease_expires_at", "row_version",
			"created_at", "updated_at",
		}},
		{"operation", &Operation{}, []string{
			"operation_id", "conversation_id", "interaction_id", "operation_key", "tool_name",
			"attempt", "attempt_status", "retryable", "row_version",
			"created_at", "updated_at",
		}},
		{"receipt", &Receipt{}, []string{
			"receipt_id", "schema_version", "owner", "conversation_id", "interaction_id",
			"operation_id", "attempt", "operation_key", "tool_name",
			"receipt_status", "evidence_durability", "required", "request_id", "trace_id",
			"causation_event_ids", "observed_evidence_refs", "business_refs", "artifact_refs",
			"partial_reasons", "row_version", "issued_at",
		}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			typ := reflect.TypeOf(fixture.target).Elem()
			preserved := map[string]any{}
			for i := 0; i < typ.NumField(); i++ {
				name := typ.Field(i).Tag.Get("json")
				if comma := strings.IndexByte(name, ','); comma >= 0 {
					name = name[:comma]
				}
				preserved[name] = true
			}
			for _, field := range fixture.fields {
				if _, ok := preserved[field]; !ok {
					t.Fatalf("%s type dropped Core field %q", fixture.name, field)
				}
			}
		})
	}
}

func TestMergeBusinessRefsPreservesDeclaredVersionAndAddsObservedResources(t *testing.T) {
	refs := mergeBusinessRefs(
		[]BusinessRef{{RefType: "object_type", RefID: "object:kn_demo:forecast", Version: "schema-v3"}},
		[]BusinessRef{
			{RefType: "object_type", RefID: "object:kn_demo:forecast", Version: "versioned"},
			{RefType: "data_resource", RefID: "resource:forecast", Version: "unversioned"},
		},
	)
	if len(refs) != 2 || refs[0].Version != "schema-v3" || refs[1].RefID != "resource:forecast" {
		t.Fatalf("merged business refs lost declared identity or observed resource: %#v", refs)
	}
}

func TestGuardFinishRecoversWhenCommittedResponseIsLost(t *testing.T) {
	terminal := false
	finishCalls := 0
	receiptCalls := 0
	operationCalls := 0
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/attempts/1:complete"):
			finishCalls++
			terminal = true
			return nil, errors.New("response lost after commit")
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/receipts/receipt-1"):
			receiptCalls++
			status := "pending"
			if terminal {
				status = "completed"
			}
			receipt := matchingFinishReceipt(status, "sha256:result")
			if status == "pending" {
				receipt.RequestID = ""
				receipt.TraceID = ""
			}
			return lifecycleJSONResponse(http.StatusOK, receipt), nil
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/operations/op-1"):
			operationCalls++
			return lifecycleJSONResponse(http.StatusOK, Operation{
				OperationID: "op-1", Attempt: 1, AttemptStatus: "completed",
			}), nil
		default:
			return lifecycleJSONResponse(http.StatusNotFound, nil), nil
		}
	})

	result, apiErr, err := NewGuard(client).Finish(
		finishLifecycleContext(false),
		pendingGuardState(),
		"sha256:result",
		false,
		false,
	)
	if err != nil || apiErr != nil {
		t.Fatalf("lost response was not recovered: api=%#v err=%v", apiErr, err)
	}
	if result.Receipt.ReceiptStatus != "completed" || finishCalls != 1 ||
		receiptCalls != 1 || operationCalls != 1 {
		t.Fatalf("unexpected lost response recovery: result=%#v finish=%d receipt=%d operation=%d",
			result, finishCalls, receiptCalls, operationCalls)
	}
}

func TestGuardFinishEmitsOneOutcomeOnlyAfterDurableFinalization(t *testing.T) {
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		return lifecycleJSONResponse(http.StatusOK, OperationResult{
			Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"},
			Receipt: Receipt{
				ReceiptID: "receipt-1", OperationID: "op-1", Attempt: 1,
				ReceiptStatus: "completed",
			},
		}), nil
	})
	guard := NewGuard(client)
	emissions := 0
	guard.emitOperationOutcome = func(_ context.Context, failed bool) {
		emissions++
		if failed {
			t.Fatal("successful finalization emitted a failed outcome")
		}
	}

	_, apiErr, err := guard.Finish(
		finishLifecycleContext(false), pendingGuardState(), "sha256:result", false, false,
	)
	if err != nil || apiErr != nil {
		t.Fatalf("finish failed: api=%#v err=%v", apiErr, err)
	}
	if emissions != 1 {
		t.Fatalf("outcome emissions=%d, want 1", emissions)
	}
}

func TestGuardFinishRejectsOppositeTerminalReceiptAfterResponseLoss(t *testing.T) {
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost:
			return nil, errors.New("response lost after commit")
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/receipts/"):
			return lifecycleJSONResponse(http.StatusOK,
				matchingFinishReceipt("failed", "sha256:result")), nil
		default:
			return lifecycleJSONResponse(http.StatusNotFound, nil), nil
		}
	})

	_, apiErr, err := NewGuard(client).Finish(
		finishLifecycleContext(false), pendingGuardState(), "sha256:result", false, false,
	)
	if err != nil || apiErr == nil || apiErr.Code != "idempotency_conflict" ||
		apiErr.RequiredAction != "use_new_idempotency_key" {
		t.Fatalf("opposite terminal receipt was accepted: api=%#v err=%v", apiErr, err)
	}
}

func TestGuardFinishRecoversTerminalReceiptWithoutClientPayloadHash(t *testing.T) {
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost:
			return nil, errors.New("response lost after commit")
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/receipts/"):
			return lifecycleJSONResponse(http.StatusOK,
				matchingFinishReceipt("completed", "")), nil
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/operations/"):
			return lifecycleJSONResponse(http.StatusOK, Operation{
				OperationID: "op-1", Attempt: 1, AttemptStatus: "completed",
			}), nil
		default:
			return lifecycleJSONResponse(http.StatusNotFound, nil), nil
		}
	})

	result, apiErr, err := NewGuard(client).Finish(
		finishLifecycleContext(false), pendingGuardState(), "sha256:result", false, false,
	)
	if err != nil || apiErr != nil || result.Receipt.ReceiptStatus != "completed" {
		t.Fatalf("terminal receipt was not recovered without client hash: result=%#v api=%#v err=%v", result, apiErr, err)
	}
}

func TestGuardFinishRecoversAuthoritativeRetryableFailureAfterResponseLoss(t *testing.T) {
	operationCalls := 0
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost:
			return nil, errors.New("response lost after commit")
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/receipts/"):
			return lifecycleJSONResponse(http.StatusOK,
				matchingFinishReceipt("failed", "sha256:failure")), nil
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/operations/"):
			operationCalls++
			return lifecycleJSONResponse(http.StatusOK, Operation{
				OperationID: "op-1", Attempt: 1, AttemptStatus: "failed", Retryable: true,
			}), nil
		default:
			return lifecycleJSONResponse(http.StatusNotFound, nil), nil
		}
	})

	result, apiErr, err := NewGuard(client).Finish(
		finishLifecycleContext(false), pendingGuardState(), "sha256:failure", true, true,
	)
	if err != nil || apiErr != nil || operationCalls != 1 ||
		result.Operation.AttemptStatus != "failed" || !result.Operation.Retryable {
		t.Fatalf("authoritative failed operation was not recovered: result=%#v api=%#v err=%v",
			result, apiErr, err)
	}
}

func TestGuardFinishRetriesTransientFailureAfterPendingConfirmation(t *testing.T) {
	finishCalls := 0
	receiptCalls := 0
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/attempts/1:complete"):
			finishCalls++
			if finishCalls == 1 {
				return nil, errors.New("temporary connection reset")
			}
			return lifecycleJSONResponse(http.StatusOK, OperationResult{
				Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"},
				Receipt:   Receipt{ReceiptID: "receipt-1", ReceiptStatus: "completed"},
			}), nil
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/receipts/receipt-1"):
			receiptCalls++
			return lifecycleJSONResponse(http.StatusOK, Receipt{
				ReceiptID: "receipt-1", ReceiptStatus: "pending",
			}), nil
		default:
			return lifecycleJSONResponse(http.StatusNotFound, nil), nil
		}
	})

	result, apiErr, err := NewGuard(client).Finish(
		finishLifecycleContext(false),
		pendingGuardState(),
		"sha256:result",
		false,
		false,
	)
	if err != nil || apiErr != nil || result.Receipt.ReceiptStatus != "completed" {
		t.Fatalf("transient finish was not retried: result=%#v api=%#v err=%v", result, apiErr, err)
	}
	if finishCalls != 2 || receiptCalls != 1 {
		t.Fatalf("unexpected transient retry calls: finish=%d receipt=%d", finishCalls, receiptCalls)
	}
}

func TestGuardFinishContinuesAfterRequestContextCancellation(t *testing.T) {
	finishCalls := 0
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		if err := request.Context().Err(); err != nil {
			t.Fatalf("finish inherited caller cancellation: %v", err)
		}
		finishCalls++
		return lifecycleJSONResponse(http.StatusOK, OperationResult{
			Operation: Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "completed"},
			Receipt:   Receipt{ReceiptID: "receipt-1", ReceiptStatus: "completed"},
		}), nil
	})

	result, apiErr, err := NewGuard(client).Finish(
		finishLifecycleContext(true),
		pendingGuardState(),
		"sha256:result",
		false,
		false,
	)
	if err != nil || apiErr != nil || finishCalls != 1 || result.Receipt.ReceiptStatus != "completed" {
		t.Fatalf("canceled caller prevented durable finish: result=%#v api=%#v err=%v calls=%d",
			result, apiErr, err, finishCalls)
	}
}

func TestGuardFinishExhaustionReturnsStableReceiptPending(t *testing.T) {
	finishCalls := 0
	client := lifecycleClientWithTransport(func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodPost:
			finishCalls++
			return nil, errors.New("Core unavailable")
		case http.MethodGet:
			return lifecycleJSONResponse(http.StatusOK, Receipt{
				ReceiptID: "receipt-1", ReceiptStatus: "pending",
			}), nil
		default:
			return lifecycleJSONResponse(http.StatusNotFound, nil), nil
		}
	})

	result, apiErr, err := NewGuard(client).Finish(
		finishLifecycleContext(false),
		pendingGuardState(),
		"sha256:result",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("retry exhaustion escaped as internal error: %v", err)
	}
	if apiErr == nil || apiErr.Code != "receipt_pending" ||
		apiErr.RequiredAction != "poll_receipt" || !apiErr.Retryable {
		t.Fatalf("retry exhaustion returned wrong error: %#v", apiErr)
	}
	if result.Receipt.ReceiptID != "receipt-1" || finishCalls < 2 {
		t.Fatalf("retry exhaustion lost stable receipt or did not retry: result=%#v calls=%d", result, finishCalls)
	}
}

type lifecycleRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn lifecycleRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func lifecycleClientWithTransport(fn lifecycleRoundTripFunc) *LifecycleClient {
	return NewLifecycleClient("http://core.test", &http.Client{Transport: fn})
}

func lifecycleJSONResponse(status int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(raw))),
	}
}

func pendingGuardState() GuardState {
	return GuardState{Result: OperationResult{
		Created: true,
		Execute: true,
		Operation: Operation{
			OperationID: "op-1", Attempt: 1, AttemptStatus: "pending",
		},
		Receipt: Receipt{ReceiptID: "receipt-1", ReceiptStatus: "pending"},
	}}
}

func finishLifecycleContext(cancel bool) context.Context {
	ctx := trustedLifecycleTestContext()
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	traceContext.RequestID = "req_finish_recovery_0001"
	ctx = common.SetTraceContextToCtx(ctx, traceContext)
	traceID := trace.TraceID{0x4b, 0x3d, 0x59, 0xda, 0xef, 0xf5, 0xbf, 0xbb, 0x23, 0xd4, 0x6c, 0x47, 0xa5, 0x05, 0x1e, 0xc9}
	spanID := trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7}
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))
	if !cancel {
		return ctx
	}
	canceled, cancelFunc := context.WithCancel(ctx)
	cancelFunc()
	return canceled
}

func matchingFinishReceipt(status, _ string) Receipt {
	return Receipt{
		ReceiptID: "receipt-1", OperationID: "op-1", Attempt: 1,
		ReceiptStatus: status,
		RequestID:     "req_finish_recovery_0001",
		TraceID:       "4b3d59daeff5bfbb23d46c47a5051ec9",
	}
}
