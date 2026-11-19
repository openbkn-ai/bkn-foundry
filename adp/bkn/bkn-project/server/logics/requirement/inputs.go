// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/locale"
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

// keyed names a record input by its object type's key when drafting gave no property that object
// type has: asked about "a given" record, it is the key that names one. Only a key of one field
// does; a reference time is a time, never a key.
func keyed(in *interfaces.RequirementInput, spec *interfaces.RequirementSpec, draft *interfaces.Draft) {
	if draft == nil || in.Role != interfaces.REQUIREMENT_INPUT_RECORD {
		return
	}
	i := slices.IndexFunc(spec.Concepts, func(c *interfaces.RequirementConcept) bool { return c.ID == in.ConceptID })
	if i < 0 || spec.Concepts[i].MapsTo == nil || spec.Concepts[i].MapsTo.Kind != interfaces.REQUIREMENT_ITEM_OBJECT_TYPE {
		return
	}
	for _, ot := range draft.Items.ObjectTypes {
		if ot.ID != spec.Concepts[i].MapsTo.ID {
			continue
		}
		if len(ot.PrimaryKeys) != 1 || slices.ContainsFunc(ot.Properties, func(p *interfaces.DraftProperty) bool {
			return p.Name == in.Property
		}) {
			return
		}
		in.Property, in.PropertyFromKey = ot.PrimaryKeys[0], true
		return
	}
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

// slotMarks are the slot placeholders in a test's text; strayName is a {name} left beside them.
var (
	slotMarks = regexp.MustCompile(`\{\{slot [0-9]+\}\}`)
	strayName = regexp.MustCompile(`\{[^{}]*\}`)
)

// unmappedConcepts are the concepts of the specification that map to no object type; none without one.
func unmappedConcepts(spec *interfaces.RequirementSpec) map[string]bool {
	unmapped := map[string]bool{}
	if spec != nil {
		for _, c := range spec.Concepts {
			unmapped[c.ID] = c.MapsTo == nil || c.MapsTo.Kind != interfaces.REQUIREMENT_ITEM_OBJECT_TYPE
		}
	}
	return unmapped
}

// serverTests are the tests a question's template determines, which the server writes rather than
// the Agent: the template asked of a record in the data, and, when an input is a record, asked of
// one that does not exist, named by the first record input. Their slots are the inputs, filled
// from the data when checked. None for a question without inputs, or with one that names no
// property or is of a concept that maps to no object type (unmapped): the server cannot tell which
// field holds its value, and the Agent's tests stand; nor when the template names a value no input
// stands for, or when it counts a period back from a time no input gives: what it asks then depends
// on when it is asked.
func serverTests(q *interfaces.RequirementQuestion, unmapped map[string]bool) []*interfaces.QuestionTest {
	if q == nil || q.Inputs == nil || len(*q.Inputs) == 0 || needsReferenceTime(q.RelativeWindow, *q.Inputs) ||
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
		if strayName.MatchString(slotMarks.ReplaceAllString(text, "")) {
			// A {name} no input stands for, as when drafting rejected it: the test would ask it.
			return nil
		}
		return &interfaces.QuestionTest{Text: text, Kind: kind, Slots: slots, Expect: expect, ByServer: true,
			Check: interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkPending}}
	}
	template := asked(inputs, interfaces.TEST_KIND_TEMPLATE, interfaces.TEST_EXPECT_EXISTS)
	if template == nil {
		return []*interfaces.QuestionTest{}
	}
	tests := []*interfaces.QuestionTest{template}
	if i := slices.IndexFunc(inputs, func(in interfaces.RequirementInput) bool {
		return in.Role == interfaces.REQUIREMENT_INPUT_RECORD
	}); i >= 0 {
		// The record that does not exist is named by the first slot.
		order := append([]interfaces.RequirementInput{inputs[i]}, slices.Delete(slices.Clone(inputs), i, i+1)...)
		tests = append(tests, asked(order, interfaces.TEST_KIND_NOT_EXIST, interfaces.TEST_EXPECT_NOT_EXISTS))
	}
	return tests
}

