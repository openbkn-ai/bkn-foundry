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

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
	"go.opentelemetry.io/otel/trace"
)

type captureEvidenceSender struct{ records []evidencepublisher.Record }

func (s *captureEvidenceSender) Send(_ context.Context, record evidencepublisher.Record) error {
	s.records = append(s.records, record)
	return nil
}

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
		ApplicationPrincipalID: "openbkn-sdk", EffectiveSubjectType: "user", EffectiveSubjectID: "acct_demo",
		ConversationID: "conv_data_query_001", SessionScopePresent: true,
		InteractionID: "int_data_query_001", OperationID: "op_data_query_001", CausationEventID: "evt_tool_called_001", Attempt: 1,
		ObservedAt: "2026-07-25T08:00:00Z",
	}
}

func TestDataQueryEvidenceCarriesOwnerAndOnlyTrustedCoreOperation(t *testing.T) {
	req := testRequestContext()
	event := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{EntityKind: EntityKindObjectInstance, KNID: "kn_demo"}, nil)[0]
	owner, ok := event["owner"].(map[string]any)
	if !ok || owner["application_principal_id"] != "openbkn-sdk" || owner["effective_subject_id"] != "acct_demo" {
		t.Fatalf("owner = %#v, want verified caller identity", event["owner"])
	}
	ec, _ := contextFromRequest(testTraceContext(), req)
	core, err := toCoreEvent(event, ec)
	if err != nil {
		t.Fatal(err)
	}
	if core.OperationID != "" {
		t.Fatalf("core operation ID = %q, want omitted local ID", core.OperationID)
	}
	req.OperationScopePresent = true
	ec, _ = contextFromRequest(testTraceContext(), req)
	core, err = toCoreEvent(event, ec)
	if err != nil {
		t.Fatal(err)
	}
	if core.OperationID != req.OperationID {
		t.Fatalf("core operation ID = %q, want upstream %q", core.OperationID, req.OperationID)
	}
}

func TestEmitDataQueryEventsDoesNotAdvertiseDroppedEvidence(t *testing.T) {
	publisher, err := evidencepublisher.New(evidencepublisher.Config{ProducerID: "ontology-query", BaseStreamID: "ontology-query", WorkloadIdentity: "ontology-query", ProcessBootID: "boot-1", CapturePolicyRevision: "41"}, &captureEvidenceSender{})
	if err != nil {
		t.Fatal(err)
	}
	SetEvidencePublisher(publisher)
	t.Cleanup(func() { SetEvidencePublisher(nil); _ = publisher.Close(context.Background()) })
	reqCtx := testRequestContext()
	reqCtx.SessionScopePresent = false
	if eventID := EmitDataQueryEvents(testTraceContext(), reqCtx, DataQuerySubject{EntityKind: EntityKindObjectInstance, KNID: "kn_demo"}, nil); eventID != "" {
		t.Fatalf("event ID = %q, want empty without trusted session scope", eventID)
	}
}

func TestEmitDataQueryEventsDoesNotAdvertiseMissingOwner(t *testing.T) {
	publisher, err := evidencepublisher.New(evidencepublisher.Config{ProducerID: "ontology-query", BaseStreamID: "ontology-query", WorkloadIdentity: "ontology-query", ProcessBootID: "boot-owner", CapturePolicyRevision: "1"}, &captureEvidenceSender{})
	if err != nil {
		t.Fatal(err)
	}
	SetEvidencePublisher(publisher)
	t.Cleanup(func() { SetEvidencePublisher(nil); _ = publisher.Close(context.Background()) })
	req := testRequestContext()
	req.ApplicationPrincipalID = ""
	if got := EmitDataQueryEvents(testTraceContext(), req, DataQuerySubject{EntityKind: EntityKindObjectInstance, KNID: "kn_demo"}, nil); got != "" {
		t.Fatalf("event ID = %q, want empty without verified owner", got)
	}
}

