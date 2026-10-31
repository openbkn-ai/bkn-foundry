// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Scenarios rest on the adopted ontology, which goes on changing after they are drafted. A
// question records the signature of the items it rests on when drafted, accepted or changed; when
// read, a question whose items now sign otherwise is marked changed, and so is each scenario it
// belongs to. Adopted object types no question rests on are listed, for drafting again to cover.

// signQuestions records the signature of each question that has none.
func signQuestions(questions []*interfaces.RequirementQuestion, draft *interfaces.Draft) {
	for _, q := range questions {
		if q.OntologySignature == "" {
			q.OntologySignature = ontologySignature(draft, q)
		}
	}
}

// markOntologyChanges marks the questions and scenarios whose items changed since, and returns
// the adopted object types no question rests on. Without a draft nothing is known to change.
func markOntologyChanges(spec *interfaces.RequirementSpec, draft *interfaces.Draft) []interfaces.ReleaseItem {
	uncovered := []interfaces.ReleaseItem{}
	if draft == nil {
		return uncovered
	}
	changed := map[string]bool{}
	needed := map[interfaces.RequirementItemRef]bool{}
	for _, q := range spec.Questions {
		q.OntologyChanged = q.OntologySignature != "" && q.OntologySignature != ontologySignature(draft, q)
		for _, id := range q.ScenarioIDs {
			changed[id] = changed[id] || q.OntologyChanged
		}
		for _, need := range q.Needs {
			needed[need] = true
		}
	}
	for _, s := range spec.Scenarios {
		s.OntologyChanged = changed[s.ID]
	}
	for _, ot := range draft.Items.ObjectTypes {
		ref := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}
		if ot.Adopted && !needed[ref] {
			uncovered = append(uncovered, interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: ot.ID, Name: ot.Name})
		}
	}
	return uncovered
}
