// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_RecordTests(t *testing.T) {
	Convey("Test filling scenario questions with a record from the data\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		svc := &requirementService{ra: ra, da: da, va: va, now: func() int64 { return 0 }}
		ctx := context.Background()
		draft := guidanceDraft()
		draft.Items.ObjectTypes[1].Table = interfaces.DraftTable{ResourceID: "r-cust"}
		draft.Items.ObjectTypes[1].PrimaryKeys = []string{"customer_id"}
		// q2 asks about one customer; q1 has no input and is asked as it is.
		req := readySpec()
		req.Spec.Concepts[1].MapsTo = &customerRef
		req.Spec.Questions[1].Text, req.Spec.Questions[1].Template = "Orders of a customer?", "Orders of customer {customer}?"
		req.Spec.Questions[1].Inputs = &[]interfaces.RequirementInput{{Name: "customer", ConceptID: "c2", Property: "customer_id",
			Role: interfaces.REQUIREMENT_INPUT_RECORD}}
		ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(req, true, nil).AnyTimes()
		sample := func(rows ...map[string]any) {
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), checkQueryTimeout).Return(rows, nil)
		}

		Convey("A question about a record is asked of the first record the data has, checked as the caller\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			sample(map[string]any{"v0": "10001"})
			va.EXPECT().Query(gomock.Any(), caller, "SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'",
				checkQueryTimeout).Return([]map[string]any{{"n": float64(1)}}, nil)

			tests, err := svc.RecordTests(ctx, caller, "p1", []string{"q1", "q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldResemble, map[string]*interfaces.ScenarioTest{"q2": {ID: recordTestID,
				Text: "Orders of customer 10001?", Expect: interfaces.TEST_EXPECT_EXISTS}})
		})

		Convey("Without a record to fill it with, the question has none to be asked of\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			sample()
			tests, err := svc.RecordTests(ctx, caller, "p1", []string{"q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldResemble, map[string]*interfaces.ScenarioTest{"q2": nil})

			// Nor without a draft, whose fields hold the values.
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			tests, err = svc.RecordTests(ctx, caller, "p1", []string{"q2"})
			So(err, ShouldBeNil)
			So(tests, ShouldResemble, map[string]*interfaces.ScenarioTest{"q2": nil})
		})

		Convey("Only the questions named are filled, and none is read for no question\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			tests, err := svc.RecordTests(ctx, caller, "p1", []string{"q1"})
			So(err, ShouldBeNil)
			So(tests, ShouldBeEmpty)
			tests, err = svc.RecordTests(ctx, caller, "p1", nil)
			So(err, ShouldBeNil)
			So(tests, ShouldBeEmpty)
		})

		Convey("Scenario questions say which the server can fill\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			ra.EXPECT().ListGuidance(gomock.Any(), "p1").Return(nil, nil)
			sq, err := svc.ScenarioQuestions(ctx, "p1")
			So(err, ShouldBeNil)
			So(sq.Questions[0].Fillable, ShouldBeFalse)
			So(sq.Questions[1].Fillable, ShouldBeTrue)
		})
	})
}
