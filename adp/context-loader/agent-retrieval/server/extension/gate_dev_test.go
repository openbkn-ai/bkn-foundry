//go:build ee_dev

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package extension

import "testing"

func TestDevGateReadsEnvironmentEveryCall(t *testing.T) {
	g := DefaultGate()

	t.Setenv("OPENBKN_FEATURES", "")
	if g.Enabled(FeatureContextProbe) {
		t.Fatal("empty OPENBKN_FEATURES should license nothing")
	}

	// Same Gate value, changed environment: the stub must re-read rather than
	// cache, matching how the shipped gate reacts to a license that changes
	// under a running process.
	t.Setenv("OPENBKN_FEATURES", "audit, context_probe ,other")
	if !g.Enabled(FeatureContextProbe) {
		t.Fatal("listed feature should be on, whitespace and neighbours notwithstanding")
	}
	if g.Enabled(Feature("other_thing")) {
		t.Fatal("a feature that is not listed must stay off (no prefix matching)")
	}
}
