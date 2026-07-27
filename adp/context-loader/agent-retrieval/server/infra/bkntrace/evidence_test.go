// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"go.opentelemetry.io/otel/trace"
)

type evidenceRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn evidenceRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func testTraceContext() context.Context {
	traceID := trace.TraceID{0x71, 0x21, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	spanID := trace.SpanID{0x71, 0x21, 0, 0, 0, 0, 0, 1}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	ctx = common.SetTraceContextToCtx(ctx, common.TraceContext{
		RequestID:          "req_context_loader_phase2_0001",
		BusinessDomain:     "domain_demo",
		InteractionID:      "int_context_loader_0001",
		OperationID:        "op_context_retrieval_0001",
		CausationEventID:   "evt_agent_tool_called_0001",
		Attempt:            2,
		ObservedAt:         "2026-07-25T08:00:00Z",
		ObservedAtProvided: true,
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID:   "acct_demo",
		AccountType: interfaces.AccessorType("user"),
	})
	return ctx
}

func TestBuildSearchSchemaEventsRejectsMissingReplayEnvelope(t *testing.T) {
	ctx := testTraceContext()
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	traceContext.ObservedAtProvided = false
	ctx = common.SetTraceContextToCtx(ctx, traceContext)
	maxConcepts := 1
	if events := BuildSearchSchemaEvents(ctx, &interfaces.SearchSchemaReq{Query: "q", MaxConcepts: &maxConcepts}, &interfaces.SearchSchemaResp{}); len(events) != 0 {
		t.Fatalf("missing propagated observed time must not create conflicting replay: %#v", events)
	}
}

func testTraceContextWithClaim() context.Context {
	ctx := testTraceContext()
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	traceContext.ClaimID = "claim_agent_answer_0001"
	return common.SetTraceContextToCtx(ctx, traceContext)
}

func TestBuildSearchSchemaEventsEmitsFactWithoutManufacturingClaim(t *testing.T) {
	maxConcepts := 5
	events := BuildSearchSchemaEvents(testTraceContext(), &interfaces.SearchSchemaReq{
		Query: "customer risk", KnID: "kn_demo", MaxConcepts: &maxConcepts,
	}, &interfaces.SearchSchemaResp{ObjectTypes: []any{map[string]any{"concept_id": "customer"}}})
	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want one fact event without upstream claim", len(events))
	}
	if events[0]["event_type"] != "retrieval.completed" {
		t.Fatalf("event_type=%v, want retrieval.completed", events[0]["event_type"])
	}
	if events[0]["bkn.trace.schema.version"] != "2.1.0" {
		t.Fatalf("schema version=%v, want 2.1.0", events[0]["bkn.trace.schema.version"])
	}
	for key, want := range map[string]any{
		"interaction_id":     "int_context_loader_0001",
		"operation_id":       "op_context_retrieval_0001",
		"causation_event_id": "evt_agent_tool_called_0001",
		"attempt":            2,
	} {
		if events[0][key] != want {
			t.Fatalf("%s=%v, want %v", key, events[0][key], want)
		}
	}
	raw, _ := json.Marshal(events)
	if strings.Contains(string(raw), "claim.created") || strings.Contains(string(raw), "claim_id") {
		t.Fatalf("producer manufactured a claim: %s", raw)
	}
}

func TestBuildSearchSchemaEventsReplayIsStable(t *testing.T) {
	maxConcepts := 5
	req := &interfaces.SearchSchemaReq{Query: "customer risk", KnID: "kn_demo", MaxConcepts: &maxConcepts}
	resp := &interfaces.SearchSchemaResp{ObjectTypes: []any{map[string]any{"concept_id": "customer"}}}
	ctx := testTraceContext()
	first := BuildSearchSchemaEvents(ctx, req, resp)
	second := BuildSearchSchemaEvents(ctx, req, resp)
	if first[0]["event_id"] != second[0]["event_id"] || first[0]["observed_at"] != second[0]["observed_at"] {
		t.Fatalf("replay changed identity or observed time: %#v != %#v", first[0], second[0])
	}
}

