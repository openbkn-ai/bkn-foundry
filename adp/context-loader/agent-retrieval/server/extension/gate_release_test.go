//go:build !ee_dev

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package extension

import (
	"os"
	"testing"
)

// A release build must not react to OPENBKN_FEATURES at all. The stub is
// excluded at compile time, so this is really asserting that the build tags did
// what they claim — the mistake this guards against is a stub that silently
// leaks into the default build.
func TestReleaseGateIgnoresEnvironment(t *testing.T) {
	t.Setenv("OPENBKN_FEATURES", "context_probe")
	if os.Getenv("OPENBKN_FEATURES") != "context_probe" {
		t.Fatal("test setup failed")
	}

	if DefaultGate().Enabled(FeatureContextProbe) {
		t.Fatal("a release build must not enable paid features from the environment")
	}
}
