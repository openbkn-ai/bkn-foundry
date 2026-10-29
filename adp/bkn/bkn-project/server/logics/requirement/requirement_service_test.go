// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"errors"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_ScenarioQuestions(t *testing.T) {
	Convey("Test ScenarioQuestions\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ra: ra, da: da}
		ctx := context.Background()
		ot := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "customer"}
		rt := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: "order_customer"}
		gone := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "carrier"}
		// An object type id that is also a relation type id elsewhere is not the relation type.
		wrongKind := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: "customer"}
		ready := &interfaces.Requirement{ProjectID: "p1", Version: 2, Status: interfaces.REQUIREMENT_STATUS_READY,
			UpdateTime: 7, Spec: interfaces.RequirementSpec{Questions: []*interfaces.RequirementQuestion{
				{ID: "q1", Text: "Which orders?", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
					Needs: []interfaces.RequirementItemRef{ot, rt, gone, wrongKind}},
				{ID: "q2", Text: "What rate?", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY},
			}}}
		draft := &interfaces.Draft{Items: interfaces.DraftItems{
			ObjectTypes:   []*interfaces.DraftObjectType{{ID: "customer"}, {ID: "order"}},
			RelationTypes: []*interfaces.DraftRelationType{{ID: "order_customer"}},
		}}

		Convey("Each question comes with its stored needs, the ones the draft lacks, and no tests yet\n", func() {
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(ready, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)

			got, err := svc.ScenarioQuestions(ctx, "p1")

			So(err, ShouldBeNil)
			So(got, ShouldResemble, &interfaces.ScenarioQuestions{SpecVersion: 2, UpdateTime: 7,
				Questions: []interfaces.ScenarioQuestion{
					{ID: "q1", Text: "Which orders?", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
						Needs:        []interfaces.RequirementItemRef{ot, rt, gone, wrongKind},
						MissingItems: []interfaces.RequirementItemRef{gone, wrongKind},
						Tests:        []interfaces.ScenarioTest{}},
					{ID: "q2", Text: "What rate?", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY,
						Needs: []interfaces.RequirementItemRef{}, MissingItems: []interfaces.RequirementItemRef{},
						Tests: []interfaces.ScenarioTest{}},
				}})
		})

		Convey("Without a draft every need is missing\n", func() {
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(ready, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)

			got, err := svc.ScenarioQuestions(ctx, "p1")

			So(err, ShouldBeNil)
			So(got.Questions[0].MissingItems, ShouldResemble, []interfaces.RequirementItemRef{ot, rt, gone, wrongKind})
		})

		Convey("A project without a ready specification has no questions, and that is no error\n", func() {
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil)
			for _, status := range []string{interfaces.REQUIREMENT_STATUS_DRAFTING, interfaces.REQUIREMENT_STATUS_FAILED} {
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{ProjectID: "p1", Version: 1,
					Status: status, Spec: ready.Spec}, true, nil)
			}

			for range 3 {
				got, err := svc.ScenarioQuestions(ctx, "p1")
				So(err, ShouldBeNil)
				So(got.Questions, ShouldResemble, []interfaces.ScenarioQuestion{})
			}
		})

		Convey("Storage errors are internal errors\n", func() {
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, errors.New("db down"))
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(ready, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, errors.New("db down"))

			_, err := svc.ScenarioQuestions(ctx, "p1")
			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Requirement_InternalError)
			_, err = svc.ScenarioQuestions(ctx, "p1")
			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Requirement_InternalError)
		})
	})
}
