// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_ProjectReleaseRestHandler(t *testing.T) {
	Convey("Test project release handlers\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		prs := pmock.NewMockProjectReleaseService(mockCtrl)
		drs := pmock.NewMockDraftService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, prs: prs, drs: drs}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
			Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User, TokenID: "Bearer t"}, nil)
		caller := interfaces.AccountInfo{ID: "u1", Type: string(hydra.VisitorType_User)}
		author := interfaces.GitAuthor{Name: "u1"}
		const base = "/api/bkn-project/v1/projects/p1"

		Convey("release-review reviews the version the query names, as the caller\n", func() {
			prs.EXPECT().ReviewRelease(gomock.Any(), "Bearer t", "p1", "1.1.0", caller).
				Return(&interfaces.ReleaseReview{ProjectID: "p1", Version: "1.1.0", Signature: "sig"}, nil)

			w := serve(handler, http.MethodGet, base+"/release-review?version=1.1.0")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"signature":"sig"`)
		})

		Convey("releases publishes what the body names and replies 201\n", func() {
			prs.EXPECT().PublishRelease(gomock.Any(), "Bearer t", "p1", interfaces.ProjectPublish{
				Version: "1.1.0", Notes: "n", Signature: "sig", Accepts: []string{"index_not_ready"},
			}, caller, author).Return(&interfaces.PublishReleaseResult{NetworkID: "supply_v1_1_0", Tag: "v1.1.0"}, nil)

			w := serveBody(handler, http.MethodPost, base+"/releases",
				[]byte(`{"version":"1.1.0","notes":"n","signature":"sig","accepts":["index_not_ready"]}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusCreated)
			So(w.Body.String(), ShouldContainSubstring, `"network_id":"supply_v1_1_0"`)
		})

		Convey("A release body that is not JSON is refused\n", func() {
			w := serveBody(handler, http.MethodPost, base+"/releases", []byte(`{"version":`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Release_InvalidRequestBody)
		})

		Convey("A refusal of the service is the reply\n", func() {
			prs.EXPECT().PublishRelease(gomock.Any(), "Bearer t", "p1", gomock.Any(), caller, author).Return(nil,
				httperrors.NewHTTPError(t.Context(), http.StatusConflict, perrors.BknProject_Release_ChecksFailed))

			w := serveBody(handler, http.MethodPost, base+"/releases", []byte(`{"version":"1.1.0"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusConflict)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Release_ChecksFailed)
		})

		Convey("branch:sync syncs the project's branch as the caller\n", func() {
			prs.EXPECT().SyncBranch(gomock.Any(), "Bearer t", "p1", caller, author).
				Return(&interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_MERGED, Commit: "b2"}, nil)

			w := serve(handler, http.MethodPost, base+"/branch:sync")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"result":"merged"`)
		})

		Convey("drift:commit commits the live network's drift and syncs the branch\n", func() {
			prs.EXPECT().CommitDrift(gomock.Any(), "Bearer t", "p1", caller, author).Return(&interfaces.ProjectDriftCommit{
				Sync: &interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_FAST_FORWARD},
			}, nil)

			w := serve(handler, http.MethodPost, base+"/drift:commit")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"result":"fast_forward"`)
		})

		Convey("catalogs:understand still reaches its handler beside the release actions\n", func() {
			drs.EXPECT().Understand(gomock.Any(), "Bearer t", "p1", gomock.Any(), caller).
				Return(&interfaces.ProjectUnderstandSubmitted{}, nil)

			So(serve(handler, http.MethodPost, base+"/catalogs:understand").Result().StatusCode, ShouldEqual, http.StatusAccepted)
			So(serve(handler, http.MethodPost, base+"/branch:merge").Result().StatusCode, ShouldEqual, http.StatusNotFound)
		})

		Convey("switch-plan reads the plan of switching to the network the query names\n", func() {
			prs.EXPECT().PlanSwitch(gomock.Any(), "Bearer t", "p1", "supply_v1_1_0").
				Return(&interfaces.SwitchPlan{ToNetworkID: "supply_v1_1_0", Schedules: []*interfaces.ActionSchedule{}}, nil)

			w := serve(handler, http.MethodGet, base+"/switch-plan?to_network_id=supply_v1_1_0")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"to_network_id":"supply_v1_1_0"`)
		})

		Convey("switches switches to the network the body names and replies 201\n", func() {
			prs.EXPECT().SwitchConsumers(gomock.Any(), "Bearer t", "p1", "supply_v1_1_0", caller).
				Return(&interfaces.Switch{ID: "s1", ToNetworkID: "supply_v1_1_0"}, nil)

			w := serveBody(handler, http.MethodPost, base+"/switches", []byte(`{"to_network_id":"supply_v1_1_0"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusCreated)
			So(w.Body.String(), ShouldContainSubstring, `"id":"s1"`)
			So(serveBody(handler, http.MethodPost, base+"/switches", []byte(`[`)).Result().StatusCode,
				ShouldEqual, http.StatusBadRequest)
		})
	})
}
