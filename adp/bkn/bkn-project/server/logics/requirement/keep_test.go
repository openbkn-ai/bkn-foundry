// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// Drafting again keeps what people made theirs; tested on a staff ontology drafted with no
// documents, every item resting on the ontology alone.

var (
	employeeRef   = interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "employees"}
	departmentRef = interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "departments"}
)

// confirmedStaff is the staff ontology, confirmed.
func confirmedStaff() *interfaces.Draft {
	draft := staffOntology()
	draft.Items.Confirmation = &interfaces.DraftConfirmation{Commit: "abc"}
	return draft
}

// staffOntology is an adopted staff ontology, and a candidate no one adopted.
func staffOntology() *interfaces.Draft {
	return &interfaces.Draft{Status: interfaces.DRAFT_STATUS_READY, Items: interfaces.DraftItems{
		ObjectTypes: []*interfaces.DraftObjectType{
			{ID: "employees", Name: "Employees", Adopted: true, PrimaryKeys: []string{"emp_no"},
				Properties: []*interfaces.DraftProperty{{Name: "emp_no", Type: "string"}, {Name: "dept_code", Type: "string"}}},
			{ID: "departments", Name: "Departments", Adopted: true, PrimaryKeys: []string{"code"},
				Properties: []*interfaces.DraftProperty{{Name: "code", Type: "string"}}},
			{ID: "badges", Name: "Badges"},
		},
		RelationTypes: []*interfaces.DraftRelationType{{ID: "employees_to_departments", Name: "Department of employee",
			Source: "employees", Target: "departments", Adopted: true,
			Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "dept_code", TargetProperty: "code"}}}},
	}}
}

// staffSpec is a ready specification of the staff ontology: s1 and q1 accepted, q2 and s4 changed
// by a person before acceptance was recorded, the rest as drafted; its concepts were drafted before
// concepts were extracted on their own. q2 rests on the kinetic need n1.
func staffSpec() *interfaces.Requirement {
	inputs := []interfaces.RequirementInput{{Name: "employee", ConceptID: "c2", Property: "emp_no", Role: interfaces.REQUIREMENT_INPUT_RECORD}}
	return &interfaces.Requirement{ProjectID: "p1", Version: 2, Status: interfaces.REQUIREMENT_STATUS_READY, CreateTime: 500, UpdateTime: 7,
		Spec: interfaces.RequirementSpec{
			Scenarios: []*interfaces.RequirementScenario{
				{ID: "s1", Name: "Staff a project", Accepted: true, SupplementReason: "from the ontology"},
				{ID: "s2", Name: "Plan headcount", SupplementReason: "from the ontology"},
				{ID: "s3", Name: "Review moves", SupplementReason: "from the ontology"},
				{ID: "s4", Name: "Report attrition", Edited: true},
			},
			Concepts: []*interfaces.RequirementConcept{
				{ID: "c1", Term: "Department", Category: "object", MapsTo: &departmentRef},
				{ID: "c2", Term: "Employee", Category: "object", MapsTo: &employeeRef},
				{ID: "c3", Term: "Badge", Category: "object", Edited: true},
				{ID: "c4", Term: "Grade", Category: "property"},
			},
			Questions: []*interfaces.RequirementQuestion{
				{ID: "q1", Text: "Which department is an employee in?", ScenarioIDs: []string{"s1"}, Accepted: true, OntologySignature: "as accepted",
					Basis: interfaces.RequirementBasis{ConceptIDs: []string{"c1"}}, Inputs: &inputs,
					Needs: []interfaces.RequirementItemRef{departmentRef, employeeRef}, Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE},
				{ID: "q2", Text: "How many employees has a department?", ScenarioIDs: []string{"s2"}, Edited: true,
					DraftTime: 300, Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY, LogicNeedIDs: []string{"n1"}},
				{ID: "q3", Text: "Who moved last month?", ScenarioIDs: []string{"s3"}, Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE},
				{ID: "q7", Text: "Who leads a department?", ScenarioIDs: []string{"s3"}, Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE},
			},
			Rules: []*interfaces.RequirementRule{{ID: "r1", Statement: "A person belongs to one department", Edited: true},
				{ID: "r2", Statement: "Moves are approved"}},
			Conflicts: []*interfaces.RequirementConflict{{ID: "k1", Topic: "Grade", Deferred: true}, {ID: "k2", Topic: "Leave"}},
			Choices: []*interfaces.RequirementChoice{{ID: "f1", Question: "Team as object?", Decision: &interfaces.RequirementChoiceDecision{OptionID: "o1"}},
				{ID: "f2", Question: "Site as object?"}},
			LogicNeeds: []*interfaces.RequirementLogicNeed{
				{ID: "n1", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Headcount", Definition: "employees per department",
					Source: interfaces.LOGIC_NEED_FROM_ANALYSIS, ScenarioIDs: []string{}},
				{ID: "n2", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Move approval", Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS,
					ScenarioIDs: []string{}},
			},
			Rejected: []*interfaces.RequirementRejected{
				{Kind: rejectedScenario, Text: "Hire everyone", Reason: rejectedQuoteNotFound},
				{Kind: rejectedRule, Text: "Moves are cheap", Reason: rejectedQuoteNotFound, Run: interfaces.REQUIREMENT_RUN_CONCEPTS},
			},
			Runs: map[string]*interfaces.RequirementRun{interfaces.REQUIREMENT_RUN_CONCEPTS: {
				Status: interfaces.REQUIREMENT_RUN_READY, RunID: "c"}},
		}}
}

