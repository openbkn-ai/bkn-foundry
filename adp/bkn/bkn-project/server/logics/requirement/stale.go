// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"slices"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Scenarios rest on the adopted ontology, which goes on changing after they are drafted. A
// question records the signature of the items it rests on when drafted, accepted or changed, and
// again when drafting again keeps it on items the ontology still adopts; when read, a question whose items now sign otherwise is marked changed, and so is each scenario it
// belongs to. Adopted object types no question rests on, the network's own among them, are listed,
// for drafting again to cover.

// signQuestions signs the questions drafting leaves as the ontology is now: those it drafted, and
// those it kept that still rest on adopted items only, so drafting again settles a change that
// leaves them standing. A kept question on an item the ontology no longer has keeps its signature,
// and stays marked changed until a person edits it or accepts it again.
func signQuestions(questions []*interfaces.RequirementQuestion, draft *interfaces.Draft, concepts []*interfaces.RequirementConcept) {
	for _, q := range questions {
		if q.OntologySignature == "" || len(goneNeeds(q, draft, concepts)) == 0 {
			q.OntologySignature = ontologySignature(draft, q)
		}
	}
}

// goneNeeds names the items q needs that the ontology no longer adopts: an item still drafted by
// its name, one no longer drafted by the term of the question's concept that maps to it. A need
// gone without a name to read is counted as gone, unnamed.
func goneNeeds(q *interfaces.RequirementQuestion, draft *interfaces.Draft, concepts []*interfaces.RequirementConcept) []string {
	if draft == nil {
		return nil
	}
	names, adopted := map[interfaces.RequirementItemRef]string{}, adoptedRefs(draft)
	for _, ot := range draft.Items.ObjectTypes {
		names[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}] = ot.Name
	}
	for _, rt := range draft.Items.RelationTypes {
		names[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: rt.ID}] = rt.Name
	}
	gone := []string{}
	for _, need := range q.Needs {
		if adopted[need] {
			continue
		}
		name, drafted := names[need]
		if !drafted {
			for _, c := range concepts {
				if c.MapsTo != nil && *c.MapsTo == need && slices.Contains(q.Basis.ConceptIDs, c.ID) {
					name = c.Term
					break
				}
			}
		}
		if !slices.Contains(gone, name) {
			gone = append(gone, name)
		}
	}
	return gone
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
		q.OntologyGone = nil
		if q.OntologyChanged {
			// What a person reads of the change: the items it rests on the ontology no longer has.
			for _, name := range goneNeeds(q, draft, spec.Concepts) {
				if name != "" {
					q.OntologyGone = append(q.OntologyGone, name)
				}
			}
		}
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
		// Scenarios cover the whole model: what the network already has as well as what is new, but
		// not the system objects that locate original text.
		if ot.Adopted && !needed[ref] && !isEvidenceObject(ot) {
			uncovered = append(uncovered, interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: ot.ID, Name: ot.Name})
		}
	}
	return uncovered
}