func TestBuildRunSQLEventsRecordsBusinessDataSourcesWithoutLeakingSQLOrRows(t *testing.T) {
	events := BuildRunSQLEvents(
		testTraceContext(),
		"SELECT demand_no, quantity FROM {{.forecast_resource}} WHERE month = '2026-06'",
		[]string{"forecast_resource"},
		&interfaces.VegaRawQueryResp{
			Entries: []map[string]any{{
				"demand_no": "DF-SECRET-001",
				"quantity":  11594,
			}},
		},
	)
	if len(events) != 1 || events[0]["event_type"] != "retrieval.completed" {
		t.Fatalf("unexpected run_sql events: %#v", events)
	}
	payload := events[0]["payload"].(map[string]any)
	if payload["candidate_count"] != 1 || payload["query_hash"] == "" {
		t.Fatalf("missing query facts: %#v", payload)
	}
	raw, _ := json.Marshal(events)
	text := string(raw)
	if !strings.Contains(text, `"ref_id":"resource:forecast_resource"`) {
		t.Fatalf("missing resource reference: %s", text)
	}
	for _, leaked := range []string{"SELECT demand_no", "2026-06", "DF-SECRET-001", "11594"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("run_sql evidence leaked %q: %s", leaked, text)
		}
	}
}

func TestBuildSearchSchemaEventsKeepsFactIndependentFromUpstreamClaim(t *testing.T) {
	maxConcepts := 5
	events := BuildSearchSchemaEvents(testTraceContextWithClaim(), &interfaces.SearchSchemaReq{
		Query: "customer risk", KnID: "kn_demo", MaxConcepts: &maxConcepts,
	}, &interfaces.SearchSchemaResp{ObjectTypes: []any{map[string]any{"concept_id": "customer", "summary": "raw"}}})
	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want one fact", len(events))
	}
	assertOnlyKeys(t, events[0]["payload"].(map[string]any), "query_hash", "candidate_count", "truncated", "version_status", "source_refs")
	for _, ref := range events[0]["payload"].(map[string]any)["source_refs"].([]map[string]any) {
		assertOnlyKeys(t, ref, "ref_id", "ref_type", "source_system", "validity", "version_status", "visibility", "summary_hash")
	}
	raw, _ := json.Marshal(events)
	for _, forbidden := range []string{"claim.created", "evidence.refs.created", "business.refs.resolved", "claim_agent_answer_0001", `"summary":`, "customer risk"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("forbidden producer payload %q: %s", forbidden, raw)
		}
	}
}

func TestBuildQueryObjectInstanceEventsKeepsQueriedObjectAsZeroResultEvidence(t *testing.T) {
	events := BuildQueryObjectInstanceEvents(
		testTraceContext(),
		&interfaces.QueryObjectInstancesReq{
			KnID: "supplychain_hd0202",
			OtID: "supplychain_hd0202_forecast",
		},
		&interfaces.QueryObjectInstancesResp{},
	)

	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want one zero-result fact", len(events))
	}
	payload := events[0]["payload"].(map[string]any)
	if payload["candidate_count"] != 0 {
		t.Fatalf("candidate_count=%v, want 0", payload["candidate_count"])
	}
	refs, ok := payload["source_refs"].([]map[string]any)
	if !ok || len(refs) != 1 {
		t.Fatalf("source_refs=%#v, want queried object ref", payload["source_refs"])
	}
	if refs[0]["ref_id"] != "object:supplychain_hd0202:supplychain_hd0202_forecast" {
		t.Fatalf("ref_id=%v, want queried object", refs[0]["ref_id"])
	}
}

