// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_DiscardDrift(t *testing.T) {
	Convey("Discarding the network's unpublished edits puts its draft back to the running version\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		kva := pmock.NewMockKNVersionAccess(ctrl)
		svc := &projectReleaseService{ps: ps, kva: kva}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "supply", Branch: "project/p1", Owner: reviewOwner}}
		ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil).AnyTimes()
		versioning := func(running int, changes bool) *interfaces.KNVersioning {
			return &interfaces.KNVersioning{Enabled: true, RunningVersion: running,
				Draft: interfaces.KNDraftState{BaseVersion: running, HasChanges: changes, Revision: 12}}
		}

		Convey("at the revision read, so edits made since are not dropped unseen\n", func() {
			kva.EXPECT().GetVersioning(gomock.Any(), reviewToken, "supply").Return(versioning(3, true), nil)
			kva.EXPECT().ResetDraft(gomock.Any(), reviewToken, "supply", 3, int64(12), true).Return(nil)

			So(svc.DiscardDrift(ctx, reviewToken, "p1", reviewOwner), ShouldBeNil)
		})

		Convey("A draft without edits is left alone\n", func() {
			kva.EXPECT().GetVersioning(gomock.Any(), reviewToken, "supply").Return(versioning(3, false), nil)

			So(svc.DiscardDrift(ctx, reviewToken, "p1", reviewOwner), ShouldBeNil)
		})

		Convey("Before a first publish there is no version to go back to\n", func() {
			kva.EXPECT().GetVersioning(gomock.Any(), reviewToken, "supply").Return(versioning(0, true), nil)

			_, code, _ := errorOf(svc.DiscardDrift(ctx, reviewToken, "p1", reviewOwner))
			So(code, ShouldEqual, perrors.BknProject_Release_NoRunningVersion)
		})

		Convey("A reset the network refuses is replied as an error\n", func() {
			kva.EXPECT().GetVersioning(gomock.Any(), reviewToken, "supply").Return(versioning(3, true), nil)
			kva.EXPECT().ResetDraft(gomock.Any(), reviewToken, "supply", 3, int64(12), true).Return(errors.New("draft changed"))

			So(svc.DiscardDrift(ctx, reviewToken, "p1", reviewOwner), ShouldNotBeNil)
		})

		Convey("Someone other than the owner is refused\n", func() {
			_, code, _ := errorOf(svc.DiscardDrift(ctx, reviewToken, "p1", interfaces.AccountInfo{ID: "u2", Type: "user"}))
			So(code, ShouldEqual, perrors.BknProject_Release_NotOwner)
		})
	})
}
