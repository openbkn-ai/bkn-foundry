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
	"vega-backend/interfaces"
)

func testTraceContext() context.Context {
	traceID := trace.TraceID{0x73, 0x22, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	spanID := trace.SpanID{0x73, 0x22, 0, 0, 0, 0, 0, 1}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), spanContext)
}

func testRequestContext() RequestContext {
	return RequestContext{
		RequestID:      "req_vega_data_0001",
		AccountID:      "acct_demo",
		AccountType:    "user",
		BusinessDomain: "domain_demo",
	}
}

func TestBuildDataQueryEventsUsesResourceAndRowRefsOnly(t *testing.T) {
	resource := &interfaces.Resource{
		ID:               "res_customer_table",
		CatalogID:        "cat_prod",
		Name:             "customer_sensitive_table",
		Category:         interfaces.ResourceCategoryTable,
		SourceIdentifier: "prod.customer_phone_table",
		SchemaDefinition: []*interfaces.Property{
			{Name: "phone", DisplayName: "Phone Number", Description: "Sensitive"},
		},
		UpdateTime: 123,
	}
	rows := []map[string]any{
		{
			"customer_id": "C-10086",
			"name":        "Sensitive Customer",
			"phone":       "13800000000",
		},
	}

	refs := append(ResourceRefs([]*interfaces.Resource{resource}), ResourceRowRefs(resource, rows)...)
	events := BuildDataQueryEvents(testTraceContext(), testRequestContext(), DataQuerySubject{
		Operation:     "data.resource.query",
		ResourceID:    resource.ID,
		CatalogID:     resource.CatalogID,
		QueryHash:     HashValue(map[string]any{"filter": "redacted"}),
		ReturnedCount: len(rows),
		TotalCount:    1,
	}, refs)

	assertSafeEvents(t, events, []string{
		`"event_type":"claim.created"`,
		`"event_type":"evidence.refs.created"`,
		`"ref_type":"resource_ref"`,
		`"ref_type":"row_ref"`,
		`"source_system":"vega-data"`,
		`"row_hash":"sha256:`,
		`"query_hash":"sha256:`,
		`"evidence_refs_hash":"sha256:`,
	}, []string{
		"customer_sensitive_table",
		"prod.customer_phone_table",
		"C-10086",
		"Sensitive Customer",
		"13800000000",
		"customer_id",
		"phone",
		"Phone Number",
		"Sensitive",
	})
}

func TestBuildDataQueryEventsDifferentiatesSameCountDifferentRows(t *testing.T) {
	resource := &interfaces.Resource{ID: "res_customer_table", CatalogID: "cat_prod"}
	subject := DataQuerySubject{
		Operation:     "data.resource.query",
		ResourceID:    resource.ID,
		CatalogID:     resource.CatalogID,
		QueryHash:     HashValue("same-query"),
		ReturnedCount: 1,
		TotalCount:    1,
	}
	first := BuildDataQueryEvents(testTraceContext(), testRequestContext(), subject, ResourceRowRefs(resource, []map[string]any{{"id": "row_1"}}))
	second := BuildDataQueryEvents(testTraceContext(), testRequestContext(), subject, ResourceRowRefs(resource, []map[string]any{{"id": "row_2"}}))

	if claimPayload(t, first)["claim_id"] == claimPayload(t, second)["claim_id"] {
		t.Fatalf("claim_id should differ for same-count different row refs")
	}
	if claimPayload(t, first)["claim_hash"] == claimPayload(t, second)["claim_hash"] {
		t.Fatalf("claim_hash should differ for same-count different row refs")
	}
}

func TestBuildDataQueryEventsRequiresTraceAndRequestContext(t *testing.T) {
	events := BuildDataQueryEvents(context.Background(), testRequestContext(), DataQuerySubject{
		Operation:     "data.resource.query",
		ResourceID:    "res_customer_table",
		CatalogID:     "cat_prod",
		QueryHash:     HashValue("query"),
		ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "row:res:demo", RefType: RefTypeRow}})
	if len(events) != 0 {
		t.Fatalf("len(events)=%d, want 0 without trace context", len(events))
	}

	events = BuildDataQueryEvents(testTraceContext(), RequestContext{}, DataQuerySubject{
		Operation:     "data.resource.query",
		ResourceID:    "res_customer_table",
		CatalogID:     "cat_prod",
		QueryHash:     HashValue("query"),
		ReturnedCount: 1,
	}, []EvidenceRef{{RefID: "row:res:demo", RefType: RefTypeRow}})
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