func TestBuildSearchSchemaEventsUsesHashAndRefsOnly(t *testing.T) {
	maxConcepts := 5
	includeColumns := true
	req := &interfaces.SearchSchemaReq{
		Query:          "customer phone and complaint risk",
		KnID:           "kn_demo",
		MaxConcepts:    &maxConcepts,
		IncludeColumns: &includeColumns,
	}
	resp := &interfaces.SearchSchemaResp{
		ObjectTypes: []any{
			map[string]any{
				"concept_id":  "customer",
				"name":        "Customer",
				"comment":     "Contains phone fields and must not be emitted",
				"module_type": "object_type",
				"_score":      0.91,
			},
		},
		RelationTypes: []any{
			map[string]any{
				"concept_id":            "customer_has_complaint",
				"source_object_type_id": "customer",
				"target_object_type_id": "complaint",
				"_score":                0.73,
			},
		},
		ActionTypes: []any{
			map[string]any{
				"id":             "notify_owner",
				"object_type_id": "customer",
			},
		},
	}

	events := BuildSearchSchemaEvents(testTraceContextWithClaim(), req, resp)
	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want 1", len(events))
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"event_type":"retrieval.completed"`) {
		t.Fatalf("missing retrieval.completed event: %s", text)
	}
	for _, leaked := range []string{"customer phone and complaint risk", "Customer", "Contains phone fields"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("event leaked raw content %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, `"query_hash":"sha256:`) {
		t.Fatalf("missing query hash: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"object:kn_demo:customer"`) {
		t.Fatalf("missing object type ref: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"relation:kn_demo:customer_has_complaint"`) {
		t.Fatalf("missing relation type ref: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"action_type:kn_demo:notify_owner"`) {
		t.Fatalf("missing action type ref: %s", text)
	}
}

func TestBuildSearchSchemaEventsRequiresTraceContext(t *testing.T) {
	maxConcepts := 5
	events := BuildSearchSchemaEvents(context.Background(), &interfaces.SearchSchemaReq{
		Query:       "schema",
		KnID:        "kn_demo",
		MaxConcepts: &maxConcepts,
	}, &interfaces.SearchSchemaResp{
		ObjectTypes: []any{map[string]any{"concept_id": "customer"}},
	})
	if len(events) != 0 {
		t.Fatalf("len(events)=%d, want 0", len(events))
	}
}

func TestBuildQueryObjectInstanceEventsUsesBusinessObjectAndPropertyRefs(t *testing.T) {
	req := &interfaces.QueryObjectInstancesReq{
		KnID:  "kn_demo",
		OtID:  "customer",
		Limit: 10,
		Filters: []interfaces.FlatFilter{
			{Field: "phone", Op: interfaces.KnOperationTypeEqual, Value: "18800001111"},
		},
		Properties: []string{"customer_name", "phone"},
	}
	resp := &interfaces.QueryObjectInstancesResp{
		Data: []any{
			map[string]any{
				"_instance_identity": map[string]any{"customer_id": "cust_001"},
				"customer_name":      "Alice",
				"phone":              "18800001111",
			},
		},
		SearchAfter: []any{"cursor_001"},
	}

	events := BuildQueryObjectInstanceEvents(testTraceContextWithClaim(), req, resp)
	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want one fact", len(events))
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"event_type":"retrieval.completed"`) {
		t.Fatalf("missing retrieval.completed event: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"object:kn_demo:customer"`) || !strings.Contains(text, `"ref_type":"object"`) {
		t.Fatalf("missing object ref: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"property:kn_demo:customer:customer_name"`) || !strings.Contains(text, `"ref_type":"property"`) {
		t.Fatalf("missing property ref: %s", text)
	}
	if !strings.Contains(text, `"query_hash":"sha256:`) {
		t.Fatalf("missing safe query hash: %s", text)
	}
	if !strings.Contains(text, `"truncated":true`) {
		t.Fatalf("missing truncation signal: %s", text)
	}
	for _, leaked := range []string{"18800001111", "Alice", "cust_001"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("event leaked raw object query content %q: %s", leaked, text)
		}
	}
}

func assertOnlyKeys(t *testing.T, value map[string]any, allowed ...string) {
	t.Helper()
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key := range value {
		if _, ok := allowedSet[key]; !ok {
			t.Fatalf("unregistered key %q in %#v", key, value)
		}
	}
}

func TestQueryObjectConditionHashIncludesSearchAfter(t *testing.T) {
	base := &interfaces.QueryObjectInstancesReq{
		KnID:        "kn_demo",
		OtID:        "customer",
		Limit:       10,
		SearchAfter: []any{"cursor_page_1"},
	}
	next := &interfaces.QueryObjectInstancesReq{
		KnID:        "kn_demo",
		OtID:        "customer",
		Limit:       10,
		SearchAfter: []any{"cursor_page_2"},
	}

	if queryObjectConditionHash(base) == queryObjectConditionHash(next) {
		t.Fatalf("condition hash should differ across search_after pages")
	}
}

func TestQueryObjectTruncatedUsesExplicitNextPageSignals(t *testing.T) {
	req := &interfaces.QueryObjectInstancesReq{
		Limit:  2,
		Offset: 0,
	}
	lastPageResp := &interfaces.QueryObjectInstancesResp{
		Data:       []any{map[string]any{"id": "inst_1"}, map[string]any{"id": "inst_2"}},
		TotalCount: 2,
	}
	if queryObjectTruncated(req, lastPageResp) {
		t.Fatalf("truncated should be false when total_count proves the current page is complete")
	}

	hasNextCursorResp := &interfaces.QueryObjectInstancesResp{
		Data:        []any{map[string]any{"id": "inst_1"}},
		SearchAfter: []any{"cursor_next"},
	}
	if !queryObjectTruncated(req, hasNextCursorResp) {
		t.Fatalf("truncated should be true when search_after indicates a next page")
	}

	hasMoreOffsetResp := &interfaces.QueryObjectInstancesResp{
		Data:       []any{map[string]any{"id": "inst_1"}, map[string]any{"id": "inst_2"}},
		TotalCount: 3,
	}
	if !queryObjectTruncated(req, hasMoreOffsetResp) {
		t.Fatalf("truncated should be true when total_count exceeds returned offset range")
	}
}

func TestBuildQueryInstanceSubgraphEventsUsesHashAndRefsOnly(t *testing.T) {
	req := &interfaces.QueryInstanceSubgraphReq{
		KnID: "kn_demo",
		RelationTypePaths: []any{
			map[string]any{
				"source_ot_id":          "customer",
				"relation_type_id":      "has_order",
				"target_ot_id":          "order",
				"source_instance_id":    "cust_001",
				"target_instance_phone": "18800001111",
				"limit":                 20,
			},
		},
	}
	resp := &interfaces.QueryInstanceSubgraphResp{
		Entries: []any{
			map[string]any{
				"source": map[string]any{
					"_instance_identity": map[string]any{"customer_id": "cust_001"},
					"name":               "Alice",
					"phone":              "18800001111",
					"relation_type":      "vip_customer_segment",
				},
				"relation": map[string]any{
					"relation_type_id": "has_order",
					"amount":           99.5,
				},
				"target": map[string]any{
					"_instance_identity": map[string]any{"order_id": "ord_001"},
					"address":            "Sensitive Address",
				},
			},
		},
	}

	events := BuildQueryInstanceSubgraphEvents(testTraceContextWithClaim(), req, resp)
	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want 1", len(events))
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"event_type":"retrieval.completed"`) {
		t.Fatalf("missing retrieval.completed event: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"relation:kn_demo:has_order"`) {
		t.Fatalf("missing relation type evidence: %s", text)
	}
	if !strings.Contains(text, `"ref_type":"relation"`) {
		t.Fatalf("missing relation evidence: %s", text)
	}
	if !strings.Contains(text, `"query_hash":"sha256:`) {
		t.Fatalf("missing safe path query hash: %s", text)
	}
	for _, leaked := range []string{"cust_001", "ord_001", "Alice", "18800001111", "Sensitive Address", "target_instance_phone"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("event leaked raw subgraph content %q: %s", leaked, text)
		}
	}
	if strings.Contains(text, "vip_customer_segment") {
		t.Fatalf("event treated data field relation_type as schema ref: %s", text)
	}
}

