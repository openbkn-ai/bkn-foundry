// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Drafting again keeps what people made theirs: the scenarios and questions they accepted, added
// or changed, the scenarios and concepts those questions rest on, the concepts and rules they
// changed, and the conflicts and choices they decided. Kept items keep their ids, so a question's
// test questions and guidance stay its own; the rest is drafted anew around them.

// keptPrefix marks a kept item's id in the message, so the Agent can name it apart from its own.
const keptPrefix = "kept-"

// keptOf is what drafting again keeps of the previous specification, nil when it keeps nothing.
// A question drafted before draft times were recorded takes the previous drafting's.
func keptOf(previous *interfaces.Requirement) *interfaces.RequirementSpec {
	// A failed drafting holds what it kept.
	if previous == nil || (previous.Status != interfaces.REQUIREMENT_STATUS_READY && previous.Status != interfaces.REQUIREMENT_STATUS_FAILED) {
		return nil
	}
	spec := &previous.Spec
	kept := &interfaces.RequirementSpec{
		Scenarios: []*interfaces.RequirementScenario{}, Concepts: []*interfaces.RequirementConcept{},
		Questions: []*interfaces.RequirementQuestion{}, Rules: []*interfaces.RequirementRule{},
		Conflicts: []*interfaces.RequirementConflict{}, Choices: []*interfaces.RequirementChoice{},
		LastIDs: map[string]int{},
	}
	for prefix, n := range spec.LastIDs {
		kept.LastIDs[prefix] = n
	}
	// No id given before is given again, of a kept item or not.
	recordIDs(kept, "s", spec.Scenarios, scenarioID)
	recordIDs(kept, "c", spec.Concepts, conceptID)
	recordIDs(kept, "q", spec.Questions, questionID)
	recordIDs(kept, "r", spec.Rules, ruleID)
	recordIDs(kept, "k", spec.Conflicts, conflictID)
	recordIDs(kept, "f", spec.Choices, choiceID)

	scenarios, concepts := map[string]bool{}, map[string]bool{}
	for _, q := range spec.Questions {
		if !q.Accepted && !q.Edited {
			continue
		}
		if q.DraftTime == 0 {
			q.DraftTime = previous.CreateTime
		}
		kept.Questions = append(kept.Questions, q)
		for _, id := range q.ScenarioIDs {
			scenarios[id] = true
		}
		for _, id := range q.Basis.ConceptIDs {
			concepts[id] = true
		}
		if q.Inputs != nil {
			for _, in := range *q.Inputs {
				concepts[in.ConceptID] = true
			}
		}
	}
	for _, s := range spec.Scenarios {
		if s.Accepted || s.Edited || scenarios[s.ID] {
			kept.Scenarios = append(kept.Scenarios, s)
		}
	}
	for _, c := range spec.Concepts {
		if c.Edited || concepts[c.ID] {
			kept.Concepts = append(kept.Concepts, c)
		}
	}
	for _, r := range spec.Rules {
		if r.Edited {
			kept.Rules = append(kept.Rules, r)
		}
	}
	for _, k := range spec.Conflicts {
		if k.Resolution != nil || k.Deferred {
			kept.Conflicts = append(kept.Conflicts, k)
		}
	}
	for _, f := range spec.Choices {
		if f.Decision != nil {
			kept.Choices = append(kept.Choices, f)
		}
	}
	if len(kept.Scenarios)+len(kept.Concepts)+len(kept.Questions)+len(kept.Rules)+len(kept.Conflicts)+len(kept.Choices) == 0 {
		return nil
	}
	return kept
}

// startingSpec is the specification drafting adds to: a copy of what is kept, or an empty one.
func startingSpec(kept *interfaces.RequirementSpec) *interfaces.RequirementSpec {
	spec := &interfaces.RequirementSpec{
		Scenarios: []*interfaces.RequirementScenario{}, Concepts: []*interfaces.RequirementConcept{},
		Questions: []*interfaces.RequirementQuestion{}, Rules: []*interfaces.RequirementRule{},
		Conflicts: []*interfaces.RequirementConflict{}, Choices: []*interfaces.RequirementChoice{},
		Rejected: []*interfaces.RequirementRejected{},
	}
	if kept == nil {
		return spec
	}
	spec.Scenarios = append(spec.Scenarios, kept.Scenarios...)
	spec.Concepts = append(spec.Concepts, kept.Concepts...)
	spec.Questions = append(spec.Questions, kept.Questions...)
	spec.Rules = append(spec.Rules, kept.Rules...)
	spec.Conflicts = append(spec.Conflicts, kept.Conflicts...)
	spec.Choices = append(spec.Choices, kept.Choices...)
	spec.LastIDs = map[string]int{}
	for prefix, n := range kept.LastIDs {
		spec.LastIDs[prefix] = n
	}
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

func conflictID(k *interfaces.RequirementConflict) string { return k.ID }
func choiceID(f *interfaces.RequirementChoice) string     { return f.ID }

// messageKept is what the message names of the kept items, by the ids the Agent may use for them.
type messageKept struct {
	Scenarios []messageKeptScenario `json:"scenarios"`
	Concepts  []messageKeptConcept  `json:"concepts"`
	Questions []messageKeptQuestion `json:"questions"`
}

type messageKeptScenario struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type messageKeptConcept struct {
	ID     string                         `json:"id"`
	Term   string                         `json:"term"`
	MapsTo *interfaces.RequirementItemRef `json:"maps_to,omitempty"`
}

type messageKeptQuestion struct {
	Text        string   `json:"text"`
	ScenarioIDs []string `json:"scenario_ids"`
}

func messageKeptOf(kept *interfaces.RequirementSpec) *messageKept {
	if kept == nil {
		return nil
	}
	m := &messageKept{Scenarios: []messageKeptScenario{}, Concepts: []messageKeptConcept{}, Questions: []messageKeptQuestion{}}
	for _, s := range kept.Scenarios {
		m.Scenarios = append(m.Scenarios, messageKeptScenario{ID: keptPrefix + s.ID, Name: s.Name})
	}
	for _, c := range kept.Concepts {
		m.Concepts = append(m.Concepts, messageKeptConcept{ID: keptPrefix + c.ID, Term: c.Term, MapsTo: c.MapsTo})
	}
	for _, q := range kept.Questions {
		ids := []string{}
		for _, id := range q.ScenarioIDs {
			ids = append(ids, keptPrefix+id)
		}
		m.Questions = append(m.Questions, messageKeptQuestion{Text: q.Text, ScenarioIDs: ids})
	}
	return m
}