func TestBuildDataQueryEventsRejectsMissingReplayEnvelope(t *testing.T) {
	req := testRequestContext()
	req.ObservedAt = ""
	if events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{EntityKind: EntityKindMetric}, nil); len(events) != 0 {
		t.Fatalf("missing bkn-event-observed-at must not create conflicting replay: %#v", events)
	}
}

func TestSubmitEventsMatchesCanonicalEvidenceFixture(t *testing.T) {
	sender := &captureEvidenceSender{}
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: "ontology-query", BaseStreamID: "ontology-query", WorkloadIdentity: "ontology-query",
		ProcessBootID: "boot-1", CapturePolicyRevision: "41",
	}, sender)
	if err != nil {
		t.Fatal(err)
	}
	SetEvidencePublisher(publisher)
	t.Cleanup(func() {
		SetEvidencePublisher(nil)
		_ = publisher.Close(context.Background())
	})

	events := BuildDataQueryEvents(testTraceContext(), testRequestContext(), DataQuerySubject{
		EntityKind: EntityKindObjectInstance, Operation: "bkn.object.query", KNID: "kn_demo",
	}, nil)
	SubmitEvents(testTraceContext(), testRequestContext(), events)
	queued := publisher.SnapshotQueue()
	if len(queued) != 1 {
		t.Fatalf("queued records = %d, want 1", len(queued))
	}
	var payload map[string]any
	if err := json.Unmarshal(queued[0].Value, &payload); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"event_id": events[0]["event_id"], "event_type": "data.query.observed", "conversation_id": "conv_data_query_001",
		"producer_id": "ontology-query", "producer_stream_id": "ontology-query:boot-1",
	} {
		if payload[key] != want {
			t.Fatalf("payload[%q] = %#v, want %#v; payload=%s", key, payload[key], want, queued[0].Value)
		}
	}
	wantHeaders := []evidencepublisher.Header{
		{Key: "content-type", Value: "application/json"},
		{Key: "bkn-trace-schema-version", Value: "3.0.0"},
		{Key: "capture_policy_revision", Value: "41"},
		{Key: "producer_instance_id", Value: "ontology-query#boot-1"},
		{Key: "bkn-evidence-record-class", Value: "live"},
	}
	if len(queued[0].Headers) != len(wantHeaders) {
		t.Fatalf("headers = %+v", queued[0].Headers)
	}
	for i := range wantHeaders {
		if queued[0].Headers[i] != wantHeaders[i] {
			t.Fatalf("header[%d] = %+v, want %+v", i, queued[0].Headers[i], wantHeaders[i])
		}
	}
}

func TestSubmitEventsDropsWithoutTrustedSessionScope(t *testing.T) {
	for _, field := range []string{"conversation_id", "interaction_id", "source_flag"} {
		t.Run(field, func(t *testing.T) {
			publisher, err := evidencepublisher.New(evidencepublisher.Config{ProducerID: "ontology-query", BaseStreamID: "ontology-query", WorkloadIdentity: "ontology-query", ProcessBootID: "boot-1", CapturePolicyRevision: "41"}, &captureEvidenceSender{})
			if err != nil {
				t.Fatal(err)
			}
			SetEvidencePublisher(publisher)
			t.Cleanup(func() { SetEvidencePublisher(nil); _ = publisher.Close(context.Background()) })
			reqCtx := testRequestContext()
			switch field {
			case "conversation_id":
				reqCtx.ConversationID = ""
			case "interaction_id":
				reqCtx.InteractionID = ""
			case "source_flag":
				reqCtx.SessionScopePresent = false
			}
			events := BuildDataQueryEvents(testTraceContext(), reqCtx, DataQuerySubject{EntityKind: EntityKindObjectInstance, KNID: "kn_demo"}, nil)
			SubmitEvents(testTraceContext(), reqCtx, events)
			if got := len(publisher.SnapshotQueue()); got != 0 {
				t.Fatalf("queued records = %d, want 0 without %s", got, field)
			}
		})
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