func ids[T any](items []T, idOf func(T) string) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, idOf(item))
	}
	return out
}

func Test_DraftBase(t *testing.T) {
	Convey("Test what scenario drafting adds to\n", t, func() {
		Convey("Accepted and changed questions with their scenarios, what they rest on, and all the other runs wrote\n", func() {
			previous := staffSpec()
			previous.Spec.Concepts = append(previous.Spec.Concepts,
				&interfaces.RequirementConcept{ID: "c5", Term: "Team", Category: "object", Source: interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS},
				&interfaces.RequirementConcept{ID: "c6", Term: "Site", Category: "object", Source: interfaces.REQUIREMENT_CONCEPT_FROM_SCENARIOS},
				&interfaces.RequirementConcept{ID: "c7", Term: "Desk", Category: "object", Source: interfaces.REQUIREMENT_CONCEPT_FROM_PERSON})

			base := draftBase(previous, false)

			So(ids(base.Questions, questionID), ShouldResemble, []string{"q1", "q2"})
			// s1 is accepted, s2 the scenario of a question kept, s4 changed by a person; s3 none of them.
			So(ids(base.Scenarios, scenarioID), ShouldResemble, []string{"s1", "s2", "s4"})
			// c1 is a question's basis, c2 its input's concept, c3 changed by a person; c4 and c6 an
			// earlier drafting's that nothing rests on. Extracted and added ones are the other runs'.
			So(ids(base.Concepts, conceptID), ShouldResemble, []string{"c1", "c2", "c3", "c5", "c7"})
			So(ids(base.Rules, ruleID), ShouldResemble, []string{"r1", "r2"})
			So(ids(base.Conflicts, conflictID), ShouldResemble, []string{"k1", "k2"})
			So(ids(base.Choices, choiceID), ShouldResemble, []string{"f1", "f2"})
			So(ids(base.LogicNeeds, logicNeedID), ShouldResemble, []string{"n1", "n2"})
			So(base.Runs, ShouldEqual, previous.Spec.Runs)
			// What drafting rejected goes: it lists its own again; the other runs' stays.
			So(base.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedRule, Text: "Moves are cheap", Reason: rejectedQuoteNotFound, Run: interfaces.REQUIREMENT_RUN_CONCEPTS}})
			// No id given before is given again, q7 not kept included.
			So(base.LastIDs, ShouldResemble, map[string]int{"s": 4, "c": 7, "q": 7, "r": 2, "k": 2, "f": 2, "n": 2})
			// A question drafted before draft times were recorded takes its specification's.
			So(base.Questions[0].DraftTime, ShouldEqual, 500)
			So(base.Questions[1].DraftTime, ShouldEqual, 300)
		})

		Convey("Starting over keeps no scenario or question, nor the drafted concepts they rested on\n", func() {
			base := draftBase(staffSpec(), true)

			So(base.Questions, ShouldBeEmpty)
			So(base.Scenarios, ShouldBeEmpty)
			So(base.Concepts, ShouldBeEmpty)
			So(ids(base.Rules, ruleID), ShouldResemble, []string{"r1", "r2"})
			So(ids(base.LogicNeeds, logicNeedID), ShouldResemble, []string{"n1", "n2"})
			So(base.LastIDs["q"], ShouldEqual, 7)
		})

		Convey("Without a specification it is empty, its lists empty\n", func() {
			base := draftBase(nil, false)

			So(base.Scenarios, ShouldNotBeNil)
			So(base.LogicNeeds, ShouldNotBeNil)
			So(base.LastIDs, ShouldBeEmpty)
			So(messageKeptOf(base), ShouldBeNil)
		})
	})
}

