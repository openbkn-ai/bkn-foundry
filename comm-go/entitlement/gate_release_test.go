//go:build !ee_dev

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"testing"

	"github.com/openbkn-ai/licverify"
)

func TestReleaseGateIgnoresTheEnvironment(t *testing.T) {
	// The whole reason the stub sits behind a build tag: a release binary runs
	// on hardware whose environment belongs to the customer. Setting the
	// development variable must do nothing at all here.
	t.Setenv("OPENBKN_EDITION", "enterprise")
	t.Setenv("OPENBKN_FEATURES", "audit")
	// DefaultGate resolves the hub from the environment, so clear it: this test
	// is about the build tag, not about whatever a developer's shell exports.
	// Left unset it would fire a real 10s-timeout request at their cluster, and
	// pass or fail depending on whether that bkn-safe happens to hold a licence.
	t.Setenv("BKN_SAFE_URL", "")

	snap := DefaultGate().Snapshot()
	if snap.Licensed || snap.Edition != licverify.EditionCommunity {
		t.Fatalf("a release build must not take its tier from the environment, got %+v", snap)
	}
}

func TestReleaseGateWithoutAHubIsCommunity(t *testing.T) {
	// A community deployment does not set the variable. That is a legitimate
	// steady state, not a misconfiguration: the process must start and behave
	// as community rather than refuse to boot.
	t.Setenv("BKN_SAFE_URL", "")

	g, run := GateWithRunner()
	if run != nil {
		t.Fatal("there is nothing to refresh without a hub")
	}
	if snap := g.Snapshot(); snap.Licensed || snap.Edition != licverify.EditionCommunity {
		t.Fatalf("snapshot = %+v, want unlicensed community", snap)
	}
}
