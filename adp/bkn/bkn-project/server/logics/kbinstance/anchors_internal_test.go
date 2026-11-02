// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import "testing"

func TestEmptyAnchorBatchStillRequiresAValidSourceNamespace(t *testing.T) {
	for _, namespace := range [][2]string{{"", "document"}, {"lineage", ""}, {"lineage", "  "}} {
		if ids, err := AssignSentenceAnchors(namespace[0], namespace[1], nil); err == nil || ids != nil {
			t.Fatal("empty extraction bypassed source namespace validation")
		}
	}
	if ids, err := AssignSentenceAnchors("lineage", "document", nil); err != nil || ids == nil || len(ids) != 0 {
		t.Fatal("valid extraction with no instances should remain empty", err)
	}
}
