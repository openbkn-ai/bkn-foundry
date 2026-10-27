// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
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

func Test_DraftRestHandler(t *testing.T) {
	Convey("Test draft handlers\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		drs := pmock.NewMockDraftService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, drs: drs}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
			Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User, TokenID: "Bearer t"}, nil)
		caller := interfaces.AccountInfo{ID: "u1", Type: string(hydra.VisitorType_User)}
		const base = "/api/bkn-project/v1/projects/p1/ontology"

		Convey("draft:generate starts drafting as the caller and replies 202\n", func() {
			drs.EXPECT().GenerateDraft(gomock.Any(), "Bearer t", "p1", caller).
				Return(&interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_DRAFTING}, nil)

			w := serve(handler, http.MethodPost, base+"/draft:generate")

			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)
			So(w.Body.String(), ShouldContainSubstring, `"status":"drafting"`)
		})

		Convey("Any other ontology action is not found\n", func() {
			So(serve(handler, http.MethodPost, base+"/draft:delete").Result().StatusCode, ShouldEqual, http.StatusNotFound)
		})

		Convey("The draft is replied as is, or with the service's error\n", func() {
			drs.EXPECT().GetDraft(gomock.Any(), "Bearer t", "p1").
				Return(&interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY}, nil)
			So(serve(handler, http.MethodGet, base+"/draft").Result().StatusCode, ShouldEqual, http.StatusOK)

			drs.EXPECT().GetDraft(gomock.Any(), "Bearer t", "p1").
				Return(nil, httperrors.NewHTTPError(context.Background(), http.StatusNotFound, perrors.BknProject_Draft_NotFound))
			w := serve(handler, http.MethodGet, base+"/draft")
			So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Draft_NotFound)
		})
	})
}
