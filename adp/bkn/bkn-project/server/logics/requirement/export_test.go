// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"gopkg.in/yaml.v3"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// exportSpec is a specification with a capability question, a knowledge question asked about a
// record the documents give as an example, and an older one with no template.
func exportSpec() *interfaces.Requirement {
	req := readySpec()
	req.CreateTime = 10
	spec := &req.Spec
	spec.Rules[0].CompilesTo = "constraint"
	spec.Questions = []*interfaces.RequirementQuestion{
		{ID: "q1", Text: "Lines of order SO-1002?", Template: "Lines of order {order}?", AnswerShape: "a list of lines",
			ScenarioIDs: []string{"s1"}, Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
			Inputs: &[]interfaces.RequirementInput{{Name: "order", ConceptID: "c1", Property: "order_no",
				Role: interfaces.REQUIREMENT_INPUT_RECORD}}},
		{ID: "q2", Text: "Which orders need approval?", ScenarioIDs: []string{"s1", "s9"}, Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY,
			AnswerShape: "orders", Basis: interfaces.RequirementBasis{ConceptIDs: []string{"c1", "c9"},
				Metrics: []string{"order amount"}, Functions: []string{"approval check"}, Skills: []string{}}},
		{ID: "q3", Text: "Whose orders?", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE},
	}
	return req
}

func exportGuidance() []*interfaces.QuestionGuidance {
	return []*interfaces.QuestionGuidance{
		{QuestionID: "q1", Question: "Lines of order SO-1002?", SpecVersion: 1, CreateTime: 20, Status: interfaces.GUIDANCE_STATUS_READY,
			Tests: []*interfaces.QuestionTest{
				{ID: "t1", Kind: interfaces.TEST_KIND_TEMPLATE, Text: "Lines of order SO-1002?", Expect: interfaces.TEST_EXPECT_EXISTS, ByServer: true,
					Slots: []interfaces.QuestionTestSlot{{ConceptID: "c1", Property: "order_no", Value: "SO-1002"}},
					Check: interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED}},
				{ID: "t2", Kind: interfaces.TEST_KIND_NOT_EXIST, Text: "Lines of order SO-9999?", Expect: interfaces.TEST_EXPECT_NOT_EXISTS,
					Slots: []interfaces.QuestionTestSlot{{ConceptID: "c1", Property: "order_no", Value: "SO-9999"}}},
				// A slot still to fill, and a value that contains another.
				{ID: "t3", Kind: "variant", Text: "Lines of the order SO-10021 placed by {{slot 2}}?", Expect: interfaces.TEST_EXPECT_SAME_RECORD,
					Slots: []interfaces.QuestionTestSlot{{ConceptID: "c1", Property: "order_no", Value: "SO-10021"},
						{ConceptID: "c2", Property: "full_name"}}},
			}},
		// Generated for an earlier wording: not exported.
		{QuestionID: "q3", Question: "Whose orders were they?", SpecVersion: 1, CreateTime: 20, Status: interfaces.GUIDANCE_STATUS_READY,
			Tests: []*interfaces.QuestionTest{{ID: "t1", Text: "Orders of ACME?", Expect: interfaces.TEST_EXPECT_EXISTS}}},
	}
}

