// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package adminwrite

import "testing"

// ResetForTest clears the socket so a test does not inherit another's mounter.
// Panics outside a test binary — see extension/testsupport.go for why this is
// enforced with testing.Testing() rather than a convention.
func ResetForTest() {
	if !testing.Testing() {
		panic("adminwrite: ResetForTest is test-only and must never run in a production binary")
	}
	resetForTest()
}
