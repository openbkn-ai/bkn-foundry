//go:build ee_dev

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"testing"

	"github.com/openbkn-ai/licverify"
)

func TestDevGateReadsTheEnvironmentEveryCall(t *testing.T) {
	g := DefaultGate()

	t.Setenv("OPENBKN_EDITION", "")
	if snap := g.Snapshot(); snap.Licensed || snap.Edition != licverify.EditionCommunity {
		t.Fatalf("unset means community, got %+v", snap)
	}

	// Same Gate value, changed environment: the stub re-reads rather than
	// resolving once, so it behaves like the shipped gate under a licence that
	// changes beneath a running process.
	t.Setenv("OPENBKN_EDITION", "enterprise")
	snap := g.Snapshot()
	if !snap.Licensed || snap.Edition != licverify.EditionEnterprise {
		t.Fatalf("snapshot = %+v, want licensed enterprise", snap)
	}
	if !snap.Edition.AtLeast(licverify.EditionProfessional) {
		t.Fatal("enterprise contains professional")
	}
}

func TestDevGateCarriesFeaturesForDisplayOnly(t *testing.T) {
	t.Setenv("OPENBKN_EDITION", "enterprise")
	t.Setenv("OPENBKN_FEATURES", " audit , perm_object_level ")

	got := DefaultGate().Snapshot().Features
	if len(got) != 2 || got[0] != "audit" || got[1] != "perm_object_level" {
		t.Fatalf("Features = %v, want the trimmed list", got)
	}
}

func TestDevGateTreatsCommunityAsUnlicensed(t *testing.T) {
	t.Setenv("OPENBKN_EDITION", "community")

	snap := DefaultGate().Snapshot()
	if snap.Licensed {
		t.Fatal("community is the absence of a paid licence, not a licensed tier")
	}
	if snap.State != licverify.StateUnlicensed {
		t.Fatalf("State = %q, want unlicensed", snap.State)
	}
}
