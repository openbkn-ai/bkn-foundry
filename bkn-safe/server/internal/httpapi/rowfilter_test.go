// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/licverify"

	coresocket "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/rowfilter"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

type rowFilterResolverStub struct {
	resolution coresocket.Resolution
	err        error
}

func (stub rowFilterResolverStub) Resolve(_ context.Context, _ coresocket.Request) (coresocket.Resolution, error) {
	return stub.resolution, stub.err
}

func registerEnterpriseRowFilterForTest(t *testing.T, resolver coresocket.Resolver) {
	t.Helper()
	coresocket.ResetForTest()
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	coresocket.Register(licverify.EditionEnterprise, resolver)
	t.Cleanup(func() {
		coresocket.ResetForTest()
		entitlement.ResetForTest()
	})
}

func TestRowFilterReturnsCommunityFallbackForTrustedUser(t *testing.T) {
	router, _, db := newTestServer(t)
	seedEnabledUser(t, db, "user-1")

	response := do(t, router, http.MethodPost, "/api/safe/v1/authz/row-filters", map[string]any{
		"accessor_id": "user-1", "object_type_refs": []string{"kn-1/customer"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("row-filter = %d: %s", response.Code, response.Body.String())
	}
	var body rowFilterResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Entries) != 1 || body.Entries[0].Predicate.Kind != "true" || body.Entries[0].EffectiveRowFilterDigest == "" {
		t.Fatalf("row-filter response = %+v, want normalized true plan and digest", body)
	}
}

func TestRowFilterBatchReturnsEveryRequestedObjectTypeInRequestOrder(t *testing.T) {
	registerEnterpriseRowFilterForTest(t, rowFilterResolverStub{resolution: coresocket.Resolution{Decisions: []coresocket.Decision{
		{ObjectTypeRef: "kn-1/customer", Plan: coresocket.Plan{Predicate: coresocket.Predicate{Kind: coresocket.PredicateTrue}}},
		{ObjectTypeRef: "kn-1/order", Plan: coresocket.Plan{Predicate: coresocket.Predicate{Kind: coresocket.PredicateFalse}}},
	}}})
	router, _, db := newTestServer(t)
	seedEnabledUser(t, db, "user-1")

	response := do(t, router, http.MethodPost, "/api/safe/v1/authz/row-filters", map[string]any{
		"accessor_id": "user-1", "object_type_refs": []string{"kn-1/order", "kn-1/customer"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("row-filters = %d: %s", response.Code, response.Body.String())
	}
	var body rowFilterResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Entries) != 2 || body.Entries[0].ObjectTypeRef != "kn-1/order" || body.Entries[0].Predicate.Kind != coresocket.PredicateFalse || body.Entries[1].ObjectTypeRef != "kn-1/customer" || body.Entries[1].Predicate.Kind != coresocket.PredicateTrue {
		t.Fatalf("batch response = %+v", body)
	}
}

func TestRowFilterActiveEnterpriseWithoutResolverIsUnavailable(t *testing.T) {
	coresocket.ResetForTest()
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	t.Cleanup(func() {
		coresocket.ResetForTest()
		entitlement.ResetForTest()
	})
	router, _, db := newTestServer(t)
	seedEnabledUser(t, db, "user-1")

	response := do(t, router, http.MethodPost, "/api/safe/v1/authz/row-filters", map[string]any{
		"accessor_id": "user-1", "object_type_refs": []string{"kn-1/customer"},
	})
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("row-filters without resolver = %d, want 503: %s", response.Code, response.Body.String())
	}
	ready := do(t, router, http.MethodGet, "/health/ready", nil)
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready without resolver = %d, want 503: %s", ready.Code, ready.Body.String())
	}
}

