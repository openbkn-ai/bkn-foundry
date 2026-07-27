// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package extension

import "testing"

// The registry is process-global by design — it models one binary's assembly,
// which happens once. That makes it awkward for tests in other packages (the
// typed sockets, and core call sites that want a fake plugged in), so the two
// helpers below are exported.
//
// Exported test helpers live in a normal source file, which means they are
// linked into the production binary. Both would otherwise be a way around the
// invariants the rest of this package exists to enforce: ResetForTest clears
// the frozen flag, so a caller could un-freeze a running server's registry and
// register an extension into it — exactly what Claim's panic is there to stop.
//
// testing.Testing() closes that off at the only point that matters. It reports
// whether the binary was built by `go test`, so in a production build these
// functions cannot run at all, no matter who calls them. A convention plus a
// lint would leave the hole open to anything the lint failed to match.

// ResetForTest returns the registry to its zero state: no gate, not frozen,
// nothing claimed. Call it at the top of any test that registers an extension,
// so tests do not inherit each other's assembly.
//
// Panics outside a test binary.
func ResetForTest() {
	mustBeTest("ResetForTest")
	reset()
}

// SetGateForTest resets the registry and installs g. It is the common opening
// line of a socket test — a plain SetGate on an already-claimed registry would
// carry the previous test's claims forward.
//
// Panics outside a test binary.
func SetGateForTest(g Gate) {
	mustBeTest("SetGateForTest")
	reset()
	SetGate(g)
}

func mustBeTest(fn string) {
	if !testing.Testing() {
		panic("extension: " + fn + " is test-only and must never run in a production binary")
	}
}