func Test_BuildExport(t *testing.T) {
	Convey("Test what is exported for Workshop and bkn-eval\n", t, func() {
		req := exportSpec()

		Convey("Workshop gets the capability questions with what they rest on, and the rules\n", func() {
			body, err := buildExport(exportTargetWorkshop, req, exportGuidance())

			So(err, ShouldBeNil)
			var got workshopExport
			So(yaml.Unmarshal(body, &got), ShouldBeNil)
			So(got.Format, ShouldEqual, "bkn-project-requirements-workshop/v1")
			So(got.ProjectID, ShouldEqual, "p1")
			So(got.SpecVersion, ShouldEqual, 1)
			So(got.Questions, ShouldResemble, []workshopQuestion{{ID: "q2", Question: "Which orders need approval?",
				AnswerShape: "orders", Scenarios: []string{"Answer"},
				Basis: exportBasis{Concepts: []string{"Order"}, Metrics: []string{"order amount"}, Functions: []string{"approval check"}}}})
			So(got.Rules, ShouldResemble, []exportRule{{ID: "r1", Statement: "Signed changes only", CompilesTo: "constraint"}})
			So(string(body), ShouldNotContainSubstring, "q1")
		})

		Convey("bkn-eval gets the knowledge questions and their current tests, and no value of a record\n", func() {
			body, err := buildExport(exportTargetEval, req, exportGuidance())

			So(err, ShouldBeNil)
			var got evalExport
			So(yaml.Unmarshal(body, &got), ShouldBeNil)
			So(got.Format, ShouldEqual, "bkn-project-requirements-eval/v1")
			So(got.AnswerSetAccess, ShouldEqual, "forbidden")
			So(got.Questions, ShouldHaveLength, 2)
			// The template, not the text the documents' example stands in.
			So(got.Questions[0].Question, ShouldEqual, "Lines of order {order}?")
			So(got.Questions[0].Inputs, ShouldResemble, []exportInput{{Name: "order", Concept: "Order", Property: "order_no",
				Role: interfaces.REQUIREMENT_INPUT_RECORD}})
			So(got.Questions[0].Tests, ShouldResemble, []exportTest{
				{Kind: interfaces.TEST_KIND_TEMPLATE, Question: "Lines of order {Order.order_no}?", Expect: interfaces.TEST_EXPECT_EXISTS},
				{Kind: interfaces.TEST_KIND_NOT_EXIST, Question: "Lines of order {Order.order_no}?", Expect: interfaces.TEST_EXPECT_NOT_EXISTS},
				{Kind: "variant", Question: "Lines of the order {Order.order_no} placed by {Customer.full_name}?",
					Expect: interfaces.TEST_EXPECT_SAME_RECORD},
			})
			// Drafted with no template: its text; its tests were for another wording.
			So(got.Questions[1].Question, ShouldEqual, "Whose orders?")
			So(got.Questions[1].Tests, ShouldBeEmpty)
			for _, value := range []string{"SO-1002", "SO-9999", "SO-10021", "ACME", "passed"} {
				So(string(body), ShouldNotContainSubstring, value)
			}
			So(string(body), ShouldNotContainSubstring, "example")
			So(string(body), ShouldNotContainSubstring, "check")
		})

		Convey("Guidance not ready, or of another version or older than the specification, is not exported\n", func() {
			for _, change := range []func(g *interfaces.QuestionGuidance){
				func(g *interfaces.QuestionGuidance) { g.Status = interfaces.GUIDANCE_STATUS_GENERATING },
				func(g *interfaces.QuestionGuidance) { g.SpecVersion = 2 },
				func(g *interfaces.QuestionGuidance) { g.CreateTime = 5 },
			} {
				guidance := exportGuidance()
				change(guidance[0])
				body, err := buildExport(exportTargetEval, req, guidance)
				So(err, ShouldBeNil)
				var got evalExport
				So(yaml.Unmarshal(body, &got), ShouldBeNil)
				So(got.Questions[0].Tests, ShouldBeEmpty)
			}
		})
	})
}

func Test_ExportRequirement(t *testing.T) {
	Convey("Test ExportRequirement\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra}
		ctx := context.Background()
		entry := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}

		Convey("A ready specification exports, read as the caller, with a file name of the project and target\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(entry, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(exportSpec(), true, nil)
			ra.EXPECT().ListGuidance(gomock.Any(), "p1").Return(exportGuidance(), nil)

			body, name, err := svc.ExportRequirement(ctx, token, "p1", exportTargetEval)

			So(err, ShouldBeNil)
			So(name, ShouldEqual, "p1-rq-1-eval.yaml")
			So(string(body), ShouldContainSubstring, "answer_set_access: forbidden")
		})

		Convey("An unknown target is refused before anything is read\n", func() {
			_, _, err := svc.ExportRequirement(ctx, token, "p1", "answers")
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidExportTarget)
		})

		Convey("Without a ready specification there is nothing to export\n", func() {
			for _, read := range []func(){
				func() { ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil) },
				func() {
					drafting := exportSpec()
					drafting.Status = interfaces.REQUIREMENT_STATUS_DRAFTING
					ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(drafting, true, nil)
				},
			} {
				ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(entry, nil)
				read()
				_, _, err := svc.ExportRequirement(ctx, token, "p1", exportTargetWorkshop)
				shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)
			}
		})

		Convey("A project the caller cannot see, or a failed read, is an error\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(nil, errors.New("no project"))
			_, _, err := svc.ExportRequirement(ctx, token, "p1", exportTargetWorkshop)
			So(err, ShouldNotBeNil)

			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(entry, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(exportSpec(), true, nil)
			ra.EXPECT().ListGuidance(gomock.Any(), "p1").Return(nil, errors.New("down"))
			_, _, err = svc.ExportRequirement(ctx, token, "p1", exportTargetEval)
			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Requirement_InternalError)
		})

		Convey("Workshop reads no guidance\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(entry, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(exportSpec(), true, nil)
			body, name, err := svc.ExportRequirement(ctx, token, "p1", exportTargetWorkshop)
			So(err, ShouldBeNil)
			So(name, ShouldEqual, "p1-rq-1-workshop.yaml")
			So(strings.HasPrefix(string(body), "format: bkn-project-requirements-workshop/v1"), ShouldBeTrue)
		})
	})
}
