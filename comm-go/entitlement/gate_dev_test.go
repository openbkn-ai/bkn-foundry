//go:build ee_dev

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"strings"
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

func TestDevGateRejectsAMisspelledEdition(t *testing.T) {
	t.Setenv("OPENBKN_EDITION", "enterprsie")

	// Edition is a plain string conversion, so a typo would otherwise sail
	// through as Licensed: true on a tier that satisfies no bar at all — "it
	// says licensed and nothing works", with nothing pointing at the
	// misspelling. This is also the half of the ee_dev CI job's reason for
	// existing that compilation alone does not cover: the stub could stop
	// refusing and the build would stay green.
	msg := mustPanic(t, "misspelled edition", func() { DefaultGate().Snapshot() })
	if !strings.Contains(msg, "not a known edition") {
		t.Fatalf("panic should name the bad value, got %q", msg)
	}
}
