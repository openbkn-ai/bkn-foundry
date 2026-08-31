// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package businessresolver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ibusinessresolver"
)

func TestResolverUsesAuthorizedBKNAndVegaAPIs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-account-id") != "user-1" || r.Header.Get("x-account-type") != "user" {
			t.Errorf("missing trusted identity headers: %+v", r.Header)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Header.Get("Authorization") != "Bearer user-access-token" {
			t.Errorf("missing user authorization header")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/bkn-backend/in/v1/knowledge-networks/supplychain":
			_, _ = w.Write([]byte(`{"id":"supplychain","name":"供应链知识网络","branch":"main"}`))
		case "/api/bkn-backend/in/v1/knowledge-networks/supplychain/object-types/forecast":
			_, _ = w.Write([]byte(`{"entries":[{"id":"forecast","name":"产品需求预测单","branch":"main","data_properties":[{"name":"forecast_month","display_name":"预测月份"}],"logic_properties":[{"name":"forecast_total","display_name":"预测总量"}]}]}`))
		case "/api/vega-backend/in/v1/resources/resource-1":
			_, _ = w.Write([]byte(`{"entries":[{"id":"resource-1","name":"需求预测数据","schema_definition":[{"name":"forecast_month","display_name":"预测月份"}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	resolver := New(server.URL, server.URL, server.Client())
	result, err := resolver.ResolveBusinessRefs(context.Background(), ibusinessresolver.ResolveRequest{
		Scope: evidencevo.QueryScope{
			AccountID:     "user-1",
			AccountType:   "user",
			Authorization: "Bearer user-access-token",
		},
		Refs: []ibusinessresolver.BusinessRef{
			{RefID: "kn:supplychain", RefType: "knowledge_network", SourceSystem: "bkn"},
			{RefID: "object:supplychain:forecast", RefType: "object_type", SourceSystem: "bkn"},
			{RefID: "property:supplychain:forecast:forecast_month", RefType: "property", SourceSystem: "bkn"},
			{RefID: "logic:supplychain:forecast:forecast_total", RefType: "logic", SourceSystem: "bkn"},
			{RefID: "resource:resource-1", RefType: "data_resource", SourceSystem: "vega"},
			{RefID: "field:resource-1:forecast_month", RefType: "data_field", SourceSystem: "vega"},
		},
	})
	if err != nil {
		t.Fatalf("resolve refs: %v", err)
	}
	want := map[string]string{
		"kn:supplychain": "供应链知识网络", "object:supplychain:forecast": "产品需求预测单",
		"property:supplychain:forecast:forecast_month": "预测月份", "resource:resource-1": "需求预测数据",
		"field:resource-1:forecast_month": "预测月份", "logic:supplychain:forecast:forecast_total": "预测总量",
	}
	wantVersion := map[string]string{
		"kn:supplychain":                               "main",
		"object:supplychain:forecast":                  "main",
		"property:supplychain:forecast:forecast_month": "main",
	}
	for _, resolution := range result {
		if resolution.Visibility != "visible" || resolution.Display == nil || resolution.Display.Name != want[resolution.RefID] {
			t.Fatalf("unexpected resolution: %+v", resolution)
		}
		if version, ok := wantVersion[resolution.RefID]; ok && resolution.Display.SourceVersion != version {
			t.Fatalf("source version for %s=%q, want %q", resolution.RefID, resolution.Display.SourceVersion, version)
		}
		delete(want, resolution.RefID)
	}
	if len(want) != 0 {
		t.Fatalf("missing resolutions: %+v", want)
	}
}

func TestResolverRejectsTypeOrSourceSystemConfusion(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"entries":[{"id":"forecast","name":"产品需求预测单"}]}`))
	}))
	defer server.Close()

	result, err := New(server.URL, server.URL, server.Client()).ResolveBusinessRefs(context.Background(), ibusinessresolver.ResolveRequest{
		Refs: []ibusinessresolver.BusinessRef{
			{RefID: "object:supplychain:forecast", RefType: "data_resource", SourceSystem: "vega"},
			{RefID: "resource:secret", RefType: "data_resource", SourceSystem: "bkn"},
			{RefID: "object_instance:supplychain:forecast:row-1", RefType: "object_instance", SourceSystem: "bkn"},
		},
	})
	if err != nil || requests != 0 || len(result) != 3 {
		t.Fatalf("type-confused refs reached an upstream resolver: requests=%d result=%+v err=%v", requests, result, err)
	}
	for _, resolution := range result {
		if resolution.Visibility != "unresolved" || resolution.Display != nil {
			t.Fatalf("unsafe ref was disclosed: %+v", resolution)
		}
	}
}