func TestBuildQueryInstanceSubgraphEventsDeduplicatesRefs(t *testing.T) {
	req := &interfaces.QueryInstanceSubgraphReq{KnID: "kn_demo", RelationTypePaths: []any{
		map[string]any{"source_ot_id": "customer", "relation_type_id": "has_order", "target_ot_id": "order"},
		map[string]any{"source_ot_id": "customer", "relation_type_id": "has_order", "target_ot_id": "order"},
	}}
	resp := &interfaces.QueryInstanceSubgraphResp{
		Entries: []any{
			map[string]any{
				"source":   map[string]any{"_instance_identity": map[string]any{"customer_id": "cust_001"}},
				"relation": map[string]any{"relation_type_id": "has_order"},
				"target":   map[string]any{"_instance_identity": map[string]any{"order_id": "ord_001"}},
			},
			map[string]any{
				"source":   map[string]any{"_instance_identity": map[string]any{"customer_id": "cust_001"}},
				"relation": map[string]any{"relation_type_id": "has_order"},
				"target":   map[string]any{"_instance_identity": map[string]any{"order_id": "ord_001"}},
			},
		},
	}

	events := BuildQueryInstanceSubgraphEvents(testTraceContext(), req, resp)
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	if got := strings.Count(text, `"ref_id":"relation:kn_demo:has_order"`); got != 1 {
		t.Fatalf("relation ref count=%d, want 1: %s", got, text)
	}
}

