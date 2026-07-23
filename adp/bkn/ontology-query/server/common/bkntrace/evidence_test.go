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
	"ontology-query/interfaces"
)

func testTraceContext() context.Context {
	traceID := trace.TraceID{0x72, 0x22, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	spanID := trace.SpanID{0x72, 0x22, 0, 0, 0, 0, 0, 1}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), spanContext)
}

func testRequestContext() RequestContext {
	return RequestContext{
		RequestID:      "req_ontology_data_0001",
		AccountID:      "acct_demo",
		AccountType:    "user",
		BusinessDomain: "domain_demo",
	}
}

func TestBuildDataQueryEventsUsesRowRefsAndHashesOnly(t *testing.T) {
	rows := []map[string]any{
		{
			"_instance_id":       "obj_customer_001",
			"_instance_identity": map[string]any{"customer_id": "C-10086"},
			"name":               "Sensitive Customer",
			"phone":              "13800000000",
		},
	}

	events := BuildDataQueryEvents(testTraceContext(), testRequestContext(), DataQuerySubject{
		EntityKind:    EntityKindObjectInstance,
		Operation:     "bkn.object.query",
		KNID:          "kn_demo",
		Branch:        "main",
		SubjectID:     "customer",
		QueryHash:     HashValue(map[string]any{"condition": "redacted"}),
		ReturnedCount: len(rows),
		TotalCount:    1,
	}, ObjectRowRefs("kn_demo", "main", "customer", rows))

	assertSafeEvents(t, events, []string{
		`"event_type":"claim.created"`,
		`"event_type":"evidence.refs.created"`,
		`"ref_type":"row_ref"`,
		`"source_system":"bkn-ontology"`,
		`"row_hash":"sha256:`,
		`"query_hash":"sha256:`,
		`"evidence_refs_hash":"sha256:`,
	}, []string{
		"C-10086",
		"Sensitive Customer",
		"13800000000",
		"customer_id",
		"phone",
	})
}

func TestBuildDataQueryEventsCoversSubgraphAndMetricRefs(t *testing.T) {
	graph := &interfaces.ObjectSubGraph{
		Objects: map[string]interfaces.ObjectInfoInSubgraph{
			"obj_1": {
				ObjectSystemInfo: interfaces.ObjectSystemInfo{
					InstanceID:       "obj_1",
					InstanceIdentity: map[string]any{"customer_id": "C-10086"},
					Display:          "Sensitive Customer",
				},
				ObjectTypeId:   "customer",
				ObjectTypeName: "Customer",
				Properties:     map[string]any{"phone": "13800000000"},
			},
		},
		RelationPaths: []interfaces.RelationPath{
			{
				Relations: []interfaces.Relation{
					{
						RelationTypeId:   "customer_has_order",
						RelationTypeName: "Customer Has Order",
						SourceObjectId:   "obj_1",
						TargetObjectId:   "obj_2",
					},
				},
				Length: 1,
			},
		},
	}
	metric := []interfaces.Data{
		{
			Labels: map[string]string{"customer_name": "Sensitive Customer"},
			Times:  []any{1720000000000},
			Values: []any{99.9},
		},
	}
	refs := append(SubgraphRefs("kn_demo", "main", graph), MetricDataRefs("kn_demo", "main", "risk_score", metric)...)

	events := BuildDataQueryEvents(testTraceContext(), testRequestContext(), DataQuerySubject{
		EntityKind:    EntityKindMetric,
		Operation:     "bkn.metric.get",
		KNID:          "kn_demo",
		Branch:        "main",
		SubjectID:     "risk_score",
		QueryHash:     HashValue("metric-query-redacted"),
		ReturnedCount: len(metric),
	}, refs)

	assertSafeEvents(t, events, []string{
		`"ref_type":"schema_ref"`,
		`"ref_id":"relation_type:customer_has_order"`,
		`"ref_type":"metric_ref"`,
		`"ref_id":"metric:risk_score"`,
		`"point_count":1`,
	}, []string{
		"C-10086",
		"Sensitive Customer",
		"13800000000",
		"customer_name",
		"Customer Has Order",
	})
}

func TestBuildDataQueryEventsDifferentiatesSameCountDifferentRefs(t *testing.T) {
	subject := DataQuerySubject{
		EntityKind:    EntityKindObjectInstance,
		Operation:     "bkn.object.query",
		KNID:          "kn_demo",
		Branch:        "main",
		SubjectID:     "customer",
		QueryHash:     HashValue("same-query"),
		ReturnedCount: 1,
		TotalCount:    1,
	}
	first := BuildDataQueryEvents(testTraceContext(), testRequestContext(), subject, ObjectRowRefs("kn_demo", "main", "customer", []map[string]any{{"_instance_id": "obj_1"}}))
	second := BuildDataQueryEvents(testTraceContext(), testRequestContext(), subject, ObjectRowRefs("kn_demo", "main", "customer", []map[string]any{{"_instance_id": "obj_2"}}))

	if claimPayload(t, first)["claim_id"] == claimPayload(t, second)["claim_id"] {
		t.Fatalf("claim_id should differ for same-count different row refs")
	}
	if claimPayload(t, first)["claim_hash"] == claimPayload(t, second)["claim_hash"] {
		t.Fatalf("claim_hash should differ for same-count different row refs")
	}
}

func TestBuildDataQueryEventsRequiresTraceAndRequestContext(t *testing.T) {
	events := BuildDataQueryEvents(context.Background(), testRequestContext(), DataQuerySubject{
		EntityKind:    EntityKindObjectInstance,
		Operation:     "bkn.object.query",
		KNID:          "kn_demo",
		Branch:        "main",
		SubjectID:     "customer",
		QueryHash:     HashValue("query"),
		ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "row:customer:demo", RefType: RefTypeRow}})
	if len(events) != 0 {
		t.Fatalf("len(events)=%d, want 0 without trace context", len(events))
	}

	events = BuildDataQueryEvents(testTraceContext(), RequestContext{}, DataQuerySubject{
		EntityKind:    EntityKindObjectInstance,
		Operation:     "bkn.object.query",
		KNID:          "kn_demo",
		Branch:        "main",
		SubjectID:     "customer",
		QueryHash:     HashValue("query"),
		ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "row:customer:demo", RefType: RefTypeRow}})
	if len(events) != 0 {
		t.Fatalf("len(events)=%d, want 0 without request context", len(events))
	}
}

func claimPayload(t *testing.T, events []Event) map[string]any {
	t.Helper()
	if len(events) == 0 {
		t.Fatalf("events are empty")
	}
	payload, ok := events[0]["payload"].(map[string]any)
	if !ok {
		t.Fatalf("claim payload missing or invalid: %#v", events[0]["payload"])
	}
	return payload
}

func assertSafeEvents(t *testing.T, events []Event, want []string, forbidden []string) {
	t.Helper()
	if len(events) != 2 {
		t.Fatalf("len(events)=%d, want 2", len(events))
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	for _, item := range want {
		if !strings.Contains(text, item) {
			t.Fatalf("missing %q in events: %s", item, text)
		}
	}
	for _, item := range forbidden {
		if strings.Contains(text, item) {
			t.Fatalf("event leaked raw content %q: %s", item, text)
		}
	}
}
