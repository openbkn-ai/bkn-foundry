// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package rowfilter

import "testing"

// ResetForTest clears the process-wide resolver so Enterprise assembly tests
// in the private code line do not inherit another test's registration. It is
// unavailable to production binaries because replacing a live resolver would
// violate the one-time assembly invariant.
func ResetForTest() {
	if !testing.Testing() {
		panic("rowfilter: ResetForTest is test-only")
	}
	reset()
}
