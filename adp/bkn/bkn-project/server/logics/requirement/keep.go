// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Running again keeps what people made theirs. Scenario drafting keeps the scenarios and questions
// people accepted, added or changed, with the scenarios those questions belong to; the concepts,
// rules, conflicts, choices and kinetic needs are the other runs' and stay as they are, but for the
// concepts an earlier drafting added that nothing kept rests on. The concepts run keeps the
// concepts, rules and kinetic needs people changed and the conflicts and choices they decided; the
// kinetic run the kinetic needs they accepted or changed. Whatever a question rests on is kept by
// every run. Kept items keep their ids, so a question's test questions and guidance stay its own;
// the rest is drafted anew around them, and no id once given is given again.

// keptPrefix marks a kept item's id in the message, so the Agent can name it apart from its own.
const keptPrefix = "kept-"

// emptySpec is a specification with nothing in it, its lists empty rather than null.
func emptySpec() *interfaces.RequirementSpec {
	return &interfaces.RequirementSpec{
		Scenarios: []*interfaces.RequirementScenario{}, Concepts: []*interfaces.RequirementConcept{},
		Questions: []*interfaces.RequirementQuestion{}, Rules: []*interfaces.RequirementRule{},
		Conflicts: []*interfaces.RequirementConflict{}, Choices: []*interfaces.RequirementChoice{},
		LogicNeeds: []*interfaces.RequirementLogicNeed{}, Rejected: []*interfaces.RequirementRejected{},
		LastIDs: map[string]int{},
	}
}

// withLists gives spec empty lists where it has none, as clients read them.
func withLists(spec *interfaces.RequirementSpec) *interfaces.RequirementSpec {
	empty := emptySpec()
	if spec.Scenarios == nil {
		spec.Scenarios = empty.Scenarios
	}
	if spec.Concepts == nil {
		spec.Concepts = empty.Concepts
	}
	if spec.Questions == nil {
		spec.Questions = empty.Questions
	}
	if spec.Rules == nil {
		spec.Rules = empty.Rules
	}
	if spec.Conflicts == nil {
		spec.Conflicts = empty.Conflicts
	}
	if spec.Choices == nil {
		spec.Choices = empty.Choices
	}
	if spec.LogicNeeds == nil {
		spec.LogicNeeds = empty.LogicNeeds
	}
	if spec.Rejected == nil {
		spec.Rejected = empty.Rejected
	}
	if spec.LastIDs == nil {
		spec.LastIDs = empty.LastIDs
	}
	return spec
}

// recordAllIDs records in spec the highest id given of every kind of from.
func recordAllIDs(spec *interfaces.RequirementSpec, from *interfaces.RequirementSpec) {
	for prefix, n := range from.LastIDs {
		spec.LastIDs[prefix] = max(spec.LastIDs[prefix], n)
	}
	recordIDs(spec, "s", from.Scenarios, scenarioID)
	recordIDs(spec, "c", from.Concepts, conceptID)
	recordIDs(spec, "q", from.Questions, questionID)
	recordIDs(spec, "r", from.Rules, ruleID)
	recordIDs(spec, "k", from.Conflicts, conflictID)
	recordIDs(spec, "f", from.Choices, choiceID)
	recordIDs(spec, "n", from.LogicNeeds, logicNeedID)
}

// questionRefs are the concepts and kinetic needs the questions rest on, as their basis, inputs
// and needs name them.
func questionRefs(questions []*interfaces.RequirementQuestion) (map[string]bool, map[string]bool) {
	concepts, needs := map[string]bool{}, map[string]bool{}
	for _, q := range questions {
		for _, id := range q.Basis.ConceptIDs {
			concepts[id] = true
		}
		if q.Inputs != nil {
			for _, in := range *q.Inputs {
				concepts[in.ConceptID] = true
			}
		}
		for _, id := range q.LogicNeedIDs {
			needs[id] = true
		}
	}
	return concepts, needs
}

