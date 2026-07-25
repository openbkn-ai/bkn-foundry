// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"encoding/json"
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
		RequestID:        "req_context_loader_phase2_0001",
		InteractionID:    "int_context_loader_0001",
		OperationID:      "op_context_retrieval_0001",
		CausationEventID: "evt_agent_tool_called_0001",
		Attempt:          2,
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID:   "acct_demo",
		AccountType: interfaces.AccessorType("user"),
	})
	return ctx
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

func TestBuildSearchSchemaEventsAddsRefsOnlyForUpstreamClaim(t *testing.T) {
	maxConcepts := 5
	events := BuildSearchSchemaEvents(testTraceContextWithClaim(), &interfaces.SearchSchemaReq{
		Query: "customer risk", KnID: "kn_demo", MaxConcepts: &maxConcepts,
	}, &interfaces.SearchSchemaResp{ObjectTypes: []any{map[string]any{"concept_id": "customer", "summary": "raw"}}})
	if len(events) != 3 {
		t.Fatalf("len(events)=%d, want fact, evidence refs and business refs", len(events))
	}
	for index, eventType := range []string{"retrieval.completed", "evidence.refs.created", "business.refs.resolved"} {
		if events[index]["event_type"] != eventType {
			t.Fatalf("events[%d].event_type=%v, want %s", index, events[index]["event_type"], eventType)
		}
		if events[index]["claim_id"] != "claim_agent_answer_0001" {
			t.Fatalf("events[%d].claim_id=%v", index, events[index]["claim_id"])
		}
	}
	assertOnlyKeys(t, events[0]["payload"].(map[string]any), "query_hash", "candidate_count", "truncated", "version_status", "source_refs")
	assertOnlyKeys(t, events[1]["payload"].(map[string]any), "claim_id", "evidence_refs")
	assertOnlyKeys(t, events[2]["payload"].(map[string]any), "claim_id", "resolver_status", "business_refs")
	for _, ref := range events[1]["payload"].(map[string]any)["evidence_refs"].([]map[string]any) {
		assertOnlyKeys(t, ref, "ref_id", "ref_type", "source_system", "validity", "version_status", "visibility", "summary_hash")
	}
	raw, _ := json.Marshal(events)
	for _, forbidden := range []string{"claim.created", `"summary":`, "customer risk"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("forbidden producer payload %q: %s", forbidden, raw)
		}
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
	if len(events) != 3 {
		t.Fatalf("len(events)=%d, want 3", len(events))
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"event_type":"retrieval.completed"`) {
		t.Fatalf("missing retrieval.completed event: %s", text)
	}
	if !strings.Contains(text, `"event_type":"evidence.refs.created"`) {
		t.Fatalf("missing evidence.refs.created event: %s", text)
	}
	for _, leaked := range []string{"customer phone and complaint risk", "Customer", "Contains phone fields"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("event leaked raw content %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, `"query_hash":"sha256:`) {
		t.Fatalf("missing query hash: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"object_type:customer"`) {
		t.Fatalf("missing object type ref: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"relation_type:customer_has_complaint"`) {
		t.Fatalf("missing relation type ref: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"action_type:notify_owner"`) {
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

func TestBuildQueryObjectInstanceEventsUsesRowRefsOnly(t *testing.T) {
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
	if len(events) != 3 {
		t.Fatalf("len(events)=%d, want fact, evidence refs and unresolved business refs", len(events))
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"event_type":"retrieval.completed"`) {
		t.Fatalf("missing retrieval.completed event: %s", text)
	}
	if !strings.Contains(text, `"event_type":"evidence.refs.created"`) {
		t.Fatalf("missing evidence.refs.created event: %s", text)
	}
	if !strings.Contains(text, `"resolver_status":"unresolved"`) || !strings.Contains(text, `"business_refs":[]`) {
		t.Fatalf("missing explicit unresolved business refs: %s", text)
	}
	if !strings.Contains(text, `"ref_type":"row_ref"`) {
		t.Fatalf("missing row_ref evidence: %s", text)
	}
	if !strings.Contains(text, `"query_hash":"sha256:`) {
		t.Fatalf("missing safe query hash: %s", text)
	}
	if !strings.Contains(text, `"truncated":true`) {
		t.Fatalf("missing truncation signal: %s", text)
	}
	for _, leaked := range []string{"18800001111", "Alice", "cust_001", "customer_name"} {
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
	if len(events) != 3 {
		t.Fatalf("len(events)=%d, want 3", len(events))
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"event_type":"retrieval.completed"`) {
		t.Fatalf("missing retrieval.completed event: %s", text)
	}
	if !strings.Contains(text, `"event_type":"evidence.refs.created"`) {
		t.Fatalf("missing evidence.refs.created event: %s", text)
	}
	if !strings.Contains(text, `"ref_type":"row_ref"`) {
		t.Fatalf("missing row_ref evidence: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"relation_type:has_order"`) {
		t.Fatalf("missing relation type evidence: %s", text)
	}
	if !strings.Contains(text, `"ref_type":"schema_ref"`) {
		t.Fatalf("missing schema_ref evidence: %s", text)
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
	req := &interfaces.QueryInstanceSubgraphReq{KnID: "kn_demo"}
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
	if got := strings.Count(text, `"ref_type":"row_ref"`); got != 2 {
		t.Fatalf("row_ref count=%d, want 2 unique refs: %s", got, text)
	}
	if got := strings.Count(text, `"ref_id":"relation_type:has_order"`); got != 1 {
		t.Fatalf("relation schema ref count=%d, want 1: %s", got, text)
	}
}

func TestBuildQueryInstanceSubgraphEventsCapsEvidenceRefs(t *testing.T) {
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
	if got := strings.Count(text, `"ref_type":"row_ref"`); got != maxSubgraphEvidenceRefs {
		t.Fatalf("row_ref count=%d, want capped %d: %s", got, maxSubgraphEvidenceRefs, text)
	}
	if !strings.Contains(text, `"truncated":true`) {
		t.Fatalf("missing truncation signal: %s", text)
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

func TestSubmitEventsHandlesServerFailureWithoutBlocking(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Setenv(envEvidenceIngestURL, server.URL)
	t.Setenv(envEvidenceIngestTimeoutMS, "500")

	SubmitEvents(testTraceContext(), nil, nil, []Event{{"event_type": "claim.created"}})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected evidence ingestion request")
}
