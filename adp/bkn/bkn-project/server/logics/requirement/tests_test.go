// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"net/http"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func storedGuidance() *interfaces.QuestionGuidance {
	return &interfaces.QuestionGuidance{ProjectID: "p1", QuestionID: "q1", SpecVersion: 1, Question: "Which orders?",
		Status: interfaces.GUIDANCE_STATUS_READY, UpdateTime: 20, Tests: []*interfaces.QuestionTest{
			{ID: "t1", Text: "Orders of 10001?", Expect: interfaces.TEST_EXPECT_EXISTS,
				Slots: []interfaces.QuestionTestSlot{{ConceptID: "c2", Property: "customer_id", Value: "10001"}},
				Check: interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED}},
			{ID: "t2", Text: "Orders of 99999?", Expect: interfaces.TEST_EXPECT_NOT_EXISTS,
				Slots: []interfaces.QuestionTestSlot{{ConceptID: "c2", Property: "customer_id", Value: "99999"}},
				Check: interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_FAILED, Reason: checkRecordExists}},
		}}
}

func Test_TestEdits(t *testing.T) {
	Convey("Test editing and deleting test questions\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, now: func() int64 { return 30 }}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{
			ID: "p1", Owner: caller}}, nil).AnyTimes()
		spec := readySpec()
		spec.Spec.Concepts[1].MapsTo = &customerRef
		draft := guidanceDraft()
		draft.Items.ObjectTypes[1].Table = interfaces.DraftTable{ResourceID: "r-cust"}
		var saved *interfaces.QuestionGuidance
		expectRead := func() {
			ra.EXPECT().GetGuidance(gomock.Any(), "p1", "q1").Return(storedGuidance(), true, nil)
		}
		expectContext := func() {
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
		}
		expectSave := func() {
			ra.EXPECT().UpdateGuidance(gomock.Any(), gomock.Any(), int64(20)).DoAndReturn(
				func(_ context.Context, g *interfaces.QuestionGuidance, _ int64) (bool, error) {
					saved = g
					return true, nil
				})
		}

		Convey("An edit changes what it sets, marks the test edited and checks it again\n", func() {
			expectRead()
			expectContext()
			va.EXPECT().Query(gomock.Any(), caller, "SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10002'", checkQueryTimeout).
				Return([]map[string]any{{"n": float64(3)}}, nil)
			expectSave()

			g, err := svc.EditTest(ctx, token, "p1", "q1", "t1", interfaces.RequirementTestEdit{UpdateTime: 20,
				Text:  ptr("Orders of customer 10002?"),
				Slots: &[]interfaces.QuestionTestSlot{{ConceptID: "c2", Property: "customer_id", Value: "10002"}}}, caller)

			So(err, ShouldBeNil)
			So(g.UpdateTime, ShouldEqual, 30)
			edited := saved.Tests[0]
			So(edited.Text, ShouldEqual, "Orders of customer 10002?")
			So(edited.Edited, ShouldBeTrue)
			So(edited.Check, ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_PASSED})
			So(saved.Tests[1].Edited, ShouldBeFalse)
		})

		Convey("An edit is refused when empty, too long, of an unknown expectation or concept\n", func() {
			for _, edit := range []interfaces.RequirementTestEdit{
				{UpdateTime: 20},
				{UpdateTime: 20, Text: ptr("  ")},
				{UpdateTime: 20, Text: ptr(strings.Repeat("a", maxItemTextRunes+1))},
				{UpdateTime: 20, Expect: ptr("maybe")},
				{UpdateTime: 20, Slots: &[]interfaces.QuestionTestSlot{{ConceptID: "c9", Property: "x", Value: "1"}}},
			} {
				expectRead()
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)

				_, err := svc.EditTest(ctx, token, "p1", "q1", "t1", edit, caller)
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidTest)
			}
		})

		Convey("An edit needs the update time read, ready guidance and a test that exists\n", func() {
			_, err := svc.EditTest(ctx, token, "p1", "q1", "t1", interfaces.RequirementTestEdit{Text: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_MissingUpdateTime)

			ra.EXPECT().GetGuidance(gomock.Any(), "p1", "q1").Return(nil, false, nil)
			_, err = svc.EditTest(ctx, token, "p1", "q1", "t1", interfaces.RequirementTestEdit{UpdateTime: 20, Text: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_TestNotFound)

			generating := storedGuidance()
			generating.Status = interfaces.GUIDANCE_STATUS_GENERATING
			ra.EXPECT().GetGuidance(gomock.Any(), "p1", "q1").Return(generating, true, nil)
			_, err = svc.EditTest(ctx, token, "p1", "q1", "t1", interfaces.RequirementTestEdit{UpdateTime: 20, Text: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Generating)

			expectRead()
			_, err = svc.EditTest(ctx, token, "p1", "q1", "t1", interfaces.RequirementTestEdit{UpdateTime: 19, Text: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Stale)

			expectRead()
			_, err = svc.EditTest(ctx, token, "p1", "q1", "t9", interfaces.RequirementTestEdit{UpdateTime: 20, Text: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_TestNotFound)

			expectRead()
			expectContext()
			va.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]map[string]any{{"n": float64(1)}}, nil)
			ra.EXPECT().UpdateGuidance(gomock.Any(), gomock.Any(), int64(20)).Return(false, nil)
			_, err = svc.EditTest(ctx, token, "p1", "q1", "t1", interfaces.RequirementTestEdit{UpdateTime: 20, Text: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Stale)
		})

		Convey("Deleting a test removes it and counts the deletion\n", func() {
			expectRead()
			expectSave()

			_, err := svc.DeleteTest(ctx, token, "p1", "q1", "t2", 20, caller)

			So(err, ShouldBeNil)
			So(saved.Tests, ShouldHaveLength, 1)
			So(saved.Tests[0].ID, ShouldEqual, "t1")
			So(saved.DeletedTests, ShouldEqual, 1)
		})

		Convey("Only the owner edits or deletes\n", func() {
			other := pmock.NewMockProjectService(ctrl)
			other.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{
				ID: "p1", Owner: interfaces.AccountInfo{ID: "u2"}}}, nil).Times(2)
			svc.ps = other

			_, err := svc.EditTest(ctx, token, "p1", "q1", "t1", interfaces.RequirementTestEdit{UpdateTime: 20, Text: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Requirement_NotOwner)
			_, err = svc.DeleteTest(ctx, token, "p1", "q1", "t1", 20, caller)
			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Requirement_NotOwner)
		})
	})
}