// objectTypeOfProperty is the adopted object type an input naming no concept reads property from:
// the only one that has it; of several, the one whose name stands right before the input's slot in
// the template (采购申请单 in "采购申请单 {单据编号} 的申请数量"), else the only one whose key it is,
// else the one with the longest name the text holds; nil otherwise. A number field many tables share
// is read from the record the question numbers, not from the one table it is the key of; a name
// elsewhere in the template may be another record the question is about, so it does not outrank the
// key.
func objectTypeOfProperty(draft *interfaces.Draft, property, template, slot, text string) *interfaces.DraftObjectType {
	if draft == nil || strings.TrimSpace(property) == "" {
		return nil
	}
	having := []*interfaces.DraftObjectType{}
	for _, ot := range draft.Items.ObjectTypes {
		if ot.Adopted && slices.ContainsFunc(ot.Properties, func(p *interfaces.DraftProperty) bool { return p.Name == property }) {
			having = append(having, ot)
		}
	}
	if len(having) <= 1 {
		if len(having) == 1 {
			return having[0]
		}
		return nil
	}
	if named := namedBeforeSlot(having, template, slot); named != nil {
		return named
	}
	keyed := slices.DeleteFunc(slices.Clone(having), func(ot *interfaces.DraftObjectType) bool {
		return len(ot.PrimaryKeys) != 1 || ot.PrimaryKeys[0] != property
	})
	if len(keyed) == 1 {
		return keyed[0]
	}
	return longestNamed(having, text)
}

// namedBeforeSlot is the object type of candidates whose name the template puts right before the
// slot, nothing but space between; the longer of two that both end there. Nil when none does.
func namedBeforeSlot(candidates []*interfaces.DraftObjectType, template, slot string) *interfaces.DraftObjectType {
	at := strings.Index(template, "{"+slot+"}")
	if slot == "" || at < 0 {
		return nil
	}
	before := strings.TrimRightFunc(template[:at], unicode.IsSpace)
	var named *interfaces.DraftObjectType
	for _, ot := range candidates {
		name := strings.TrimSpace(ot.Name)
		if name != "" && strings.HasSuffix(before, name) && (named == nil || len(name) > len(strings.TrimSpace(named.Name))) {
			named = ot
		}
	}
	return named
}

// longestNamed is the object type of candidates with the longest name the text holds; nil when it
// holds none.
func longestNamed(candidates []*interfaces.DraftObjectType, text string) *interfaces.DraftObjectType {
	var longest *interfaces.DraftObjectType
	for _, ot := range candidates {
		name := strings.TrimSpace(ot.Name)
		if name != "" && strings.Contains(text, name) && (longest == nil || len(name) > len(strings.TrimSpace(longest.Name))) {
			longest = ot
		}
	}
	return longest
}

// hasConceptOf tells whether the specification has a concept of the object type.
func hasConceptOf(spec *interfaces.RequirementSpec, ot *interfaces.DraftObjectType) bool {
	ref := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}
	return slices.ContainsFunc(spec.Concepts, func(c *interfaces.RequirementConcept) bool {
		return c.MapsTo != nil && *c.MapsTo == ref
	})
}

// conceptOfObjectType is the specification's concept of the object type, added when it has none:
// a question's input names a concept, and a project with no requirement documents, such as one over
// a knowledge base alone, has none to name.
func conceptOfObjectType(spec *interfaces.RequirementSpec, draft *interfaces.Draft,
	ot *interfaces.DraftObjectType) *interfaces.RequirementConcept {
	ref := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}
	if i := slices.IndexFunc(spec.Concepts, func(c *interfaces.RequirementConcept) bool {
		return c.MapsTo != nil && *c.MapsTo == ref
	}); i >= 0 {
		return spec.Concepts[i]
	}
	reason, ok := locale.InputConceptTexts[projecthooks.DraftLanguage(draft)]
	if !ok {
		reason = locale.InputConceptTexts[interfaces.NAMING_LANGUAGE_EN_US]
	}
	concept := &interfaces.RequirementConcept{
		ID: nextID(spec, "c", spec.Concepts, conceptID), Term: ot.Name, Category: interfaces.CONCEPT_VERDICT_OBJECT,
		Definition: ot.Description, Citations: []interfaces.RequirementCitation{}, MapsTo: &ref,
		SupplementReason: fmt.Sprintf(reason, ot.Name), Source: interfaces.REQUIREMENT_CONCEPT_FROM_SCENARIOS,
	}
	spec.Concepts = append(spec.Concepts, concept)
	return concept
}
