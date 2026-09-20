// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package rowfilter

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

type fakeResolver struct {
	plan  Plan
	err   error
	calls int
	seen  Request
}

func (fake *fakeResolver) Resolve(_ context.Context, request Request) (Plan, error) {
	fake.calls++
	fake.seen = request
	return fake.plan, fake.err
}

func setEdition(t *testing.T, edition licverify.Edition) {
	t.Helper()
	reset()
	entitlement.SetGateForTest(entitlement.FixedGate(edition))
	t.Cleanup(func() {
		reset()
		entitlement.ResetForTest()
	})
}

func testRequest() Request {
	return Request{
		ObjectTypeRef: "kn-1/customer",
		Caller: Caller{
			UserID:              "user-1",
			RoleIDs:             []string{"sales"},
			DirectDepartmentIDs: []string{"department-1"},
			DepartmentTreeIDs:   []string{"department-1", "department-2"},
		},
	}
}

func TestCommunityFallbackIsTrue(t *testing.T) {
	setEdition(t, licverify.EditionCommunity)
	response, err := Resolve(t.Context(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Plan.Predicate.Kind != PredicateTrue {
		t.Fatalf("fallback predicate = %+v, want TRUE", response.Plan.Predicate)
	}
	if response.EffectiveRowFilterDigest == "" {
		t.Fatal("fallback digest is empty")
	}
	if Available() {
		t.Fatal("empty Community socket must not be available")
	}
}

func TestEnterpriseResolverGetsTrustedCallerAndNormalizedPlan(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	fake := &fakeResolver{plan: Plan{Predicate: Predicate{Kind: PredicateOr, Predicates: []Predicate{
		{Kind: PredicateIn, Property: "region", Values: []Value{{Type: ValueString, String: "south"}, {Type: ValueString, String: "east"}}},
		{Kind: PredicateIn, Property: "owner", Values: []Value{{Type: ValueString, String: "user-1"}}},
	}}}}
	Register(licverify.EditionEnterprise, fake)

	response, err := Resolve(t.Context(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 || fake.seen.Caller.UserID != "user-1" || len(fake.seen.Caller.DepartmentTreeIDs) != 2 {
		t.Fatalf("resolver request = %+v, calls = %d", fake.seen, fake.calls)
	}
	if response.Plan.Predicate.Kind != PredicateOr || response.EffectiveRowFilterDigest == "" {
		t.Fatalf("response = %+v", response)
	}
}

func TestEquivalentPlansHaveSameDigest(t *testing.T) {
	request := testRequest()
	first, err := Fallback(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := responseFor(request.ObjectTypeRef, Plan{Predicate: Predicate{Kind: PredicateOr, Predicates: []Predicate{
		{Kind: PredicateIn, Property: "region", Values: []Value{{Type: ValueString, String: "south"}, {Type: ValueString, String: "east"}}},
		{Kind: PredicateIn, Property: "region", Values: []Value{{Type: ValueString, String: "east"}, {Type: ValueString, String: "south"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	third, err := responseFor(request.ObjectTypeRef, Plan{Predicate: Predicate{Kind: PredicateIn, Property: "region", Values: []Value{{Type: ValueString, String: "east"}, {Type: ValueString, String: "south"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if second.EffectiveRowFilterDigest != third.EffectiveRowFilterDigest {
		t.Fatalf("equivalent plans got %q and %q", second.EffectiveRowFilterDigest, third.EffectiveRowFilterDigest)
	}
	if first.EffectiveRowFilterDigest == second.EffectiveRowFilterDigest {
		t.Fatal("TRUE and a restricted plan must not share a digest")
	}
}

func TestInvalidPlanFailsClosed(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	for _, plan := range []Plan{
		{Predicate: Predicate{Kind: PredicateIn, Property: "region"}},
		{Predicate: Predicate{Kind: PredicateIn, Property: "region", Values: []Value{{Type: ValueString, String: "east"}, {Type: ValueString, String: "east"}}}},
		{Predicate: Predicate{Kind: PredicateTrue, Property: "region"}},
		{Predicate: Predicate{Kind: PredicateKind("sql"), Property: "region"}},
	} {
		reset()
		Register(licverify.EditionEnterprise, &fakeResolver{plan: plan})
		if _, err := Resolve(t.Context(), testRequest()); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("Resolve(%+v) error = %v, want ErrInvalidPlan", plan, err)
		}
	}
}

func TestDepartmentTreeSizedPlanIsNotLimitedLikeValueSet(t *testing.T) {
	values := make([]Value, 0, 101)
	for index := 0; index < 101; index++ {
		values = append(values, Value{Type: ValueString, String: strconv.Itoa(index)})
	}
	plan, err := Normalize(Plan{Predicate: Predicate{Kind: PredicateIn, Property: "department_id", Values: values}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Predicate.Values) != 101 {
		t.Fatalf("normalized values = %d, want 101", len(plan.Predicate.Values))
	}
}

func TestResolverFailureDoesNotFallBack(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	want := errors.New("row-filter store unavailable")
	Register(licverify.EditionEnterprise, &fakeResolver{err: want})
	if _, err := Resolve(t.Context(), testRequest()); !errors.Is(err, want) {
		t.Fatalf("Resolve error = %v, want %v", err, want)
	}
}

func TestLicenseDowngradeStopsResolver(t *testing.T) {
	reset()
	edition := licverify.EditionEnterprise
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Licensed: edition != licverify.EditionCommunity, Edition: edition}
	}))
	t.Cleanup(func() {
		reset()
		entitlement.ResetForTest()
	})
	fake := &fakeResolver{plan: Plan{Predicate: Predicate{Kind: PredicateFalse}}}
	Register(licverify.EditionEnterprise, fake)
	edition = licverify.EditionCommunity
	response, err := Resolve(t.Context(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 || response.Plan.Predicate.Kind != PredicateTrue {
		t.Fatalf("downgrade response = %+v, calls = %d", response, fake.calls)
	}
}

func TestRegisterGuardsAssembly(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	resolver := &fakeResolver{}
	Register(licverify.EditionEnterprise, resolver)
	if !Registered() || !Available() {
		t.Fatal("registered Enterprise resolver should be available")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("second registration must panic")
		}
	}()
	Register(licverify.EditionEnterprise, resolver)
}

func TestRegisterRejectsNonEnterpriseMinimum(t *testing.T) {
	for _, edition := range []licverify.Edition{
		licverify.EditionCommunity,
		licverify.EditionProfessional,
		licverify.EditionIndustry,
		"",
	} {
		t.Run(string(edition), func(t *testing.T) {
			setEdition(t, licverify.EditionIndustry)
			defer func() {
				if recover() == nil {
					t.Fatalf("Register(%q) must panic", edition)
				}
			}()
			Register(edition, &fakeResolver{})
		})
	}
}
