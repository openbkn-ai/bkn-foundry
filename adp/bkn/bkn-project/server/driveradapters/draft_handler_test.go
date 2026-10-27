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
			drs.EXPECT().GenerateDraft(gomock.Any(), "Bearer t", "p1", caller, false).
				Return(&interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_DRAFTING}, nil)

			w := serve(handler, http.MethodPost, base+"/draft:generate")

			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)
			So(w.Body.String(), ShouldContainSubstring, `"status":"drafting"`)
		})

		Convey("draft:generate discards decisions only when the body says so\n", func() {
			drs.EXPECT().GenerateDraft(gomock.Any(), "Bearer t", "p1", caller, true).
				Return(&interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_DRAFTING}, nil)

			w := serveBody(handler, http.MethodPost, base+"/draft:generate", []byte(`{"discard_decisions":true}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)

			w = serveBody(handler, http.MethodPost, base+"/draft:generate", []byte(`{"discard_decisions":`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Draft_InvalidRequestBody)
		})

		Convey("An item edit is handed to the service with what the body sets\n", func() {
			drs.EXPECT().EditDraftItem(gomock.Any(), "Bearer t", "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders",
				gomock.Any(), caller).
				DoAndReturn(func(_ context.Context, _, _, _, _ string, edit interfaces.DraftItemEdit,
					_ interfaces.AccountInfo) (*interfaces.Draft, error) {
					So(edit.UpdateTime, ShouldEqual, 7)
					So(*edit.Adopted, ShouldBeFalse)
					So(*edit.Name, ShouldEqual, "Sales orders")
					So(edit.Description, ShouldBeNil)
					return &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: 8}, nil
				})

			w := serveBody(handler, http.MethodPatch, base+"/draft/items/object_types/orders",
				[]byte(`{"update_time":7,"adopted":false,"name":"Sales orders"}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"update_time":8`)
		})

		Convey("An item edit with a malformed body is refused\n", func() {
			w := serveBody(handler, http.MethodPatch, base+"/draft/items/object_types/orders", []byte(`not json`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Draft_InvalidRequestBody)
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
