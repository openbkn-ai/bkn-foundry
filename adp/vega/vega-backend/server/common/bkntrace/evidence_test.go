// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"vega-backend/interfaces"
)

func testTraceContext() context.Context {
	traceID := trace.TraceID{0x73, 0x22, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	spanID := trace.SpanID{0x73, 0x22, 0, 0, 0, 0, 0, 1}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), spanContext)
}

func testRequestContext() RequestContext {
	return RequestContext{
		RequestID:        "req_vega_data_0001",
		AccountID:        "acct_demo",
		AccountType:      "user",
		BusinessDomain:   "domain_demo",
		InteractionID:    "int_vega_query_0001",
		OperationID:      "op_vega_query_0001",
		CausationEventID: "evt_retrieval_completed_0001",
		Attempt:          2,
	}
}

func TestBuildDataQueryEventsEmitsFactWithoutManufacturingClaim(t *testing.T) {
	reqCtx := testRequestContext()
	events := BuildDataQueryEvents(testTraceContext(), reqCtx, DataQuerySubject{
		Operation: "data.resource.query", QueryHash: HashValue("safe"), ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "resource:demo", RefType: RefTypeResource, Summary: map[string]any{"raw": "must-not-appear"}}})
	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want one fact event", len(events))
	}
	if events[0]["event_type"] != "data.query.observed" || events[0]["bkn.trace.schema.version"] != "2.1.0" {
		t.Fatalf("unexpected fact event: %#v", events[0])
	}
	for key, want := range map[string]any{
		"interaction_id": "int_vega_query_0001", "operation_id": "op_vega_query_0001",
		"causation_event_id": "evt_retrieval_completed_0001", "attempt": 2,
	} {
		if events[0][key] != want {
			t.Fatalf("%s=%v, want %v", key, events[0][key], want)
		}
	}
	raw, _ := json.Marshal(events)
	if strings.Contains(string(raw), "claim.created") || strings.Contains(string(raw), `"summary":`) {
		t.Fatalf("unsafe or manufactured event: %s", raw)
	}
}

func TestBuildDataQueryEventsAddsRefsOnlyForUpstreamClaim(t *testing.T) {
	reqCtx := testRequestContext()
	reqCtx.ClaimID = "claim_agent_answer_0001"
	events := BuildDataQueryEvents(testTraceContext(), reqCtx, DataQuerySubject{
		Operation: "data.resource.query", ResourceID: "res_demo", CatalogID: "cat_demo",
		QueryHash: HashValue("safe"), ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "resource:res_demo", RefType: RefTypeResource, Summary: map[string]any{"raw": "must-not-appear"}}})
	if len(events) != 3 {
		t.Fatalf("len(events)=%d, want fact, evidence refs and business refs", len(events))
	}
	for index, eventType := range []string{"data.query.observed", "evidence.refs.created", "business.refs.resolved"} {
		if events[index]["event_type"] != eventType || events[index]["claim_id"] != "claim_agent_answer_0001" {
			t.Fatalf("unexpected event %d: %#v", index, events[index])
		}
	}
	assertOnlyKeys(t, eventPayload(t, events[0]), "query_hash", "query_type", "row_count", "truncated", "version_status", "resource_refs")
	assertOnlyKeys(t, eventPayload(t, events[1]), "claim_id", "evidence_refs")
	assertOnlyKeys(t, eventPayload(t, events[2]), "claim_id", "resolver_status", "business_refs")
	for _, ref := range eventPayload(t, events[1])["evidence_refs"].([]map[string]any) {
		assertOnlyKeys(t, ref, "ref_id", "ref_type", "source_system", "validity", "version_status", "visibility", "summary_hash")
	}
	raw, _ := json.Marshal(events)
	for _, forbidden := range []string{"claim.created", `"summary":`, "must-not-appear"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("forbidden %q: %s", forbidden, raw)
		}
	}
}

func TestBuildDataQueryEventsKeepsZeroResultFact(t *testing.T) {
	events := BuildDataQueryEvents(testTraceContext(), testRequestContext(), DataQuerySubject{
		Operation: "data.resource.query",
		QueryHash: HashValue("zero-result-query"),
	}, nil)
	if len(events) != 1 || events[0]["event_type"] != "data.query.observed" {
		t.Fatalf("zero-result query must remain observable: %#v", events)
	}
	payload := eventPayload(t, events[0])
	if payload["row_count"] != 0 {
		t.Fatalf("row_count=%v, want 0", payload["row_count"])
	}
}

func TestBuildDataQueryEventsMarksClaimResolutionWithoutRefsUnresolved(t *testing.T) {
	reqCtx := testRequestContext()
	reqCtx.ClaimID = "claim_agent_answer_0001"
	events := BuildDataQueryEvents(testTraceContext(), reqCtx, DataQuerySubject{
		Operation: "data.resource.query",
		QueryHash: HashValue("zero-result-query"),
	}, nil)
	if len(events) != 2 {
		t.Fatalf("len(events)=%d, want fact and unresolved business refs", len(events))
	}
	if events[1]["event_type"] != "business.refs.resolved" {
		t.Fatalf("unexpected event: %#v", events[1])
	}
	payload := eventPayload(t, events[1])
	if payload["resolver_status"] != "unresolved" {
		t.Fatalf("resolver_status=%v, want unresolved", payload["resolver_status"])
	}
}