func TestBuildQueryInstanceSubgraphEventsDoesNotDeriveRefsFromRowContent(t *testing.T) {
	entries := make([]any, 0, maxSubgraphEvidenceRefs+5)
	for i := 0; i < maxSubgraphEvidenceRefs+5; i++ {
		entries = append(entries, map[string]any{
			"source": map[string]any{
				"_instance_identity": map[string]any{"customer_id": i},
			},
		})
	}
	resp := &interfaces.QueryInstanceSubgraphResp{Entries: entries}

	events := BuildQueryInstanceSubgraphEvents(testTraceContext(), &interfaces.QueryInstanceSubgraphReq{KnID: "kn_demo"}, resp)
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, `"ref_type":"row_ref"`) || strings.Contains(text, "customer_id") {
		t.Fatalf("row-derived ref leaked: %s", text)
	}
}

func TestEmitSearchSchemaEventsNoopsWhenIngestDisabled(t *testing.T) {
	t.Setenv(envEvidenceIngestURL, "")
	maxConcepts := 5

	EmitSearchSchemaEvents(testTraceContext(), nil, &interfaces.SearchSchemaReq{
		Query:       "schema",
		KnID:        "kn_demo",
		MaxConcepts: &maxConcepts,
	}, &interfaces.SearchSchemaResp{
		ObjectTypes: []any{map[string]any{"concept_id": "customer"}},
	})
}

func TestSubmitEventsNoopsWhenAccountContextMissing(t *testing.T) {
	t.Setenv(envEvidenceIngestURL, "http://127.0.0.1:1/ingest")
	ctx := common.SetTraceContextToCtx(trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{0x71, 0x21, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2},
		SpanID:  trace.SpanID{0x71, 0x21, 0, 0, 0, 0, 0, 2},
	})), common.TraceContext{RequestID: "req_context_loader_phase2_no_account"})

	SubmitEvents(ctx, nil, nil, []Event{{"event_type": "claim.created"}})
}

