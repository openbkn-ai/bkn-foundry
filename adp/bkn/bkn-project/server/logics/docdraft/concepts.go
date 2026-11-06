// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package docdraft

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	evidenceReferenceRole  = "evidence_reference"
	evidenceOccurrenceRole = "evidence_occurrence"
)

// isEvidence tells an evidence item: what locates a business item's original text in its document,
// such as the file, block or image it was read from.
func isEvidence(document *interfaces.DraftDocumentModel) bool {
	return document != nil && strings.HasPrefix(document.Role, "evidence_")
}

// Concept reads a document item as a concept: evidence is none; an event is suggested as one; the
// files are those its citations name.
func (Hooks) Concept(document *interfaces.DraftDocumentModel) projecthooks.DocumentConcept {
	if document == nil {
		return projecthooks.DocumentConcept{}
	}
	if isEvidence(document) {
		return projecthooks.DocumentConcept{Evidence: true}
	}
	concept := projecthooks.DocumentConcept{Because: "document_" + document.Role}
	if document.Role == "event" {
		concept.Suggested = interfaces.CONCEPT_VERDICT_EVENT
	}
	seen := map[string]bool{}
	for _, citation := range document.Citations {
		if !seen[citation.FileID] {
			seen[citation.FileID] = true
			concept.Sources = append(concept.Sources, interfaces.DraftConceptSource{
				Kind: interfaces.CONCEPT_SOURCE_DOCUMENT, ID: citation.FileID})
		}
	}
	return concept
}

// FollowEvidence adopts the evidence items the adopted business items need, and leaves out those no
// adopted business item needs any more: an evidence object is in while a business item adopted
// references it, a reference while its business item is, an occurrence while both its ends are.
// No person decides on evidence; it returns the decisions of what it changed.
func (Hooks) FollowEvidence(items *interfaces.DraftItems) []*interfaces.DraftDecision {
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
