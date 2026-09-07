// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permdata

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/propertyaccess"
)

type fakeResolver struct {
	result Resolution
	err    error
	calls  int
	seen   Request
}

func (fake *fakeResolver) Resolve(_ context.Context, request Request) (Resolution, error) {
	fake.calls++
	fake.seen = request
	return fake.result, fake.err
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

func testRequest(base propertyaccess.Level) Request {
	return Request{
		AccessorID: "user-1",
		Items: []RequestItem{{
			ObjectTypeRef: "kn-1/customer",
			Properties:    []string{"name", "mobile"},
			BaseLevel:     base,
		}},
	}
}

func TestCommunityFallbackUsesBaseLevel(t *testing.T) {
	setEdition(t, licverify.EditionCommunity)
	response, err := Resolve(t.Context(), testRequest(propertyaccess.Schema))
	if err != nil {
		t.Fatal(err)
	}
	for _, property := range response.Entries[0].Properties {
		if property.Level != propertyaccess.Schema || property.Source != SourceObjectType {
			t.Errorf("property = %+v, want schema from object_type", property)
		}
	}
	if Available() {
		t.Fatal("empty community socket must not be available")
	}
}

func TestEnterpriseResolutionIsClampedByBaseLevel(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	fake := &fakeResolver{result: Resolution{Entries: []ResolutionEntry{{
		ObjectTypeRef: "kn-1/customer",
		Properties: []ResolvedProperty{
			{Name: "name", Level: propertyaccess.Full, Explicit: true},
			{Name: "mobile", Level: propertyaccess.None, Explicit: true},
		},
	}}}}
	Register(licverify.EditionEnterprise, fake)

	response, err := Resolve(t.Context(), testRequest(propertyaccess.Schema))
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("resolver calls = %d, want one batch call", fake.calls)
	}
	properties := response.Entries[0].Properties
	if properties[0].Level != propertyaccess.Schema || properties[0].Source != SourceObjectType {
		t.Errorf("explicit full over schema base = %+v, want schema from object_type", properties[0])
	}
	if properties[1].Level != propertyaccess.None || properties[1].Source != SourceProperty {
		t.Errorf("explicit none = %+v, want none from property", properties[1])
	}
}

func TestImplicitResolutionMustBeNeutralFull(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	fake := &fakeResolver{result: Resolution{Entries: []ResolutionEntry{{
		ObjectTypeRef: "kn-1/customer",
		Properties: []ResolvedProperty{
			{Name: "name", Level: propertyaccess.Full},
			{Name: "mobile", Level: propertyaccess.Masked},
		},
	}}}}
	Register(licverify.EditionEnterprise, fake)
	if _, err := Resolve(t.Context(), testRequest(propertyaccess.Full)); !errors.Is(err, ErrInvalidResolution) {
		t.Fatalf("Resolve error = %v, want ErrInvalidResolution", err)
	}
}

func TestUnknownOrIncompleteResolutionFailsClosed(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	for _, test := range []struct {
		name   string
		result Resolution
	}{
		{name: "missing entry", result: Resolution{}},
		{name: "wrong object", result: Resolution{Entries: []ResolutionEntry{{ObjectTypeRef: "kn-2/customer"}}}},
		{name: "missing property", result: Resolution{Entries: []ResolutionEntry{{ObjectTypeRef: "kn-1/customer"}}}},
		{name: "unknown level", result: Resolution{Entries: []ResolutionEntry{{
			ObjectTypeRef: "kn-1/customer",
			Properties: []ResolvedProperty{
				{Name: "name", Level: propertyaccess.Level("future"), Explicit: true},
				{Name: "mobile", Level: propertyaccess.Full},
			},
		}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reset()
			fake := &fakeResolver{result: test.result}
			Register(licverify.EditionEnterprise, fake)
			if _, err := Resolve(t.Context(), testRequest(propertyaccess.Full)); !errors.Is(err, ErrInvalidResolution) {
				t.Fatalf("Resolve error = %v, want ErrInvalidResolution", err)
			}
		})
	}
}

func TestResolverFailureIsReturnedWithoutFallback(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	want := errors.New("property grant store unavailable")
	Register(licverify.EditionEnterprise, &fakeResolver{err: want})
	if _, err := Resolve(t.Context(), testRequest(propertyaccess.Full)); !errors.Is(err, want) {
		t.Fatalf("Resolve error = %v, want %v", err, want)
	}
}

func TestLicenseDowngradeStopsConsultingEnterprise(t *testing.T) {
	reset()
	edition := licverify.EditionEnterprise
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Licensed: edition != licverify.EditionCommunity, Edition: edition}
	}))
	t.Cleanup(func() {
		reset()
		entitlement.ResetForTest()
	})
	fake := &fakeResolver{result: Resolution{Entries: []ResolutionEntry{{
		ObjectTypeRef: "kn-1/customer",
		Properties: []ResolvedProperty{
			{Name: "name", Level: propertyaccess.None, Explicit: true},
			{Name: "mobile", Level: propertyaccess.None, Explicit: true},
		},
	}}}}
	Register(licverify.EditionEnterprise, fake)

	edition = licverify.EditionCommunity
	response, err := Resolve(t.Context(), testRequest(propertyaccess.Full))
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("resolver calls after downgrade = %d, want zero", fake.calls)
	}
	for _, property := range response.Entries[0].Properties {
		if property.Level != propertyaccess.Full || property.Source != SourceObjectType {
			t.Errorf("downgraded property = %+v, want community full", property)
		}
	}
}

func TestRegisterGuardsAssembly(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	fake := &fakeResolver{}
	Register(licverify.EditionEnterprise, fake)
	if !Registered() || !Available() {
		t.Fatal("registered enterprise resolver should be available")
	}

	defer func() {
		if recover() == nil {
			t.Fatal("second registration must panic")
		}
	}()
	Register(licverify.EditionEnterprise, fake)
}

func TestRegisterNilPanics(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	defer func() {
		if recover() == nil {
			t.Fatal("Register(nil) must panic")
		}
	}()
	Register(licverify.EditionEnterprise, nil)
}

func TestRegisterWithoutTierPanics(t *testing.T) {
	setEdition(t, licverify.EditionEnterprise)
	defer func() {
		if recover() == nil {
			t.Fatal("Register without a minimum edition must panic")
		}
	}()
	Register("", &fakeResolver{})
}
