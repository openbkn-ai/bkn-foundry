// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Why an input of a question cannot stand, recorded as the reason of a rejected input.
const (
	rejectedInput                = "input"
	inputUnknownConcept          = "input_unknown_concept"
	inputUnknownProperty         = "input_unknown_property"
	inputNotInTemplate           = "input_not_in_template"
	inputNotTime                 = "input_not_time"
	inputUnknownRole             = "input_unknown_role"
	inputDuplicateName           = "input_duplicate_name"
	rejectedWindowWithoutRefTime = "window_without_reference_time"
)

// timeTypes are the property types a reference time can have.
var timeTypes = map[string]bool{"date": true, "datetime": true, "timestamp": true, "time": true}

// inputProblem says why an input of a question cannot stand, "" when it can: it names a concept of
// the specification, a property of the object type that concept maps to when it is drafted (a
// time one for a reference time), and its name stands in the template as {name}.
func inputProblem(in interfaces.RequirementInput, spec *interfaces.RequirementSpec, draft *interfaces.Draft,
	template string) string {
	if in.Role != interfaces.REQUIREMENT_INPUT_RECORD && in.Role != interfaces.REQUIREMENT_INPUT_REFERENCE_TIME {
		return inputUnknownRole
	}
	i := slices.IndexFunc(spec.Concepts, func(c *interfaces.RequirementConcept) bool { return c.ID == in.ConceptID })
	if i < 0 {
		return inputUnknownConcept
	}
	if strings.TrimSpace(in.Name) == "" || !strings.Contains(template, "{"+in.Name+"}") {
		return inputNotInTemplate
	}
	concept := spec.Concepts[i]
	if concept.MapsTo == nil || concept.MapsTo.Kind != interfaces.REQUIREMENT_ITEM_OBJECT_TYPE || draft == nil {
		// Not drafted yet: the property cannot be checked, nor whether it is a time.
		if in.Role == interfaces.REQUIREMENT_INPUT_REFERENCE_TIME && in.Property == "" {
			return inputNotTime
		}
		return ""
	}
	for _, ot := range draft.Items.ObjectTypes {
		if ot.ID != concept.MapsTo.ID {
			continue
		}
		for _, p := range ot.Properties {
			if p.Name != in.Property {
				continue
			}
			if in.Role == interfaces.REQUIREMENT_INPUT_REFERENCE_TIME && !timeTypes[p.Type] {
				return inputNotTime
			}
			return ""
		}
		return inputUnknownProperty
	}
	return ""
}

// needsReferenceTime says a question counting a period back has no reference time among its inputs.
func needsReferenceTime(relativeWindow bool, inputs []interfaces.RequirementInput) bool {
	return relativeWindow && !slices.ContainsFunc(inputs, func(in interfaces.RequirementInput) bool {
		return in.Role == interfaces.REQUIREMENT_INPUT_REFERENCE_TIME
	})
}

// inputConcepts are the concepts a question's inputs are of: nil when it was drafted before inputs,
// and its tests are not held to them; empty when it has none.
func inputConcepts(q *interfaces.RequirementQuestion) map[string]bool {
	if q == nil || q.Inputs == nil {
		return nil
	}
	concepts := map[string]bool{}
	for _, in := range *q.Inputs {
		concepts[in.ConceptID] = true
	}
	return concepts
}

// offInputs says a test does not ask about its question's inputs: it has no slot, or a slot of
// another concept, so it names an answer field or nothing given; or, expected to name one record
// twice, it does not name one input concept by two properties.
func offInputs(slots []interfaces.QuestionTestSlot, expect string, concepts map[string]bool) bool {
	if len(slots) == 0 || slices.ContainsFunc(slots, func(s interfaces.QuestionTestSlot) bool { return !concepts[s.ConceptID] }) {
		return true
	}
	if expect != interfaces.TEST_EXPECT_SAME_RECORD {
		return false
	}
	for i, a := range slots {
		for _, b := range slots[i+1:] {
			if a.ConceptID == b.ConceptID && a.Property != b.Property {
				return false
			}
		}
	}
	return true
}

// namesAll says the slots name each of the concepts.
func namesAll(slots []interfaces.QuestionTestSlot, concepts map[string]bool) bool {
	for c := range concepts {
		if !slices.ContainsFunc(slots, func(s interfaces.QuestionTestSlot) bool { return s.ConceptID == c }) {
			return false
		}
	}
	return true
}
