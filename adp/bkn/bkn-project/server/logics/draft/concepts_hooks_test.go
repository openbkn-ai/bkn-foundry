// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// conceptHooks stands for an edition that reads document items for the concept view: an
// evidence_ role is evidence, the event role an event, and the files cited its sources. The real
// rules are the edition's; these tests check what the draft does with what it is told.
type conceptHooks struct{ noDocumentHooks }

func (conceptHooks) DocumentConcept(document *interfaces.DraftDocumentModel) projecthooks.DocumentConcept {
	concept := projecthooks.DocumentConcept{Evidence: strings.HasPrefix(document.Role, "evidence_"),
		Event: document.Role == "event", Because: "document_" + document.Role}
	seen := map[string]bool{}
	for _, citation := range document.Citations {
		if !seen[citation.FileID] {
			seen[citation.FileID] = true
			concept.Sources = append(concept.Sources, interfaces.DraftConceptSource{Kind: interfaces.CONCEPT_SOURCE_DOCUMENT, ID: citation.FileID})
		}
	}
	if len(concept.Sources) == 0 {
		concept.Sources = []interfaces.DraftConceptSource{{Kind: interfaces.CONCEPT_SOURCE_DOCUMENT}}
	}
	return concept
}

func (conceptHooks) ConceptBlocker(code string) (string, bool) {
	if code == "BknProject.Draft.BusinessIdentityNotVerified" {
		return interfaces.CONCEPT_BLOCKED_NEEDS_IDENTITY, true
	}
	return "", false
}
