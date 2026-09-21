// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunWiresTheFileStore checks the real assembly end to end: the shipped
// dataset goes through the file adapter and the command-line adapter.
func TestRunWiresTheFileStore(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"validate", "../../datasets/cypher-probe/dataset.json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cypher-probe@1") {
		t.Fatalf("unexpected output %q", stdout.String())
	}
}
