// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permobject

import "testing"

// ResetForTest clears the socket so a test does not inherit another's
// Authorizer.
//
// It exists because Register refuses a second registration — one process
// assembles once, and a silently replaced Authorizer would be an authorization
// change with no trace. That guard makes every test after the first one panic
// unless the socket can be emptied between them, and the enterprise code line
// tests its own assembly from another repository, so an unexported reset does
// not reach it. adminwrite exports the same helper for the same reason; this
// package was missing it, which only surfaced once the guard existed.
//
// Panics outside a test binary. testing.Testing() rather than a convention plus
// a lint, because this function is a way around the invariant the guard exists
// to enforce: it lets a caller drop a running server's Authorizer and install
// another. testing.Testing() reports whether the binary was built by `go test`,
// so in a production build it cannot run at all, no matter who calls it.
func ResetForTest() {
	if !testing.Testing() {
		panic("permobject: ResetForTest is test-only and must never run in a production binary")
	}
	reset()
}
