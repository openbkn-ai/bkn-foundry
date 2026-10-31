// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"errors"
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
// by a person before acceptance was recorded, the rest as drafted.
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
					DraftTime: 300, Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE},
				{ID: "q3", Text: "Who moved last month?", ScenarioIDs: []string{"s3"}, Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE},
				{ID: "q7", Text: "Who leads a department?", ScenarioIDs: []string{"s3"}, Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE},
			},
			Rules: []*interfaces.RequirementRule{{ID: "r1", Statement: "A person belongs to one department", Edited: true},
				{ID: "r2", Statement: "Moves are approved"}},
			Conflicts: []*interfaces.RequirementConflict{{ID: "k1", Topic: "Grade", Deferred: true}, {ID: "k2", Topic: "Leave"}},
			Choices: []*interfaces.RequirementChoice{{ID: "f1", Question: "Team as object?", Decision: &interfaces.RequirementChoiceDecision{OptionID: "o1"}},
				{ID: "f2", Question: "Site as object?"}},
			Rejected: []*interfaces.RequirementRejected{},
		}}
}

func ids[T any](items []T, idOf func(T) string) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, idOf(item))
	}
	return out
}

func Test_KeptOf(t *testing.T) {
	Convey("Test what drafting again keeps\n", t, func() {
		Convey("Accepted and changed questions, the scenarios and concepts they rest on, changed items and decisions\n", func() {
			kept := keptOf(staffSpec())

			So(ids(kept.Questions, questionID), ShouldResemble, []string{"q1", "q2"})
			// s1 is accepted, s2 the scenario of a question kept, s4 changed by a person; s3 none of them.
			So(ids(kept.Scenarios, scenarioID), ShouldResemble, []string{"s1", "s2", "s4"})
			// c1 is a question's basis, c2 its input's concept, c3 changed by a person.
			So(ids(kept.Concepts, conceptID), ShouldResemble, []string{"c1", "c2", "c3"})
			So(ids(kept.Rules, ruleID), ShouldResemble, []string{"r1"})
			So(ids(kept.Conflicts, conflictID), ShouldResemble, []string{"k1"})
			So(ids(kept.Choices, choiceID), ShouldResemble, []string{"f1"})
			// No id given before is given again, q7 not kept included.
			So(kept.LastIDs, ShouldResemble, map[string]int{"s": 4, "c": 4, "q": 7, "r": 2, "k": 2, "f": 2})
			// A question drafted before draft times were recorded takes its specification's.
			So(kept.Questions[0].DraftTime, ShouldEqual, 500)
			So(kept.Questions[1].DraftTime, ShouldEqual, 300)
		})

		Convey("Nothing is kept of a drafting, of none, or of a specification no one made theirs\n", func() {
			drafting := staffSpec()
			drafting.Status = interfaces.REQUIREMENT_STATUS_DRAFTING
			untouched := &interfaces.Requirement{Status: interfaces.REQUIREMENT_STATUS_READY, Spec: interfaces.RequirementSpec{
				Questions: []*interfaces.RequirementQuestion{{ID: "q1", Text: "Who?"}}}}

			So(keptOf(nil), ShouldBeNil)
			So(keptOf(drafting), ShouldBeNil)
			So(keptOf(untouched), ShouldBeNil)
		})

		Convey("A failed drafting holds what it kept, kept again\n", func() {
			failed := staffSpec()
			failed.Status = interfaces.REQUIREMENT_STATUS_FAILED

			So(ids(keptOf(failed).Questions, questionID), ShouldResemble, []string{"q1", "q2"})
		})
	})
}

