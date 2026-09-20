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
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

type fakeResolver struct {
	resolution Resolution
	err        error
	calls      int
	seen       Request
}

func (fake *fakeResolver) Resolve(_ context.Context, request Request) (Resolution, error) {
	fake.calls++
	fake.seen = request
	return fake.resolution, fake.err
}

type fakeLifecycle struct {
	objectTypeRef string
}

func (fake *fakeLifecycle) DeleteSubject(context.Context, *gorm.DB, SubjectType, string) error {
	return nil
}

func (fake *fakeLifecycle) DeleteObjectType(_ context.Context, objectTypeRef string) error {
	fake.objectTypeRef = objectTypeRef
	return nil
}

func (fake *fakeLifecycle) DeleteKnowledgeNetwork(context.Context, string) error {
	return nil
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
		ObjectTypeRefs: []string{"kn-1/customer"},
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
	if len(response.Entries) != 1 || response.Entries[0].Plan.Predicate.Kind != PredicateTrue {
		t.Fatalf("fallback response = %+v, want one TRUE entry", response)
	}
	if response.Entries[0].EffectiveRowFilterDigest == "" {
		t.Fatal("fallback digest is empty")
	}
	if Available() {
		t.Fatal("empty Community socket must not be available")
	}
}

func TestEnterpriseResolverGetsTrustedCallerAndNormalizedPlan(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	plan := Plan{Predicate: Predicate{Kind: PredicateOr, Predicates: []Predicate{
		{Kind: PredicateIn, Property: "region", Values: []Value{{Type: ValueString, String: "south"}, {Type: ValueString, String: "east"}}},
		{Kind: PredicateIn, Property: "owner", Values: []Value{{Type: ValueString, String: "user-1"}}},
	}}}
	fake := &fakeResolver{resolution: Resolution{Decisions: []Decision{{ObjectTypeRef: "kn-1/customer", Plan: plan}}}}
	Register(licverify.EditionEnterprise, fake)

	response, err := Resolve(t.Context(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 || fake.seen.Caller.UserID != "user-1" || len(fake.seen.Caller.DepartmentTreeIDs) != 2 {
		t.Fatalf("resolver request = %+v, calls = %d", fake.seen, fake.calls)
	}
	if len(response.Entries) != 1 || response.Entries[0].Plan.Predicate.Kind != PredicateOr || response.Entries[0].EffectiveRowFilterDigest == "" {
		t.Fatalf("response = %+v", response)
	}
}

func TestEnterpriseWithoutResolverFailsClosed(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	if _, err := Resolve(t.Context(), testRequest()); !errors.Is(err, ErrResolverUnavailable) {
		t.Fatalf("Resolve without Enterprise resolver error = %v, want %v", err, ErrResolverUnavailable)
	}
}

func TestBatchResolutionMustCoverExactlyRequestedObjectTypes(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	request := testRequest()
	request.ObjectTypeRefs = []string{"kn-1/customer", "kn-1/order"}
	Register(licverify.EditionEnterprise, &fakeResolver{resolution: Resolution{Decisions: []Decision{
		{ObjectTypeRef: "kn-1/customer", Plan: Plan{Predicate: Predicate{Kind: PredicateTrue}}},
	}}})
	if _, err := Resolve(t.Context(), request); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("partial batch error = %v, want %v", err, ErrInvalidPlan)
	}
}

func TestLifecycleUsesRegisteredEnterpriseCleaner(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	lifecycle := &fakeLifecycle{}
	RegisterLifecycle(lifecycle)
	if err := DeleteObjectType(t.Context(), "kn-1/customer"); err != nil {
		t.Fatal(err)
	}
	if lifecycle.objectTypeRef != "kn-1/customer" {
		t.Fatalf("cleaned object_type_ref = %q", lifecycle.objectTypeRef)
	}
}

func TestEquivalentPlansHaveSameDigest(t *testing.T) {
	request := testRequest()
	first, err := Fallback(request)
	if err != nil {
		t.Fatal(err)
	}
	secondPlan := Plan{Predicate: Predicate{Kind: PredicateOr, Predicates: []Predicate{
		{Kind: PredicateIn, Property: "region", Values: []Value{{Type: ValueString, String: "south"}, {Type: ValueString, String: "east"}}},
		{Kind: PredicateIn, Property: "region", Values: []Value{{Type: ValueString, String: "east"}, {Type: ValueString, String: "south"}}},
	}}}
	second, err := responseFor(request, Resolution{Decisions: []Decision{{ObjectTypeRef: "kn-1/customer", Plan: secondPlan}}})
	if err != nil {
		t.Fatal(err)
	}
	thirdPlan := Plan{Predicate: Predicate{Kind: PredicateIn, Property: "region", Values: []Value{{Type: ValueString, String: "east"}, {Type: ValueString, String: "south"}}}}
	third, err := responseFor(request, Resolution{Decisions: []Decision{{ObjectTypeRef: "kn-1/customer", Plan: thirdPlan}}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Entries[0].EffectiveRowFilterDigest != third.Entries[0].EffectiveRowFilterDigest {
		t.Fatalf("equivalent plans got %q and %q", second.Entries[0].EffectiveRowFilterDigest, third.Entries[0].EffectiveRowFilterDigest)
	}
	if first.Entries[0].EffectiveRowFilterDigest == second.Entries[0].EffectiveRowFilterDigest {
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
		Register(licverify.EditionEnterprise, &fakeResolver{resolution: Resolution{Decisions: []Decision{{ObjectTypeRef: "kn-1/customer", Plan: plan}}}})
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
	fake := &fakeResolver{resolution: Resolution{Decisions: []Decision{{ObjectTypeRef: "kn-1/customer", Plan: Plan{Predicate: Predicate{Kind: PredicateFalse}}}}}}
	Register(licverify.EditionEnterprise, fake)
	edition = licverify.EditionCommunity
	response, err := Resolve(t.Context(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 || len(response.Entries) != 1 || response.Entries[0].Plan.Predicate.Kind != PredicateTrue {
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
