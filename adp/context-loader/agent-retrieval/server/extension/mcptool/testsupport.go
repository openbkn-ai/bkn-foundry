// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcptool

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// The socket is process-global by design — it models one binary's assembly,
// which happens once. Tests in other packages (the MCP adapter above all) still
// need to plug a fake in, so the helper below is exported.
//
// Exported test helpers live in a normal source file and are therefore linked
// into the production binary, and this one clears a registry that
// entitlement.MustBeAssembling panics to protect. entitlement's own helpers
// already refuse to run outside a test binary; the same guard is repeated here
// so neither half of a reset is reachable from production code.

// ResetForTest empties the socket and reinstalls g as the licence gate,
// unfreezing the assembly registry along the way. It is the opening line of a
// socket test: without it a test inherits the previous test's tools.
//
// Panics outside a test binary.
func ResetForTest(g entitlement.Gate) {
	if !testing.Testing() {
		panic("mcptool: ResetForTest is test-only and must never run in a production binary")
	}
	reset()
	entitlement.SetGateForTest(g)
}
