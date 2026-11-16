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
	Convey("The owner discards the network's unpublished edits through the project's lineage\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ls := pmock.NewMockLineageService(ctrl)
		svc := &projectReleaseService{ps: ps, ls: ls}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "supply", Branch: "project/p1", Owner: reviewOwner}}
		ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil).AnyTimes()

		Convey("on the project's network\n", func() {
			ls.EXPECT().DiscardDraft(gomock.Any(), reviewToken, "supply").Return(nil)

			So(svc.DiscardDrift(ctx, reviewToken, "p1", reviewOwner), ShouldBeNil)
		})

		Convey("replying what the lineage refused\n", func() {
			refused := errors.New("draft changed")
			ls.EXPECT().DiscardDraft(gomock.Any(), reviewToken, "supply").Return(refused)

			So(svc.DiscardDrift(ctx, reviewToken, "p1", reviewOwner), ShouldEqual, refused)
		})

		Convey("Someone other than the owner is refused\n", func() {
			_, code, _ := errorOf(svc.DiscardDrift(ctx, reviewToken, "p1", interfaces.AccountInfo{ID: "u2", Type: "user"}))
			So(code, ShouldEqual, perrors.BknProject_Release_NotOwner)
		})
	})
}
