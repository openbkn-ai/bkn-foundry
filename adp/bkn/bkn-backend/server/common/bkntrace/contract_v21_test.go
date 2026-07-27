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

	"bkn-backend/interfaces"
)

func TestBuildSchemaReadEventsV21RecordsReadWithoutFabricatingClaim(t *testing.T) {
	req := testRequestContext()
	req.InteractionID = "int_schema_001"
	req.OperationID = "op_schema_001"
	req.CausationEventID = "evt_retrieval_001"
	req.Attempt = 2

	events := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{
		EntityKind: EntityKindObjectType, Operation: "bkn.schema.object_type.get", KNID: "kn_demo", Branch: "main", ReturnedCount: 1,
	}, []EvidenceRef{
		{RefID: "object:kn_demo:customer", RefType: RefTypeObject},
		{RefID: "property:kn_demo:customer:risk_level", RefType: RefTypeProperty},
	})

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
	payload := event["payload"].(map[string]any)
	assertPayloadKeys(t, event, "kn_id", "read_kind", "version_status", "schema_version", "business_refs")
	refs := payload["business_refs"].([]map[string]any)
	if len(refs) != 2 || refs[0]["ref_type"] != "object" || refs[1]["ref_type"] != "property" {
		t.Fatalf("business_refs=%#v", refs)
	}
	raw, _ := json.Marshal(events)
	for _, forbidden := range []string{"claim.created", "evidence.refs.created", "business.refs.resolved", "claim_id", "summary"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("events contain forbidden value %q: %s", forbidden, raw)
		}
	}
}

func TestBuildSchemaReadEventsV21DoesNotBindFactsToUpstreamClaim(t *testing.T) {
	req := testRequestContext()
	req.InteractionID = "int_schema_002"
	req.OperationID = "op_schema_002"
	req.ClaimID = "claim_upstream_001"

	events := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{
		EntityKind: EntityKindMetric, Operation: "bkn.schema.metric.get", KNID: "kn_demo", Branch: "main", ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "metric:kn_demo:risk_score", RefType: RefTypeMetric}})

	if len(events) != 1 {
		t.Fatalf("unexpected events: %#v", events)
	}
	if _, exists := events[0]["claim_id"]; exists {
		t.Fatalf("fact event must not bind claim: %#v", events[0])
	}
	refs := events[0]["payload"].(map[string]any)["business_refs"].([]map[string]any)
	if len(refs) != 1 || refs[0]["ref_type"] != "metric" {
		t.Fatalf("business_refs=%#v", refs)
	}
	raw, _ := json.Marshal(events)
	for _, forbidden := range []string{"claim.created", "evidence.refs.created", "business.refs.resolved", "claim_upstream_001"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("events contain forbidden value %q: %s", forbidden, raw)
		}
	}
}

func TestBuildSchemaReadEventsV21PreservesRelationSemanticsAndStableReplay(t *testing.T) {
	req := testRequestContext()
	first := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{EntityKind: EntityKindRelationType, KNID: "kn_demo"}, []EvidenceRef{{RefID: "relation:kn_demo:owns", RefType: RefTypeRelation}})
	second := BuildSchemaReadEvents(testTraceContext(), req, ReadSubject{EntityKind: EntityKindRelationType, KNID: "kn_demo"}, []EvidenceRef{{RefID: "relation:kn_demo:owns", RefType: RefTypeRelation}})
	ref := first[0]["payload"].(map[string]any)["business_refs"].([]map[string]any)[0]
	if ref["ref_type"] != "relation" {
		t.Fatalf("relation ref mapped incorrectly: %#v", ref)
	}
	if first[0]["event_id"] != second[0]["event_id"] {
		t.Fatalf("event_id is not replay stable: %q != %q", first[0]["event_id"], second[0]["event_id"])
	}
}

func TestSchemaRefsAreKnowledgeNetworkQualifiedAndNeverGuessed(t *testing.T) {
	refs := ObjectTypeRefs([]*interfaces.ObjectType{
		{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "customer", DataProperties: []*interfaces.DataProperty{{Name: "risk_level"}}}, KNID: "kn_a"},
		{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "customer"}, KNID: "kn_b"},
		{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "customer"}},
	})
	if len(refs) != 3 || refs[0].RefID != "object:kn_a:customer" || refs[1].RefID != "property:kn_a:customer:risk_level" || refs[2].RefID != "object:kn_b:customer" {
		t.Fatalf("refs are ambiguous or guessed: %#v", refs)
	}
	relationRefs := RelationTypeRefs([]*interfaces.RelationType{{KNID: "kn_a", RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "owns"}}})
	if len(relationRefs) != 1 || relationRefs[0].RefID != "relation:kn_a:owns" || relationRefs[0].RefType != RefTypeRelation {
		t.Fatalf("relation ref is not qualified and semantic: %#v", relationRefs)
	}
	actionRefs := ActionTypeRefs([]*interfaces.ActionType{{KNID: "kn_a", ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "notify"}}})
	metricRefs := MetricRefs([]*interfaces.MetricDefinition{{KnID: "kn_a", ID: "risk_score"}})
	if len(actionRefs) != 1 || actionRefs[0].RefID != "action_type:kn_a:notify" || len(metricRefs) != 1 || metricRefs[0].RefID != "metric:kn_a:risk_score" {
		t.Fatalf("action/metric refs are not qualified: actions=%#v metrics=%#v", actionRefs, metricRefs)
	}
	events := BuildSchemaReadEvents(testTraceContext(), testRequestContext(), ReadSubject{EntityKind: EntityKindObjectType, KNID: "kn_a"}, []EvidenceRef{{RefID: "object:kn_b:customer", RefType: RefTypeObject}})
	if refs := events[0]["payload"].(map[string]any)["business_refs"].([]map[string]any); len(refs) != 0 {
		t.Fatalf("fact accepted a ref from another knowledge network: %#v", refs)
	}
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
