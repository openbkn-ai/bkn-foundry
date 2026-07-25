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

func TestBuildDataQueryEventsV21RecordsFactWithoutFabricatingClaim(t *testing.T) {
	req := testRequestContext()
	req.InteractionID = "int_query_001"
	req.OperationID = "op_query_001"
	req.CausationEventID = "evt_knowledge_001"
	req.Attempt = 3

	events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{
		EntityKind: EntityKindObjectInstance, Operation: "bkn.object.query", KNID: "kn_demo", Branch: "main", SubjectID: "customer",
		QueryHash: HashValue("safe shape"), ReturnedCount: 1, TotalCount: 1,
	}, []EvidenceRef{{RefID: "object_instance:customer:abc", RefType: RefTypeRow, Summary: map[string]any{"row": "secret"}}})

	if len(events) != 1 {
		t.Fatalf("len(events)=%d, want one observed query event", len(events))
	}
	event := events[0]
	if event["event_type"] != "data.query.observed" || event["bkn.trace.schema.version"] != "2.1.0" {
		t.Fatalf("unexpected event contract: %#v", event)
	}
	for key, want := range map[string]any{"interaction_id": "int_query_001", "operation_id": "op_query_001", "causation_event_id": "evt_knowledge_001", "attempt": 3} {
		if event[key] != want {
			t.Fatalf("%s=%#v, want %#v", key, event[key], want)
		}
	}
	raw, _ := json.Marshal(events)
	for _, forbidden := range []string{"claim.created", "evidence.refs.created", "business.refs.resolved", "summary", "row\"", "secret"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("events contain forbidden value %q: %s", forbidden, raw)
		}
	}
}

func TestBuildDataQueryEventsV21AddsRefsOnlyForUpstreamClaim(t *testing.T) {
	req := testRequestContext()
	req.InteractionID = "int_query_002"
	req.OperationID = "op_query_002"
	req.ClaimID = "claim_upstream_002"

	events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{
		EntityKind: EntityKindMetric, Operation: "bkn.metric.get", KNID: "kn_demo", Branch: "main", SubjectID: "risk_score",
		QueryHash: HashValue("safe shape"), ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "metric:risk_score", RefType: RefTypeMetric, Summary: map[string]any{"labels": "secret"}}})

	if len(events) != 3 || events[1]["event_type"] != "evidence.refs.created" || events[2]["event_type"] != "business.refs.resolved" {
		t.Fatalf("unexpected events: %#v", events)
	}
	for _, event := range events {
		if event["claim_id"] != "claim_upstream_002" {
			t.Fatalf("claim_id=%#v", event["claim_id"])
		}
	}
	assertPayloadKeys(t, events[0], "query_hash", "query_type", "row_count", "truncated", "version_status")
	assertPayloadKeys(t, events[1], "claim_id", "evidence_refs")
	assertPayloadKeys(t, events[2], "claim_id", "resolver_status", "business_refs")
	refs := events[1]["payload"].(map[string]any)["evidence_refs"].([]map[string]any)
	assertRefKeys(t, refs[0])
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(refs[0]["summary_hash"].(string)) {
		t.Fatalf("invalid summary_hash: %#v", refs[0]["summary_hash"])
	}
	raw, _ := json.Marshal(events)
	for _, forbidden := range []string{"claim.created", "summary\"", "labels", "secret", "subject_refs", "partial_reason"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("events contain forbidden value %q: %s", forbidden, raw)
		}
	}
}

func TestBuildDataQueryEventsV21ReportsUnresolvedWithoutLeakingRef(t *testing.T) {
	req := testRequestContext()
	req.ClaimID = "claim_upstream_unresolved"
	events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{EntityKind: EntityKindObjectInstance, QueryHash: HashValue("empty")}, nil)
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
