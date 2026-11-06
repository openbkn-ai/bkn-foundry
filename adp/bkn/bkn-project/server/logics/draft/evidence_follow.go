// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	evidenceReferenceRole  = "evidence_reference"
	evidenceOccurrenceRole = "evidence_occurrence"
)

// followEvidence adopts the evidence items the adopted business items need, and leaves out those no
// adopted business item needs any more: an evidence object is in while a business item adopted
// references it, a reference while its business item is, an occurrence while both its ends are.
// No person decides on evidence; it returns the decisions of what it changed.
func followEvidence(items *interfaces.DraftItems) []*interfaces.DraftDecision {
	evidence := map[string]*interfaces.DraftObjectType{}
	adopted := map[string]bool{}
	for _, ot := range items.ObjectTypes {
		adopted[ot.ID] = ot.Adopted
		if isEvidence(ot.Document) {
			evidence[ot.ID] = ot
		}
	}
	if len(evidence) == 0 {
		return nil
	}
	decisions := []*interfaces.DraftDecision{}
	set := func(kind string, id string, current *bool, want bool) {
		if *current == want {
			return
		}
		*current = want
		action := interfaces.DRAFT_DECISION_UNADOPT
		if want {
			action = interfaces.DRAFT_DECISION_ADOPT
		}
		decisions = append(decisions, &interfaces.DraftDecision{Kind: kind, ItemID: id, Action: action,
			Before: !want, After: want})
	}

	needed := map[string]bool{}
	for _, rt := range items.RelationTypes {
		if rt.Document == nil || rt.Document.Role != evidenceReferenceRole || evidence[rt.Target] == nil {
			continue
		}
		business := adopted[rt.Source] && evidence[rt.Source] == nil
		needed[rt.Target] = needed[rt.Target] || business
	}
	// An occurrence ties evidence that locates the same text, such as an image and the media it
	// occurs in: one needed needs the other.
	for changed := true; changed; {
		changed = false
		for _, rt := range items.RelationTypes {
			if rt.Document == nil || rt.Document.Role != evidenceOccurrenceRole ||
				evidence[rt.Source] == nil || evidence[rt.Target] == nil {
				continue
			}
			if needed[rt.Source] != needed[rt.Target] {
				needed[rt.Source], needed[rt.Target], changed = true, true, true
			}
		}
	}
	// In the draft's order, so the decisions read the same each time.
	for _, ot := range items.ObjectTypes {
		if evidence[ot.ID] != nil {
			set(interfaces.DRAFT_KIND_OBJECT_TYPES, ot.ID, &ot.Adopted, needed[ot.ID])
		}
	}
	for _, rt := range items.RelationTypes {
		if rt.Document == nil || evidence[rt.Target] == nil {
			continue
		}
		switch rt.Document.Role {
		case evidenceReferenceRole:
			set(interfaces.DRAFT_KIND_RELATION_TYPES, rt.ID, &rt.Adopted,
				adopted[rt.Source] && evidence[rt.Source] == nil && needed[rt.Target])
		case evidenceOccurrenceRole:
			set(interfaces.DRAFT_KIND_RELATION_TYPES, rt.ID, &rt.Adopted, needed[rt.Source] && needed[rt.Target])
		}
	}
	return decisions
}
