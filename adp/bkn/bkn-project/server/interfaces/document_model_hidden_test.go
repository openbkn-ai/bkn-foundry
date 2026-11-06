// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "testing"

func TestDocumentHiddenColumnKeepsBusinessFields(t *testing.T) {
	for _, c := range []struct {
		role, property string
		hidden         bool
	}{
		{"entity", "document_id", true}, {"fact", "source_quote", true}, {"file", "invoice_no", false},
		{"event", "effective_from", false}, {"evidence_block", "source_quote", false},
		{"evidence_document", "model_fingerprint", true}, {"relation", "document_id", false},
	} {
		if DocumentHiddenColumn(c.role, c.property) != c.hidden {
			t.Fatalf("%s.%s hidden=%v", c.role, c.property, !c.hidden)
		}
	}
}
