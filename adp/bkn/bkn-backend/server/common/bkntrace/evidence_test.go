// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"bkn-backend/interfaces"
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
		RequestID:        "req_bkn_backend_schema_0001",
		AccountID:        "acct_demo",
		AccountType:      "user",
		InteractionID:    "int_schema_read_001",
		OperationID:      "op_schema_read_001",
		CausationEventID: "evt_retrieval_001",
		Attempt:          2,
		ObservedAt:       "2026-07-25T08:00:00Z",
	}
}

func TestEmitSchemaReadEventsDoesNotAdvertiseDroppedEvidence(t *testing.T) {
	publisher, err := evidencepublisher.New(evidencepublisher.Config{ProducerID: "bkn-backend", BaseStreamID: "backend", WorkloadIdentity: "bkn-backend", ProcessBootID: "boot-1", CapturePolicyRevision: "41"}, &captureEvidenceSender{})
	if err != nil {
		t.Fatal(err)
	}
	setEvidencePublisher(publisher)
	t.Cleanup(func() { setEvidencePublisher(nil); _ = publisher.Close(context.Background()) })
	reqCtx := testRequestContext()
	if eventID := EmitSchemaReadEvents(testTraceContext(), reqCtx, ReadSubject{EntityKind: EntityKindObjectType, KNID: "kn_demo"}, nil); eventID != "" {
		t.Fatalf("event ID = %q, want empty without trusted session scope", eventID)
	}
}

func TestBuildSchemaReadEventsRejectsMissingReplayEnvelope(t *testing.T) {
	req := testRequestContext()
	req.ObservedAt = ""
	if events := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{EntityKind: EntityKindObjectType}, nil); len(events) != 0 {
		t.Fatalf("missing bkn-event-observed-at must not create conflicting replay: %#v", events)
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
