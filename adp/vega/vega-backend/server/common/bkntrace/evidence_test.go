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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"vega-backend/interfaces"

	"go.opentelemetry.io/otel/trace"
)

type evidenceRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn evidenceRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

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
		RequestID:          "req_vega_data_0001",
		AccountID:          "acct_demo",
		AccountType:        "user",
		InteractionID:      "int_vega_query_0001",
		OperationID:        "op_vega_query_0001",
		CausationEventID:   "evt_retrieval_completed_0001",
		Attempt:            2,
		ObservedAt:         "2026-07-25T08:00:00Z",
		ObservedAtProvided: true,
	}
}

func TestBuildDataQueryEventsReplayIsStable(t *testing.T) {
	subject := DataQuerySubject{Operation: "data.resource.query", QueryHash: HashValue("safe")}
	first := BuildDataQueryEvents(testTraceContext(), testRequestContext(), subject, nil)
	second := BuildDataQueryEvents(testTraceContext(), testRequestContext(), subject, nil)
	if first[0]["event_id"] != second[0]["event_id"] || first[0]["observed_at"] != second[0]["observed_at"] {
		t.Fatalf("replay changed identity or observed time: %#v != %#v", first[0], second[0])
	}
}

func TestBuildDataQueryEventsRejectsMissingReplayEnvelope(t *testing.T) {
	req := testRequestContext()
	req.ObservedAtProvided = false
	if events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{Operation: "data.resource.query"}, nil); len(events) != 0 {
		t.Fatalf("missing bkn-event-observed-at must not create conflicting replay: %#v", events)
	}
	req.ObservedAtProvided = true
	req.ObservedAt = "not-a-timestamp"
	if events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{Operation: "data.resource.query"}, nil); len(events) != 0 {
		t.Fatalf("invalid bkn-event-observed-at must not create conflicting replay: %#v", events)
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

func TestPostBatchReturnsCoreValidationCode(t *testing.T) {
	previous := evidenceHTTPClient
	t.Cleanup(func() { evidenceHTTPClient = previous })
	evidenceHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body: io.NopCloser(strings.NewReader(
				`{"code":"BKN_TRACE_OWNERSHIP_CONFLICT","message":"trace request or ownership differs from the committed trace"}`,
			)),
		}, nil
	})}

	err := postBatch("http://trace.local", time.Second, batch{})
	if err == nil || !strings.Contains(err.Error(), "BKN_TRACE_OWNERSHIP_CONFLICT") {
		t.Fatalf("error=%v, want bounded Core validation code", err)
	}
}