func Test_KeepForConcepts(t *testing.T) {
	Convey("Test what the concepts run keeps\n", t, func() {
		docsNeed := func(id string, accepted, edited bool, conceptIDs ...string) *interfaces.RequirementLogicNeed {
			return &interfaces.RequirementLogicNeed{ID: id, Kind: interfaces.LOGIC_NEED_METRIC, Name: id,
				Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS, Accepted: accepted, Edited: edited, ConceptIDs: conceptIDs}
		}
		extracted := func(id string, edited bool) *interfaces.RequirementConcept {
			return &interfaces.RequirementConcept{ID: id, Term: id, Category: "object",
				Source: interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS, Edited: edited}
		}
		spec := func() *interfaces.RequirementSpec {
			spec := emptySpec()
			spec.Concepts = []*interfaces.RequirementConcept{
				extracted("c1", false), extracted("c2", true), extracted("c3", false), extracted("c4", false),
				{ID: "c5", Term: "Site", Source: interfaces.REQUIREMENT_CONCEPT_FROM_SCENARIOS},
				{ID: "c6", Term: "Desk", Source: interfaces.REQUIREMENT_CONCEPT_FROM_PERSON},
				// Drafted before concepts were extracted on their own: extracted again.
				{ID: "c7", Term: "Legacy"},
			}
			spec.Questions = []*interfaces.RequirementQuestion{{ID: "q1", Basis: interfaces.RequirementBasis{ConceptIDs: []string{"c3"}},
				LogicNeedIDs: []string{"n3"}}}
			spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
				docsNeed("n1", false, false, "c1"), docsNeed("n2", true, false, "c4"), docsNeed("n3", false, false),
				docsNeed("n4", false, true),
				{ID: "n5", Kind: interfaces.LOGIC_NEED_API, Name: "n5", Source: interfaces.LOGIC_NEED_FROM_ANALYSIS},
			}
			spec.Rules = []*interfaces.RequirementRule{{ID: "r1", Edited: true}, {ID: "r2"}}
			spec.Conflicts = []*interfaces.RequirementConflict{{ID: "k1", Deferred: true},
				{ID: "k2", Resolution: &interfaces.RequirementResolution{Reason: "r"}}, {ID: "k3"}}
			spec.Choices = []*interfaces.RequirementChoice{{ID: "f1", Decision: &interfaces.RequirementChoiceDecision{OptionID: "o1"}}, {ID: "f2"}}
			return spec
		}

		Convey("What people made theirs and what questions and kept needs rest on stays; the other runs' items too\n", func() {
			s := spec()

			keepForConcepts(s, false)

			// n1 no one made theirs; n2 accepted, n3 a question rests on, n4 changed, n5 the analysis'.
			So(ids(s.LogicNeeds, logicNeedID), ShouldResemble, []string{"n2", "n3", "n4", "n5"})
			// c1 rested only on n1, which went; c4 rests on n2, kept; c3 a question's; c2 changed.
			So(ids(s.Concepts, conceptID), ShouldResemble, []string{"c2", "c3", "c4", "c5", "c6"})
			So(ids(s.Rules, ruleID), ShouldResemble, []string{"r1"})
			So(ids(s.Conflicts, conflictID), ShouldResemble, []string{"k1", "k2"})
			So(ids(s.Choices, choiceID), ShouldResemble, []string{"f1"})
		})

		Convey("Starting over keeps of them only what questions rest on\n", func() {
			s := spec()

			keepForConcepts(s, true)

			So(ids(s.LogicNeeds, logicNeedID), ShouldResemble, []string{"n3", "n5"})
			So(ids(s.Concepts, conceptID), ShouldResemble, []string{"c3", "c5", "c6"})
			So(s.Rules, ShouldBeEmpty)
			So(s.Conflicts, ShouldBeEmpty)
			So(s.Choices, ShouldBeEmpty)
		})

		Convey("A need linked to an implementation stays, even when starting over\n", func() {
			for _, discard := range []bool{false, true} {
				s := spec()
				s.LogicNeeds = append(s.LogicNeeds, &interfaces.RequirementLogicNeed{ID: "n6", Kind: interfaces.LOGIC_NEED_METRIC,
					Name: "n6", Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS, ConceptIDs: []string{"c1"},
					Link: &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, ID: "m1"}})

				keepForConcepts(s, discard)

				So(ids(s.LogicNeeds, logicNeedID), ShouldContain, "n6")
				// The concept the linked need rests on stays with it.
				So(ids(s.Concepts, conceptID), ShouldContain, "c1")
			}
		})
	})
}

