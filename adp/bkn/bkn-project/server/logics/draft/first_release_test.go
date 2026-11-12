// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_DraftService_FirstReleaseVersion(t *testing.T) {
	Convey("Test draftService firstReleaseVersion\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		kva := pmock.NewMockKNVersionAccess(ctrl)
		s := &draftService{kva: kva}
		ctx := context.Background()

		Convey("A network a project started from goes on from its versions\n", func() {
			kva.EXPECT().ListVersions(gomock.Any(), "token", "kn1").Return([]*interfaces.KNVersion{
				{Version: 2, Label: "v1.2.0", State: interfaces.KN_VERSION_STATE_RUNNING},
				{Version: 1, Label: "v1.0.0", State: interfaces.KN_VERSION_STATE_HISTORY},
			}, nil)
			So(s.firstReleaseVersion(ctx, "token", "kn1"), ShouldEqual, "1.3.0")
		})

		Convey("A network running an unlabelled version starts at 1.1.0\n", func() {
			kva.EXPECT().ListVersions(gomock.Any(), "token", "kn1").Return([]*interfaces.KNVersion{
				{Version: 1, State: interfaces.KN_VERSION_STATE_RUNNING},
			}, nil)
			So(s.firstReleaseVersion(ctx, "token", "kn1"), ShouldEqual, "1.1.0")
		})

		Convey("A network without versions, or whose versions cannot be read, starts at 1.0.0\n", func() {
			kva.EXPECT().ListVersions(gomock.Any(), "token", "kn1").Return([]*interfaces.KNVersion{}, nil)
			So(s.firstReleaseVersion(ctx, "token", "kn1"), ShouldEqual, "1.0.0")
			kva.EXPECT().ListVersions(gomock.Any(), "token", "kn1").Return(nil, errors.New("down"))
			So(s.firstReleaseVersion(ctx, "token", "kn1"), ShouldEqual, "1.0.0")
		})

		Convey("Before the first release, reviewing names that version\n", func() {
			So(nextVersion(nil, "1.1.0"), ShouldEqual, "1.1.0")
			So(nextVersion(&interfaces.Release{Version: "1.1.0"}, "1.1.0"), ShouldEqual, "1.1.1")
		})
	})
}
