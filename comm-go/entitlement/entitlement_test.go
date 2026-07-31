// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/licverify"
)

func fixed(ed licverify.Edition) Gate {
	return GateFunc(func() Snapshot {
		return Snapshot{Licensed: ed != licverify.EditionCommunity, Edition: ed}
	})
}

func mustPanic(t *testing.T, what string, fn func()) string {
	t.Helper()
	var got any
	func() {
		defer func() { got = recover() }()
		fn()
	}()
	if got == nil {
		t.Fatalf("%s: expected panic, got none", what)
	}
	msg, _ := got.(string)
	return msg
}

func TestZeroGateIsCommunity(t *testing.T) {
	reset()
	// Before SetGate runs, nothing paid is on. A wiring mistake must fail
	// towards withholding paid capability, not giving it away.
	if got := Current(); got != licverify.EditionCommunity {
		t.Fatalf("Current() = %q before SetGate, want community", got)
	}
	if Licensed() {
		t.Fatal("Licensed() must be false before a gate is installed")
	}
	if AtLeast(licverify.EditionProfessional) {
		t.Fatal("no paid tier may be satisfied by the zero gate")
	}
}

func TestAtLeastReadsLiveSnapshot(t *testing.T) {
	reset()
	// The gate is consulted on every call: a licence that lapses under a
	// running process has to take effect without a restart, which a cached
	// bool would prevent.
	ed := licverify.EditionEnterprise
	SetGate(GateFunc(func() Snapshot { return Snapshot{Licensed: true, Edition: ed} }))

	if !AtLeast(licverify.EditionEnterprise) {
		t.Fatal("enterprise licence should satisfy the enterprise bar")
	}
	ed = licverify.EditionCommunity
	if AtLeast(licverify.EditionEnterprise) {
		t.Fatal("capability must go dark the moment the licence does")
	}
}

func TestIndustryInheritsEnterprise(t *testing.T) {
	reset()
	SetGate(fixed(licverify.EditionIndustry))
	// The customer who paid the most must not lose capability — this is why
	// the check is AtLeast and not equality.
	for _, min := range []licverify.Edition{
		licverify.EditionCommunity, licverify.EditionProfessional, licverify.EditionEnterprise, licverify.EditionIndustry,
	} {
		if !AtLeast(min) {
			t.Errorf("industry should satisfy %s", min)
		}
	}
}

func TestFeaturesAreCopiedNotShared(t *testing.T) {
	reset()
	SetGate(GateFunc(func() Snapshot {
		return Snapshot{Licensed: true, Edition: licverify.EditionEnterprise, Features: []string{"audit"}}
	}))

	got := Features()
	got[0] = "tampered"
	if again := Features(); again[0] != "audit" {
		t.Fatal("Features must hand out a copy; a caller mutated the snapshot")
	}
}

func TestSnapCopiesFeaturesToo(t *testing.T) {
	reset()
	SetGate(GateFunc(func() Snapshot {
		return Snapshot{Licensed: true, Edition: licverify.EditionEnterprise, Features: []string{"audit"}}
	}))

	// A Snapshot is a value, but its Features field would otherwise share the
	// published snapshot's backing array — an operator endpoint sorting the
	// list in place would rewrite what every other reader sees. Features() has
	// this guarantee under test; without the same here, the copy in Snap looks
	// like removable duplication.
	Snap().Features[0] = "tampered"
	if again := Snap(); again.Features[0] != "audit" {
		t.Fatal("Snap must copy Features; a caller mutated the published snapshot")
	}
}

func TestSetGateAfterFreezePanics(t *testing.T) {
	reset()
	SetGate(fixed(licverify.EditionEnterprise))
	Freeze()

	msg := mustPanic(t, "SetGate after Freeze", func() { SetGate(fixed(licverify.EditionCommunity)) })
	if !strings.Contains(msg, "after Freeze") {
		t.Fatalf("panic should say the registry was frozen, got %q", msg)
	}
}

func TestSetGateNilPanics(t *testing.T) {
	reset()
	mustPanic(t, "SetGate(nil)", func() { SetGate(nil) })
}

func TestMarkAssembledIsIdempotentPerCapability(t *testing.T) {
	reset()
	SetGate(fixed(licverify.EditionEnterprise))

	// One capability routinely has several entry points — an extra tool plus a
	// decorator on an existing one. Both belong to the same capability, and the
	// second must not be an error.
	MarkAssembled("context_probe", licverify.EditionEnterprise)
	MarkAssembled("context_probe", licverify.EditionEnterprise)

	got := Assembled()
	if len(got) != 1 || got[0].Name != "context_probe" || got[0].MinEdition != licverify.EditionEnterprise {
		t.Fatalf("Assembled() = %+v, want one enterprise capability", got)
	}
}

func TestMarkAssembledRejectsConflictingMinimums(t *testing.T) {
	reset()
	MarkAssembled("audit", licverify.EditionEnterprise)
	// Two entry points of one capability disagreeing about what it costs is a
	// wiring bug: whichever registers last would silently decide the price.
	msg := mustPanic(t, "conflicting MinEdition", func() {
		MarkAssembled("audit", licverify.EditionProfessional)
	})
	if !strings.Contains(msg, "two different minimum editions") {
		t.Fatalf("panic should name the conflict, got %q", msg)
	}
}

func TestMarkAssembledRequiresMinEdition(t *testing.T) {
	reset()
	// The zero value would silently register a paid capability as community —
	// the exact failure this package exists to prevent.
	msg := mustPanic(t, "missing MinEdition", func() { MarkAssembled("audit", "") })
	if !strings.Contains(msg, "without a MinEdition") {
		t.Fatalf("panic should point at the missing field, got %q", msg)
	}
}

func TestRegistrationDoesNotDependOnTheLicence(t *testing.T) {
	reset()
	SetGate(fixed(licverify.EditionCommunity))

	// Registration is unconditional on purpose: a certificate installed later
	// must take effect without restarting the process, which is impossible if
	// an unlicensed boot registered nothing.
	MarkAssembled("audit", licverify.EditionEnterprise)
	if len(Assembled()) != 1 {
		t.Fatal("an unlicensed process must still assemble its capabilities")
	}
	if AtLeast(licverify.EditionEnterprise) {
		t.Fatal("…but they must not be usable while unlicensed")
	}
}

func TestMustBeAssemblingAndFreeze(t *testing.T) {
	reset()
	MustBeAssembling("mcptool")
	Freeze()

	msg := mustPanic(t, "register after Freeze", func() { MustBeAssembling("mcptool") })
	if !strings.Contains(msg, "after Freeze") {
		t.Fatalf("panic should say the window is closed, got %q", msg)
	}
	mustPanic(t, "MarkAssembled after Freeze", func() { MarkAssembled("late", licverify.EditionEnterprise) })
	mustPanic(t, "second Freeze", func() { Freeze() })

	if !Frozen() {
		t.Fatal("Frozen() should report the closed registry")
	}
}

func TestCommunityBuildAssemblesNothing(t *testing.T) {
	reset()
	SetGate(fixed(licverify.EditionCommunity))
	Freeze()

	if got := Assembled(); len(got) != 0 {
		t.Fatalf("a community build has no enterprise code to register, got %+v", got)
	}
}

func TestAssembledIsSortedByName(t *testing.T) {
	reset()
	MarkAssembled("zeta", licverify.EditionEnterprise)
	MarkAssembled("alpha", licverify.EditionProfessional)

	got := Assembled()
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "zeta" {
		t.Fatalf("Assembled() should be name-ordered, got %+v", got)
	}
}
