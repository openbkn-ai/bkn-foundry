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

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"go.opentelemetry.io/otel/trace"
)

func testTraceContext() context.Context {
	traceID := trace.TraceID{0x71, 0x21, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	spanID := trace.SpanID{0x71, 0x21, 0, 0, 0, 0, 0, 1}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	ctx = common.SetTraceContextToCtx(ctx, common.TraceContext{
		RequestID: "req_context_loader_phase2_0001",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID:   "acct_demo",
		AccountType: interfaces.AccessorType("user"),
	})
	return ctx
}

func TestBuildSearchSchemaEventsUsesHashAndRefsOnly(t *testing.T) {
	maxConcepts := 5
	includeColumns := true
	req := &interfaces.SearchSchemaReq{
		Query:          "customer phone and complaint risk",
		KnID:           "kn_demo",
		MaxConcepts:    &maxConcepts,
		IncludeColumns: &includeColumns,
	}
	resp := &interfaces.SearchSchemaResp{
		ObjectTypes: []any{
			map[string]any{
				"concept_id":  "customer",
				"name":        "Customer",
				"comment":     "Contains phone fields and must not be emitted",
				"module_type": "object_type",
				"_score":      0.91,
			},
		},
		RelationTypes: []any{
			map[string]any{
				"concept_id":            "customer_has_complaint",
				"source_object_type_id": "customer",
				"target_object_type_id": "complaint",
				"_score":                0.73,
			},
		},
		ActionTypes: []any{
			map[string]any{
				"id":             "notify_owner",
				"object_type_id": "customer",
			},
		},
	}

	events := BuildSearchSchemaEvents(testTraceContext(), req, resp)
	if len(events) != 2 {
		t.Fatalf("len(events)=%d, want 2", len(events))
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"event_type":"claim.created"`) {
		t.Fatalf("missing claim.created event: %s", text)
	}
	if !strings.Contains(text, `"event_type":"evidence.refs.created"`) {
		t.Fatalf("missing evidence.refs.created event: %s", text)
	}
	for _, leaked := range []string{"customer phone and complaint risk", "Customer", "Contains phone fields"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("event leaked raw content %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, `"query_hash":"sha256:`) {
		t.Fatalf("missing query hash: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"object_type:customer"`) {
		t.Fatalf("missing object type ref: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"relation_type:customer_has_complaint"`) {
		t.Fatalf("missing relation type ref: %s", text)
	}
	if !strings.Contains(text, `"ref_id":"action_type:notify_owner"`) {
		t.Fatalf("missing action type ref: %s", text)
	}
}

func TestBuildSearchSchemaEventsRequiresTraceContext(t *testing.T) {
	maxConcepts := 5
	events := BuildSearchSchemaEvents(context.Background(), &interfaces.SearchSchemaReq{
		Query:       "schema",
		KnID:        "kn_demo",
		MaxConcepts: &maxConcepts,
	}, &interfaces.SearchSchemaResp{
		ObjectTypes: []any{map[string]any{"concept_id": "customer"}},
	})
	if len(events) != 0 {
		t.Fatalf("len(events)=%d, want 0", len(events))
	}
}

func TestBuildQueryObjectInstanceEventsUsesRowRefsOnly(t *testing.T) {
	req := &interfaces.QueryObjectInstancesReq{
		KnID:  "kn_demo",
		OtID:  "customer",
		Limit: 10,
		Filters: []interfaces.FlatFilter{
			{Field: "phone", Op: interfaces.KnOperationTypeEqual, Value: "18800001111"},
		},
		Properties: []string{"customer_name", "phone"},
	}
	resp := &interfaces.QueryObjectInstancesResp{
		Data: []any{
			map[string]any{
				"_instance_identity": map[string]any{"customer_id": "cust_001"},
				"customer_name":      "Alice",
				"phone":              "18800001111",
			},
		},
		SearchAfter: []any{"cursor_001"},
	}

	events := BuildQueryObjectInstanceEvents(testTraceContext(), req, resp)
	if len(events) != 2 {
		t.Fatalf("len(events)=%d, want 2", len(events))
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"event_type":"claim.created"`) {
		t.Fatalf("missing claim.created event: %s", text)
	}
	if !strings.Contains(text, `"event_type":"evidence.refs.created"`) {
		t.Fatalf("missing evidence.refs.created event: %s", text)
	}
	if !strings.Contains(text, `"ref_type":"row_ref"`) {
		t.Fatalf("missing row_ref evidence: %s", text)
	}
	if !strings.Contains(text, `"condition_hash":"sha256:`) {
		t.Fatalf("missing condition hash: %s", text)
	}
	if !strings.Contains(text, `"properties_hash":"sha256:`) {
		t.Fatalf("missing properties hash: %s", text)
	}
	if !strings.Contains(text, `"truncated":true`) {
		t.Fatalf("missing truncation signal: %s", text)
	}
	for _, leaked := range []string{"18800001111", "Alice", "cust_001", "customer_name"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("event leaked raw object query content %q: %s", leaked, text)
		}
	}
}