func Test_BuildSpecKeeps(t *testing.T) {
	Convey("Test drafting around what is kept\n", t, func() {
		kept := keptOf(staffSpec())
		noDocs := map[string]*interfaces.RequirementDocument{}

		Convey("Kept items stay first and as they are; drafted ones join them and take ids none had\n", func() {
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
				Rules:     []agentRule{{Statement: "A person belongs to one department"}},
				Conflicts: []agentConflict{{Topic: "Grade"}},
				Choices:   []agentChoice{{Question: "Team as object?"}},
			}

			spec, _ := buildSpec(out, noDocs, staffOntology(), kept)

			So(ids(spec.Scenarios, scenarioID), ShouldResemble, []string{"s1", "s2", "s4", "s5"})
			So(spec.Scenarios[0], ShouldEqual, kept.Scenarios[0])
			So(ids(spec.Concepts, conceptID), ShouldResemble, []string{"c1", "c2", "c3", "c5"})
			So(ids(spec.Questions, questionID), ShouldResemble, []string{"q1", "q2", "q8", "q9"})
			q8, q9 := spec.Questions[2], spec.Questions[3]
			// Kept ids are named by the Agent with their prefix; repeats are its own names for them.
			So(q8.ScenarioIDs, ShouldResemble, []string{"s5", "s1"})
			So(q8.Basis.ConceptIDs, ShouldResemble, []string{"c1", "c2", "c5"})
			So(q8.Needs, ShouldResemble, []interfaces.RequirementItemRef{departmentRef, employeeRef})
			So(q9.ScenarioIDs, ShouldResemble, []string{"s1"})
			// Without documents every item rests on its reason; rules, conflicts and choices need
			// documents, and repeats of kept ones are not listed rejected.
			So(q8.Citations, ShouldBeEmpty)
			So(ids(spec.Rules, ruleID), ShouldResemble, []string{"r1"})
			So(ids(spec.Conflicts, conflictID), ShouldResemble, []string{"k1"})
			So(ids(spec.Choices, choiceID), ShouldResemble, []string{"f1"})
			So(spec.Rejected, ShouldBeEmpty)
		})

		Convey("A concept maps only to an adopted item\n", func() {
			badge := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "badges"}
			out := &draftOutput{Concepts: []agentConcept{{ID: "b", Term: "Access badge", Category: "object",
				SupplementReason: "x", MapsTo: &badge}}}

			spec, _ := buildSpec(out, noDocs, staffOntology(), nil)

			So(spec.Concepts[0].MapsTo, ShouldBeNil)
		})

		Convey("The message names the kept items by prefixed ids, for the Agent to build on\n", func() {
			message, _, _, err := buildDraftMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, nil, staffOntology(), kept)

			So(err, ShouldBeNil)
			var body draftMessage
			So(json.Unmarshal([]byte(message[strings.Index(message, "{"):]), &body), ShouldBeNil)
			So(body.Documents, ShouldBeEmpty)
			So(body.ObjectTypes, ShouldHaveLength, 2)
			So(body.Kept.Scenarios[0], ShouldResemble, messageKeptScenario{ID: "kept-s1", Name: "Staff a project"})
			So(body.Kept.Concepts[1].ID, ShouldEqual, "kept-c2")
			So(body.Kept.Concepts[1].MapsTo, ShouldResemble, &employeeRef)
			So(body.Kept.Questions[0], ShouldResemble, messageKeptQuestion{Text: "Which department is an employee in?",
				ScenarioIDs: []string{"kept-s1"}})

			message, _, _, _ = buildDraftMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, nil, staffOntology(), nil)
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
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(staffOntology(), true, nil).Times(2)
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
			answer(`{"scenarios":[],"concepts":[],"questions":[{"text":"Who joined this year?","scenario_ids":["kept-s1"],
				"layer":"knowledge","supplement_reason":"staffing"}],"rules":[],"conflicts":[],"choices":[],"doc_types":[]}`)

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_READY)
			So(ids(finished.Spec.Questions, questionID), ShouldResemble, []string{"q1", "q2", "q8"})
			So(finished.Spec.Questions[0].DraftTime, ShouldEqual, 500)
			So(finished.Spec.Questions[2].DraftTime, ShouldEqual, 1000)
			So(finished.Spec.Questions[2].ScenarioIDs, ShouldResemble, []string{"s1"})
			// A kept question keeps the ontology it was accepted on; the new one is signed now, as
			// is a kept one never signed.
			So(finished.Spec.Questions[0].OntologySignature, ShouldEqual, "as accepted")
			So(finished.Spec.Questions[2].OntologySignature, ShouldEqual, ontologySignature(staffOntology(), finished.Spec.Questions[2]))
			So(finished.Spec.Questions[1].OntologySignature, ShouldNotBeEmpty)
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
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, now: func() int64 { return 1000 }, newID: func() string { return "run2" }}
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, CatalogIDs: []string{"c1"}}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{}, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(staffOntology(), true, nil)
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
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, ba: ba,
			now: func() int64 { return 1000 }, newID: func() string { return "run2" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, CatalogIDs: []string{"c1"}}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{}, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(staffOntology(), true, nil).Times(2)
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(staffSpec(), true, nil)
		ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), int64(0)).Return(true, nil)
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
