// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A requirement concept says what the business needs; the data says what can be modeled. So a
// requirement concept is matched to a concept of the data, an object of a table or one a
// knowledge-base extraction drafted, or to nothing yet: it names and describes what it is matched
// to, and is never an item of its own.

// dataConcept tells a concept of the data from a requirement concept listed on its own.
func dataConcept(key string) bool {
	return !strings.HasPrefix(key, requirementConceptKey(""))
}

// matchedKey is the key of the data concept a requirement concept's match names, empty when it
// names none standing: an object type of the draft, or the object read from a table, before
// drafting the table's candidate.
func matchedKey(match *interfaces.RequirementMatch, byKey map[string]*interfaces.DraftConcept, order []string) string {
	if match == nil || match.Target == nil || match.Excluded {
		return ""
	}
	return targetKey(*match.Target, byKey, order)
}

func targetKey(target interfaces.RequirementMatchTarget, byKey map[string]*interfaces.DraftConcept, order []string) string {
	switch target.Kind {
	case interfaces.REQUIREMENT_MATCH_OBJECT_TYPE:
		if key := itemKey(interfaces.DRAFT_KIND_OBJECT_TYPES, target.ID); byKey[key] != nil {
			return key
		}
	case interfaces.REQUIREMENT_MATCH_TABLE:
		for _, key := range order {
			c := byKey[key]
			if c.Item != nil && c.Item.Kind == interfaces.DRAFT_KIND_OBJECT_TYPES && c.Binding != nil &&
				c.Binding.Kind == interfaces.CONCEPT_BINDING_TABLE && c.Binding.ID == target.ID {
				return key
			}
		}
		if key := tableConceptKey(target.ID); byKey[key] != nil {
			return key
		}
	}
	return ""
}

// matchTarget names a data concept in a match: an object read from a table by its table, any other
// object by its object type; nil for a concept that is no object.
func matchTarget(c *interfaces.DraftConcept) *interfaces.RequirementMatchTarget {
	if c.Item != nil && c.Item.Kind != interfaces.DRAFT_KIND_OBJECT_TYPES {
		return nil
	}
	if c.Binding != nil && c.Binding.Kind == interfaces.CONCEPT_BINDING_TABLE && c.Binding.ID != "" {
		return &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_TABLE, ID: c.Binding.ID}
	}
	if c.Item != nil {
		return &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_OBJECT_TYPE, ID: c.Item.ID}
	}
	return nil
}

// sourcedFrom is the data concept that lists the requirement concept among its sources, as its
// term names it, empty when none does.
func sourcedFrom(id string, byKey map[string]*interfaces.DraftConcept, order []string) string {
	for _, key := range order {
		if !dataConcept(key) {
			continue
		}
		if slices.ContainsFunc(byKey[key].Sources, func(s interfaces.DraftConceptSource) bool {
			return s.Kind == interfaces.CONCEPT_SOURCE_REQUIREMENT && s.ID == id
		}) {
			return key
		}
	}
	return ""
}

// requirementMatches lists the requirement concepts with the data concept each is matched to: as a
// person merged it into one, as its match names one, as it maps to a drafted item, or as its term
// names one. One a person left out, or judged no part of the ontology, is not included; one matched
// to nothing waits for data.
func requirementMatches(concepts []*interfaces.RequirementConcept, byKey map[string]*interfaces.DraftConcept,
	order []string, mergedInto map[string]string) []interfaces.DraftRequirementConcept {
	kinds := map[string]string{
		interfaces.REQUIREMENT_ITEM_OBJECT_TYPE:   interfaces.DRAFT_KIND_OBJECT_TYPES,
		interfaces.REQUIREMENT_ITEM_RELATION_TYPE: interfaces.DRAFT_KIND_RELATION_TYPES,
	}
	listed := []interfaces.DraftRequirementConcept{}
	for _, rc := range concepts {
		if _, ok := requirementVerdicts[rc.Category]; !ok {
			// Evidence follows the business items.
			continue
		}
		entry := interfaces.DraftRequirementConcept{ID: rc.ID, Term: rc.Term, Category: rc.Category,
			Definition: rc.Definition}
		if len(rc.Citations) > 0 {
			entry.Quote = rc.Citations[0].Quote
		}
		m := rc.Match
		key, confirmed := "", false
		switch into := mergedInto[requirementConceptKey(rc.ID)]; {
		case into != "" && byKey[into] != nil && dataConcept(into):
			key, entry.By, confirmed = into, interfaces.REQUIREMENT_MATCH_BY_PERSON, true
		case m != nil && m.Target != nil:
			key, entry.By, confirmed = targetKey(*m.Target, byKey, order), m.By, m.Confirmed
		case rc.MapsTo != nil && byKey[itemKey(kinds[rc.MapsTo.Kind], rc.MapsTo.ID)] != nil:
			key, entry.By, confirmed = itemKey(kinds[rc.MapsTo.Kind], rc.MapsTo.ID), interfaces.REQUIREMENT_MATCH_BY_AI, rc.Edited
			if rc.Edited {
				entry.By = interfaces.REQUIREMENT_MATCH_BY_PERSON
			}
		case m == nil:
			key, entry.By = sourcedFrom(rc.ID, byKey, order), interfaces.REQUIREMENT_MATCH_BY_NAME
		}
		// One merged into another is matched to the one it went into.
		if into := mergedInto[key]; into != "" && byKey[into] != nil && mergedInto[into] == "" {
			key = into
		}
		if m != nil && (m.Target == nil || entry.By == m.By) {
			entry.Basis = m.Basis
		}
		if c := byKey[key]; c != nil && dataConcept(key) {
			entry.Match = &interfaces.DraftRequirementMatch{Key: key, Name: c.Name, Binding: c.Binding,
				Target: matchTarget(c)}
		} else if m != nil {
			entry.By = m.By
		} else {
			entry.By = ""
		}
		switch {
		case m != nil && m.Excluded:
			entry.State = interfaces.REQUIREMENT_MATCH_EXCLUDED
		case entry.Match == nil && rc.Category == "not_ontology":
			entry.State = interfaces.REQUIREMENT_MATCH_EXCLUDED
			if entry.Basis == "" {
				entry.Basis = rc.NotOntologyReason
			}
		case entry.Match == nil:
			entry.State = interfaces.REQUIREMENT_MATCH_UNMATCHED
		case confirmed:
			entry.State = interfaces.REQUIREMENT_MATCH_CONFIRMED
		default:
			entry.State = interfaces.REQUIREMENT_MATCH_PENDING
		}
		listed = append(listed, entry)
	}
	return listed
}
