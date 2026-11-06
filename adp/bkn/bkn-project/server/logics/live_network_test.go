// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_LiveNetwork(t *testing.T) {
	Convey("Test LiveNetwork\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ra := pmock.NewMockReleaseAccess(ctrl)
		ctx := context.Background()
		latest := ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "kn1", Limit: 1})

		Convey("Before the first release the live network is the lineage's own\n", func() {
			latest.Return([]*interfaces.Release{}, nil)
			network, err := LiveNetwork(ctx, ra, "kn1")
			So(err, ShouldBeNil)
			So(network, ShouldEqual, "kn1")
		})

		Convey("After a release it is the network of the latest release\n", func() {
			latest.Return([]*interfaces.Release{{NetworkID: "kn1_v1_1_0"}}, nil)
			network, err := LiveNetwork(ctx, ra, "kn1")
			So(err, ShouldBeNil)
			So(network, ShouldEqual, "kn1_v1_1_0")
		})

		Convey("A failed read is an error\n", func() {
			latest.Return(nil, errors.New("db down"))
			network, err := LiveNetwork(ctx, ra, "kn1")
			So(err, ShouldNotBeNil)
			So(network, ShouldBeEmpty)
		})
	})
}
