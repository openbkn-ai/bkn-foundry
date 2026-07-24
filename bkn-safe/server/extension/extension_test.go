// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package extension

import (
	"strings"
	"testing"
)

// allowAll is a gate that licenses everything.
func allowAll() Gate { return GateFunc(func(Feature) bool { return true }) }

// only licenses exactly the listed features.
func only(fs ...Feature) Gate {
	set := map[Feature]bool{}
	for _, f := range fs {
		set[f] = true
	}
	return GateFunc(func(f Feature) bool { return set[f] })
}

// mustPanic runs fn and returns the panic value, failing if it did not panic.
func mustPanic(t *testing.T, what string, fn func()) any {
	t.Helper()
	var got any
	func() {
		defer func() { got = recover() }()
		fn()
	}()
	if got == nil {
		t.Fatalf("%s: expected panic, got none", what)
	}
	return got
}

func TestZeroGateDeniesEverything(t *testing.T) {
	reset()
	// Before SetGate runs, nothing paid is on. A wiring mistake must fail
	// closed on paid capability rather than hand it out free.
	for _, f := range []Feature{FeatureRBACBasic, FeaturePermObjectLevel, FeatureAudit} {
		if Enabled(f) {
			t.Errorf("Enabled(%s) = true before SetGate, want false", f)
		}
	}
}

func TestGateReadsLiveSnapshot(t *testing.T) {
	reset()
	// The gate must be consulted on every call — a cached bool would leave an
	// expired license granting capability until restart.
	licensed := true
	SetGate(GateFunc(func(f Feature) bool { return licensed && f == FeatureRBACBasic }))

	if !Enabled(FeatureRBACBasic) {
		t.Fatal("rbac_basic should be on while licensed")
	}
	licensed = false
	if Enabled(FeatureRBACBasic) {
		t.Fatal("rbac_basic should go dark the moment the license does, without a restart")
	}
}

func TestClaimTwicePanics(t *testing.T) {
	reset()
	SetGate(allowAll())
	Claim(FeaturePermObjectLevel, "first")

	got := mustPanic(t, "second Claim", func() { Claim(FeaturePermObjectLevel, "second") })
	msg, _ := got.(string)
	if !strings.Contains(msg, "already registered by first") {
		t.Fatalf("panic message should name the incumbent, got %q", msg)
	}
}

func TestClaimAfterFreezePanics(t *testing.T) {
	reset()
	SetGate(allowAll())
	Freeze()

	got := mustPanic(t, "Claim after Freeze", func() { Claim(FeatureAudit, "late") })
	msg, _ := got.(string)
	if !strings.Contains(msg, "after Freeze") {
		t.Fatalf("panic message should say the registry was frozen, got %q", msg)
	}
}

func TestClaimWithoutLicensePanics(t *testing.T) {
	reset()
	SetGate(only(FeatureRBACBasic))

	// ee is expected to check its license before registering; reaching Claim
	// for an unlicensed feature means that check was skipped.
	got := mustPanic(t, "unlicensed Claim", func() { Claim(FeaturePermObjectLevel, "ee") })
	msg, _ := got.(string)
	if !strings.Contains(msg, "without a license") {
		t.Fatalf("panic message should say the license is missing, got %q", msg)
	}
}

func TestSetGateAfterFreezePanics(t *testing.T) {
	reset()
	SetGate(allowAll())
	Freeze()

	mustPanic(t, "SetGate after Freeze", func() { SetGate(allowAll()) })
}

func TestFreezeTwicePanics(t *testing.T) {
	reset()
	SetGate(allowAll())
	Freeze()

	mustPanic(t, "second Freeze", func() { Freeze() })
}

func TestRegisteredIsSortedAndAvailableTracksLicense(t *testing.T) {
	reset()
	licensed := true
	SetGate(GateFunc(func(Feature) bool { return licensed }))
	Claim(FeaturePermObjectLevel, "permobject")
	Claim(FeatureAudit, "audit")

	got := Registered()
	want := []string{"audit", "perm_object_level"}
	if len(got) != len(want) {
		t.Fatalf("Registered() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Registered() = %v, want %v", got, want)
		}
	}

	if !Available(FeaturePermObjectLevel) {
		t.Fatal("a registered, licensed feature should be available")
	}
	// A license that lapses after assembly leaves the socket filled but the
	// capability must go dark all the same.
	licensed = false
	if Available(FeaturePermObjectLevel) {
		t.Fatal("capability should go dark when the license lapses, socket notwithstanding")
	}
	if !Frozen() {
		// Freeze was never called here; guard the assertion's own premise.
		if len(Registered()) != 2 {
			t.Fatal("registry lost claims")
		}
	}
}

func TestCommunityBuildRegistersNothing(t *testing.T) {
	reset()
	SetGate(only()) // community: no paid features
	Freeze()

	if len(Registered()) != 0 {
		t.Fatalf("community build should have an empty socket set, got %v", Registered())
	}
	if Available(FeaturePermObjectLevel) {
		t.Fatal("perm_object_level must not be available in a community build")
	}
}