// draftBase is the specification scenario drafting adds to: what it keeps of the previous one, all
// of it but the scenarios, questions and concepts of an earlier drafting no one made theirs. With
// discard set it keeps no scenario or question.
func draftBase(previous *interfaces.Requirement, discard bool) *interfaces.RequirementSpec {
	spec := emptySpec()
	if previous == nil {
		return spec
	}
	from := &previous.Spec
	recordAllIDs(spec, from)
	scenarios := map[string]bool{}
	for _, q := range from.Questions {
		if discard || !q.Accepted && !q.Edited {
			continue
		}
		// A question drafted before draft times were recorded takes the previous drafting's.
		if q.DraftTime == 0 {
			q.DraftTime = previous.CreateTime
		}
		spec.Questions = append(spec.Questions, q)
		for _, id := range q.ScenarioIDs {
			scenarios[id] = true
		}
	}
	for _, s := range from.Scenarios {
		if !discard && (s.Accepted || s.Edited) || scenarios[s.ID] {
			spec.Scenarios = append(spec.Scenarios, s)
		}
	}
	concepts, _ := questionRefs(spec.Questions)
	for _, c := range from.Concepts {
		drafted := c.Source == "" || c.Source == interfaces.REQUIREMENT_CONCEPT_FROM_SCENARIOS
		if !drafted || concepts[c.ID] || !discard && c.Edited {
			spec.Concepts = append(spec.Concepts, c)
		}
	}
	spec.Rules = append(spec.Rules, from.Rules...)
	spec.Conflicts = append(spec.Conflicts, from.Conflicts...)
	spec.Choices = append(spec.Choices, from.Choices...)
	spec.LogicNeeds = append(spec.LogicNeeds, from.LogicNeeds...)
	spec.Rejected = rejectedOf(from.Rejected, "")
	spec.Runs = from.Runs
	return spec
}

// sameText tells two names or texts apart only by case and the spaces around them.
func sameText(a string, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// keptScenario is the kept scenario a drafted one repeats, by its name.
func keptScenario(spec *interfaces.RequirementSpec, name string) *interfaces.RequirementScenario {
	i := slices.IndexFunc(spec.Scenarios, func(s *interfaces.RequirementScenario) bool { return sameText(s.Name, name) })
	if i < 0 {
		return nil
	}
	return spec.Scenarios[i]
}

func conflictID(k *interfaces.RequirementConflict) string   { return k.ID }
func choiceID(f *interfaces.RequirementChoice) string       { return f.ID }
func logicNeedID(n *interfaces.RequirementLogicNeed) string { return n.ID }

// messageKept is what the message names of the kept scenarios and questions, by the ids the Agent
// may use for them.
type messageKept struct {
	Scenarios []messageKeptScenario `json:"scenarios"`
	Questions []messageKeptQuestion `json:"questions"`
}

type messageKeptScenario struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type messageKeptQuestion struct {
	Text        string   `json:"text"`
	ScenarioIDs []string `json:"scenario_ids"`
}

// messageKeptOf names the kept scenarios and questions of spec, nil when there are none.
func messageKeptOf(spec *interfaces.RequirementSpec) *messageKept {
	if len(spec.Scenarios)+len(spec.Questions) == 0 {
		return nil
	}
	m := &messageKept{Scenarios: []messageKeptScenario{}, Questions: []messageKeptQuestion{}}
	for _, s := range spec.Scenarios {
		m.Scenarios = append(m.Scenarios, messageKeptScenario{ID: keptPrefix + s.ID, Name: s.Name})
	}
	for _, q := range spec.Questions {
		ids := []string{}
		for _, id := range q.ScenarioIDs {
			ids = append(ids, keptPrefix+id)
		}
		m.Questions = append(m.Questions, messageKeptQuestion{Text: q.Text, ScenarioIDs: ids})
	}
	return m
}
