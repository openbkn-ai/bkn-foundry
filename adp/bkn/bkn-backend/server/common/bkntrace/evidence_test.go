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

	"bkn-backend/interfaces"
	"go.opentelemetry.io/otel/trace"
)

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
	}
}

func TestBuildSchemaReadEventsRecordsKnowledgeWithoutFabricatingClaim(t *testing.T) {
	items := []*interfaces.ObjectType{{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "customer", OTName: "Customer PII", DataProperties: []*interfaces.DataProperty{{Name: "phone", DisplayName: "Phone Number"}}},
		CommonInfo:             interfaces.CommonInfo{Comment: "must not emit"}, KNID: "kn_demo", Branch: "main",
	}}
	events := BuildSchemaReadEvents(testTraceContext(), testRequestContext(), ReadSubject{
		EntityKind: EntityKindObjectType, Operation: "bkn.schema.object_type.list", KNID: "kn_demo", Branch: "main", ReturnedCount: 1,
	}, ObjectTypeRefs(items))

	assertSafeEvents(t, events, 1, []string{
		`"event_type":"knowledge.read.observed"`, `"bkn.trace.schema.version":"2.1.0"`,
		`"interaction_id":"int_schema_read_001"`, `"operation_id":"op_schema_read_001"`,
		`"causation_event_id":"evt_retrieval_001"`, `"attempt":2`, `"kn_id":"kn_demo"`,
		`"read_kind":"object_type"`,
	}, []string{`"event_type":"claim.created"`, `"event_type":"evidence.refs.created"`, `"event_type":"business.refs.resolved"`, `"summary":`, "Customer PII", "phone", "must not emit"})
}

func TestBuildSchemaReadEventsLinksControlledBusinessRefsOnlyWithUpstreamClaim(t *testing.T) {
	req := testRequestContext()
	req.ClaimID = "claim_agent_001"
	events := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{
		EntityKind: EntityKindObjectType, Operation: "bkn.schema.object_type.get", KNID: "kn_demo", Branch: "main", ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "object_type:customer", RefType: RefTypeSchema, Summary: map[string]any{"raw": "must-not-appear"}}})

	assertSafeEvents(t, events, 3, []string{
		`"event_type":"knowledge.read.observed"`, `"event_type":"evidence.refs.created"`, `"event_type":"business.refs.resolved"`,
		`"claim_id":"claim_agent_001"`, `"ref_id":"object_type:customer"`, `"ref_type":"object"`,
		`"resolver_status":"resolved"`, `"version_status":"unversioned"`, `"visibility":"visible"`,
	}, []string{`"event_type":"claim.created"`, `"summary":`, "must-not-appear"})
}

func TestBuildSchemaReadEventsGeneratesCausalIDs(t *testing.T) {
	req := testRequestContext()
	req.InteractionID, req.OperationID, req.CausationEventID = "", "", ""
	events := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{EntityKind: EntityKindMetric, KNID: "kn_demo", ReturnedCount: 1}, []EvidenceRef{{RefID: "metric:risk", RefType: RefTypeMetric}})
	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want 1", len(events))
	}
	for _, key := range []string{"interaction_id", "operation_id"} {
		if strings.TrimSpace(events[0][key].(string)) == "" {
			t.Fatalf("%s was not generated: %#v", key, events[0])
		}
	}
}

func TestBuildSchemaReadEventsRequiresTraceAndRequest(t *testing.T) {
	if got := BuildSchemaReadEvents(context.Background(), testRequestContext(), ReadSubject{EntityKind: EntityKindObjectType}, []EvidenceRef{{RefID: "object_type:x", RefType: RefTypeSchema}}); len(got) != 0 {
		t.Fatalf("events without trace=%d", len(got))
	}
	if got := BuildSchemaReadEvents(testTraceContext(), RequestContext{}, ReadSubject{EntityKind: EntityKindObjectType}, []EvidenceRef{{RefID: "object_type:x", RefType: RefTypeSchema}}); len(got) != 0 {
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
