// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/requirement"
)

// freshServices makes NewRegressionService and NewProjectReleaseService build their services
// again from the services set now, and puts the ones built before back when the test ends: a test
// run again, as with -count, sets new mocks that services built in an earlier run would not hold.
func freshServices(t *testing.T) {
	savedRGS, savedPRS := rgs, prs
	rgsOnce, rgs, prsOnce, prs = sync.Once{}, nil, sync.Once{}, nil
	t.Cleanup(func() {
		rgsOnce, rgs, prsOnce, prs = sync.Once{}, savedRGS, sync.Once{}, savedPRS
		if savedRGS != nil {
			rgsOnce.Do(func() {})
		}
		if savedPRS != nil {
			prsOnce.Do(func() {})
		}
	})
}

func Test_NewRegressionService(t *testing.T) {
	Convey("Test NewRegressionService\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		freshServices(t)

		rga := pmock.NewMockRegressionAccess(mockCtrl)
		baa := pmock.NewMockBknAgentAccess(mockCtrl)
		gs := pmock.NewMockGitStore(mockCtrl)
		va := pmock.NewMockVegaAccess(mockCtrl)
		kva := pmock.NewMockKNVersionAccess(mockCtrl)
		logics.SetVegaAccess(va)
		logics.SetKNVersionAccess(kva)
		logics.SetRegressionAccess(rga)
		logics.SetBknAgentAccess(baa)
		logics.SetGitStore(gs)

		Convey("It answers with the service's regression store, bkn-agent, git store and specification\n", func() {
			svc, ok := NewRegressionService().(*regressionService)

			So(ok, ShouldBeTrue)
			So(svc.ra, ShouldEqual, rga)
			So(svc.ba, ShouldEqual, baa)
			So(svc.gs, ShouldEqual, gs)
			So(svc.va, ShouldEqual, va)
			So(svc.kva, ShouldEqual, kva)
			So(svc.rs, ShouldEqual, requirement.NewRequirementService())
			So(svc.run, ShouldNotBeNil)
			So(NewRegressionService(), ShouldEqual, svc)
		})

		Convey("A project's release service starts regressions with it and reads them from the store\n", func() {
			prs, ok := NewProjectReleaseService(&common.AppSetting{}).(*projectReleaseService)

			So(ok, ShouldBeTrue)
			So(prs.rgs, ShouldEqual, NewRegressionService())
			So(prs.rga, ShouldEqual, rga)
			So(prs.va, ShouldEqual, va)
		})
	})
}
