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
		latest := ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "kn1", State: interfaces.RELEASE_STATE_PUBLISHED, Limit: 1})

		Convey("Before the first release the live network is the lineage's own\n", func() {
			latest.Return([]*interfaces.Release{}, nil)
			network, err := LiveNetwork(ctx, ra, "kn1")
			So(err, ShouldBeNil)
			So(network, ShouldEqual, "kn1")
		})

		Convey("After a release it is still the lineage's network, which keeps its id\n", func() {
			latest.Return([]*interfaces.Release{{NetworkID: "kn1"}}, nil)
			network, err := LiveNetwork(ctx, ra, "kn1")
			So(err, ShouldBeNil)
			So(network, ShouldEqual, "kn1")
		})

		Convey("A failed read is an error\n", func() {
			latest.Return(nil, errors.New("db down"))
			network, err := LiveNetwork(ctx, ra, "kn1")
			So(err, ShouldNotBeNil)
			So(network, ShouldBeEmpty)
		})
	})
}

func TestLiveNetworkOfTellsWhetherReleased(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	ra := pmock.NewMockReleaseAccess(ctrl)
	ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
	if network, released, err := LiveNetworkOf(context.Background(), ra, "kn1"); err != nil || released || network != "kn1" {
		t.Fatalf("before a release: %q %v %v", network, released, err)
	}
	ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return([]*interfaces.Release{{NetworkID: "kn1"}}, nil)
	if network, released, err := LiveNetworkOf(context.Background(), ra, "kn1"); err != nil || !released || network != "kn1" {
		t.Fatalf("after a release: %q %v %v", network, released, err)
	}
}