func TestPostBatchWithRetryDoesNotRetryPermanentClientError(t *testing.T) {
	previous := evidenceHTTPClient
	t.Cleanup(func() { evidenceHTTPClient = previous })
	var calls atomic.Int32
	evidenceHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body: io.NopCloser(strings.NewReader(
				`{"code":"BKN_TRACE_OWNERSHIP_CONFLICT","message":"trace request or ownership differs from the committed trace"}`,
			)),
		}, nil
	})}

	err := postBatchWithRetry("http://trace.local", time.Second, batch{})
	if err == nil {
		t.Fatal("permanent client error must be returned")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d, want one request for permanent client error", calls.Load())
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

func TestBuildDataQueryEvidenceLinksAuthorizedQueryAndResultArtifacts(t *testing.T) {
	reqCtx := testRequestContext()
	query := map[string]any{
		"resource_id": "res_purchase_order",
		"filter_condition": map[string]any{
			"field": "supplier_id", "operation": "eq", "value": "SUP-001",
		},
		"output_fields": []string{"order_id", "amount"},
		"limit":         20,
	}
	result := map[string]any{
		"entries": []map[string]any{{
			"order_id": "PO-2024-001",
			"amount":   128000,
		}},
		"total_count": 1,
		"truncated":   false,
	}
	subject := DataQuerySubject{
		Operation: "data.resource.query", ResourceID: "res_purchase_order",
		CatalogID: "cat_supplychain", QueryHash: HashValue(query), ReturnedCount: 1, TotalCount: 1,
	}

	artifacts, events := BuildDataQueryEvidence(
		testTraceContext(), reqCtx, subject,
		[]EvidenceRef{{RefID: "resource:res_purchase_order", RefType: RefTypeResource}},
		query, result,
	)

	if len(artifacts) != 2 || len(events) != 1 {
		t.Fatalf("artifacts=%d events=%d, want 2 and 1", len(artifacts), len(events))
	}
	if artifacts[0].ArtifactType != ArtifactTypeQuery || artifacts[0].ContentHash != "" {
		t.Fatalf("unexpected query artifact: %#v", artifacts[0])
	}
	if artifacts[1].ArtifactType != ArtifactTypeDataResult || artifacts[1].ContentHash != "" {
		t.Fatalf("unexpected result artifact: %#v", artifacts[1])
	}
	for _, artifact := range artifacts {
		if artifact.SchemaVersion != ArtifactContractVersion ||
			artifact.RequestID != reqCtx.RequestID ||
			artifact.TraceID != "73220000000000000000000000000001" {
			t.Fatalf("artifact does not preserve trace ownership: %#v", artifact)
		}
		if artifact.AccountID != "acct_demo" || artifact.AccountType != "user" {
			t.Fatalf("artifact does not preserve authorization scope: %#v", artifact)
		}
	}
	payload := eventPayload(t, events[0])
	if payload["query_artifact_ref"] != "artifact:"+artifacts[0].ArtifactID ||
		payload["result_artifact_ref"] != "artifact:"+artifacts[1].ArtifactID {
		t.Fatalf("event does not link artifacts: %#v", payload)
	}
	if events[0]["bkn.trace.schema.version"] != ArtifactContractVersion {
		t.Fatalf("artifact-linked event must use %s: %#v", ArtifactContractVersion, events[0])
	}
	raw, _ := json.Marshal(events)
	for _, businessValue := range []string{"SUP-001", "PO-2024-001", "128000"} {
		if strings.Contains(string(raw), businessValue) {
			t.Fatalf("business content leaked into event %q: %s", businessValue, raw)
		}
	}
}

func TestPostArtifactSendsDedicatedIngestTokenAndFullBusinessContent(t *testing.T) {
	t.Setenv("BKN_TRACE_EVIDENCE_INGEST_TOKEN", "producer-token")
	previous := evidenceHTTPClient
	t.Cleanup(func() { evidenceHTTPClient = previous })
	evidenceHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("X-BKN-Trace-Ingest-Token"); got != "producer-token" {
			t.Fatalf("ingest token header=%q", got)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"order_id":"PO-2024-001"`) {
			t.Fatalf("authorized artifact lost business content: %s", body)
		}
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	artifact := Artifact{
		ArtifactID:    "art_query_001",
		ArtifactType:  ArtifactTypeQuery,
		RequestID:     "req_vega_data_0001",
		TraceID:       "73220000000000000000000000000001",
		ContentType:   "application/json",
		SchemaVersion: ArtifactContractVersion,
		ObservedAt:    "2026-07-25T08:00:00Z",
		ContentHash:   ArtifactContentHash(map[string]any{"order_id": "PO-2024-001"}),
		Content:       map[string]any{"order_id": "PO-2024-001"},
		AccountID:     "acct_demo",
		AccountType:   "user",
	}
	if err := postArtifact("http://trace.local/api/agent-observability/v1/evidence/artifacts", time.Second, artifact); err != nil {
		t.Fatal(err)
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

func TestBuildDataQueryEventsKeepsFactIndependentFromUpstreamClaim(t *testing.T) {
	reqCtx := testRequestContext()
	reqCtx.ClaimID = "claim_agent_answer_0001"
	events := BuildDataQueryEvents(testTraceContext(), reqCtx, DataQuerySubject{
		Operation: "data.resource.query", ResourceID: "res_demo", CatalogID: "cat_demo",
		QueryHash: HashValue("safe"), ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "resource:res_demo", RefType: RefTypeResource, Summary: map[string]any{"raw": "must-not-appear"}}})
	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want one fact", len(events))
	}
	assertOnlyKeys(t, eventPayload(t, events[0]), "query_hash", "query_type", "row_count", "truncated", "version_status", "resource_refs", "field_refs")
	raw, _ := json.Marshal(events)
	for _, forbidden := range []string{"claim.created", "evidence.refs.created", "business.refs.resolved", "claim_agent_answer_0001", `"summary":`, "must-not-appear"} {
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

func TestBuildDataQueryEventsDoesNotCreateClaimResolutionEvents(t *testing.T) {
	reqCtx := testRequestContext()
	reqCtx.ClaimID = "claim_agent_answer_0001"
	events := BuildDataQueryEvents(testTraceContext(), reqCtx, DataQuerySubject{
		Operation: "data.resource.query",
		QueryHash: HashValue("zero-result-query"),
	}, nil)
	if len(events) != 1 || events[0]["event_type"] != "data.query.observed" {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestBuildDataQueryEventsRejectsUnregisteredRowRefs(t *testing.T) {
	reqCtx := testRequestContext()
	reqCtx.ClaimID = "claim_agent_answer_0001"
	events := BuildDataQueryEvents(testTraceContext(), reqCtx, DataQuerySubject{
		Operation: "data.resource.query", QueryHash: HashValue("safe"), ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "resource_row:demo:abc", RefType: "row_ref", Summary: map[string]any{"row_hash": HashValue("row")}}})
	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want fact", len(events))
	}
	payload := eventPayload(t, events[0])
	if len(payload["resource_refs"].([]map[string]any)) != 0 || len(payload["field_refs"].([]map[string]any)) != 0 {
		t.Fatalf("unregistered row ref was retained: %#v", payload)
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
		`"ref_type":"resource"`,
		`"ref_type":"field"`,
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

func TestBuildDataQueryEventsDoesNotFingerprintRows(t *testing.T) {
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
	if string(firstRefs) != string(secondRefs) {
		t.Fatalf("row content must not affect indexed refs: %s != %s", firstRefs, secondRefs)
	}
}

func TestBuildDataQueryEventsRequiresTraceAndRequestContext(t *testing.T) {
	events := BuildDataQueryEvents(context.Background(), testRequestContext(), DataQuerySubject{
		Operation:     "data.resource.query",
		ResourceID:    "res_customer_table",
		CatalogID:     "cat_prod",
		QueryHash:     HashValue("query"),
		ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "resource:demo", RefType: RefTypeResource}})
	if len(events) != 0 {
		t.Fatalf("len(events)=%d, want 0 without trace context", len(events))
	}

	events = BuildDataQueryEvents(testTraceContext(), RequestContext{}, DataQuerySubject{
		Operation:     "data.resource.query",
		ResourceID:    "res_customer_table",
		CatalogID:     "cat_prod",
		QueryHash:     HashValue("query"),
		ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "resource:demo", RefType: RefTypeResource}})
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
	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want 1", len(events))
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
