// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func shouldBeHTTPError(err error, status int, code string) {
	var httpErr *httperrors.HTTPError
	So(errors.As(err, &httpErr), ShouldBeTrue)
	So(httpErr.HTTPCode, ShouldEqual, status)
	So(httpErr.ErrorCode, ShouldEqual, code)
}

func Test_ReleaseService_ListReleases(t *testing.T) {
	Convey("Test releaseService ListReleases\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ra := pmock.NewMockReleaseAccess(mockCtrl)
		svc := &releaseService{ra: ra}
		ctx := context.Background()
		r3 := &interfaces.Release{ID: "r3", CreateTime: 3}
		r2 := &interfaces.Release{ID: "r2", CreateTime: 2}
		r1 := &interfaces.Release{ID: "r1", CreateTime: 1}

		Convey("A full page carries a cursor that resumes after its last release\n", func() {
			ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "orders", Limit: 3}).
				Return([]*interfaces.Release{r3, r2, r1}, nil)
			ra.EXPECT().CountReleases(gomock.Any(), "orders").Return(int64(3), nil)

			list, err := svc.ListReleases(ctx, "orders", 2, "")

			So(err, ShouldBeNil)
			So(list.Entries, ShouldResemble, []*interfaces.Release{r3, r2})
			So(list.Total, ShouldEqual, 3)
			So(list.NextCursor, ShouldNotBeNil)

			ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{
				LineageID: "orders", Limit: 3, Cursor: &interfaces.ReleaseCursor{CreateTime: 2, ID: "r2"},
			}).Return([]*interfaces.Release{r1}, nil)
			ra.EXPECT().CountReleases(gomock.Any(), "orders").Return(int64(3), nil)

			next, err := svc.ListReleases(ctx, "orders", 2, *list.NextCursor)

			So(err, ShouldBeNil)
			So(next.Entries, ShouldResemble, []*interfaces.Release{r1})
			So(next.NextCursor, ShouldBeNil)
		})

		Convey("An empty lineage returns an empty page\n", func() {
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return([]*interfaces.Release{}, nil)
			ra.EXPECT().CountReleases(gomock.Any(), "none").Return(int64(0), nil)

			list, err := svc.ListReleases(ctx, "none", 20, "")

			So(err, ShouldBeNil)
			So(list.Entries, ShouldNotBeNil)
			So(list.Entries, ShouldBeEmpty)
			So(list.Total, ShouldEqual, 0)
			So(list.NextCursor, ShouldBeNil)
		})

		Convey("A cursor that was not issued is rejected\n", func() {
			for _, cursor := range []string{"not base64!", "bm90IGpzb24", encodeCursor(interfaces.ReleaseCursor{CreateTime: 1})} {
				_, err := svc.ListReleases(ctx, "orders", 20, cursor)

				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_InvalidParameter_Cursor)
			}
		})

		Convey("A list failure is an internal error\n", func() {
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))

			_, err := svc.ListReleases(ctx, "orders", 20, "")

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Release_InternalError)
		})

		Convey("A count failure is an internal error\n", func() {
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return([]*interfaces.Release{}, nil)
			ra.EXPECT().CountReleases(gomock.Any(), "orders").Return(int64(0), errors.New("db down"))

			_, err := svc.ListReleases(ctx, "orders", 20, "")

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Release_InternalError)
		})
	})
}

func Test_ReleaseService_GetRelease(t *testing.T) {
	Convey("Test releaseService GetRelease\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ra := pmock.NewMockReleaseAccess(mockCtrl)
		svc := &releaseService{ra: ra}
		ctx := context.Background()

		Convey("Success\n", func() {
			expected := &interfaces.Release{ID: "r1", NetworkID: "orders_1-0-0"}
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "orders_1-0-0").Return(expected, true, nil)

			release, err := svc.GetRelease(ctx, "orders_1-0-0")

			So(err, ShouldBeNil)
			So(release, ShouldEqual, expected)
		})

		Convey("A network no release published is not found\n", func() {
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "orders").Return(nil, false, nil)

			_, err := svc.GetRelease(ctx, "orders")

			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Release_NotFound)
		})

		Convey("A read failure is an internal error\n", func() {
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "orders").Return(nil, false, errors.New("db down"))

			_, err := svc.GetRelease(ctx, "orders")

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Release_InternalError)
		})
	})
}