func TestBuildDataQueryEventsMarksRowOnlyBusinessRefsUnresolved(t *testing.T) {
	reqCtx := testRequestContext()
	reqCtx.ClaimID = "claim_agent_answer_0001"
	events := BuildDataQueryEvents(testTraceContext(), reqCtx, DataQuerySubject{
		Operation: "data.resource.query", QueryHash: HashValue("safe"), ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "resource_row:demo:abc", RefType: RefTypeRow, Summary: map[string]any{"row_hash": HashValue("row")}}})
	if len(events) != 3 {
		t.Fatalf("len(events)=%d, want fact, evidence refs and unresolved business refs", len(events))
	}
	payload := eventPayload(t, events[2])
	if payload["resolver_status"] != "unresolved" {
		t.Fatalf("resolver_status=%v, want unresolved", payload["resolver_status"])
	}
	refs, ok := payload["business_refs"].([]map[string]any)
	if !ok || len(refs) != 0 {
		t.Fatalf("business_refs=%#v, want empty registered array", payload["business_refs"])
	}
}

func TestBuildDataQueryEventsUsesResourceAndRowRefsOnly(t *testing.T) {
	resource := &interfaces.Resource{
		ID:               "res_customer_table",
		CatalogID:        "cat_prod",
		Name:             "customer_sensitive_table",
		Category:         interfaces.ResourceCategoryTable,
		SourceIdentifier: "prod.customer_phone_table",
		SchemaDefinition: []*interfaces.Property{
			{Name: "phone", DisplayName: "Phone Number", Description: "Sensitive"},
		},
		UpdateTime: 123,
	}
	rows := []map[string]any{
		{
			"customer_id": "C-10086",
			"name":        "Sensitive Customer",
			"phone":       "13800000000",
		},
	}

	refs := append(ResourceRefs([]*interfaces.Resource{resource}), ResourceRowRefs(resource, rows)...)
	reqCtx := testRequestContext()
	reqCtx.ClaimID = "claim_agent_answer_0001"
	events := BuildDataQueryEvents(testTraceContext(), reqCtx, DataQuerySubject{
		Operation:     "data.resource.query",
		ResourceID:    resource.ID,
		CatalogID:     resource.CatalogID,
		QueryHash:     HashValue(map[string]any{"filter": "redacted"}),
		ReturnedCount: len(rows),
		TotalCount:    1,
	}, refs)

	assertSafeEvents(t, events, []string{
		`"event_type":"data.query.observed"`,
		`"event_type":"evidence.refs.created"`,
		`"event_type":"business.refs.resolved"`,
		`"ref_type":"resource_ref"`,
		`"ref_type":"row_ref"`,
		`"source_system":"vega-data"`,
		`"query_hash":"sha256:`,
	}, []string{
		"customer_sensitive_table",
		"prod.customer_phone_table",
		"C-10086",
		"Sensitive Customer",
		"13800000000",
		"customer_id",
		"phone",
		"Phone Number",
		"Sensitive",
	})
}

func TestBuildDataQueryEventsDifferentiatesSameCountDifferentRows(t *testing.T) {
	resource := &interfaces.Resource{ID: "res_customer_table", CatalogID: "cat_prod"}
	subject := DataQuerySubject{
		Operation:     "data.resource.query",
		ResourceID:    resource.ID,
		CatalogID:     resource.CatalogID,
		QueryHash:     HashValue("same-query"),
		ReturnedCount: 1,
		TotalCount:    1,
	}
	first := BuildDataQueryEvents(testTraceContext(), testRequestContext(), subject, ResourceRowRefs(resource, []map[string]any{{"id": "row_1"}}))
	second := BuildDataQueryEvents(testTraceContext(), testRequestContext(), subject, ResourceRowRefs(resource, []map[string]any{{"id": "row_2"}}))

	firstPayload := eventPayload(t, first[0])
	secondPayload := eventPayload(t, second[0])
	firstRefs, _ := json.Marshal(firstPayload["resource_refs"])
	secondRefs, _ := json.Marshal(secondPayload["resource_refs"])
	if string(firstRefs) == string(secondRefs) {
		t.Fatalf("safe ref hashes should differ for different rows")
	}
}

func TestBuildDataQueryEventsRequiresTraceAndRequestContext(t *testing.T) {
	events := BuildDataQueryEvents(context.Background(), testRequestContext(), DataQuerySubject{
		Operation:     "data.resource.query",
		ResourceID:    "res_customer_table",
		CatalogID:     "cat_prod",
		QueryHash:     HashValue("query"),
		ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "row:res:demo", RefType: RefTypeRow}})
	if len(events) != 0 {
		t.Fatalf("len(events)=%d, want 0 without trace context", len(events))
	}

	events = BuildDataQueryEvents(testTraceContext(), RequestContext{}, DataQuerySubject{
		Operation:     "data.resource.query",
		ResourceID:    "res_customer_table",
		CatalogID:     "cat_prod",
		QueryHash:     HashValue("query"),
		ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "row:res:demo", RefType: RefTypeRow}})
	if len(events) != 0 {
		t.Fatalf("len(events)=%d, want 0 without request context", len(events))
	}
}

func eventPayload(t *testing.T, event Event) map[string]any {
	t.Helper()
	payload, ok := event["payload"].(map[string]any)
	if !ok {
		t.Fatalf("event payload missing or invalid: %#v", event["payload"])
	}
	return payload
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

func assertSafeEvents(t *testing.T, events []Event, want []string, forbidden []string) {
	t.Helper()
	if len(events) != 3 {
		t.Fatalf("len(events)=%d, want 3", len(events))
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	for _, item := range want {
		if !strings.Contains(text, item) {
			t.Fatalf("missing %q in events: %s", item, text)
		}
	}
	for _, item := range forbidden {
		if strings.Contains(text, item) {
			t.Fatalf("event leaked raw content %q: %s", item, text)
		}
	}
}
