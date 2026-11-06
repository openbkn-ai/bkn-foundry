// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
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

func Test_RequirementLinkRestHandler(t *testing.T) {
	Convey("Test kinetic need link handlers\n", t, func() {
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
		const base = "/api/bkn-project/v1/projects/p1/requirements/logic-needs"
		spec := &interfaces.Requirement{ProjectID: "p1", Version: 1, Status: interfaces.REQUIREMENT_STATUS_READY, UpdateTime: 8}

		Convey("The needs are listed with their live implementations\n", func() {
			rqs.EXPECT().ListLogicNeeds(gomock.Any(), "Bearer t", "p1").Return(&interfaces.LogicNeedsView{UpdateTime: 7,
				NetworkID: "kn1", Needs: []*interfaces.LogicNeedView{{
					RequirementLogicNeed: &interfaces.RequirementLogicNeed{ID: "n1", Kind: interfaces.LOGIC_NEED_METRIC},
					Implementation:       &interfaces.LogicNeedImplementation{Reason: interfaces.LOGIC_IMPLEMENTATION_MISSING},
					LiveObjectTypeIDs:    []string{"order"}}}}, nil)

			w := serve(handler, http.MethodGet, base)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"network_id":"kn1"`)
			So(w.Body.String(), ShouldContainSubstring, `"reason":"missing"`)
			So(w.Body.String(), ShouldContainSubstring, `"live_object_type_ids":["order"]`)
		})

		Convey("A list or candidates the service refuses is replied as its error\n", func() {
			rqs.EXPECT().ListLogicNeeds(gomock.Any(), "Bearer t", "p1").
				Return(nil, httpError(http.StatusNotFound, perrors.BknProject_Requirement_NotFound))
			rqs.EXPECT().LogicNeedCandidates(gomock.Any(), "Bearer t", "p1", "n9").
				Return(nil, httpError(http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound))

			w := serve(handler, http.MethodGet, base)
			So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_NotFound)
			w = serve(handler, http.MethodGet, base+"/n9/candidates")
			So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_ItemNotFound)
		})

		Convey("The candidates of a need are listed\n", func() {
			rqs.EXPECT().LogicNeedCandidates(gomock.Any(), "Bearer t", "p1", "n1").Return(&interfaces.LogicNeedCandidates{
				NetworkID: "kn1", Candidates: []*interfaces.LogicNeedCandidate{{Type: interfaces.LOGIC_LINK_OBJECT_METRIC,
					ID: "m1", Name: "Late rate", Reasons: []string{interfaces.LOGIC_CANDIDATE_SAME_NAME}}}}, nil)

			w := serve(handler, http.MethodGet, base+"/n1/candidates")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"reasons":["same_name"]`)
		})

		Convey("A need is linked from the body and unlinked over the update time asked\n", func() {
			rqs.EXPECT().LinkLogicNeed(gomock.Any(), "Bearer t", "p1", "n2", interfaces.RequirementLogicLinkEdit{UpdateTime: 7,
				Type: interfaces.LOGIC_LINK_FUNCTION, ID: "f1", BoxID: "b1"}, caller).Return(spec, nil)
			rqs.EXPECT().UnlinkLogicNeed(gomock.Any(), "Bearer t", "p1", "n2", int64(7), caller).Return(spec, nil)

			w := serveBody(handler, http.MethodPut, base+"/n2/link", []byte(`{"update_time":7,"type":"function","id":"f1","box_id":"b1"}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"update_time":8`)
			w = serve(handler, http.MethodDelete, base+"/n2/link?update_time=7")
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"update_time":8`)
		})

		Convey("A link the service refuses is replied as its error\n", func() {
			rqs.EXPECT().LinkLogicNeed(gomock.Any(), "Bearer t", "p1", "n1", gomock.Any(), caller).
				Return(nil, httpError(http.StatusServiceUnavailable, perrors.BknProject_Requirement_ImplementationUnavailable))
			rqs.EXPECT().UnlinkLogicNeed(gomock.Any(), "Bearer t", "p1", "n1", int64(0), caller).
				Return(nil, httpError(http.StatusBadRequest, perrors.BknProject_Requirement_MissingUpdateTime))

			w := serveBody(handler, http.MethodPut, base+"/n1/link", []byte(`{"update_time":7,"type":"object_metric","id":"m1"}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusServiceUnavailable)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_ImplementationUnavailable)
			w = serve(handler, http.MethodDelete, base+"/n1/link")
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("A malformed body or update time is refused before the service\n", func() {
			w := serveBody(handler, http.MethodPut, base+"/n1/link", []byte(`[`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_InvalidRequestBody)
			w = serve(handler, http.MethodDelete, base+"/n1/link?update_time=soon")
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_MissingUpdateTime)
		})
	})
}
