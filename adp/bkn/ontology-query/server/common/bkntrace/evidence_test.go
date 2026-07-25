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
)

func testTraceContext() context.Context {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{0x72, 0x22, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		SpanID:  trace.SpanID{0x72, 0x22, 0, 0, 0, 0, 0, 1}, TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), sc)
}

func testRequestContext() RequestContext {
	return RequestContext{
		RequestID: "req_ontology_data_0001", AccountID: "acct_demo", AccountType: "user", BusinessDomain: "domain_demo",
		InteractionID: "int_data_query_001", OperationID: "op_data_query_001", CausationEventID: "evt_tool_called_001", Attempt: 1,
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
	}, []string{`"event_type":"claim.created"`, `"event_type":"evidence.refs.created"`, `"event_type":"business.refs.resolved"`, `"summary":`, "Sensitive Customer", "13800000000", "phone"})
}

func TestBuildDataQueryEventsLinksControlledRefsOnlyWithUpstreamClaim(t *testing.T) {
	req := testRequestContext()
	req.ClaimID = "claim_agent_002"
	events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{
		EntityKind: EntityKindObjectInstance, Operation: "bkn.object.query", KNID: "kn_demo", Branch: "main", SubjectID: "customer", QueryHash: HashValue("safe-shape"), ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "object_instance:customer:hash", RefType: RefTypeRow, Summary: map[string]any{"row": "must-not-appear"}}})

	assertSafeEvents(t, events, 3, []string{
		`"event_type":"data.query.observed"`, `"event_type":"evidence.refs.created"`, `"event_type":"business.refs.resolved"`,
		`"claim_id":"claim_agent_002"`, `"ref_id":"object_instance:customer:hash"`, `"summary_hash":"sha256:`,
		`"resolver_status":"resolved"`,
	}, []string{`"event_type":"claim.created"`, `"summary":`, "must-not-appear"})
}

func TestBuildDataQueryEventsGeneratesCausalIDs(t *testing.T) {
	req := testRequestContext()
	req.InteractionID, req.OperationID, req.CausationEventID = "", "", ""
	events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{EntityKind: EntityKindMetric, QueryHash: HashValue("q")}, []EvidenceRef{{RefID: "metric:risk", RefType: RefTypeMetric}})
	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want 1", len(events))
	}
	for _, key := range []string{"interaction_id", "operation_id"} {
		value, _ := events[0][key].(string)
		if strings.TrimSpace(value) == "" {
			t.Fatalf("%s not generated: %#v", key, events[0])
		}
	}
}

func TestBuildDataQueryEventsRequiresTraceAndRequest(t *testing.T) {
	if got := BuildDataQueryEvents(context.Background(), testRequestContext(), DataQuerySubject{}, []EvidenceRef{{RefID: "row:x", RefType: RefTypeRow}}); len(got) != 0 {
		t.Fatalf("events without trace=%d", len(got))
	}
	if got := BuildDataQueryEvents(testTraceContext(), RequestContext{}, DataQuerySubject{}, []EvidenceRef{{RefID: "row:x", RefType: RefTypeRow}}); len(got) != 0 {
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
