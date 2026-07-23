// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package extension

// The registry is process-global by design — it models one binary's assembly,
// which happens once. That makes it awkward for tests in other packages (the
// typed sockets, and core call sites that want a fake plugged in), so the two
// helpers below are exported.
//
// They are test-only. Production assembly uses SetGate + Freeze exactly once
// from the server entry point; anything calling these outside a _test.go file
// is a bug, and CI lints for it.

// ResetForTest returns the registry to its zero state: no gate, not frozen,
// nothing claimed. Call it at the top of any test that registers an extension,
// so tests do not inherit each other's assembly.
func ResetForTest() { reset() }

// SetGateForTest resets the registry and installs g. It is the common opening
// line of a socket test — a plain SetGate on an already-claimed registry would
// carry the previous test's claims forward.
func SetGateForTest(g Gate) {
	reset()
	SetGate(g)
}
