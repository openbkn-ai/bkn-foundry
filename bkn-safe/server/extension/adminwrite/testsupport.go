// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package adminwrite

import "testing"

// ResetForTest clears the socket so a test does not inherit another's mounter.
//
// Panics outside a test binary. testing.Testing() rather than a convention plus
// a lint, because this function is a way around the invariants the socket
// exists to enforce — it clears the frozen flag, so a caller could re-open a
// running server's socket and register into it. testing.Testing() reports
// whether the binary was built by `go test`, so in a production build it cannot
// run at all, no matter who calls it. entitlement.ResetForTest guards itself the
// same way and for the same reason.
func ResetForTest() {
	if !testing.Testing() {
		panic("adminwrite: ResetForTest is test-only and must never run in a production binary")
	}
	resetForTest()
}
