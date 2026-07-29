// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcptool

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension"
)

// The socket is process-global by design — it models one binary's assembly,
// which happens once. Tests in other packages (the MCP handler, above all)
// still need to plug a fake in, so the helper below is exported.
//
// Exported test helpers live in a normal source file and are therefore linked
// into the production binary, and this one clears a registry that Register and
// Decorate panic to protect. extension.SetGateForTest already refuses to run
// outside a test binary; the same guard is repeated here so that neither half
// of a reset can be reached from production code.

// ResetForTest empties the socket and reinstalls g as the license gate,
// unfreezing the extension registry along the way. It is the opening line of a
// socket test: without the reset, a test inherits the previous test's tools and
// claims.
//
// Panics outside a test binary.
func ResetForTest(g extension.Gate) {
	if !testing.Testing() {
		panic("mcptool: ResetForTest is test-only and must never run in a production binary")
	}
	reset()
	extension.SetGateForTest(g)
}
