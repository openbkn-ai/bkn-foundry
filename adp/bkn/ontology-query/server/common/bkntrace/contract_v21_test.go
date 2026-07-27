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
	}, []EvidenceRef{
		{RefID: "object:kn_demo:customer", RefType: RefTypeObject},
		{RefID: "property:kn_demo:customer:risk_level", RefType: RefTypeProperty},
	})

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
	assertPayloadKeys(t, event, "query_hash", "query_type", "row_count", "truncated", "version_status", "resource_refs", "field_refs")
	payload := event["payload"].(map[string]any)
	if len(payload["resource_refs"].([]map[string]any)) != 1 || len(payload["field_refs"].([]map[string]any)) != 1 {
		t.Fatalf("query refs missing: %#v", payload)
	}
	raw, _ := json.Marshal(events)
	for _, forbidden := range []string{"claim.created", "evidence.refs.created", "business.refs.resolved", "claim_id", "summary", "row\"", "secret"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("events contain forbidden value %q: %s", forbidden, raw)
		}
	}
}

func TestBuildDataQueryEventsV21DoesNotBindFactToUpstreamClaim(t *testing.T) {
	req := testRequestContext()
	req.InteractionID = "int_query_002"
	req.OperationID = "op_query_002"
	req.ClaimID = "claim_upstream_002"

	events := BuildDataQueryEvents(testTraceContext(), req, DataQuerySubject{
		EntityKind: EntityKindMetric, Operation: "bkn.metric.get", KNID: "kn_demo", Branch: "main", SubjectID: "risk_score",
		QueryHash: HashValue("safe shape"), ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "metric:kn_demo:risk_score", RefType: RefTypeMetric}})

	if len(events) != 1 {
		t.Fatalf("unexpected events: %#v", events)
	}
	if _, exists := events[0]["claim_id"]; exists {
		t.Fatalf("fact event must not bind claim: %#v", events[0])
	}
	if strings.Contains(mustJSON(events), "claim_upstream_002") {
		t.Fatalf("upstream claim leaked into fact: %s", mustJSON(events))
	}
}

func TestBuildDataQueryEventsV21StableReplay(t *testing.T) {
	req := testRequestContext()
	subject := DataQuerySubject{EntityKind: EntityKindMetric, QueryHash: HashValue("same")}
	first := BuildDataQueryEvents(testTraceContext(), req, subject, []EvidenceRef{{RefID: "metric:kn_demo:risk", RefType: RefTypeMetric}})
	second := BuildDataQueryEvents(testTraceContext(), req, subject, []EvidenceRef{{RefID: "metric:kn_demo:risk", RefType: RefTypeMetric}})
	if first[0]["event_id"] != second[0]["event_id"] {
		t.Fatalf("event_id is not replay stable: %q != %q", first[0]["event_id"], second[0]["event_id"])
	}
}

func mustJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
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
