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
	traceID := trace.TraceID{0x71, 0x22, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	spanID := trace.SpanID{0x71, 0x22, 0, 0, 0, 0, 0, 1}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), spanContext)
}

func testRequestContext() RequestContext {
	return RequestContext{
		RequestID:      "req_bkn_backend_schema_0001",
		AccountID:      "acct_demo",
		AccountType:    "user",
		BusinessDomain: "domain_demo",
	}
}

func TestBuildSchemaReadEventsUsesRefsAndHashesOnly(t *testing.T) {
	items := []*interfaces.ObjectType{
		{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID:   "customer",
				OTName: "Customer PII",
				DataProperties: []*interfaces.DataProperty{
					{Name: "phone", DisplayName: "Phone Number", Comment: "Do not emit this field"},
				},
			},
			CommonInfo: interfaces.CommonInfo{Comment: "Contains sensitive model notes"},
			KNID:       "kn_demo",
			Branch:     "main",
		},
	}

	events := BuildSchemaReadEvents(testTraceContext(), testRequestContext(), ReadSubject{
		EntityKind:    EntityKindObjectType,
		Operation:     "bkn.schema.object_type.list",
		KNID:          "kn_demo",
		Branch:        "main",
		ReturnedCount: len(items),
	}, ObjectTypeRefs(items))

	assertSafeEvents(t, events, []string{
		`"event_type":"claim.created"`,
		`"event_type":"evidence.refs.created"`,
		`"ref_id":"object_type:customer"`,
		`"ref_type":"schema_ref"`,
		`"summary_hash":"sha256:`,
		`"property_count":1`,
	}, []string{
		"Customer PII",
		"phone",
		"Phone Number",
		"Do not emit this field",
		"Contains sensitive model notes",
	})
}

func TestBuildSchemaReadEventsCoversRelationActionAndMetricRefs(t *testing.T) {
	relationRefs := RelationTypeRefs([]*interfaces.RelationType{
		{
			RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
				RTID:               "customer_has_order",
				RTName:             "Customer Order Relation",
				SourceObjectTypeID: "customer",
				TargetObjectTypeID: "order",
				Type:               interfaces.RELATION_TYPE_DIRECT,
				MappingRules:       []interfaces.Mapping{{SourceProp: interfaces.SimpleProperty{Name: "customer_id"}}},
			},
			CommonInfo: interfaces.CommonInfo{Comment: "Mapping detail must not be emitted"},
		},
	})
	actionRefs := ActionTypeRefs([]*interfaces.ActionType{
		{
			ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
				ATID:         "notify_owner",
				ATName:       "Notify Account Owner",
				ActionType:   "tool",
				ActionIntent: "Send customer phone to owner",
				ObjectTypeID: "customer",
			},
		},
	})
	metricRefs := MetricRefs([]*interfaces.MetricDefinition{
		{
			ID:         "customer_risk_score",
			Name:       "Customer Risk Score",
			MetricType: "derived",
			ScopeType:  "object_type",
			ScopeRef:   "customer",
			CommonInfo: interfaces.CommonInfo{Comment: "Sensitive metric commentary"},
			Branch:     "main",
			KnID:       "kn_demo",
			Unit:       "score",
			UnitType:   "number",
			UpdateTime: 123,
			CreateTime: 100,
			ModuleType: "metric",
		},
	})
	refs := append(append(relationRefs, actionRefs...), metricRefs...)

	events := BuildSchemaReadEvents(testTraceContext(), testRequestContext(), ReadSubject{
		EntityKind:    EntityKindMetric,
		Operation:     "bkn.schema.mixed.test",
		KNID:          "kn_demo",
		Branch:        "main",
		ReturnedCount: len(refs),
	}, refs)

	assertSafeEvents(t, events, []string{
		`"ref_id":"relation_type:customer_has_order"`,
		`"ref_type":"schema_ref"`,
		`"ref_id":"action_type:notify_owner"`,
		`"ref_type":"action_ref"`,
		`"ref_id":"metric:customer_risk_score"`,
		`"ref_type":"metric_ref"`,
		`"version_status":"unversioned"`,
	}, []string{
		"Customer Order Relation",
		"Mapping detail must not be emitted",
		"customer_id",
		"Notify Account Owner",
		"Send customer phone to owner",
		"Customer Risk Score",
		"Sensitive metric commentary",
	})
}

func TestBuildSchemaReadEventsRequiresTraceAndRequestContext(t *testing.T) {
	events := BuildSchemaReadEvents(context.Background(), testRequestContext(), ReadSubject{
		EntityKind:    EntityKindObjectType,
		Operation:     "bkn.schema.object_type.list",
		KNID:          "kn_demo",
		Branch:        "main",
		ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "object_type:customer", RefType: RefTypeSchema}})
	if len(events) != 0 {
		t.Fatalf("len(events)=%d, want 0 without trace context", len(events))
	}

	events = BuildSchemaReadEvents(testTraceContext(), RequestContext{}, ReadSubject{
		EntityKind:    EntityKindObjectType,
		Operation:     "bkn.schema.object_type.list",
		KNID:          "kn_demo",
		Branch:        "main",
		ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "object_type:customer", RefType: RefTypeSchema}})
	if len(events) != 0 {
		t.Fatalf("len(events)=%d, want 0 without request context", len(events))
	}
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
