// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"testing"

	"github.com/openbkn-ai/licverify"
)

// The gate and the assembly registry are process-global by design: they model
// one binary's licence and one binary's assembly, each of which happens once.
// That makes them awkward for tests in other packages — sockets and call sites
// both need to plug a fake in — so the helpers below are exported.
//
// Exported test helpers live in a normal source file, which means they are
// linked into the production binary. Both of these would otherwise be a way
// around the invariants the rest of this package exists to enforce:
// ResetForTest clears the frozen flag, so a caller could unfreeze a running
// server's registry and register into it — exactly what MustBeAssembling
// panics to prevent.
//
// testing.Testing() closes that off at the only point that matters: it reports
// whether the binary was built by `go test`, so in a production build these
// cannot run at all, no matter who calls them. A convention plus a lint would
// leave the hole open to whatever the lint failed to match.

// ResetForTest returns the package to its zero state: community gate, not
// frozen, nothing assembled. Call it at the top of any test that registers a
// capability, so tests do not inherit each other's assembly.
//
// Panics outside a test binary.
func ResetForTest() {
	mustBeTest("ResetForTest")
	reset()
}

// SetGateForTest resets and then installs g. It is the usual opening line of a
// socket test — a plain SetGate on an already-frozen registry would panic, and
// on an already-assembled one would carry the previous test's state forward.
//
// Panics outside a test binary.
func SetGateForTest(g Gate) {
	mustBeTest("SetGateForTest")
	reset()
	SetGate(g)
}

// FixedGate is a Gate that always reports the same edition, for tests and for
// the rare caller that needs a deterministic tier (a smoke harness, say).
//
// Panics outside a test binary.
func FixedGate(ed licverify.Edition) Gate {
	mustBeTest("FixedGate")
	return GateFunc(func() Snapshot {
		if ed == licverify.EditionCommunity || ed == "" {
			return Snapshot{Edition: licverify.EditionCommunity, State: licverify.StateUnlicensed}
		}
		return Snapshot{Licensed: true, Edition: ed, State: licverify.StateValid}
	})
}

func mustBeTest(fn string) {
	if !testing.Testing() {
		panic("entitlement: " + fn + " is test-only and must never run in a production binary")
	}
}