func TestResolverSupportsExistingEvidenceServiceRefTypes(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/bkn-backend/in/v1/knowledge-networks/supplychain":
			_, _ = w.Write([]byte(`{"id":"supplychain","name":"供应链知识网络"}`))
		case "/api/bkn-backend/in/v1/knowledge-networks/supplychain/action-types/notify_owner":
			_, _ = w.Write([]byte(`{"entries":[{"id":"notify_owner","name":"通知负责人"}]}`))
		case "/api/vega-backend/in/v1/resources/resource-1":
			_, _ = w.Write([]byte(`{"entries":[{"id":"resource-1","name":"需求预测数据","schema_definition":[{"name":"forecast_month","display_name":"预测月份"}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	result, err := New(server.URL, server.URL, server.Client()).ResolveBusinessRefs(context.Background(), ibusinessresolver.ResolveRequest{
		Refs: []ibusinessresolver.BusinessRef{
			{RefID: "kn:supplychain", RefType: "kn", SourceSystem: "context-loader"},
			{RefID: "resource:resource-1", RefType: "resource", SourceSystem: "vega-data"},
			{RefID: "field:resource-1:forecast_month", RefType: "field", SourceSystem: "vega-data"},
			{RefID: "action_type:supplychain:notify_owner", RefType: "action", SourceSystem: "context-loader"},
		},
	})
	if err != nil || len(result) != 4 {
		t.Fatalf("resolve existing evidence service refs: result=%+v err=%v", result, err)
	}
	for _, resolution := range result {
		if resolution.Visibility != "visible" || resolution.Display == nil {
			t.Fatalf("existing evidence service ref was not resolved: %+v", resolution)
		}
	}
}

func TestResolverRecognizesRegisteredProducerSourceAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		provided  string
		authority string
		matches   bool
	}{
		{"bkn", "bkn", true},
		{"bkn-backend", "bkn", true},
		{"context-loader", "bkn", true},
		{"vega", "vega", true},
		{"vega-data", "vega", true},
		{"bkn", "vega", false},
		{"vega-data", "bkn", false},
	}
	for _, test := range tests {
		if got := resolverSourceMatches(test.provided, test.authority); got != test.matches {
			t.Fatalf("source alias %q -> %q matched=%t, want %t", test.provided, test.authority, got, test.matches)
		}
	}
}

func TestResolverKindFallsBackToSupportedPrefixWhenRefTypeIsMissing(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"kn:supplychain": "kn", "object:supplychain:forecast": "object",
		"property:supplychain:forecast:qty": "property", "relation:supplychain:contains": "relation",
		"resource:resource-1": "resource", "field:resource-1:qty": "field",
		"metric:supplychain:total": "metric", "logic:supplychain:forecast:total": "logic",
		"function:supplychain:calculate": "function", "action_type:supplychain:approve": "action_type",
	}
	for refID, wantKind := range tests {
		kind, source := resolverKind(ibusinessresolver.BusinessRef{RefID: refID})
		wantSource := "bkn"
		if wantKind == "resource" || wantKind == "field" {
			wantSource = "vega"
		}
		if kind != wantKind || source != wantSource {
			t.Fatalf("%s resolved to %s/%s, want %s/%s", refID, kind, source, wantKind, wantSource)
		}
	}
}

func TestResolverMapsForbiddenToUnauthorizedWithoutLeakingDisplay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	result, err := New(server.URL, server.URL, server.Client()).ResolveBusinessRefs(context.Background(), ibusinessresolver.ResolveRequest{
		Scope: evidencevo.QueryScope{AccountID: "user-2", AccountType: "user"},
		Refs:  []ibusinessresolver.BusinessRef{{RefID: "resource:secret", RefType: "data_resource", SourceSystem: "vega"}},
	})
	if err != nil || len(result) != 1 || result[0].Visibility != "unauthorized" || result[0].Display != nil {
		t.Fatalf("forbidden response must be opaque unauthorized: result=%+v err=%v", result, err)
	}
}

func TestEntitySourceVersionPrefersAuthorizedSourceMetadata(t *testing.T) {
	if got := entitySourceVersion(namedEntity{Branch: "main", Version: "v12"}); got != "main" {
		t.Fatalf("branch version=%q", got)
	}
	if got := entitySourceVersion(namedEntity{Version: "v12"}); got != "v12" {
		t.Fatalf("resource version=%q", got)
	}
}