func Test_KeepForKinetic(t *testing.T) {
	Convey("Test what the kinetic run keeps\n", t, func() {
		spec := func() *interfaces.RequirementSpec {
			spec := emptySpec()
			need := func(id, source string, accepted, edited bool) *interfaces.RequirementLogicNeed {
				return &interfaces.RequirementLogicNeed{ID: id, Kind: interfaces.LOGIC_NEED_METRIC, Name: id, Source: source,
					Accepted: accepted, Edited: edited}
			}
			spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
				need("n1", interfaces.LOGIC_NEED_FROM_ANALYSIS, false, false),
				need("n2", interfaces.LOGIC_NEED_FROM_ANALYSIS, true, false),
				need("n3", interfaces.LOGIC_NEED_FROM_SCENARIOS, false, true),
				need("n4", interfaces.LOGIC_NEED_FROM_SCENARIOS, false, false),
				need("n5", interfaces.LOGIC_NEED_FROM_ANALYSIS, false, false),
				need("n6", interfaces.LOGIC_NEED_FROM_DOCUMENTS, false, false),
				need("n7", interfaces.LOGIC_NEED_FROM_PERSON, false, false),
			}
			spec.Questions = []*interfaces.RequirementQuestion{{ID: "q1", LogicNeedIDs: []string{"n5"}}}
			return spec
		}

		s := spec()
		keepForKinetic(s, false)
		So(ids(s.LogicNeeds, logicNeedID), ShouldResemble, []string{"n2", "n3", "n5", "n6", "n7"})

		s = spec()
		keepForKinetic(s, true)
		So(ids(s.LogicNeeds, logicNeedID), ShouldResemble, []string{"n5", "n6", "n7"})

		// A need linked to an implementation stays, even when starting over.
		for _, discard := range []bool{false, true} {
			s = spec()
			s.LogicNeeds[0].Link = &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, ID: "m1"}
			keepForKinetic(s, discard)
			So(ids(s.LogicNeeds, logicNeedID), ShouldContain, "n1")
		}
	})
}

