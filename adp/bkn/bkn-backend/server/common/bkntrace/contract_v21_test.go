// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.

// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestBuildSchemaReadEventsV21RecordsReadWithoutFabricatingClaim(t *testing.T) {
	req := testRequestContext()
	req.InteractionID = "int_schema_001"
	req.OperationID = "op_schema_001"
	req.CausationEventID = "evt_retrieval_001"
	req.Attempt = 2

	events := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{
		EntityKind: EntityKindObjectType, Operation: "bkn.schema.object_type.get", KNID: "kn_demo", Branch: "main", ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "object_type:customer", RefType: RefTypeSchema, Summary: map[string]any{"forbidden_raw": "secret"}}})

	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want one observed read event", len(events))
	}
	event := events[0]
	if event["event_type"] != "knowledge.read.observed" || event["bkn.trace.schema.version"] != "2.1.0" {
		t.Fatalf("unexpected event contract: %#v", event)
	}
	for key, want := range map[string]any{"interaction_id": "int_schema_001", "operation_id": "op_schema_001", "causation_event_id": "evt_retrieval_001", "attempt": 2} {
		if event[key] != want {
			t.Fatalf("%s=%#v, want %#v", key, event[key], want)
		}
	}
	raw, _ := json.Marshal(events)
	for _, forbidden := range []string{"claim.created", "evidence.refs.created", "business.refs.resolved", "summary", "forbidden_raw", "secret"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("events contain forbidden value %q: %s", forbidden, raw)
		}
	}
}

func TestBuildSchemaReadEventsV21AddsRefsOnlyForUpstreamClaim(t *testing.T) {
	req := testRequestContext()
	req.InteractionID = "int_schema_002"
	req.OperationID = "op_schema_002"
	req.ClaimID = "claim_upstream_001"

	events := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{
		EntityKind: EntityKindMetric, Operation: "bkn.schema.metric.get", KNID: "kn_demo", Branch: "main", ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "metric:risk_score", RefType: RefTypeMetric, Summary: map[string]any{"metric_formula": "raw formula"}}})

	if len(events) != 3 {
		t.Fatalf("len(events)=%d, want observed read plus evidence and business refs", len(events))
	}
	if events[1]["event_type"] != "evidence.refs.created" || events[2]["event_type"] != "business.refs.resolved" {
		t.Fatalf("unexpected events: %#v", events)
	}
	for _, event := range events {
		if event["claim_id"] != "claim_upstream_001" {
			t.Fatalf("claim_id=%#v", event["claim_id"])
		}
	}
	assertPayloadKeys(t, events[0], "kn_id", "read_kind", "version_status")
	assertPayloadKeys(t, events[1], "claim_id", "evidence_refs")
	assertPayloadKeys(t, events[2], "claim_id", "resolver_status", "business_refs")
	refs := events[1]["payload"].(map[string]any)["evidence_refs"].([]map[string]any)
	assertRefKeys(t, refs[0])
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(refs[0]["summary_hash"].(string)) {
		t.Fatalf("invalid summary_hash: %#v", refs[0]["summary_hash"])
	}
	raw, _ := json.Marshal(events)
	for _, forbidden := range []string{"claim.created", "summary\"", "metric_formula", "raw formula", "subject_refs", "partial_reason"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("events contain forbidden value %q: %s", forbidden, raw)
		}
	}
}

func TestBuildSchemaReadEventsV21ReportsUnresolvedWithoutLeakingRef(t *testing.T) {
	req := testRequestContext()
	req.ClaimID = "claim_upstream_unresolved"
	events := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{EntityKind: EntityKindObjectType, KNID: "kn_demo"}, nil)
	if len(events) != 2 || events[1]["event_type"] != "business.refs.resolved" {
		t.Fatalf("unexpected unresolved events: %#v", events)
	}
	payload := events[1]["payload"].(map[string]any)
	if payload["resolver_status"] != "unresolved" || len(payload["business_refs"].([]map[string]any)) != 0 {
		t.Fatalf("unexpected unresolved payload: %#v", payload)
	}
	assertPayloadKeys(t, events[1], "claim_id", "resolver_status", "business_refs")
}

func assertPayloadKeys(t *testing.T, event Event, allowed ...string) {
	t.Helper()
	payload := event["payload"].(map[string]any)
	want := map[string]bool{}
	for _, key := range allowed {
		want[key] = true
	}
	for key := range payload {
		if !want[key] {
			t.Fatalf("unregistered payload key %q in %#v", key, payload)
		}
	}
}

func assertRefKeys(t *testing.T, ref map[string]any) {
	t.Helper()
	allowed := map[string]bool{"ref_id": true, "ref_type": true, "source_system": true, "validity": true, "version_status": true, "visibility": true, "summary_hash": true}
	for key := range ref {
		if !allowed[key] {
			t.Fatalf("unregistered ref key %q in %#v", key, ref)
		}
	}
}
