// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package finegrained

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

func TestAvailabilityRequiresAssemblyAndLiveEdition(t *testing.T) {
	entitlement.ResetForTest()
	reset()
	t.Cleanup(func() {
		reset()
		entitlement.ResetForTest()
	})

	edition := licverify.EditionCommunity
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Edition: edition, Licensed: edition != licverify.EditionCommunity}
	}))
	if Assembled() || Available() {
		t.Fatal("Community binary reported the paid shape as assembled or available")
	}

	Register(licverify.EditionProfessional)
	if !Assembled() || Available() {
		t.Fatal("assembled capability became available below Professional")
	}
	registered := false
	for _, capability := range entitlement.Assembled() {
		if capability.Name == Capability && capability.MinEdition == licverify.EditionProfessional {
			registered = true
			break
		}
	}
	if !registered {
		t.Fatalf("%q was not registered at Professional: %+v", Capability, entitlement.Assembled())
	}
	edition = licverify.EditionProfessional
	if !Available() {
		t.Fatal("Professional licence did not enable the assembled capability")
	}
	edition = licverify.EditionIndustry
	if !Available() {
		t.Fatal("Industry did not inherit the Professional capability")
	}
	edition = licverify.EditionCommunity
	if Available() {
		t.Fatal("downgrade did not close the paid request shape")
	}
}

func TestRegisterRejectsDuplicateAssembly(t *testing.T) {
	entitlement.ResetForTest()
	reset()
	t.Cleanup(func() {
		reset()
		entitlement.ResetForTest()
	})
	Register(licverify.EditionProfessional)
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration did not panic")
		}
	}()
	Register(licverify.EditionProfessional)
}