func Test_BuildSpecKeeps(t *testing.T) {
	Convey("Test drafting around what is kept\n", t, func() {
		base := func() *interfaces.RequirementSpec { return draftBase(staffSpec(), false) }
		noDocs := map[string]*interfaces.RequirementDocument{}

		Convey("Kept items stay first and as they are; drafted ones join them and take ids none had\n", func() {
			spec := base()
			keptScenario := spec.Scenarios[0]
			out := &draftOutput{
				Scenarios: []agentScenario{
					{ID: "a", Name: "Onboard a hire", SupplementReason: "employees and departments"},
					// The same scenario as one kept: merged into it.
					{ID: "b", Name: " staff a PROJECT ", SupplementReason: "again"},
				},
				Concepts: []agentConcept{
					{ID: "x", Term: "department", Category: "object", SupplementReason: "again",
						MapsTo: &departmentRef},
					{ID: "y", Term: "Hire date", Category: "property", SupplementReason: "when they start"},
				},
				Questions: []agentQuestion{
					{Text: "Which department does a hire join?", ScenarioIDs: []string{"a", "kept-s1"}, ConceptIDs: []string{"x", "kept-c2", "y"},
						Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE, SupplementReason: "onboarding needs it"},
					{Text: "Which projects can be staffed?", ScenarioIDs: []string{"b"}, Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
						SupplementReason: "staffing needs it"},
					// A kept question drafted again is not repeated.
					{Text: "which department is an employee in?", SupplementReason: "again"},
				},
			}

			buildSpec(out, noDocs, staffOntology(), spec)

			So(ids(spec.Scenarios, scenarioID), ShouldResemble, []string{"s1", "s2", "s4", "s5"})
			So(spec.Scenarios[0], ShouldEqual, keptScenario)
			So(ids(spec.Concepts, conceptID), ShouldResemble, []string{"c1", "c2", "c3", "c5"})
			So(ids(spec.Questions, questionID), ShouldResemble, []string{"q1", "q2", "q8", "q9"})
			q8, q9 := spec.Questions[2], spec.Questions[3]
			// Kept ids are named by the Agent with their prefix; repeats are its own names for them.
			So(q8.ScenarioIDs, ShouldResemble, []string{"s5", "s1"})
			So(q8.Basis.ConceptIDs, ShouldResemble, []string{"c1", "c2", "c5"})
			So(q8.Needs, ShouldResemble, []interfaces.RequirementItemRef{departmentRef, employeeRef})
			So(q9.ScenarioIDs, ShouldResemble, []string{"s1"})
			So(q8.Citations, ShouldBeEmpty)
			// Rules, conflicts, choices and needs are the other runs': drafting leaves them as they are.
			So(ids(spec.Rules, ruleID), ShouldResemble, []string{"r1", "r2"})
			So(ids(spec.Conflicts, conflictID), ShouldResemble, []string{"k1", "k2"})
			So(ids(spec.Choices, choiceID), ShouldResemble, []string{"f1", "f2"})
			// The kept q2 rests on n1: n1 serves its scenario.
			So(spec.LogicNeeds[0].ScenarioIDs, ShouldResemble, []string{"s2"})
			So(spec.Rejected, ShouldHaveLength, 1)
		})

		Convey("At most maxSpecQuestions new questions are kept; kept ones do not count\n", func() {
			spec := base()
			kept := len(spec.Questions)
			out := &draftOutput{}
			for i := range maxSpecQuestions + 1 {
				out.Questions = append(out.Questions, agentQuestion{Text: fmt.Sprintf("Which employees joined in week %d?", i),
					Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE, SupplementReason: "staffing"})
			}

			buildSpec(out, noDocs, staffOntology(), spec)

			So(spec.Questions, ShouldHaveLength, kept+maxSpecQuestions)
			So(spec.Rejected[len(spec.Rejected)-1].Reason, ShouldEqual, rejectedTooMany)
		})

		Convey("A concept maps only to an adopted item\n", func() {
			badge := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "badges"}
			out := &draftOutput{Concepts: []agentConcept{{ID: "b", Term: "Access badge", Category: "object",
				SupplementReason: "x", MapsTo: &badge}}}

			spec := built(out, noDocs, staffOntology())

			So(spec.Concepts[0].MapsTo, ShouldBeNil)
		})

		Convey("The message names the kept items by prefixed ids, and the concepts, rules and needs by their own\n", func() {
			spec := base()
			message, _, _, err := buildDraftMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, nil, staffOntology(), spec, spec, nil, "", interfaces.RequirementKnowledge{})

			So(err, ShouldBeNil)
			var body draftMessage
			So(json.Unmarshal([]byte(message[strings.Index(message, "{"):]), &body), ShouldBeNil)
			So(body.Documents, ShouldBeEmpty)
			So(body.ObjectTypes, ShouldHaveLength, 2)
			So(body.Kept.Scenarios[0], ShouldResemble, messageKeptScenario{ID: "kept-s1", Name: "Staff a project"})
			So(body.Kept.Questions[0], ShouldResemble, messageKeptQuestion{Text: "Which department is an employee in?",
				ScenarioIDs: []string{"kept-s1"}})
			So(body.Concepts[1].ID, ShouldEqual, "c2")
			So(body.Concepts[1].MapsTo, ShouldResemble, &employeeRef)
			So(body.Rules, ShouldHaveLength, 2)
			So(body.LogicNeeds[0], ShouldResemble, messageLogicNeed{ID: "n1", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Headcount",
				Definition: "employees per department"})

			message, _, _, _ = buildDraftMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, nil, staffOntology(), emptySpec(), emptySpec(), nil, "", interfaces.RequirementKnowledge{})
			So(message, ShouldNotContainSubstring, `"kept"`)
		})
	})
}