func TestRowFilterRejectsUntrustedCallerAndMalformedTarget(t *testing.T) {
	router, _, _ := newTestServer(t)
	for name, body := range map[string]map[string]any{
		"missing caller": {"accessor_id": "missing", "object_type_refs": []string{"kn-1/customer"}},
		"bad target":     {"accessor_id": "missing", "object_type_refs": []string{"not/a/valid/ref"}},
	} {
		t.Run(name, func(t *testing.T) {
			response := do(t, router, http.MethodPost, "/api/safe/v1/authz/row-filters", body)
			if name == "bad target" && response.Code != http.StatusBadRequest {
				t.Fatalf("row-filter = %d, want 400: %s", response.Code, response.Body.String())
			}
			if name == "missing caller" && response.Code != http.StatusServiceUnavailable {
				t.Fatalf("row-filter = %d, want 503: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRowFilterPreservesRestrictedPredicateValueTypes(t *testing.T) {
	plan := coresocket.Plan{Predicate: coresocket.Predicate{
		Kind: coresocket.PredicateOr,
		Predicates: []coresocket.Predicate{
			{Kind: coresocket.PredicateIn, Property: "owner_id", Values: []coresocket.Value{{Type: coresocket.ValueString, String: ""}}},
			{Kind: coresocket.PredicateIn, Property: "priority", Values: []coresocket.Value{{Type: coresocket.ValueInteger, Integer: 0}}},
			{Kind: coresocket.PredicateIn, Property: "active", Values: []coresocket.Value{{Type: coresocket.ValueBoolean, Boolean: false}}},
		},
	}}
	registerEnterpriseRowFilterForTest(t, rowFilterResolverStub{resolution: coresocket.Resolution{Decisions: []coresocket.Decision{{ObjectTypeRef: "kn-1/customer", Plan: plan}}}})
	router, _, db := newTestServer(t)
	seedEnabledUser(t, db, "user-1")

	response := do(t, router, http.MethodPost, "/api/safe/v1/authz/row-filters", map[string]any{
		"accessor_id": "user-1", "object_type_refs": []string{"kn-1/customer"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("row-filter = %d: %s", response.Code, response.Body.String())
	}
	var body rowFilterResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Entries) != 1 || body.Entries[0].Predicate.Kind != coresocket.PredicateOr || len(body.Entries[0].Predicate.Predicates) != 3 {
		t.Fatalf("response = %+v, want one entry with three restricted branches", body)
	}
	seen := map[coresocket.ValueType]rowFilterValue{}
	for _, predicate := range body.Entries[0].Predicate.Predicates {
		if len(predicate.Values) != 1 {
			t.Fatalf("predicate values = %+v, want exactly one", predicate.Values)
		}
		seen[predicate.Values[0].Type] = predicate.Values[0]
	}
	if value := seen[coresocket.ValueString]; value.String == nil || *value.String != "" || value.Integer != nil || value.Boolean != nil {
		t.Fatalf("string value = %+v, want typed empty string", value)
	}
	if value := seen[coresocket.ValueInteger]; value.Integer == nil || *value.Integer != 0 || value.String != nil || value.Boolean != nil {
		t.Fatalf("integer value = %+v, want typed zero", value)
	}
	if value := seen[coresocket.ValueBoolean]; value.Boolean == nil || *value.Boolean || value.String != nil || value.Integer != nil {
		t.Fatalf("boolean value = %+v, want typed false", value)
	}
}

func TestRowFilterResolverFailureIsUnavailable(t *testing.T) {
	registerEnterpriseRowFilterForTest(t, rowFilterResolverStub{err: errors.New("policy store unavailable")})
	router, _, db := newTestServer(t)
	seedEnabledUser(t, db, "user-1")

	response := do(t, router, http.MethodPost, "/api/safe/v1/authz/row-filters", map[string]any{
		"accessor_id": "user-1", "object_type_refs": []string{"kn-1/customer"},
	})
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("row-filter = %d, want 503: %s", response.Code, response.Body.String())
	}
}

func TestRowFilterUsesCanonicalSafeObjectReference(t *testing.T) {
	objectTypeRef := strings.Repeat("k", 40) + "/" + strings.Repeat("o", 40)
	if !coresocket.ValidObjectTypeRef(objectTypeRef) {
		t.Fatalf("ValidObjectTypeRef(%q) = false", objectTypeRef)
	}
	if coresocket.ValidObjectTypeRef(objectTypeRef+"x") || coresocket.ValidObjectTypeRef("kn//object") || coresocket.ValidObjectTypeRef(" kn/object") {
		t.Fatal("invalid canonical object type references were accepted")
	}
}
