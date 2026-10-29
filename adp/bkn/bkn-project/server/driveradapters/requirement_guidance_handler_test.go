// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_RequirementGuidanceRestHandler(t *testing.T) {
	Convey("Test test question handlers\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		rqs := pmock.NewMockRequirementService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, rqs: rqs}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
			Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User, TokenID: "Bearer t"}, nil)
		caller := interfaces.AccountInfo{ID: "u1", Type: string(hydra.VisitorType_User)}
		const base = "/api/bkn-project/v1/projects/p1/requirements/questions/q1"
		g := &interfaces.QuestionGuidance{ProjectID: "p1", QuestionID: "q1", Status: interfaces.GUIDANCE_STATUS_READY, UpdateTime: 30}

		Convey("dev:generate starts generating and replies 202\n", func() {
			rqs.EXPECT().GenerateGuidance(gomock.Any(), "Bearer t", "p1", "q1", caller).
				Return(&interfaces.QuestionGuidance{QuestionID: "q1", Status: interfaces.GUIDANCE_STATUS_GENERATING}, nil)

			w := serve(handler, http.MethodPost, base+"/dev:generate")

			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)
			So(w.Body.String(), ShouldContainSubstring, `"status":"generating"`)
			w = serve(handler, http.MethodPost, base+"/dev:other")
			So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
		})

		Convey("dev reads the guidance with whether it is current\n", func() {
			rqs.EXPECT().GetGuidance(gomock.Any(), "Bearer t", "p1", "q1").
				Return(&interfaces.QuestionGuidanceView{QuestionGuidance: g, Current: true}, nil)

			w := serve(handler, http.MethodGet, base+"/dev")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"current":true`)
		})

		Convey("A test question is edited from the body and deleted with the update time in the query\n", func() {
			rqs.EXPECT().EditTest(gomock.Any(), "Bearer t", "p1", "q1", "t1", gomock.Any(), caller).DoAndReturn(
				func(_ context.Context, _, _, _, _ string, edit interfaces.RequirementTestEdit, _ interfaces.AccountInfo) (*interfaces.QuestionGuidance, error) {
					So(edit.UpdateTime, ShouldEqual, 20)
					So(*edit.Text, ShouldEqual, "Orders of 10002?")
					So(*edit.Slots, ShouldResemble, []interfaces.QuestionTestSlot{{ConceptID: "c2", Property: "customer_id", Value: "10002"}})
					So(edit.Expect, ShouldBeNil)
					return g, nil
				})
			rqs.EXPECT().DeleteTest(gomock.Any(), "Bearer t", "p1", "q1", "t2", int64(20), caller).Return(g, nil)

			w := serveBody(handler, http.MethodPut, base+"/tests/t1",
				[]byte(`{"update_time":20,"text":"Orders of 10002?","slots":[{"concept_id":"c2","property":"customer_id","value":"10002"}]}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"update_time":30`)
			w = serve(handler, http.MethodDelete, base+"/tests/t2?update_time=20")
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		})

		Convey("A malformed body or update time is refused before the service\n", func() {
			w := serveBody(handler, http.MethodPut, base+"/tests/t1", []byte(`{`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_InvalidRequestBody)
			w = serve(handler, http.MethodDelete, base+"/tests/t2?update_time=soon")
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_MissingUpdateTime)
		})
	})
}
