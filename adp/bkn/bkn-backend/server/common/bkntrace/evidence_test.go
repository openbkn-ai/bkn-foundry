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

	"bkn-backend/interfaces"
	"go.opentelemetry.io/otel/trace"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func testTraceContext() context.Context {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{0x71, 0x22, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		SpanID:  trace.SpanID{0x71, 0x22, 0, 0, 0, 0, 0, 1}, TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), sc)
}

func testRequestContext() RequestContext {
	return RequestContext{
		RequestID: "req_bkn_backend_schema_0001", AccountID: "acct_demo", AccountType: "user", BusinessDomain: "domain_demo",
		InteractionID: "int_schema_read_001", OperationID: "op_schema_read_001", CausationEventID: "evt_retrieval_001", Attempt: 2,
		ObservedAt: "2026-07-25T08:00:00Z",
	}
}

func TestBuildSchemaReadEventsRejectsMissingReplayEnvelope(t *testing.T) {
	req := testRequestContext()
	req.ObservedAt = ""
	if events := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{EntityKind: EntityKindObjectType}, nil); len(events) != 0 {
		t.Fatalf("missing bkn-event-observed-at must not create conflicting replay: %#v", events)
	}
}

func TestPostBatchWithRetryRetriesNon2xx(t *testing.T) {
	previous := evidenceHTTPClient
	t.Cleanup(func() { evidenceHTTPClient = previous })
	var calls atomic.Int32
	evidenceHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
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
	evidenceHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("X-BKN-Trace-Ingest-Token"); got != "producer-token" {
			t.Fatalf("ingest token header=%q", got)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if err := postBatch("http://trace.local", time.Second, batch{}); err != nil {
		t.Fatal(err)
	}
}

func TestPostBatchReportsOnlySafeValidationCodeAndPaths(t *testing.T) {
	previous := evidenceHTTPClient
	t.Cleanup(func() { evidenceHTTPClient = previous })
	evidenceHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{
			"code":"BKN_TRACE_REQUIRED_FIELD_MISSING",
			"message":"sensitive customer value",
			"details":[
				{"code":"BKN_TRACE_REQUIRED_FIELD_MISSING","path":"$.events[0].payload.business_refs","message":"secret one"},
				{"code":"BKN_TRACE_REFERENCE_ID_INVALID","path":"$.events[0].payload.business_refs[0].ref_id","message":"secret two"}
			]
		}`
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	err := postBatch("http://trace.local", time.Second, batch{})
	if err == nil {
		t.Fatal("expected validation failure")
	}
	got := err.Error()
	for _, want := range []string{
		"HTTP 400",
		"code=BKN_TRACE_REQUIRED_FIELD_MISSING",
		"paths=$.events[0].payload.business_refs,$.events[0].payload.business_refs[0].ref_id",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q from %q", want, got)
		}
	}
	for _, forbidden := range []string{"sensitive customer value", "secret one", "secret two"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("unsafe response value %q leaked in %q", forbidden, got)
		}
	}
}

func TestBuildSchemaReadEventsRecordsKnowledgeWithoutFabricatingClaim(t *testing.T) {
	items := []*interfaces.ObjectType{{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "customer", OTName: "Customer PII", DataProperties: []*interfaces.DataProperty{{Name: "risk_level", DisplayName: "Risk Level"}}},
		CommonInfo:             interfaces.CommonInfo{Comment: "must not emit"}, KNID: "kn_demo", Branch: "main",
	}}
	events := BuildSchemaReadEvents(testTraceContext(), testRequestContext(), ReadSubject{
		EntityKind: EntityKindObjectType, Operation: "bkn.schema.object_type.list", KNID: "kn_demo", Branch: "main", ReturnedCount: 1,
	}, ObjectTypeRefs(items))

	assertSafeEvents(t, events, 1, []string{
		`"event_type":"knowledge.read.observed"`, `"bkn.trace.schema.version":"2.1.0"`,
		`"interaction_id":"int_schema_read_001"`, `"operation_id":"op_schema_read_001"`,
		`"causation_event_id":"evt_retrieval_001"`, `"attempt":2`, `"kn_id":"kn_demo"`,
		`"read_kind":"object_type"`, `"ref_id":"object:kn_demo:customer"`, `"ref_type":"object"`,
		`"ref_id":"property:kn_demo:customer:risk_level"`, `"ref_type":"property"`,
	}, []string{`"event_type":"claim.created"`, `"event_type":"evidence.refs.created"`, `"event_type":"business.refs.resolved"`, `"summary":`, "Customer PII", "Risk Level", "must not emit"})
}

func TestBuildSchemaReadEventsKeepsFactRefsIndependentFromUpstreamClaim(t *testing.T) {
	req := testRequestContext()
	req.ClaimID = "claim_agent_001"
	events := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{
		EntityKind: EntityKindObjectType, Operation: "bkn.schema.object_type.get", KNID: "kn_demo", Branch: "main", ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "object:kn_demo:customer", RefType: RefTypeObject, Summary: map[string]any{"raw": "must-not-appear"}}})

	assertSafeEvents(t, events, 1, []string{
		`"event_type":"knowledge.read.observed"`, `"ref_id":"object:kn_demo:customer"`, `"ref_type":"object"`,
		`"version_status":"unversioned"`, `"visibility":"visible"`,
	}, []string{`"event_type":"claim.created"`, `"event_type":"evidence.refs.created"`, `"event_type":"business.refs.resolved"`, `"claim_id":"claim_agent_001"`, `"summary":`, "must-not-appear"})
}

func TestBuildSchemaReadEventsRejectsMissingCausalIDs(t *testing.T) {
	req := testRequestContext()
	req.InteractionID, req.OperationID, req.CausationEventID = "", "", ""
	events := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{EntityKind: EntityKindMetric, KNID: "kn_demo", ReturnedCount: 1}, []EvidenceRef{{RefID: "metric:kn_demo:risk", RefType: RefTypeMetric}})
	if len(events) != 0 {
		t.Fatalf("missing interaction/operation must not create an unstable envelope: %#v", events)
	}
}

func TestBuildSchemaReadEventsRequiresTraceAndRequest(t *testing.T) {
	if got := BuildSchemaReadEvents(context.Background(), testRequestContext(), ReadSubject{EntityKind: EntityKindObjectType}, []EvidenceRef{{RefID: "object:kn_demo:x", RefType: RefTypeObject}}); len(got) != 0 {
		t.Fatalf("events without trace=%d", len(got))
	}
	if got := BuildSchemaReadEvents(testTraceContext(), RequestContext{}, ReadSubject{EntityKind: EntityKindObjectType}, []EvidenceRef{{RefID: "object:kn_demo:x", RefType: RefTypeObject}}); len(got) != 0 {
		t.Fatalf("events without request=%d", len(got))
	}
}

func assertSafeEvents(t *testing.T, events []Event, count int, want, forbidden []string) {
	t.Helper()
	if len(events) != count {
		t.Fatalf("len(events)=%d, want %d", len(events), count)
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, item := range want {
		if !strings.Contains(text, item) {
			t.Fatalf("missing %q: %s", item, text)
		}
	}
	for _, item := range forbidden {
		if strings.Contains(text, item) {
			t.Fatalf("leaked/forbidden %q: %s", item, text)
		}
	}
}