func Test_RedraftKeeps(t *testing.T) {
	Convey("Test drafting again with no documents\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, ba: ba,
			now: func() int64 { return 1000 }, newID: func() string { return "run2" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller,
			CatalogIDs: []string{"c1"}, NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{}, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmedStaff(), true, nil).Times(2)
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(staffSpec(), true, nil)
		// The row holds what is kept from the start, written only over the specification as read.
		ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), int64(7)).DoAndReturn(
			func(_ context.Context, req *interfaces.Requirement, _ int64, _ int64) (bool, error) {
				So(ids(req.Spec.Questions, questionID), ShouldResemble, []string{"q1", "q2"})
				return true, nil
			})
		va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return(nil, nil)
		var finished *interfaces.Requirement
		ra.EXPECT().FinishDrafting(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *interfaces.Requirement) (bool, error) {
				finished = req
				return true, nil
			})
		answer := func(output string) {
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
					So(req.Message, ShouldContainSubstring, `"kept":{"scenarios":[{"id":"kept-s1"`)
					So(req.Message, ShouldNotContainSubstring, "badges")
					return &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil
				})
		}

		Convey("The kept items stay with their ids and draft times; new questions take this drafting's\n", func() {
			answer(`{"scenarios":[],"concepts":[],"questions":[{"text":"Who joined after {as_of}?","template":"Who joined after {as_of}?","scenario_ids":["kept-s1"],
				"layer":"knowledge","supplement_reason":"staffing"}],"rules":[],"conflicts":[],"choices":[],"doc_types":[]}`)

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_READY)
			So(ids(finished.Spec.Questions, questionID), ShouldResemble, []string{"q1", "q2", "q8"})
			So(finished.Spec.Questions[0].DraftTime, ShouldEqual, 500)
			So(finished.Spec.Questions[2].DraftTime, ShouldEqual, 1000)
			So(finished.Spec.Questions[2].ScenarioIDs, ShouldResemble, []string{"s1"})
			// People read the text in business words; the template keeps its slot.
			So(finished.Spec.Questions[2].Text, ShouldEqual, "Who joined after a given date?")
			So(finished.Spec.Questions[2].Template, ShouldEqual, "Who joined after {as_of}?")
			// A kept question keeps the ontology it was accepted on; the new one is signed now, as
			// is a kept one never signed.
			So(finished.Spec.Questions[0].OntologySignature, ShouldEqual, "as accepted")
			So(finished.Spec.Questions[2].OntologySignature, ShouldEqual, ontologySignature(staffOntology(), finished.Spec.Questions[2]))
			So(finished.Spec.Questions[1].OntologySignature, ShouldNotBeEmpty)
			// What the concepts and kinetic runs wrote survives drafting.
			So(ids(finished.Spec.Rules, ruleID), ShouldResemble, []string{"r1", "r2"})
			So(ids(finished.Spec.Conflicts, conflictID), ShouldResemble, []string{"k1", "k2"})
			So(ids(finished.Spec.Choices, choiceID), ShouldResemble, []string{"f1", "f2"})
			So(ids(finished.Spec.LogicNeeds, logicNeedID), ShouldResemble, []string{"n1", "n2"})
			So(finished.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS].Status, ShouldEqual, interfaces.REQUIREMENT_RUN_READY)
			So(finished.Spec.Rejected, ShouldHaveLength, 1)
		})

		Convey("A drafting that fails keeps them too\n", func() {
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(nil, errors.New("down"))

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_FAILED)
			So(ids(finished.Spec.Questions, questionID), ShouldResemble, []string{"q1", "q2"})
		})
	})

	Convey("A specification changed since it was read is not drafted over\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, now: func() int64 { return 1000 }, newID: func() string { return "run2" }}
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, CatalogIDs: []string{"c1"}}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{}, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmedStaff(), true, nil)
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(staffSpec(), true, nil)
		ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), int64(7)).Return(false, interfaces.ErrRequirementChanged)

		_, err := svc.GenerateRequirement(context.Background(), token, "p1", caller, false)

		shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Stale)
	})

	Convey("Starting over keeps nothing\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, ba: ba,
			now: func() int64 { return 1000 }, newID: func() string { return "run2" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, CatalogIDs: []string{"c1"}}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{}, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmedStaff(), true, nil).Times(2)
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(staffSpec(), true, nil)
		// What the other runs wrote stays, so even starting over writes only over the specification as read.
		ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), int64(7)).Return(true, nil)
		va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return(nil, nil)
		ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
				So(req.Message, ShouldNotContainSubstring, `"kept"`)
				return nil, errors.New("down")
			})
		var finished *interfaces.Requirement
		ra.EXPECT().FinishDrafting(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *interfaces.Requirement) (bool, error) {
				finished = req
				return true, nil
			})

		_, err := svc.GenerateRequirement(context.Background(), token, "p1", caller, true)

		So(err, ShouldBeNil)
		So(finished.Spec.Questions, ShouldBeEmpty)
		So(ids(finished.Spec.Rules, ruleID), ShouldResemble, []string{"r1", "r2"})
	})
}

func Test_GuidanceOfKeptQuestion(t *testing.T) {
	Convey("Guidance generated for a kept question stays current when drafting again\n", t, func() {
		req := &interfaces.Requirement{Version: 3, CreateTime: 1000}
		kept := &interfaces.RequirementQuestion{Text: "Who?", DraftTime: 500}
		legacy := &interfaces.RequirementQuestion{Text: "Who?"}
		g := &interfaces.QuestionGuidance{SpecVersion: 2, Question: "Who?", CreateTime: 600}

		So(guidanceCurrent(req, kept, g), ShouldBeTrue)
		// Older than the question, or of another text, it is not.
		So(guidanceCurrent(req, kept, &interfaces.QuestionGuidance{Question: "Who?", CreateTime: 400}), ShouldBeFalse)
		So(guidanceCurrent(req, kept, &interfaces.QuestionGuidance{Question: "Whom?", CreateTime: 600}), ShouldBeFalse)
		// A question without a draft time keeps the old rule.
		So(guidanceCurrent(req, legacy, g), ShouldBeFalse)
		So(guidanceCurrent(req, legacy, &interfaces.QuestionGuidance{SpecVersion: 3, Question: "Who?", CreateTime: 1000}), ShouldBeTrue)
	})
}
