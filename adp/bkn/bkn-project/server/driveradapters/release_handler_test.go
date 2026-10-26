// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"encoding/json"
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

func Test_ReleaseRestHandler(t *testing.T) {
	Convey("Test release handlers\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		rs := pmock.NewMockReleaseService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, rs: rs}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User}, nil)

		listURL := "/api/bkn-project/v1/lineages/orders/releases"

		Convey("List uses the default limit and returns the entries envelope\n", func() {
			next := "abc"
			rs.EXPECT().ListReleases(gomock.Any(), "orders", interfaces.DEFAULT_RELEASE_LIMIT, "").
				Return(&interfaces.ReleaseList{
					Entries:    []*interfaces.Release{{ID: "r1", LineageID: "orders", NetworkID: "orders_1-0-0", State: interfaces.RELEASE_STATE_LIVE}},
					Total:      2,
					NextCursor: &next,
				}, nil)

			w := serve(handler, http.MethodGet, listURL)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			var body map[string]any
			So(json.Unmarshal(w.Body.Bytes(), &body), ShouldBeNil)
			So(body["total"], ShouldEqual, 2)
			So(body["next_cursor"], ShouldEqual, "abc")
			So(body["entries"], ShouldHaveLength, 1)
		})

		Convey("List passes limit and cursor through\n", func() {
			rs.EXPECT().ListReleases(gomock.Any(), "orders", 5, "c1").Return(&interfaces.ReleaseList{Entries: []*interfaces.Release{}}, nil)

			w := serve(handler, http.MethodGet, listURL+"?limit=5&cursor=c1")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"entries":[]`)
			So(w.Body.String(), ShouldContainSubstring, `"next_cursor":null`)
		})

		Convey("List rejects a limit out of range\n", func() {
			for _, limit := range []string{"0", "101", "abc"} {
				w := serve(handler, http.MethodGet, listURL+"?limit="+limit)

				So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
				So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_InvalidParameter_Limit)
			}
		})

		Convey("List replies the service error\n", func() {
			rs.EXPECT().ListReleases(gomock.Any(), "orders", interfaces.DEFAULT_RELEASE_LIMIT, "bad").
				Return(nil, httperrors.NewHTTPError(context.Background(), http.StatusBadRequest, perrors.BknProject_InvalidParameter_Cursor))

			w := serve(handler, http.MethodGet, listURL+"?cursor=bad")

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_InvalidParameter_Cursor)
		})

		Convey("Get returns the bare release\n", func() {
			rs.EXPECT().GetRelease(gomock.Any(), "orders_1-0-0").
				Return(&interfaces.Release{ID: "r1", NetworkID: "orders_1-0-0", Version: "1.0.0"}, nil)

			w := serve(handler, http.MethodGet, "/api/bkn-project/v1/releases/orders_1-0-0")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"network_id":"orders_1-0-0"`)
		})

		Convey("Get replies not found\n", func() {
			rs.EXPECT().GetRelease(gomock.Any(), "orders").
				Return(nil, httperrors.NewHTTPError(context.Background(), http.StatusNotFound, perrors.BknProject_Release_NotFound))

			w := serve(handler, http.MethodGet, "/api/bkn-project/v1/releases/orders")

			So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Release_NotFound)
		})
	})
}
