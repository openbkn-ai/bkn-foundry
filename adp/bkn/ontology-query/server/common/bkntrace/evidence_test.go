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

	"go.opentelemetry.io/otel/trace"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func testTraceContext() context.Context {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{0x72, 0x22, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		SpanID:  trace.SpanID{0x72, 0x22, 0, 0, 0, 0, 0, 1}, TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), sc)
}

func testRequestContext() RequestContext {
	return RequestContext{
		RequestID: "req_ontology_data_0001", AccountID: "acct_demo", AccountType: "user",
		InteractionID: "int_data_query_001", OperationID: "op_data_query_001", CausationEventID: "evt_tool_called_001", Attempt: 1,
		ObservedAt: "2026-07-25T08:00:00Z",
	}
}

func TestBuildDataQueryEventsRejectsMissingReplayEnvelope(t *testing.T) {
	req := testRequestContext()
	req.ObservedAt = ""
	if events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{EntityKind: EntityKindMetric}, nil); len(events) != 0 {
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

func TestBuildDataQueryEventsRecordsDataWithoutFabricatingClaim(t *testing.T) {
	rows := []map[string]any{{"_instance_id": "obj_customer_001", "name": "Sensitive Customer", "phone": "13800000000"}}
	events := BuildDataQueryEvents(testTraceContext(), testRequestContext(), DataQuerySubject{
		EntityKind: EntityKindObjectInstance, Operation: "bkn.object.query", KNID: "kn_demo", Branch: "main", SubjectID: "customer",
		QueryHash: HashValue(map[string]any{"condition": "redacted"}), ReturnedCount: 1, TotalCount: 1,
	}, ObjectRowRefs("kn_demo", "main", "customer", rows))

	assertSafeEvents(t, events, 1, []string{
		`"event_type":"data.query.observed"`, `"bkn.trace.schema.version":"2.1.0"`,
		`"interaction_id":"int_data_query_001"`, `"operation_id":"op_data_query_001"`,
		`"causation_event_id":"evt_tool_called_001"`, `"query_type":"object_instance"`,
		`"query_hash":"sha256:`, `"row_count":1`, `"truncated":false`,
		`"ref_id":"object:kn_demo:customer"`, `"ref_type":"object"`,
	}, []string{`"event_type":"claim.created"`, `"event_type":"evidence.refs.created"`, `"event_type":"business.refs.resolved"`, `"summary":`, "Sensitive Customer", "13800000000", "phone"})
}

func TestBuildDataQueryEventsKeepsFactRefsIndependentFromUpstreamClaim(t *testing.T) {
	req := testRequestContext()
	req.ClaimID = "claim_agent_002"
	events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{
		EntityKind: EntityKindObjectInstance, Operation: "bkn.object.query", KNID: "kn_demo", Branch: "main", SubjectID: "customer", QueryHash: HashValue("safe-shape"), ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "object:kn_demo:customer", RefType: RefTypeObject, Summary: map[string]any{"row": "must-not-appear"}}})

	assertSafeEvents(t, events, 1, []string{
		`"event_type":"data.query.observed"`, `"ref_id":"object:kn_demo:customer"`, `"ref_type":"object"`,
	}, []string{`"event_type":"claim.created"`, `"event_type":"evidence.refs.created"`, `"event_type":"business.refs.resolved"`, `"claim_id":"claim_agent_002"`, `"summary":`, "must-not-appear"})
}

func TestBuildDataQueryEventsRejectsMissingCausalIDs(t *testing.T) {
	req := testRequestContext()
	req.InteractionID, req.OperationID, req.CausationEventID = "", "", ""
	events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{EntityKind: EntityKindMetric, QueryHash: HashValue("q")}, []EvidenceRef{{RefID: "metric:kn_demo:risk", RefType: RefTypeMetric}})
	if len(events) != 0 {
		t.Fatalf("missing interaction/operation must not create an unstable envelope: %#v", events)
	}
}

func TestBuildDataQueryEventsRequiresTraceAndRequest(t *testing.T) {
	if got := BuildDataQueryEvents(context.Background(), testRequestContext(), DataQuerySubject{}, []EvidenceRef{{RefID: "object:kn_demo:x", RefType: RefTypeObject}}); len(got) != 0 {
		t.Fatalf("events without trace=%d", len(got))
	}
	if got := BuildDataQueryEvents(testTraceContext(), RequestContext{}, DataQuerySubject{}, []EvidenceRef{{RefID: "object:kn_demo:x", RefType: RefTypeObject}}); len(got) != 0 {
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
