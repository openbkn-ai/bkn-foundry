// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permdata

import "testing"

// ResetForTest clears the process-wide resolver. It cannot run in a production
// binary because replacing a live authorization implementation is unsafe.
func ResetForTest() {
	if !testing.Testing() {
		panic("permdata: ResetForTest is test-only")
	}
	reset()
}