func TestSubmitEventsPreservesCallerOwnedConversationID(t *testing.T) {
	t.Setenv(envEvidenceIngestURL, "http://trace.local/ingest")
	previous := evidenceHTTPClient
	t.Cleanup(func() { evidenceHTTPClient = previous })
	payloads := make(chan batch, 1)
	evidenceHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		var payload batch
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode evidence batch: %v", err)
		}
		payloads <- payload
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}

	headers := map[string]string{
		common.HeaderBKNRequestID:       "req_context_loader_phase2_0002",
		"bkn-conversation-id":           "agent:thread_supply_chain",
		common.HeaderBKNInteractionID:   "int_context_loader_0002",
		common.HeaderBKNOperationID:     "op_context_retrieval_0002",
		common.HeaderBKNAttempt:         "1",
		common.HeaderBusinessDomain:     "domain_demo",
		common.HeaderBKNEventObservedAt: "2026-07-27T09:00:00Z",
	}
	traceID := trace.TraceID{0x71, 0x22, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	spanID := trace.SpanID{0x71, 0x22, 0, 0, 0, 0, 0, 1}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))
	ctx = common.SetTraceContextToCtx(ctx, common.TraceContextFromHeaders(func(key string) string {
		return headers[key]
	}))
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "acct_demo", AccountType: interfaces.AccessorType("user"),
	})

	SubmitEvents(ctx, nil, nil, []Event{{"event_type": "retrieval.completed"}})
	select {
	case payload := <-payloads:
		if got := payload.Trace["bkn.conversation.id"]; got != "agent:thread_supply_chain" {
			t.Fatalf("bkn.conversation.id=%v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for evidence batch")
	}
}

func TestPostBatchWithRetryTreatsNon2xxAsFailure(t *testing.T) {
	previous := evidenceHTTPClient
	t.Cleanup(func() { evidenceHTTPClient = previous })
	var calls atomic.Int32
	evidenceHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		status := http.StatusServiceUnavailable
		if calls.Add(1) == 3 {
			status = http.StatusNoContent
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if err := postBatchWithRetry("http://trace.local", time.Second, batch{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d, want 3", calls.Load())
	}
}

func TestPostBatchSendsDedicatedIngestToken(t *testing.T) {
	t.Setenv("BKN_TRACE_EVIDENCE_INGEST_TOKEN", "producer-token")
	previous := evidenceHTTPClient
	t.Cleanup(func() { evidenceHTTPClient = previous })
	evidenceHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("X-BKN-Trace-Ingest-Token"); got != "producer-token" {
			t.Fatalf("ingest token header=%q", got)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if err := postBatch("http://trace.local", time.Second, batch{}); err != nil {
		t.Fatal(err)
	}
}

func captureIngestedTrace(t *testing.T, ctx context.Context) map[string]any {
	t.Helper()
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		if _, err := io.ReadFull(r.Body, body); err != nil {
			t.Errorf("read ingest body: %v", err)
		}
		select {
		case bodies <- body:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv(envEvidenceIngestURL, server.URL)
	t.Setenv(envEvidenceIngestTimeoutMS, "500")

	SubmitEvents(ctx, nil, nil, []Event{{"event_type": "claim.created"}})

	select {
	case body := <-bodies:
		var payload struct {
			Trace map[string]any `json:"trace"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode ingest body: %v", err)
		}
		return payload.Trace
	case <-time.After(2 * time.Second):
		t.Fatalf("expected evidence ingestion request")
		return nil
	}
}

func TestSubmitEventsCarriesCorrelationIDs(t *testing.T) {
	ctx := common.SetTraceContextToCtx(testTraceContext(), common.TraceContext{
		RequestID:      "req_context_loader_phase2_0002",
		ConversationID: "agent:thread_abc",
		InteractionID:  "itr_2026072701",
	})

	traceBlock := captureIngestedTrace(t, ctx)
	if got := traceBlock["bkn.conversation.id"]; got != "agent:thread_abc" {
		t.Fatalf("expected conversation id in trace block, got %v", got)
	}
	if got := traceBlock["bkn.interaction.id"]; got != "itr_2026072701" {
		t.Fatalf("expected interaction id in trace block, got %v", got)
	}
}

func TestSubmitEventsOmitsCorrelationIDsWhenAbsent(t *testing.T) {
	traceBlock := captureIngestedTrace(t, testTraceContext())

	if _, ok := traceBlock["bkn.conversation.id"]; ok {
		t.Fatalf("expected no conversation id when the caller sent none")
	}
	if _, ok := traceBlock["bkn.interaction.id"]; ok {
		t.Fatalf("expected no interaction id when the caller sent none")
	}
}
