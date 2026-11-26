// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"testing"
	"uuid"
)

func TestNewDefinitionRevisionIsCompactUUIDV7(t *testing.T) {
	revision := NewDefinitionRevision()
	if len(revision) != 32 {
		t.Fatalf("revision = %q, want 32 hexadecimal characters", revision)
	}
	id, err := uuid.Parse(revision)
	if err != nil {
		t.Fatalf("revision %q is invalid: %v", revision, err)
	}
	if version := id[6] >> 4; version != 7 {
		t.Fatalf("revision UUID version = %d, want 7", version)
	}
}
