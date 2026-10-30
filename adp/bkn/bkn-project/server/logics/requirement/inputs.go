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
	inputConceptNotMapped        = "input_concept_not_mapped"
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
// the specification, a property of the object type that concept maps to (a time one for a
// reference time), and its name stands in the template as {name}. Before the project has a draft,
// a concept maps to nothing yet and its property cannot be checked; once it has one, an input on a
// concept that maps to no object type names no field a value could be read from.
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
	unmapped := concept.MapsTo == nil || concept.MapsTo.Kind != interfaces.REQUIREMENT_ITEM_OBJECT_TYPE
	if unmapped && draft != nil {
		return inputConceptNotMapped
	}
	if unmapped || draft == nil {
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

// inputConcepts are the concepts a question's inputs are of, each with the properties its inputs
// name: nil when it was drafted before inputs, and its tests are not held to them; empty when it
// has none.
func inputConcepts(q *interfaces.RequirementQuestion) map[string][]string {
	if q == nil || q.Inputs == nil {
		return nil
	}
	concepts := map[string][]string{}
	for _, in := range *q.Inputs {
		concepts[in.ConceptID] = append(concepts[in.ConceptID], in.Property)
	}
	return concepts
}

// offInputs says a test does not ask about its question's inputs: it has no slot, or a slot of
// another concept, so it names an answer field or nothing given; or, expected to name the
// question's record another way, it is not of one input concept, or names it only by the
// properties the inputs do, which is no other way.
func offInputs(slots []interfaces.QuestionTestSlot, expect string, concepts map[string][]string) bool {
	if len(slots) == 0 || slices.ContainsFunc(slots, func(s interfaces.QuestionTestSlot) bool {
		_, ok := concepts[s.ConceptID]
		return !ok
	}) {
		return true
	}
	if expect != interfaces.TEST_EXPECT_SAME_RECORD {
		return false
	}
	concept := slots[0].ConceptID
	return slices.ContainsFunc(slots, func(s interfaces.QuestionTestSlot) bool { return s.ConceptID != concept }) ||
		!slices.ContainsFunc(slots, func(s interfaces.QuestionTestSlot) bool {
			return !slices.Contains(concepts[concept], s.Property)
		})
}

// namesAll says the slots name each of the concepts.
func namesAll(slots []interfaces.QuestionTestSlot, concepts map[string][]string) bool {
	for c := range concepts {
		if !slices.ContainsFunc(slots, func(s interfaces.QuestionTestSlot) bool { return s.ConceptID == c }) {
			return false
		}
	}
	return true
}

// serverTests are the tests a question's template determines, which the server writes rather than
// the Agent: the template asked of a record in the data, and, when an input is a record, asked of
// one that does not exist, named by the first record input. Their slots are the inputs, filled
// from the data when checked. None for a question without inputs, or with one that names no
// property or is of a concept that maps to no object type (unmapped): the server cannot tell which
// field holds its value, and the Agent's tests stand.
func serverTests(q *interfaces.RequirementQuestion, unmapped map[string]bool) []*interfaces.QuestionTest {
	if q == nil || q.Inputs == nil || len(*q.Inputs) == 0 ||
		slices.ContainsFunc(*q.Inputs, func(in interfaces.RequirementInput) bool {
			return in.Property == "" || unmapped[in.ConceptID]
		}) {
		return []*interfaces.QuestionTest{}
	}
	inputs := *q.Inputs
	asked := func(order []interfaces.RequirementInput, kind string, expect string) *interfaces.QuestionTest {
		text, slots := q.Template, []interfaces.QuestionTestSlot{}
		for i, in := range order {
			text = strings.ReplaceAll(text, "{"+in.Name+"}", slotPlaceholder(i))
			slots = append(slots, interfaces.QuestionTestSlot{ConceptID: in.ConceptID, Property: in.Property})
		}
		return &interfaces.QuestionTest{Text: text, Kind: kind, Slots: slots, Expect: expect, ByServer: true,
			Check: interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkPending}}
	}
	tests := []*interfaces.QuestionTest{asked(inputs, interfaces.TEST_KIND_TEMPLATE, interfaces.TEST_EXPECT_EXISTS)}
	if i := slices.IndexFunc(inputs, func(in interfaces.RequirementInput) bool {
		return in.Role == interfaces.REQUIREMENT_INPUT_RECORD
	}); i >= 0 {
		// The record that does not exist is named by the first slot.
		order := append([]interfaces.RequirementInput{inputs[i]}, slices.Delete(slices.Clone(inputs), i, i+1)...)
		tests = append(tests, asked(order, interfaces.TEST_KIND_NOT_EXIST, interfaces.TEST_EXPECT_NOT_EXISTS))
	}
	return tests
}
